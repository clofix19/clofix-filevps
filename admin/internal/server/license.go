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

	"github.com/clofix/filevps/worker/pkg/proto"
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
	MaxDNSZones             *int `json:"max_dns_zones,omitempty"`
	MaxDNSRecords           *int `json:"max_dns_records,omitempty"`
	MaxKubernetesClusters   *int `json:"max_kubernetes_clusters,omitempty"`
	MaxKubernetesNodes      *int `json:"max_kubernetes_nodes,omitempty"`
	MaxKubernetesNodePools  *int `json:"max_kubernetes_node_pools,omitempty"`
	MaxDockerContainers     *int `json:"max_docker_containers,omitempty"`
	MaxDockerApplications   *int `json:"max_docker_applications,omitempty"`
	MaxDockerCompose        *int `json:"max_docker_compose,omitempty"`
	MaxDockerWorkers        *int `json:"max_docker_workers,omitempty"`
	MaxDockerVolumes        *int `json:"max_docker_volumes,omitempty"`
	MaxDockerBackups        *int `json:"max_docker_backups,omitempty"`
	MaxRedisClusters        *int `json:"max_redis_clusters,omitempty"`

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
const (
	// licenseCheckInterval is how often the controller revalidates with the company license server.
	licenseCheckInterval = 2 * time.Minute
	// licenseStopGrace is how long after Expires existing workloads may keep running
	// before the controller auto-stops them. Creates are blocked as soon as Expires passes.
	licenseStopGrace = 7 * 24 * time.Hour
)

func (s *Server) checkControllerLicense(ctx context.Context) error {
	return s.checkControllerLicenseOpt(ctx, true)
}

// checkControllerLicenseOpt validates against the company license server.
// When the license server is unreachable, a previously synced grant (plan, duration,
// expires, last sync) is retained until Expires so the controller can keep operating offline.
func (s *Server) checkControllerLicenseOpt(ctx context.Context, retainOffline bool) error {
	d := s.license
	d.Lock()
	serverURL, encrypted, id := d.ServerURL, append([]byte{}, d.EncryptedKey...), d.InstallationID
	prevGrant, prevStatus, prevCheck := d.Grant, d.Status, d.LastCheck
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
	transportErr := e != nil
	if e == nil {
		defer res.Body.Close()
		if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&grant) != nil {
			status = "Invalid license server response"
			transportErr = true
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
	if transportErr && retainOffline && prevGrant.Valid && prevGrant.InstallationID == id && !prevGrant.Expires.IsZero() {
		d.Grant = prevGrant
		d.LastCheck = prevCheck // keep last successful sync date
		if time.Now().Before(prevGrant.Expires) {
			d.Status = fmt.Sprintf("Valid (offline · cached until %s)", prevGrant.Expires.UTC().Format(time.RFC3339))
		} else {
			d.Status = fmt.Sprintf("Expired (offline · last sync %s)", prevCheck.UTC().Format(time.RFC3339))
		}
		if e := d.save(); e != nil {
			d.Grant, d.Status, d.LastCheck = prevGrant, prevStatus, prevCheck
			return fmt.Errorf("could not persist license validation")
		}
		if !time.Now().Before(prevGrant.Expires) {
			return fmt.Errorf("license expired; renew and validate the license")
		}
		return nil
	}
	d.Grant = grant
	d.Status = status
	if grant.Valid {
		d.LastCheck = time.Now().UTC()
	}
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

// licensePhase reports enforcement state from the cached grant.
// active: before Expires; expired: after Expires (creates blocked); stopped: after Expires+7d (auto-stop).
func (s *Server) licensePhase() (phase string, expires, stopAfter time.Time, offline bool) {
	d := s.license
	d.Lock()
	defer d.Unlock()
	offline = d.Status != "" && strings.Contains(strings.ToLower(d.Status), "offline")
	if len(d.EncryptedKey) == 0 || !d.Grant.Valid || d.Grant.InstallationID != d.InstallationID || d.Grant.Expires.IsZero() {
		return "invalid", time.Time{}, time.Time{}, offline
	}
	expires = d.Grant.Expires.UTC()
	stopAfter = expires.Add(licenseStopGrace)
	now := time.Now().UTC()
	if now.Before(expires) {
		return "active", expires, stopAfter, offline
	}
	if now.Before(stopAfter) {
		return "expired", expires, stopAfter, offline
	}
	return "stopped", expires, stopAfter, offline
}

func (s *Server) getControllerLicense(w http.ResponseWriter, r *http.Request) {
	d := s.license
	d.Lock()
	view := map[string]any{
		"management_url":  os.Getenv("CLOFIX_LICENSE_PUBLIC_URL"),
		"server_url":      d.ServerURL,
		"configured":      len(d.EncryptedKey) > 0,
		"installation_id": d.InstallationID,
		"last_check":      d.LastCheck,
		"last_sync":       d.LastCheck,
		"grant":           d.Grant,
		"status":          d.Status,
		"enforcement":     true,
	}
	d.Unlock()
	phase, expires, stopAfter, offline := s.licensePhase()
	view["phase"] = phase
	view["offline"] = offline
	if !expires.IsZero() {
		view["expires"] = expires
		view["stop_services_after"] = stopAfter
	}
	view["stop_grace_days"] = int(licenseStopGrace / (24 * time.Hour))
	writeJSON(w, 200, view)
}

func (s *Server) RunLicenseScheduler(ctx context.Context) {
	s.checkControllerLicenseOpt(ctx, true)
	s.enforceExpiredLicenseStops()
	ticker := time.NewTicker(licenseCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkControllerLicenseOpt(ctx, true)
			s.enforceExpiredLicenseStops()
		}
	}
}

// enforceExpiredLicenseStops stops running workloads once Expires+7d has passed.
// Workloads stay stopped until a renewed grant (later Expires) is validated.
func (s *Server) enforceExpiredLicenseStops() {
	phase, _, _, _ := s.licensePhase()
	if phase != "stopped" {
		return
	}
	s.stopAllForExpiredLicense()
}

func (s *Server) stopAllForExpiredLicense() {
	instances, err := s.Store.ListInstances()
	if err == nil {
		for _, inst := range instances {
			if inst.State != proto.StateRunning && inst.State != "RUNNING" {
				continue
			}
			op := proto.Operation{ID: inst.ID + "/" + proto.ActionStop + "/license/" + randHex(4), Action: proto.ActionStop, InstanceID: inst.ID}
			if e := s.Store.QueueOp(inst.WorkerID, op); e == nil {
				s.Store.Audit("license", "expire.stop", inst.ID, "vps")
			}
		}
	}
	if s.applications != nil {
		s.applications.Lock()
		changed := false
		for id, a := range s.applications.Managed {
			if strings.EqualFold(a.Desired, "stopped") {
				continue
			}
			a.Desired = "stopped"
			s.applications.Managed[id] = a
			changed = true
			s.Store.Audit("license", "expire.stop", id, "managed-app")
		}
		if changed {
			_ = s.applications.save()
		}
		s.applications.Unlock()
	}
	if s.docker != nil {
		s.docker.mu.Lock()
		for id, c := range s.docker.Containers {
			if strings.EqualFold(c.Desired, "stopped") || strings.EqualFold(c.State, "exited") || strings.EqualFold(c.State, "stopped") {
				continue
			}
			c.Desired = "stopped"
			s.docker.Containers[id] = c
			s.Store.Audit("license", "expire.stop", id, "docker")
		}
		_ = s.docker.save()
		s.docker.mu.Unlock()
	}
}
