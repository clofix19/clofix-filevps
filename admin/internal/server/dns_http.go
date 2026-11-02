package server

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

// ServeHTTPNames proxies Host: *.clofix.internal to the published FileVPS HTTP port.
// Bind failures are logged; the admin API stays up.
func (s *Server) ServeHTTPNames(addr string) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("HTTP names listen %s: %v", addr, err)
		return
	}
	log.Printf("HTTP names on %s", addr)
	srv := &http.Server{Handler: http.HandlerFunc(s.serveNamedHTTP), ReadHeaderTimeout: 5 * time.Second}
	_ = srv.Serve(ln)
}

func (s *Server) serveNamedHTTP(w http.ResponseWriter, r *http.Request) {
	if s.certificates != nil && s.serveACMEChallenge(w, r) {
		return
	}
	if id, ok := s.namedLoadBalancer(r.Host); ok {
		r.SetPathValue("id", id)
		r.SetPathValue("path", strings.TrimPrefix(r.URL.Path, "/"))
		r.SetPathValue("alb_domain", "true")
		s.proxyLoadBalancer(w, r)
		return
	}
	port, ok := s.namedHTTPPort(r.Host)
	if !ok {
		http.Error(w, "unknown CloFix hostname", http.StatusNotFound)
		return
	}
	backend := os.Getenv("CLOFIX_LB_WORKER_HOST")
	if backend == "" {
		backend = "127.0.0.1"
	}
	target, err := url.Parse("http://" + net.JoinHostPort(backend, strconv.Itoa(port)))
	if err != nil {
		http.Error(w, "invalid backend", http.StatusBadGateway)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = r.Host
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("Forwarded")
	}
	proxy.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second, DisableKeepAlives: true}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "backend unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func canonicalCloFixName(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if host == "clofix.internel" || strings.HasSuffix(host, ".clofix.internel") {
		return strings.TrimSuffix(host, ".internel") + ".internal"
	}
	return host
}

func (s *Server) namedLoadBalancer(host string) (string, bool) {
	host = canonicalCloFixName(host)
	if host == "" || s.balancers == nil {
		return "", false
	}
	_ = s.refreshDNS()
	s.dns.Lock()
	zone := s.dns.Zone.Domain
	var ids []string
	for _, rec := range s.dns.Records {
		if rec.Name == host && rec.Resource == "lb" && rec.ResourceID != "" {
			ids = append(ids, rec.ResourceID)
		}
	}
	s.dns.Unlock()
	s.balancers.Lock()
	defer s.balancers.Unlock()
	for _, id := range ids {
		if b := s.balancers.Items[id]; b != nil && !b.Deleting && b.Mode != "nlb" {
			return b.ID, true
		}
	}
	for _, b := range s.balancers.Items {
		if b == nil || b.Deleting || b.Mode == "nlb" {
			continue
		}
		kind := "alb"
		if host == dnsJoin(dnsLabel(b.ID), kind, zone) || b.Name != "" && host == dnsJoin(dnsLabel(b.Name), kind, zone) || b.Slug != "" && host == dnsJoin(dnsLabel(b.Slug), kind, zone) {
			return b.ID, true
		}
	}
	return "", false
}

func (s *Server) namedHTTPPort(host string) (int, bool) {
	host = canonicalCloFixName(host)
	if host == "" {
		return 0, false
	}
	_ = s.refreshDNS()
	s.dns.Lock()
	recs := make([]dnsRecord, 0, 2)
	for _, rec := range s.dns.Records {
		if rec.Name == host {
			recs = append(recs, rec)
		}
	}
	s.dns.Unlock()
	for _, rec := range recs {
		if p := s.publishHTTPPort(rec); p > 0 {
			return p, true
		}
	}
	for _, rec := range recs {
		if rec.Type != "CNAME" {
			continue
		}
		target := strings.ToLower(strings.TrimSuffix(rec.Value, "."))
		s.dns.Lock()
		for _, other := range s.dns.Records {
			if other.Name != target {
				continue
			}
			if p := s.publishHTTPPort(other); p > 0 {
				s.dns.Unlock()
				return p, true
			}
		}
		s.dns.Unlock()
	}
	return 0, false
}

func cksLabHTTPPort() int {
	raw := strings.TrimSpace(os.Getenv("CLOFIX_KUBERNETES_LAB_HTTP_PORT"))
	if raw == "" {
		return 19080
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || n > 65535 {
		return 19080
	}
	return n
}

func cksNamedHTTPPort(port int) int {
	lab := cksLabHTTPPort()
	switch port {
	case 0, 80, 443, 8080, lab:
		return lab
	default:
		return port
	}
}

func (s *Server) publishHTTPPort(rec dnsRecord) int {
	if rec.Resource == "cks" {
		return cksNamedHTTPPort(rec.Port)
	}
	if rec.Port >= 1024 {
		return rec.Port
	}
	if rec.Resource == "vps" && rec.ResourceID != "" {
		inst, err := s.Store.GetInstance(rec.ResourceID)
		if err == nil && inst.Spec.SSHPort > 0 {
			if rec.Port == 443 {
				return proto.HostHTTPSPort(inst.Spec.SSHPort)
			}
			return proto.HostHTTPPort(inst.Spec.SSHPort)
		}
	}
	return rec.Port
}
