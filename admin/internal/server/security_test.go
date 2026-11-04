package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestComputeSecurityScoreFormula(t *testing.T) {
	off := false
	inst := store.Instance{ID: "vps-a", Spec: proto.InstanceSpec{ProtectionMode: &off, PortMappings: []proto.PortMapping{{GuestPort: 8080, HostPort: 37001}}}}
	wk := store.Worker{ID: "w1", Status: "draining", Capacity: scheduler.Capacity{RAMTotal: 1 << 30, UserNS: false}}
	active := store.Worker{ID: "w2", Status: "active", Capacity: scheduler.Capacity{RAMTotal: 1 << 30, UserNS: false}}
	score := computeSecurityScore([]store.Instance{inst}, []store.Worker{wk, active}, map[string]map[string]string{}, nil)
	// protection -4, delete -1, http waf -5, extra port -3, inactive -10, user_ns -15 = 62
	if score.Score != 62 {
		t.Fatalf("score=%d findings=%v", score.Score, score.Findings)
	}
}

func TestListAuditAndSecurityOverview(t *testing.T) {
	s, m := newSrv(t)
	_ = m.Audit("admin", "test.action", "target-1", "ok")
	h := s.AdminHandler()

	if permissionResource("/api/audit") != "security" || permissionResource("/api/security/overview") != "security" {
		t.Fatal("audit/security must map to security IAM")
	}
	if w := do(h, "GET", "/api/audit?limit=10", "", ""); w.Code != 401 {
		t.Fatalf("want 401 got %d", w.Code)
	}
	listed := do(h, "GET", "/api/audit?limit=10", "", tok)
	if listed.Code != 200 {
		t.Fatal(listed.Body.String())
	}
	var audit struct {
		Events []store.AuditEntry `json:"events"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &audit); err != nil || len(audit.Events) < 1 {
		t.Fatalf("audit body: %s err=%v", listed.Body.String(), err)
	}

	overview := do(h, "GET", "/api/security/overview", "", tok)
	if overview.Code != 200 {
		t.Fatal(overview.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(overview.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["score"]; !ok {
		t.Fatalf("missing score: %v", body)
	}
	if _, ok := body["findings"]; !ok {
		t.Fatalf("missing findings: %v", body)
	}
}

func TestSecurityModesDryRun(t *testing.T) {
	s, m := newSrv(t)
	on := true
	_ = m.SaveInstance(store.Instance{
		ID: "vps-mode01", WorkerID: "w-mode", State: proto.StateRunning,
		Spec: proto.InstanceSpec{ProtectionMode: &on}, Created: time.Now(),
	})
	_ = m.UpsertWorker(store.Worker{ID: "w-mode", Status: "active", Capacity: scheduler.Capacity{RAMTotal: 1 << 30, UserNS: true}})
	h := s.AdminHandler()
	w := do(h, "POST", "/api/security/modes/tpl-lockdown/dry-run", `{"instance_ids":["vps-mode01"]}`, tok)
	if w.Code != 200 {
		t.Fatalf("dry-run %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Changes []map[string]any `json:"changes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Changes) == 0 {
		t.Fatalf("changes: %s", w.Body.String())
	}
}
