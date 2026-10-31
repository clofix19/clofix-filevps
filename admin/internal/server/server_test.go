package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/pki"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

const tok = "test-admin-token-123456"

func newSrv(t *testing.T) (*Server, *store.MemoryStore) {
	if os.Getenv("CLOFIX_DATA_DIR") == "" {
		t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	}
	ca, err := pki.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := store.NewMemory()
	s := New(m, ca, tok, "")
	setTestLicense(s)
	return s, m
}

func do(h http.Handler, method, path, body, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminAuthRequired(t *testing.T) {
	s, _ := newSrv(t)
	if c := do(s.AdminHandler(), "GET", "/api/workers", "", "").Code; c != 401 {
		t.Fatalf("want 401 got %d", c)
	}
	if c := do(s.AdminHandler(), "GET", "/api/workers", "", "wrong").Code; c != 401 {
		t.Fatalf("want 401 got %d", c)
	}
	if c := do(s.AdminHandler(), "GET", "/api/workers", "", tok).Code; c != 200 {
		t.Fatalf("want 200 got %d", c)
	}
}

func TestEnrollmentTokenIsSingleUse(t *testing.T) {
	s, _ := newSrv(t)
	w := do(s.AdminHandler(), "POST", "/api/enrollment-tokens", "{}", tok)
	var out struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatal("no token returned")
	}
	if err := s.Store.ConsumeToken(hashToken(out.Token), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ConsumeToken(hashToken(out.Token), time.Now()); err == nil {
		t.Fatal("token replay must fail")
	}
}

func TestAutomaticPlacementAndRejection(t *testing.T) {
	s, m := newSrv(t)
	m.UpsertWorker(store.Worker{ID: "w1", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{
		RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	ok := `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":512,"swap_mb":0,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`
	if c := do(s.AdminHandler(), "POST", "/api/instances", ok, tok).Code; c != 201 {
		t.Fatalf("want 201 got %d", c)
	}
	ops, _ := m.TakeOps("w1")
	if len(ops) != 1 || ops[0].Action != "create" {
		t.Fatalf("expected one queued create op, got %+v", ops)
	}
	huge := `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":65536,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`
	if c := do(s.AdminHandler(), "POST", "/api/instances", huge, tok).Code; c != 409 {
		t.Fatalf("want 409 got %d", c)
	}
}

func TestHeartbeatRequiresClientCert(t *testing.T) {
	s, _ := newSrv(t)
	if c := do(s.WorkerHandler(), "POST", "/v1/heartbeat", "{}", "").Code; c != 401 {
		t.Fatalf("want 401 got %d", c)
	}
}

// Tests of individual controller features use a valid unrestricted entitlement fixture.
func setTestLicense(s *Server) {
	unlimited := -1
	s.license.EncryptedKey = []byte("test-fixture")
	s.license.LastCheck = time.Now()
	s.license.Grant = licenseGrant{InstallationID: s.license.InstallationID, Valid: true, Expires: time.Now().Add(24 * time.Hour), Plan: licensePlan{MaxDatabaseProjects: &unlimited, MaxDatabases: &unlimited, MaxDatabaseUsers: &unlimited, MaxDatabaseSchedules: &unlimited, MaxVPS: 100000, MaxWorkers: 100000, MaxLB: 100000, MaxHealthChecks: &unlimited, MaxBackups: &unlimited, MaxVolumes: &unlimited, MaxStorageGB: &unlimited, MaxS3Connections: &unlimited, MaxApplications: &unlimited, MaxManagedApps: &unlimited, MaxNotificationChannels: &unlimited, MaxIAMUsers: &unlimited, MaxIAMGroups: &unlimited, MaxIAMPolicies: &unlimited, MaxAccessKeys: &unlimited}}
}
