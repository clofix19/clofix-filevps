package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestManagedDeployCredentialsAndFencedResults(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	h := s.AdminHandler()
	_ = m.UpsertWorker(store.Worker{ID: "managed-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{ManagedTemplates: []string{"adminer"}, RAMTotal: 8 << 30, MemAvailable: 7 << 30, CPUCores: 8, DiskTotal: 20 << 30, DiskFree: 10 << 30}})
	response := do(h, "POST", "/api/managed-apps", `{"name":"Client database","template":"adminer"}`, tok)
	if response.Code != 202 {
		t.Fatalf("deploy %d: %s", response.Code, response.Body.String())
	}
	var app managedApp
	_ = json.Unmarshal(response.Body.Bytes(), &app)
	if app.ID == "" || app.URL != "http://localhost:25000/" || app.Spec.GatewayHash != "" || len(app.Credential) != 0 {
		t.Fatalf("invalid deployment response: %+v", app)
	}
	credentials := do(h, "POST", "/api/managed-apps/"+app.ID+"/credentials", `{}`, tok)
	if credentials.Code != 200 {
		t.Fatal(credentials.Body.String())
	}
	var login map[string]string
	_ = json.Unmarshal(credentials.Body.Bytes(), &login)
	if login["username"] != "client" || len(login["password"]) < 32 {
		t.Fatal("weak/missing generated credentials")
	}
	disk, _ := os.ReadFile(s.applications.file)
	if strings.Contains(string(disk), login["password"]) {
		t.Fatal("gateway password persisted in plaintext")
	}
	instances, _ := m.ListInstances()
	if len(instances) != 0 {
		t.Fatal("managed deployment created a client VPS")
	}
	ops, _ := m.TakeOps("managed-worker")
	if len(ops) != 1 || ops[0].Managed == nil || ops[0].Managed.GatewayHash != hashToken(login["password"]) {
		t.Fatal("native managed operation not queued")
	}
	result := proto.OpResult{OpID: ops[0].ID, OK: true}
	s.completeManaged("wrong-worker", result)
	if s.applications.Managed[app.ID].Pending == "" {
		t.Fatal("foreign result accepted")
	}
	s.completeManaged("managed-worker", result)
	if got := s.applications.Managed[app.ID]; got.Pending != "" || got.State != "RUNNING" {
		t.Fatal("deployment did not complete")
	}
	stopped := do(h, "POST", "/api/managed-apps/"+app.ID+"/stop", `{}`, tok)
	if stopped.Code != 202 {
		t.Fatal(stopped.Body.String())
	}
	s.completeManaged("managed-worker", result)
	if s.applications.Managed[app.ID].State != "STOPPING" {
		t.Fatal("stale deploy acknowledgement overwrote stop")
	}
	if got := do(h, "POST", "/api/managed-apps/"+app.ID+"/credentials", `{}`, ""); got.Code != 401 {
		t.Fatal("anonymous credential read")
	}
	if !iamPermissionSet["managed-apps:manage"] || permissionResource("/api/managed-apps/templates") != "managed-apps" {
		t.Fatal("managed IAM scope missing")
	}
}
func TestManagedNoUnpreparedDeployment(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	response := do(s.AdminHandler(), "POST", "/api/managed-apps", `{"name":"App","template":"adminer"}`, tok)
	if response.Code != 409 {
		t.Fatalf("missing template accepted: %d", response.Code)
	}
	if len(s.applications.Managed) != 0 {
		t.Fatal("failed placement left a phantom deployment")
	}
}
func TestManagedPublicURLRejectsInjectedHosts(t *testing.T) {
	for _, host := range []string{"https://evil.test", "evil.test/path", "user@evil.test", "javascript:alert(1)", "evil.test\nHeader:x"} {
		if managedAppURL(host, 25000) != "" {
			t.Fatal("invalid public host accepted", host)
		}
	}
}
func TestJavaApplicationServersInLibrary(t *testing.T) {
	for _, key := range []string{"wildfly", "tomcat"} {
		if !serviceCatalog[key] {
			t.Fatal("Java application server missing from catalog", key)
		}
	}
}
