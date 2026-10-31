package server

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestLicenseQuotaZeroMissingAndConcurrentCreation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	zero := 0
	s.license.Grant.Plan.MaxHealthChecks = &zero
	body := `{"name":"HTTP","type":"http","target":"http://example.test/","enabled":true,"threshold":2}`
	if w := do(h, "POST", "/api/monitoring/monitors", body, tok); w.Code != 403 {
		t.Fatal("zero quota allowed creation", w.Code)
	}
	s.license.Grant.Plan.MaxHealthChecks = nil
	if w := do(h, "POST", "/api/monitoring/monitors", body, tok); w.Code != 403 {
		t.Fatal("unspecified quota allowed creation")
	}
	one := 1
	s.license.Grant.Plan.MaxHealthChecks = &one
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(h, "POST", "/api/monitoring/monitors", body, tok).Code }()
	}
	wg.Wait()
	close(codes)
	accepted := 0
	for c := range codes {
		if c == http.StatusOK {
			accepted++
		} else if c != 403 {
			t.Fatal("unexpected response", c)
		}
	}
	if accepted != 1 || len(s.monitoring.Monitors) != 1 {
		t.Fatal("concurrent quota bypass", accepted)
	}
	var id string
	for k := range s.monitoring.Monitors {
		id = k
	}
	s.license.Grant.Valid = false
	if w := do(h, "DELETE", "/api/monitoring/monitors/"+id, "", tok); w.Code != 200 {
		t.Fatal("cleanup blocked", w.Code)
	}
	if w := do(h, "PUT", "/api/license", `{"server_url":"http://license.example","clear":true}`, tok); w.Code != 200 {
		t.Fatal("license repair blocked")
	}
}
func TestLicenseExpiryBindingStalenessAndStorage(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	s.license.Grant.InstallationID = "different-id"
	if _, e := s.licensedPlan(); e == nil {
		t.Fatal("wrong installation accepted")
	}
	setTestLicense(s)
	s.license.Grant.Expires = time.Now().Add(-time.Second)
	if _, e := s.licensedPlan(); e == nil {
		t.Fatal("expired license accepted")
	}
	setTestLicense(s)
	s.license.LastCheck = time.Now().Add(-11 * time.Minute)
	if _, e := s.licensedPlan(); e == nil {
		t.Fatal("stale license accepted")
	}
	setTestLicense(s)
	cap := 1
	s.license.Grant.Plan.MaxStorageGB = &cap
	m.SaveInstance(store.Instance{ID: "vps-existing", Spec: proto.InstanceSpec{DiskMB: 900}})
	if e := s.checkStorage(125<<20, false); e == nil {
		t.Fatal("storage over quota accepted")
	}
	if e := s.checkStorage(100<<20, false); e != nil {
		t.Fatal(e)
	}
	if e := s.checkStorage(0, true); e == nil {
		t.Fatal("unbounded volume accepted under finite storage")
	}
	if e := s.checkStorage(-100<<20, false); e != nil {
		t.Fatal("shrinking blocked")
	}
	s.license.Grant.Plan.MaxVPS = 1
	if w := do(s.AdminHandler(), "POST", "/api/instances", `{}`, tok); w.Code != 403 {
		t.Fatal("instance quota bypass", w.Code)
	}
	s.license.Grant.Valid = false
	if w := do(s.AdminHandler(), "POST", "/api/instances/vps-existing/stop", "", tok); w.Code != 202 {
		t.Fatal("safe stop blocked", w.Code)
	}
}
func TestQuotaUsageAndPendingResourcesCount(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	s.license.Grant.Plan.MaxWorkers = 1
	m.UpsertWorker(store.Worker{ID: "worker", Status: "pending"})
	if e := s.checkQuota("workers", 1); e == nil {
		t.Fatal("pending worker not counted")
	}
	m.SetWorkerStatus("worker", "revoked")
	if e := s.checkQuota("workers", 1); e != nil {
		t.Fatal(e)
	}
	one := 1
	s.license.Grant.Plan.MaxBackups = &one
	s.backups["backup"] = proto.BackupInfo{ID: "backup", State: "CREATING"}
	if e := s.checkQuota("backups", 1); e == nil {
		t.Fatal("pending backup not counted")
	}
	w := do(s.AdminHandler(), "GET", "/api/license/usage", "", tok)
	if w.Code != 200 {
		t.Fatal(fmt.Sprint(w.Code), w.Body.String())
	}
}
