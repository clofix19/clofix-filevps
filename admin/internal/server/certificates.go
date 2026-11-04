package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

type certificateRecord struct {
	Domain     string    `json:"domain"`
	Target     string    `json:"target"` // alb | managed-app
	ResourceID string    `json:"resource_id"`
	State      string    `json:"state"` // pending|issued|failed
	Error      string    `json:"error,omitempty"`
	Issued     time.Time `json:"issued,omitempty"`
	Expires    time.Time `json:"expires,omitempty"`
	Updated    time.Time `json:"updated"`
}

type certificateStore struct {
	sync.Mutex
	path       string
	Items      map[string]certificateRecord `json:"items"`
	Challenges map[string]string            `json:"-"` // token -> keyAuth
	certs      map[string]*tls.Certificate  `json:"-"`
	email      string
	directory  string
}

func loadCertificates(dir string) *certificateStore {
	d := &certificateStore{
		path: filepath.Join(dir, "certificates.json"), Items: map[string]certificateRecord{},
		Challenges: map[string]string{}, certs: map[string]*tls.Certificate{},
		email: strings.TrimSpace(os.Getenv("CLOFIX_ACME_EMAIL")),
		directory: strings.TrimSpace(os.Getenv("CLOFIX_ACME_DIRECTORY")),
	}
	if d.directory == "" {
		d.directory = acme.LetsEncryptURL
	}
	b, err := os.ReadFile(d.path)
	if err == nil {
		_ = json.Unmarshal(b, d)
	}
	if d.Items == nil {
		d.Items = map[string]certificateRecord{}
	}
	return d
}

func (d *certificateStore) save() error {
	b, err := json.MarshalIndent(struct {
		Items map[string]certificateRecord `json:"items"`
	}{Items: d.Items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

func (s *Server) certificateRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/certificates", s.auth(s.listCertificates))
	m.HandleFunc("POST /api/certificates", s.auth(s.requestCertificate))
	m.HandleFunc("DELETE /api/certificates/{domain}", s.auth(s.deleteCertificate))
}

func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	d := s.certificates
	d.Lock()
	defer d.Unlock()
	items := make([]certificateRecord, 0, len(d.Items))
	for _, item := range d.Items {
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{
		"items": items, "acme_email_configured": d.email != "",
		"note": "Point the domain A/AAAA at this controller, open TCP 80 for HTTP-01, then request a certificate. TLS is served on CLOFIX_HTTPS_LISTEN (:443).",
	})
}

func (s *Server) requestCertificate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain     string `json:"domain"`
		Target     string `json:"target"`
		ResourceID string `json:"resource_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid certificate request")
		return
	}
	domain := strings.ToLower(strings.TrimSpace(in.Domain))
	if !validALBDomain(domain) {
		fail(w, 400, "enter a valid DNS hostname")
		return
	}
	target := strings.ToLower(strings.TrimSpace(in.Target))
	if target != "alb" && target != "managed-app" {
		fail(w, 400, "target must be alb or managed-app")
		return
	}
	if strings.TrimSpace(in.ResourceID) == "" {
		fail(w, 400, "resource_id is required")
		return
	}
	if target == "alb" {
		s.balancers.Lock()
		b := s.balancers.Items[in.ResourceID]
		s.balancers.Unlock()
		if b == nil || b.Mode == "nlb" {
			fail(w, 404, "ALB not found")
			return
		}
		found := false
		for _, d := range b.Domains {
			if d == domain {
				found = true
				break
			}
		}
		if !found {
			fail(w, 400, "attach the domain to the ALB before requesting a certificate")
			return
		}
	}
	if target == "managed-app" {
		s.applications.Lock()
		app, ok := s.applications.Managed[in.ResourceID]
		s.applications.Unlock()
		if !ok || !canAccessApplication(r, app.Owner) {
			fail(w, 404, "managed application not found")
			return
		}
		found := false
		for _, d := range app.Domains {
			if d == domain {
				found = true
				break
			}
		}
		if !found {
			fail(w, 400, "attach the domain to the managed app before requesting a certificate")
			return
		}
	}
	d := s.certificates
	d.Lock()
	email := d.email
	if email == "" {
		d.Unlock()
		fail(w, 409, "set CLOFIX_ACME_EMAIL on the controller before issuing certificates")
		return
	}
	rec := certificateRecord{Domain: domain, Target: target, ResourceID: in.ResourceID, State: "pending", Updated: time.Now().UTC()}
	d.Items[domain] = rec
	_ = d.save()
	d.Unlock()
	go s.issueCertificate(domain)
	writeJSON(w, 202, map[string]any{"certificate": rec, "queued": true})
}

func (s *Server) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSpace(r.PathValue("domain")))
	d := s.certificates
	d.Lock()
	defer d.Unlock()
	if _, ok := d.Items[domain]; !ok {
		fail(w, 404, "certificate not found")
		return
	}
	delete(d.Items, domain)
	delete(d.certs, domain)
	_ = d.save()
	writeJSON(w, 200, map[string]string{"deleted": domain})
}

func (s *Server) issueCertificate(domain string) {
	d := s.certificates
	failRec := func(msg string) {
		d.Lock()
		rec := d.Items[domain]
		rec.State, rec.Error, rec.Updated = "failed", msg, time.Now().UTC()
		d.Items[domain] = rec
		_ = d.save()
		d.Unlock()
	}
	if d.email == "" {
		failRec("ACME email not configured")
		return
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		failRec(err.Error())
		return
	}
	client := &acme.Client{Key: key, DirectoryURL: d.directory}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	acct, err := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + d.email}}, acme.AcceptTOS)
	if err != nil {
		failRec("ACME register: " + err.Error())
		return
	}
	_ = acct
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		failRec("ACME order: " + err.Error())
		return
	}
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			failRec(err.Error())
			return
		}
		var challenge *acme.Challenge
		for i := range authz.Challenges {
			if authz.Challenges[i].Type == "http-01" {
				challenge = authz.Challenges[i]
				break
			}
		}
		if challenge == nil {
			failRec("HTTP-01 challenge unavailable")
			return
		}
		resp, err := client.HTTP01ChallengeResponse(challenge.Token)
		if err != nil {
			failRec(err.Error())
			return
		}
		d.Lock()
		d.Challenges[challenge.Token] = resp
		d.Unlock()
		defer func(token string) {
			d.Lock()
			delete(d.Challenges, token)
			d.Unlock()
		}(challenge.Token)
		if _, err := client.Accept(ctx, challenge); err != nil {
			failRec("ACME accept: " + err.Error())
			return
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			failRec("ACME authorization: " + err.Error())
			return
		}
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		failRec(err.Error())
		return
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{domain}}, certKey)
	if err != nil {
		failRec(err.Error())
		return
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		failRec("ACME finalize: " + err.Error())
		return
	}
	leaf, err := x509.ParseCertificate(ders[0])
	if err != nil {
		failRec(err.Error())
		return
	}
	var pemCert strings.Builder
	for _, der := range ders {
		_ = pem.Encode(&pemCert, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		failRec(err.Error())
		return
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair([]byte(pemCert.String()), keyPEM)
	if err != nil {
		failRec(err.Error())
		return
	}
	dir := filepath.Join(filepath.Dir(d.path), "certs")
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, domain+".crt"), []byte(pemCert.String()), 0600)
	_ = os.WriteFile(filepath.Join(dir, domain+".key"), keyPEM, 0600)
	d.Lock()
	d.certs[domain] = &pair
	rec := d.Items[domain]
	rec.State, rec.Error, rec.Issued, rec.Expires, rec.Updated = "issued", "", time.Now().UTC(), leaf.NotAfter.UTC(), time.Now().UTC()
	d.Items[domain] = rec
	_ = d.save()
	d.Unlock()
}

func (s *Server) serveACMEChallenge(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		return false
	}
	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return true
	}
	s.certificates.Lock()
	resp := s.certificates.Challenges[token]
	s.certificates.Unlock()
	if resp == "" {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(resp))
	return true
}

func (s *Server) ServeHTTPSNames(addr string) {
	if addr == "" || s.certificates == nil || s.certificates.email == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("HTTPS names listen %s: %v", addr, err)
		return
	}
	log.Printf("HTTPS names (ACME) on %s", addr)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: s.getACMECertificate, NextProtos: []string{"h2", "http/1.1"}}
	srv := &http.Server{Handler: http.HandlerFunc(s.serveNamedHTTP), ReadHeaderTimeout: 5 * time.Second, TLSConfig: cfg}
	_ = srv.Serve(tls.NewListener(ln, cfg))
}

func (s *Server) getACMECertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := strings.ToLower(strings.TrimSpace(hello.ServerName))
	s.certificates.Lock()
	defer s.certificates.Unlock()
	if cert := s.certificates.certs[host]; cert != nil {
		return cert, nil
	}
	dir := filepath.Join(filepath.Dir(s.certificates.path), "certs")
	crt, err := os.ReadFile(filepath.Join(dir, host+".crt"))
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(filepath.Join(dir, host+".key"))
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return nil, err
	}
	s.certificates.certs[host] = &pair
	return &pair, nil
}
