// Package license implements the separate online license management service.
package license

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

)

//go:embed web/*
var ui embed.FS

type Plan struct {
	MaxDatabaseProjects  *int `json:"max_database_projects,omitempty"`
	MaxDatabases         *int `json:"max_databases,omitempty"`
	MaxDatabaseUsers     *int `json:"max_database_users,omitempty"`
	MaxDatabaseSchedules *int `json:"max_database_schedules,omitempty"`

	MaxHealthChecks         *int `json:"max_health_checks,omitempty"`
	MaxBackups              *int `json:"max_backups,omitempty"`
	MaxVolumes              *int `json:"max_volumes,omitempty"`
	MaxStorageGB            *int `json:"max_storage_gb,omitempty"`
	MaxS3Connections        *int `json:"max_s3_connections,omitempty"`
	MaxApplications         *int `json:"max_applications,omitempty"`
	MaxManagedApps          *int `json:"max_managed_apps,omitempty"`
	MaxNotificationChannels *int `json:"max_notification_channels,omitempty"`
	MaxIAMUsers             *int `json:"max_iam_users,omitempty"`
	MaxIAMGroups            *int `json:"max_iam_groups,omitempty"`
	MaxIAMPolicies          *int `json:"max_iam_policies,omitempty"`
	MaxAccessKeys           *int `json:"max_access_keys,omitempty"`
	MaxDNSZones             *int `json:"max_dns_zones,omitempty"`
	MaxDNSRecords           *int `json:"max_dns_records,omitempty"`
	MaxKubernetesClusters   *int `json:"max_kubernetes_clusters,omitempty"`
	MaxKubernetesNodes      *int `json:"max_kubernetes_nodes,omitempty"`
	MaxKubernetesNodePools  *int `json:"max_kubernetes_node_pools,omitempty"`
	MaxDockerContainers     *int `json:"max_docker_containers,omitempty"`
	MaxDockerApplications   *int `json:"max_docker_applications,omitempty"`
	MaxDockerWorkers        *int `json:"max_docker_workers,omitempty"`
	MaxRedisClusters        *int `json:"max_redis_clusters,omitempty"`

	ID             string `json:"id"`
	Name           string `json:"name"`
	MaxVPS         int    `json:"max_vps"`
	MaxWorkers     int    `json:"max_workers"`
	MaxLB          int    `json:"max_load_balancers"`
	MaxActivations int    `json:"max_activations"`
	Active         bool   `json:"active"`
}
type License struct {
	InstallationID string    `json:"installation_id"`
	EncryptedKey   []byte    `json:"encrypted_key,omitempty"`
	KeyAvailable   bool      `json:"key_available"`
	DurationDays   int       `json:"duration_days"`
	Deleted        bool      `json:"deleted"`
	ID             string    `json:"id"`
	PlanID         string    `json:"plan_id"`
	CustomerID     string    `json:"customer_id,omitempty"`
	Customer       string    `json:"customer"`
	KeyHash        string    `json:"key_hash,omitempty"`
	Expires        time.Time `json:"expires"`
	Revoked        bool      `json:"revoked"`
	Installations  []string  `json:"installations"`
	Created        time.Time `json:"created"`
}
type Grant struct {
	InstallationID string    `json:"installation_id"`
	DurationDays   int       `json:"duration_days"`
	Valid          bool      `json:"valid"`
	LicenseID      string    `json:"license_id,omitempty"`
	Customer       string    `json:"customer,omitempty"`
	Plan           Plan      `json:"plan"`
	Expires        time.Time `json:"expires"`
	Error          string    `json:"error,omitempty"`
}
type Service struct {
	keyCipher cipher.AEAD
	sync.Mutex
	Plans     map[string]Plan         `json:"plans"`
	Licenses  map[string]License      `json:"licenses"`
	Customers map[string]Customer     `json:"customers"`
	Admins    map[string]AdminAccount `json:"admins"`
	store     Store
	file      string // data-dir path used for encryption key + JSON fallback
	sessions  map[string]adminSession
	rates     map[string]rate
}
type rate struct {
	at time.Time
	n  int
}

// New opens the file-backed store under dir (licenses.json + encryption key).
// username/password bootstrap the first company admin on an empty store.
func New(dir, username, password string) (*Service, error) {
	return newService(NewFileStore(dir), dir, username, password)
}

// NewWithDatabase opens Postgres when databaseURL is set; otherwise uses the file store.
// If Postgres is empty and licenses.json exists under dir, data is imported once.
// First deploy seeds the company admin account and a default Standard plan.
func NewWithDatabase(dir, username, password, databaseURL string) (*Service, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return New(dir, username, password)
	}
	pg, err := NewPostgresStore(databaseURL)
	if err != nil {
		return nil, err
	}
	empty, err := pg.(*postgresStore).empty()
	if err != nil {
		_ = pg.Close()
		return nil, err
	}
	if empty {
		file := NewFileStore(dir)
		plans, licenses, customers, admins, _, loadErr := file.Load()
		if loadErr != nil {
			_ = pg.Close()
			return nil, loadErr
		}
		if len(plans) > 0 || len(licenses) > 0 || len(customers) > 0 || len(admins) > 0 {
			if err := pg.Save(plans, licenses, customers, admins); err != nil {
				_ = pg.Close()
				return nil, fmt.Errorf("import licenses.json into postgres: %w", err)
			}
		}
	}
	return newService(pg, dir, username, password)
}

func newService(store Store, dir, username, password string) (*Service, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Service{
		Plans: map[string]Plan{}, Licenses: map[string]License{}, Customers: map[string]Customer{}, Admins: map[string]AdminAccount{},
		store: store, file: filepath.Join(dir, "licenses.json"),
		sessions: map[string]adminSession{}, rates: map[string]rate{},
	}
	plans, licenses, customers, admins, raw, err := store.Load()
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	s.Plans, s.Licenses, s.Customers, s.Admins = plans, licenses, customers, admins
	if s.Customers == nil {
		s.Customers = map[string]Customer{}
	}
	if s.Admins == nil {
		s.Admins = map[string]AdminAccount{}
	}
	if len(raw) > 0 {
		var legacy struct {
			Plans map[string]struct {
				DurationDays int `json:"duration_days"`
			} `json:"plans"`
		}
		json.Unmarshal(raw, &legacy)
		changed := false
		for id, l := range s.Licenses {
			if l.DurationDays == 0 {
				days := legacy.Plans[l.PlanID].DurationDays
				if days < 1 {
					days = int((l.Expires.Sub(l.Created) + 24*time.Hour - 1) / (24 * time.Hour))
					if days < 1 {
						days = 1
					}
					if days > 36500 {
						days = 36500
					}
				}
				l.DurationDays = days
				s.Licenses[id] = l
				changed = true
			}
		}
		if changed {
			backup := s.file + ".before-key-duration"
			if _, err := os.Stat(backup); os.IsNotExist(err) {
				if err = os.WriteFile(backup, raw, 0600); err != nil {
					_ = store.Close()
					return nil, err
				}
			}
			if err := s.save(); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
	}
	changedBinding := false
	for id, p := range s.Plans {
		if p.MaxActivations != 1 {
			p.MaxActivations = 1
			s.Plans[id] = p
			changedBinding = true
		}
	}
	for id, l := range s.Licenses {
		if l.InstallationID == "" && len(l.Installations) > 0 {
			l.InstallationID = l.Installations[0]
			l.Installations = []string{l.InstallationID}
			s.Licenses[id] = l
			changedBinding = true
		}
	}
	if changedBinding {
		if len(raw) > 0 {
			backup := s.file + ".before-single-installation"
			if _, e := os.Stat(backup); os.IsNotExist(e) {
				if e = os.WriteFile(backup, raw, 0600); e != nil {
					_ = store.Close()
					return nil, e
				}
			}
		}
		if e := s.save(); e != nil {
			_ = store.Close()
			return nil, e
		}
	}
	if e := s.bootstrap(username, password); e != nil {
		_ = store.Close()
		return nil, e
	}
	if e := s.initKeyCipher(); e != nil {
		_ = store.Close()
		return nil, e
	}
	return s, nil
}
func (s *Service) save() error {
	if s.store == nil {
		return fmt.Errorf("license store is not configured")
	}
	return s.store.Save(s.Plans, s.Licenses, s.Customers, s.Admins)
}
func randomID(prefix string) string {
	raw := make([]byte, 24)
	if _, e := rand.Read(raw); e != nil {
		panic(e)
	}
	return prefix + hex.EncodeToString(raw)
}
func hash(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
func safe(l License) License {
	l.KeyAvailable = len(l.EncryptedKey) > 0
	l.KeyHash = ""
	l.EncryptedKey = nil
	return l
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, code int, message string) {
	respond(w, code, map[string]string{"error": message})
}
func (s *Service) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !s.sessionOK(token) {
			problem(w, 401, "unauthorized")
			return
		}
		next(w, r)
	}
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	serveFile := func(name, contentType string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, err := ui.ReadFile(name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "no-store")
			w.Write(raw)
		}
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/plans", http.StatusFound)
	})
	mux.HandleFunc("GET /login", serveFile("web/login.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /plans", serveFile("web/plans.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /customers", serveFile("web/customers.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /issue", serveFile("web/issue.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /licenses", serveFile("web/licenses.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /common.css", serveFile("web/common.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /common.js", serveFile("web/common.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /clofix-logo.png", serveFile("web/clofix-logo.png", "image/png"))
	mux.HandleFunc("GET /clofix-mark.svg", serveFile("web/clofix-mark.svg", "image/svg+xml; charset=utf-8"))
	mux.HandleFunc("POST /api/login", s.loginAuth)
	mux.HandleFunc("POST /api/logout", s.auth(s.logoutAuth))
	mux.HandleFunc("GET /api/plans", s.auth(s.listPlans))
	mux.HandleFunc("POST /api/plans", s.auth(s.savePlan))
	mux.HandleFunc("PUT /api/plans/{id}", s.auth(s.savePlan))
	mux.HandleFunc("DELETE /api/plans/{id}", s.auth(s.deletePlan))
	mux.HandleFunc("GET /api/customers", s.auth(s.listCustomers))
	mux.HandleFunc("POST /api/customers", s.auth(s.saveCustomer))
	mux.HandleFunc("PUT /api/customers/{id}", s.auth(s.saveCustomer))
	mux.HandleFunc("DELETE /api/customers/{id}", s.auth(s.deleteCustomer))
	mux.HandleFunc("POST /api/licenses/{id}/key", s.auth(s.revealKey))
	mux.HandleFunc("PUT /api/licenses/{id}/key", s.auth(s.restoreKey))
	mux.HandleFunc("PUT /api/licenses/{id}", s.auth(s.modifyLicense))
	mux.HandleFunc("DELETE /api/licenses/{id}", s.auth(s.deleteLicense))
	mux.HandleFunc("GET /api/licenses", s.auth(s.listLicenses))
	mux.HandleFunc("POST /api/licenses", s.auth(s.createLicense))
	mux.HandleFunc("POST /api/licenses/{id}/revoke", s.auth(s.revokeLicense))
	mux.HandleFunc("POST /api/licenses/{id}/renew", s.auth(s.renewLicense))
	mux.HandleFunc("DELETE /api/licenses/{id}/activations/{installation}", s.auth(s.releaseActivation))
	mux.HandleFunc("POST /api/validate", s.validate)
	return mux
}

func (s *Service) resolveCustomer(customerID, customerName string) (Customer, string, error) {
	customerID = strings.TrimSpace(customerID)
	customerName = strings.TrimSpace(customerName)
	if customerID != "" {
		c, ok := s.Customers[customerID]
		if !ok {
			return Customer{}, "", fmt.Errorf("customer not found")
		}
		return c, c.Name, nil
	}
	if customerName == "" || len(customerName) > 160 {
		return Customer{}, "", fmt.Errorf("customer required")
	}
	for _, c := range s.Customers {
		if strings.EqualFold(c.Name, customerName) {
			return c, c.Name, nil
		}
	}
	// Legacy free-text customer names remain valid for older licenses / tests.
	return Customer{}, customerName, nil
}

func (s *Service) listCustomers(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	out := []Customer{}
	for _, c := range s.Customers {
		out = append(out, c)
	}
	respond(w, 200, out)
}

func (s *Service) saveCustomer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Contact string `json:"contact"`
		Notes   string `json:"notes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		problem(w, 400, "invalid customer payload")
		return
	}
	name := strings.TrimSpace(in.Name)
	contact := strings.TrimSpace(in.Contact)
	notes := strings.TrimSpace(in.Notes)
	if name == "" || len(name) > 160 || len(contact) > 160 || len(notes) > 1000 {
		problem(w, 400, "customer name required (max 160); contact max 160; notes max 1000")
		return
	}
	s.Lock()
	defer s.Unlock()
	id := r.PathValue("id")
	old, exists := s.Customers[id]
	if id != "" && !exists {
		problem(w, 404, "customer not found")
		return
	}
	for _, c := range s.Customers {
		if c.ID == id {
			continue
		}
		if strings.EqualFold(c.Name, name) {
			problem(w, 409, "another customer already uses this name")
			return
		}
	}
	now := time.Now().UTC()
	c := Customer{ID: id, Name: name, Contact: contact, Notes: notes, Updated: now}
	if !exists {
		c.ID = randomID("org-")
		c.Created = now
	} else {
		c.Created = old.Created
	}
	s.Customers[c.ID] = c
	for lid, l := range s.Licenses {
		if l.CustomerID == c.ID && l.Customer != c.Name {
			l.Customer = c.Name
			s.Licenses[lid] = l
		}
	}
	if e := s.save(); e != nil {
		if exists {
			s.Customers[c.ID] = old
		} else {
			delete(s.Customers, c.ID)
		}
		problem(w, 500, "could not save customer")
		return
	}
	respond(w, 200, c)
}

func (s *Service) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	id := r.PathValue("id")
	old, ok := s.Customers[id]
	if !ok {
		problem(w, 404, "customer not found")
		return
	}
	for _, l := range s.Licenses {
		if !l.Deleted && (l.CustomerID == id || (l.CustomerID == "" && strings.EqualFold(l.Customer, old.Name))) {
			problem(w, 409, "customer has issued licenses; delete or reassign those licenses first")
			return
		}
	}
	delete(s.Customers, id)
	if e := s.save(); e != nil {
		s.Customers[id] = old
		problem(w, 500, "could not delete customer")
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}
func (s *Service) listPlans(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	out := []Plan{}
	for _, p := range s.Plans {
		out = append(out, p)
	}
	respond(w, 200, out)
}
func (s *Service) savePlan(w http.ResponseWriter, r *http.Request) {
	var p Plan
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&p) != nil || strings.TrimSpace(p.Name) == "" || len(p.Name) > 80 || p.MaxVPS < 1 || p.MaxVPS > 1000000 || p.MaxWorkers < 1 || p.MaxWorkers > 100000 || p.MaxLB < 1 || p.MaxLB > 100000 || p.MaxActivations != 1 {
		problem(w, 400, "valid name, positive resource limits, exactly one installation required")
		return
	}
	for _, limit := range []*int{p.MaxDatabaseProjects, p.MaxDatabases, p.MaxDatabaseUsers, p.MaxDatabaseSchedules, p.MaxHealthChecks, p.MaxBackups, p.MaxVolumes, p.MaxStorageGB, p.MaxS3Connections, p.MaxApplications, p.MaxManagedApps, p.MaxNotificationChannels, p.MaxIAMUsers, p.MaxIAMGroups, p.MaxIAMPolicies, p.MaxAccessKeys, p.MaxDNSZones, p.MaxDNSRecords, p.MaxKubernetesClusters, p.MaxKubernetesNodes, p.MaxKubernetesNodePools, p.MaxDockerContainers, p.MaxDockerApplications, p.MaxDockerWorkers, p.MaxRedisClusters} {
		if limit != nil && (*limit < -1 || *limit > 1000000) {
			problem(w, 400, "optional resource limits must be -1 (unlimited), 0 (not included), or up to 1000000; omit for unspecified")
			return
		}
	}
	s.Lock()
	defer s.Unlock()
	p.ID = r.PathValue("id")
	old, exists := s.Plans[p.ID]
	if p.ID != "" && !exists {
		problem(w, 404, "plan not found")
		return
	}
	if !exists {
		p.ID = randomID("plan-")
	}
	s.Plans[p.ID] = p
	if e := s.save(); e != nil {
		if exists {
			s.Plans[p.ID] = old
		} else {
			delete(s.Plans, p.ID)
		}
		problem(w, 500, "could not save plan")
		return
	}
	respond(w, 200, p)
}
func (s *Service) deletePlan(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	id := r.PathValue("id")
	old, ok := s.Plans[id]
	if !ok {
		problem(w, 404, "plan not found")
		return
	}
	for _, l := range s.Licenses {
		if l.PlanID == id {
			problem(w, 409, "plan is used by a license; disable it to stop new issuance")
			return
		}
	}
	delete(s.Plans, id)
	if e := s.save(); e != nil {
		s.Plans[id] = old
		problem(w, 500, "could not delete plan")
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}
func (s *Service) listLicenses(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	out := []License{}
	for _, l := range s.Licenses {
		out = append(out, safe(l))
	}
	respond(w, 200, out)
}
func (s *Service) createLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InstallationID string `json:"installation_id"`
		DurationDays   int    `json:"duration_days"`
		PlanID         string `json:"plan_id"`
		CustomerID     string `json:"customer_id"`
		Customer       string `json:"customer"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || in.DurationDays < 1 || in.DurationDays > 36500 || !validInstallationID(in.InstallationID) {
		problem(w, 400, "plan, customer and license duration (1–36500 days) and installation ID required")
		return
	}
	s.Lock()
	defer s.Unlock()
	cust, customerName, err := s.resolveCustomer(in.CustomerID, in.Customer)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	p, ok := s.Plans[in.PlanID]
	if !ok || !p.Active {
		problem(w, 400, "select an active plan")
		return
	}
	key := randomID("CFL-")
	now := time.Now().UTC()
	l := License{InstallationID: in.InstallationID, DurationDays: in.DurationDays, ID: randomID("lic-"), PlanID: p.ID, CustomerID: cust.ID, Customer: customerName, KeyHash: hash(key), Created: now, Expires: now.Add(time.Duration(in.DurationDays) * 24 * time.Hour), Installations: []string{in.InstallationID}}
	encrypted, e := s.encryptKey(l.ID, key)
	if e != nil {
		problem(w, 500, "could not encrypt license key")
		return
	}
	l.EncryptedKey = encrypted
	s.Licenses[l.ID] = l
	if e := s.save(); e != nil {
		delete(s.Licenses, l.ID)
		problem(w, 500, "could not save license")
		return
	}
	respond(w, 201, map[string]any{"key": key, "license": safe(l)})
}
func (s *Service) revokeLicense(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	id := r.PathValue("id")
	l, ok := s.Licenses[id]
	if !ok {
		problem(w, 404, "license not found")
		return
	}
	old := l
	l.Revoked = true
	s.Licenses[id] = l
	if e := s.save(); e != nil {
		s.Licenses[id] = old
		problem(w, 500, "could not revoke license")
		return
	}
	respond(w, 200, safe(l))
}
func (s *Service) releaseActivation(w http.ResponseWriter, r *http.Request) {
	problem(w, 409, "license binding is permanent; revoke and issue a replacement for a different installation")
}
func (s *Service) validate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key          string `json:"key"`
		Installation string `json:"installation_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || len(in.Key) > 256 || len(in.Key) < 16 || !validInstallationID(in.Installation) {
		problem(w, 400, "license key and installation ID required")
		return
	}
	s.Lock()
	defer s.Unlock()
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	for ip, v := range s.rates {
		if now.Sub(v.at) > time.Minute {
			delete(s.rates, ip)
		}
	}
	v := s.rates[host]
	if v.n >= 60 || len(s.rates) >= 4096 && v.n == 0 {
		problem(w, 429, "validation rate limit")
		return
	}
	if v.n == 0 {
		v.at = now
	}
	v.n++
	s.rates[host] = v
	wanted := hash(in.Key)
	var l License
	found := false
	for _, candidate := range s.Licenses {
		if subtle.ConstantTimeCompare([]byte(candidate.KeyHash), []byte(wanted)) == 1 {
			l = candidate
			found = true
			break
		}
	}
	if !found {
		respond(w, 401, Grant{Error: "invalid license key"})
		return
	}
	if l.Deleted {
		respond(w, 403, Grant{Error: "license deleted"})
		return
	}
	if l.Revoked {
		respond(w, 403, Grant{Error: "license revoked"})
		return
	}
	if !now.Before(l.Expires) {
		respond(w, 403, Grant{Error: "license expired"})
		return
	}
	p, ok := s.Plans[l.PlanID]
	if !ok {
		respond(w, 403, Grant{Error: "license plan unavailable"})
		return
	}
	if l.InstallationID != "" && l.InstallationID != in.Installation {
		respond(w, 403, Grant{Error: "license is bound to a different installation ID"})
		return
	}
	if l.InstallationID == "" {
		old := l
		l.InstallationID = in.Installation
		l.Installations = []string{in.Installation}
		s.Licenses[l.ID] = l
		if e := s.save(); e != nil {
			s.Licenses[l.ID] = old
			problem(w, 500, "could not bind license")
			return
		}
	}
	respond(w, 200, Grant{InstallationID: l.InstallationID, DurationDays: l.DurationDays, Valid: true, LicenseID: l.ID, Customer: l.Customer, Plan: p, Expires: l.Expires})
}

func (s *Service) renewLicense(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	l, ok := s.Licenses[r.PathValue("id")]
	if !ok {
		problem(w, 404, "license not found")
		return
	}
	p, ok := s.Plans[l.PlanID]
	if !ok || !p.Active || l.Revoked || l.Deleted {
		problem(w, 409, "renewal requires an active plan and non-revoked license")
		return
	}
	old := l
	base := l.Expires
	if base.Before(time.Now()) {
		base = time.Now().UTC()
	}
	if base.After(time.Now().Add(100 * 365 * 24 * time.Hour)) {
		problem(w, 400, "license expiry exceeds renewal horizon")
		return
	}
	l.Expires = base.Add(time.Duration(l.DurationDays) * 24 * time.Hour)
	s.Licenses[l.ID] = l
	if e := s.save(); e != nil {
		s.Licenses[l.ID] = old
		problem(w, 500, "could not renew license")
		return
	}
	respond(w, 200, safe(l))
}

func (s *Service) modifyLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DurationDays *int      `json:"duration_days"`
		CustomerID   string    `json:"customer_id"`
		Customer     string    `json:"customer"`
		PlanID       string    `json:"plan_id"`
		Expires      time.Time `json:"expires"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || in.Expires.IsZero() || in.DurationDays != nil && (*in.DurationDays < 1 || *in.DurationDays > 36500) {
		problem(w, 400, "customer, plan and expiry required")
		return
	}
	s.Lock()
	defer s.Unlock()
	l, ok := s.Licenses[r.PathValue("id")]
	if !ok {
		problem(w, 404, "license not found")
		return
	}
	if l.Deleted {
		problem(w, 409, "deleted licenses cannot be modified")
		return
	}
	cust, customerName, err := s.resolveCustomer(in.CustomerID, in.Customer)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	p, ok := s.Plans[in.PlanID]
	if !ok || !p.Active && l.PlanID != in.PlanID {
		problem(w, 400, "select an active plan")
		return
	}
	if in.PlanID != l.PlanID && len(l.Installations) > p.MaxActivations {
		problem(w, 409, "release excess activations before changing to this plan")
		return
	}
	if in.Expires.Before(l.Created) {
		problem(w, 400, "expiry cannot precede creation")
		return
	}
	old := l
	if in.DurationDays != nil {
		l.DurationDays = *in.DurationDays
	}
	l.CustomerID = cust.ID
	l.Customer = customerName
	l.PlanID = in.PlanID
	l.Expires = in.Expires.UTC()
	s.Licenses[l.ID] = l
	if e := s.save(); e != nil {
		s.Licenses[l.ID] = old
		problem(w, 500, "could not modify license")
		return
	}
	respond(w, 200, safe(l))
}
func (s *Service) deleteLicense(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	l, ok := s.Licenses[r.PathValue("id")]
	if !ok {
		problem(w, 404, "license not found")
		return
	}
	old := l
	l.Deleted = true
	s.Licenses[l.ID] = l
	if e := s.save(); e != nil {
		s.Licenses[l.ID] = old
		problem(w, 500, "could not delete license")
		return
	}
	respond(w, 200, safe(l))
}

func validInstallationID(id string) bool {
	if len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
