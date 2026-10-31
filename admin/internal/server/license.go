package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Wire types only: the customer controller does not import the company license server.
type licensePlan struct {
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

	ID             string `json:"id"`
	Name           string `json:"name"`
	MaxVPS         int    `json:"max_vps"`
	MaxWorkers     int    `json:"max_workers"`
	MaxLB          int    `json:"max_load_balancers"`
	MaxActivations int    `json:"max_activations"`
	Active         bool   `json:"active"`
}
type licenseGrant struct {
	InstallationID string      `json:"installation_id"`
	DurationDays   int         `json:"duration_days"`
	Valid          bool        `json:"valid"`
	LicenseID      string      `json:"license_id,omitempty"`
	Customer       string      `json:"customer,omitempty"`
	Plan           licensePlan `json:"plan"`
	Expires        time.Time   `json:"expires"`
	Error          string      `json:"error,omitempty"`
}

type controllerLicense struct {
	sync.Mutex
	ServerURL      string       `json:"server_url"`
	EncryptedKey   []byte       `json:"encrypted_key,omitempty"`
	InstallationID string       `json:"installation_id"`
	LastCheck      time.Time    `json:"last_check"`
	Grant          licenseGrant `json:"grant"`
	Status         string       `json:"status"`
	file           string
}

func loadControllerLicense(dir string) *controllerLicense {
	d := &controllerLicense{file: filepath.Join(dir, "license.json"), InstallationID: "ctl-" + randHex(16), ServerURL: os.Getenv("CLOFIX_LICENSE_SERVER_URL"), Status: "Not configured"}
	if raw, e := os.ReadFile(d.file); e == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid license.json")
		}
	}
	if _, e := os.Stat(d.file); os.IsNotExist(e) {
		if e = d.save(); e != nil {
			panic("cannot persist controller installation ID")
		}
	}
	return d
}
func (d *controllerLicense) save() error {
	if e := os.MkdirAll(filepath.Dir(d.file), 0700); e != nil {
		return e
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if e = os.WriteFile(d.file+".tmp", raw, 0600); e != nil {
		return e
	}
	return os.Rename(d.file+".tmp", d.file)
}
func (s *Server) licenseRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/license/usage", s.auth(s.licenseUsage))
	m.HandleFunc("GET /api/license", s.auth(s.getControllerLicense))
	m.HandleFunc("PUT /api/license", s.auth(s.saveControllerLicense))
	m.HandleFunc("POST /api/license/validate", s.auth(s.validateControllerLicense))
}
func (s *Server) getControllerLicense(w http.ResponseWriter, r *http.Request) {
	d := s.license
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, map[string]any{"management_url": os.Getenv("CLOFIX_LICENSE_PUBLIC_URL"), "server_url": d.ServerURL, "configured": len(d.EncryptedKey) > 0, "installation_id": d.InstallationID, "last_check": d.LastCheck, "grant": d.Grant, "status": d.Status, "enforcement": true})
}
func (s *Server) saveControllerLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerURL string `json:"server_url"`
		Key       string `json:"key"`
		Clear     bool   `json:"clear"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid license settings")
		return
	}
	in.Key = strings.TrimSpace(in.Key)
	if strings.HasPrefix(in.Key, "lic-") {
		fail(w, 400, "this is a license ID; enter the CFL- license key shown when the license was issued")
		return
	}
	if in.Clear && in.Key != "" {
		fail(w, 400, "choose either a new license key or removal of the saved key")
		return
	}
	in.ServerURL = strings.TrimRight(strings.TrimSpace(in.ServerURL), "/")
	u, e := url.Parse(in.ServerURL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		fail(w, 400, "valid HTTP/HTTPS license server base URL required")
		return
	}
	d := s.license
	d.Lock()
	defer d.Unlock()
	oldURL, oldKey, oldGrant, oldStatus, oldCheck := d.ServerURL, d.EncryptedKey, d.Grant, d.Status, d.LastCheck
	key := d.EncryptedKey
	if in.Clear {
		key = nil
	} else if strings.TrimSpace(in.Key) != "" {
		if len(in.Key) > 256 {
			fail(w, 400, "license key too long")
			return
		}
		key, e = s.encryptApplicationSecrets(map[string]string{"license_key": strings.TrimSpace(in.Key)})
		if e != nil {
			fail(w, 500, "could not encrypt license key")
			return
		}
	}
	d.ServerURL = in.ServerURL
	d.EncryptedKey = key
	d.Grant = licenseGrant{}
	d.Status = "Not validated"
	d.LastCheck = time.Time{}
	if len(key) == 0 {
		d.Status = "Not configured"
	}
	if e = d.save(); e != nil {
		d.ServerURL, d.EncryptedKey, d.Grant, d.Status, d.LastCheck = oldURL, oldKey, oldGrant, oldStatus, oldCheck
		fail(w, 500, "could not save license configuration")
		return
	}
	s.Store.Audit("admin", "license.configure", d.InstallationID, "ok")
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (s *Server) checkControllerLicense(ctx context.Context) error {
	d := s.license
	d.Lock()
	serverURL, encrypted, id := d.ServerURL, append([]byte{}, d.EncryptedKey...), d.InstallationID
	d.Unlock()
	if len(encrypted) == 0 {
		return fmt.Errorf("configure a license key first")
	}
	secrets, e := s.decryptApplicationSecrets(encrypted)
	if e != nil {
		return fmt.Errorf("could not decrypt license key")
	}
	raw, _ := json.Marshal(map[string]string{"key": secrets["license_key"], "installation_id": id})
	req, e := http.NewRequestWithContext(ctx, "POST", serverURL+"/api/validate", bytes.NewReader(raw))
	if e != nil {
		return fmt.Errorf("invalid license server configuration")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	grant := licenseGrant{}
	status := "License server unavailable"
	if e == nil {
		defer res.Body.Close()
		if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&grant) != nil {
			status = "Invalid license server response"
		} else if res.StatusCode == 200 && grant.Valid && grant.InstallationID == id {
			status = "Valid"
		} else {
			if grant.Valid && grant.InstallationID != id {
				grant.Error = "license installation ID does not match this controller"
			}
			grant.Valid = false
			status = grant.Error
			if status == "" {
				status = fmt.Sprintf("License server returned HTTP %d", res.StatusCode)
			}
		}
	}
	d.Lock()
	defer d.Unlock()
	if d.ServerURL != serverURL || !bytes.Equal(d.EncryptedKey, encrypted) {
		return fmt.Errorf("license settings changed during validation; retry")
	}
	d.Grant = grant
	d.Status = status
	d.LastCheck = time.Now().UTC()
	if e := d.save(); e != nil {
		return fmt.Errorf("could not persist license validation")
	}
	if !grant.Valid {
		return fmt.Errorf("%s", status)
	}
	return nil
}
func (s *Server) validateControllerLicense(w http.ResponseWriter, r *http.Request) {
	e := s.checkControllerLicense(r.Context())
	s.Store.Audit("admin", "license.validate", "controller", fmt.Sprint(e))
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	s.getControllerLicense(w, r)
}
func (s *Server) RunLicenseScheduler(ctx context.Context) {
	s.checkControllerLicense(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkControllerLicense(ctx)
		}
	}
}
