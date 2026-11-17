package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestControllerLicenseConnectionEncryptionAndRevocation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	key := "CFL-controller-test-key-never-store-plain"
	healthLimit := 25
	backupLimit := 0
	channelLimit := -1
	databaseProjects := 4
	databases := 20
	databaseUsers := 40
	databaseSchedules := 15
	var revoked atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/validate" {
			t.Error("wrong validation endpoint")
		}
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["key"] != key || in["installation_id"] == "" {
			t.Error("missing validation credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if revoked.Load() {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(licenseGrant{Error: "license revoked"})
			return
		}
		json.NewEncoder(w).Encode(licenseGrant{InstallationID: in["installation_id"], DurationDays: 30, Valid: true, LicenseID: "lic-test", Customer: "Customer", Plan: licensePlan{ID: "plan-test", Name: "Basic", MaxVPS: 10, MaxDatabaseProjects: &databaseProjects, MaxDatabases: &databases, MaxDatabaseUsers: &databaseUsers, MaxDatabaseSchedules: &databaseSchedules, MaxHealthChecks: &healthLimit, MaxBackups: &backupLimit, MaxNotificationChannels: &channelLimit}, Expires: time.Now().Add(24 * time.Hour)})
	}))
	defer remote.Close()
	s, _ := newSrv(t)
	h := s.AdminHandler()
	payload, _ := json.Marshal(map[string]string{"server_url": remote.URL, "key": key})
	if w := do(h, "PUT", "/api/license", string(payload), tok); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := do(h, "POST", "/api/license/validate", "", tok); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if s.license.Grant.DurationDays != 30 || !s.license.Grant.Valid || s.license.Grant.Plan.MaxVPS != 10 || s.license.Grant.Plan.MaxDatabaseProjects == nil || *s.license.Grant.Plan.MaxDatabaseProjects != 4 || s.license.Grant.Plan.MaxDatabases == nil || *s.license.Grant.Plan.MaxDatabases != 20 || s.license.Grant.Plan.MaxDatabaseUsers == nil || *s.license.Grant.Plan.MaxDatabaseUsers != 40 || s.license.Grant.Plan.MaxDatabaseSchedules == nil || *s.license.Grant.Plan.MaxDatabaseSchedules != 15 || s.license.Grant.Plan.MaxHealthChecks == nil || *s.license.Grant.Plan.MaxHealthChecks != 25 || s.license.Grant.Plan.MaxBackups == nil || *s.license.Grant.Plan.MaxBackups != 0 || s.license.Grant.Plan.MaxNotificationChannels == nil || *s.license.Grant.Plan.MaxNotificationChannels != -1 {
		t.Fatal("grant missing")
	}
	raw, _ := os.ReadFile(s.license.file)
	if strings.Contains(string(raw), key) {
		t.Fatal("license not encrypted")
	}
	w := do(h, "GET", "/api/license", "", tok)
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "encrypted_key") {
		t.Fatal("key exposed")
	}
	id := s.license.InstallationID
	reloaded := loadControllerLicense(strings.TrimSuffix(s.license.file, "/license.json"))
	if reloaded.InstallationID != id {
		t.Fatal("activation identity lost")
	}
	revoked.Store(true)
	if w := do(h, "POST", "/api/license/validate", "", tok); w.Code != 502 || s.license.Grant.Valid {
		t.Fatal("revoked license cached as valid")
	}
	if permissionResource("/api/license") != "license" {
		t.Fatal("wrong IAM scope")
	}
	if w := do(h, "PUT", "/api/license", string(payload), ""); w.Code != 401 {
		t.Fatal("no authentication")
	}
}

func TestControllerRejectsLicenseIDAndConflictingRemoval(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.license.EncryptedKey = nil
	h := s.AdminHandler()
	for _, payload := range []string{`{"server_url":"http://license.example","key":"lic-record-id"}`, `{"server_url":"http://license.example","key":"CFL-test-key","clear":true}`} {
		w := do(h, "PUT", "/api/license", payload, tok)
		if w.Code != 400 || len(s.license.EncryptedKey) != 0 {
			t.Fatal("invalid input changed saved license", w.Code)
		}
	}
}

func TestLicenseOfflineRetainAndExpireStop(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	key := "CFL-offline-retain-test-key-0123456789abcdef"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		json.NewEncoder(w).Encode(licenseGrant{
			InstallationID: in["installation_id"], DurationDays: 30, Valid: true, LicenseID: "lic-off",
			Customer: "Customer", Plan: licensePlan{ID: "plan-off", Name: "Basic", MaxVPS: 10, MaxWorkers: 3, MaxLB: 5},
			Expires: time.Now().Add(24 * time.Hour),
		})
	}))
	s, m := newSrv(t)
	h := s.AdminHandler()
	payload, _ := json.Marshal(map[string]string{"server_url": remote.URL, "key": key})
	if w := do(h, "PUT", "/api/license", string(payload), tok); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := do(h, "POST", "/api/license/validate", "", tok); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	synced := s.license.LastCheck
	remote.Close()
	s.license.ServerURL = "http://127.0.0.1:1"
	if err := s.checkControllerLicenseOpt(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !s.license.LastCheck.Equal(synced) {
		t.Fatalf("last sync must stay %v got %v", synced, s.license.LastCheck)
	}
	if s.license.Grant.Plan.Name != "Basic" || s.license.Grant.DurationDays != 30 {
		t.Fatal("cached plan/duration lost while offline")
	}
	if phase, _, _, offline := s.licensePhase(); phase != "active" || !offline {
		t.Fatalf("want active offline, got phase=%s offline=%v status=%q", phase, offline, s.license.Status)
	}
	if _, err := s.licensedPlan(); err != nil {
		t.Fatal("creates must work offline before expiry:", err)
	}
	s.license.Grant.Expires = time.Now().Add(-time.Minute)
	if _, err := s.licensedPlan(); err == nil {
		t.Fatal("expired license must block creates")
	}
	if phase, _, _, _ := s.licensePhase(); phase != "expired" {
		t.Fatalf("want expired got %s", phase)
	}
	s.license.Grant.Expires = time.Now().Add(-(licenseStopGrace + time.Hour))
	if phase, _, _, _ := s.licensePhase(); phase != "stopped" {
		t.Fatalf("want stopped got %s", phase)
	}
	m.SaveInstance(store.Instance{ID: "vps-lic", WorkerID: "w1", State: proto.StateRunning, Spec: proto.InstanceSpec{Hostname: "lic"}})
	m.UpsertWorker(store.Worker{ID: "w1", Status: "active", LastHeartbeat: time.Now()})
	s.enforceExpiredLicenseStops()
	ops, _ := m.TakeOps("w1")
	found := false
	for _, op := range ops {
		if op.Action == proto.ActionStop && op.InstanceID == "vps-lic" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected license expiry stop op, got %+v", ops)
	}
}
