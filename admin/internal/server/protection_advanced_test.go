package server

import (
	"encoding/json"
	"testing"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestProtectionCatalogStatuses(t *testing.T) {
	cat := advancedProtectionCatalog()
	if len(cat) < 70 {
		t.Fatalf("catalog too small: %d", len(cat))
	}
	seen := map[int]bool{}
	for _, f := range cat {
		if seen[f.ID] {
			t.Fatalf("duplicate feature id %d", f.ID)
		}
		seen[f.ID] = true
		switch f.Status {
		case "live", "partial", "planned", "satisfied_by_ro":
		default:
			t.Fatalf("bad status %q for %d", f.Status, f.ID)
		}
	}
}

func TestFileVPSPostureSatisfiedByRO(t *testing.T) {
	s, _ := newSrv(t)
	on := true
	_ = s.Store.SaveInstance(store.Instance{
		ID: "vps-prot01", WorkerID: "w1", State: proto.StateRunning,
		Spec: proto.InstanceSpec{ProtectionMode: &on, SSHAuthorizedKey: "ssh-ed25519 AAAA"},
	})
	inst, _ := s.Store.GetInstance("vps-prot01")
	rows := s.fileVPSProtectionPosture([]store.Instance{inst}, nil)
	if len(rows) != 1 || rows[0]["root_ro"] != true || rows[0]["package_install_locked"] != true {
		t.Fatalf("%v", rows)
	}
}

func TestProtectionCatalogAPI(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	w := do(h, "GET", "/api/security/protection-catalog", "", tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var body struct {
		Features []protectionFeature `json:"features"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Features) < 70 {
		t.Fatalf("%s", w.Body.String())
	}
}
