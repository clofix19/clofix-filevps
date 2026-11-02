package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestFirewallAPIValidationIAMAndRestrict(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	_ = m.UpsertWorker(store.Worker{ID: "w-fw", Hostname: "fw-host", Status: "active"})
	_ = m.SaveInstance(store.Instance{
		ID: "vps-fw01", WorkerID: "w-fw", State: proto.StateRunning,
		Spec: proto.InstanceSpec{IPAddress: "10.231.0.20", VPCID: "vpc-default", SSHPort: 22020, PortMappings: []proto.PortMapping{{Protocol: "tcp", GuestPort: 8080, HostPort: proto.HostCustomBase + 1}}},
	})
	h := s.AdminHandler()

	if page, title, _, ok := uiPage("/firewall"); !ok || page != "firewall" || title != "Firewall" {
		t.Fatalf("firewall page: %s %s %v", page, title, ok)
	}
	if permissionResource("/api/firewall") != "security" || permissionResource("/api/firewall/workers/w-fw") != "security" {
		t.Fatal("firewall must use security IAM")
	}
	html := do(h, "GET", "/firewall", "", tok)
	if html.Code != 200 || !strings.Contains(html.Body.String(), "Worker host firewall") {
		t.Fatalf("page: %d %s", html.Code, html.Body.String())
	}
	if w := do(h, "GET", "/api/firewall", "", ""); w.Code != 401 {
		t.Fatal("missing auth", w.Code)
	}
	listed := do(h, "GET", "/api/firewall", "", tok)
	if listed.Code != 200 {
		t.Fatal(listed.Body.String())
	}
	if !strings.Contains(listed.Body.String(), `"restrict":false`) || !strings.Contains(listed.Body.String(), `"host_port":22020`) {
		t.Fatal("default allow / maps missing", listed.Body.String())
	}

	if w := do(h, "PUT", "/api/firewall/workers/w-fw", `{"restrict":true,"rules":[{"action":"allow","protocol":"tcp","port":22,"source":"0.0.0.0/0"}]}`, tok); w.Code != 400 {
		t.Fatalf("port 22: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "PUT", "/api/firewall/workers/w-fw", `{"restrict":true,"rules":[{"action":"allow","protocol":"tcp","port":22022,"source":"10.0.0.0/../8"}]}`, tok); w.Code != 400 {
		t.Fatalf("bad cidr: %d %s", w.Code, w.Body.String())
	}
	saved := do(h, "PUT", "/api/firewall/workers/w-fw", `{"restrict":true,"rules":[{"action":"allow","protocol":"tcp","port":22022,"source":"10.0.0.0/8"}]}`, tok)
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	ops, _ := m.TakeOps("w-fw")
	if len(ops) != 1 || ops[0].Action != proto.ActionUpdateFirewall || ops[0].Firewall == nil || !ops[0].Firewall.Restrict {
		t.Fatalf("queued host op: %+v", ops)
	}
	if ops[0].Firewall.Rules[0].Source != "10.0.0.0/8" {
		t.Fatal(ops[0].Firewall.Rules)
	}

	open := do(h, "PUT", "/api/firewall/workers/w-fw", `{"restrict":false,"rules":[]}`, tok)
	if open.Code != 200 {
		t.Fatal(open.Body.String())
	}
	openOps, _ := m.TakeOps("w-fw")
	if len(openOps) != 1 || openOps[0].Firewall.Restrict {
		t.Fatal("restrict=false must keep current open behavior")
	}

	inst := do(h, "PUT", "/api/firewall/instances/vps-fw01", `{"restrict":true,"rules":[{"action":"allow","protocol":"tcp","port":22,"source":"10.231.0.0/16"}]}`, tok)
	if inst.Code != 200 {
		t.Fatal(inst.Body.String())
	}
	vpsOps, _ := m.TakeOps("w-fw")
	if len(vpsOps) != 1 || vpsOps[0].InstanceID != "vps-fw01" || vpsOps[0].Firewall.GuestIP != "10.231.0.20" {
		t.Fatalf("queued vps op: %+v", vpsOps)
	}

	specs := s.firewallsForWorker("w-fw")
	if len(specs) != 2 || specs[0].Scope != "host" || specs[1].InstanceID != "vps-fw01" {
		t.Fatalf("heartbeat specs: %+v", specs)
	}

	_ = s.Store.SaveIAMPolicy(store.IAMPolicy{ID: "pol-fwread00000000", Name: "fw-read", Permissions: []string{"security:read"}})
	_ = s.Store.SaveIAMUser(store.IAMUser{ID: "usr-fwread00000000", AccountID: platformAccount, Kind: "iam", Username: "fwreader", Role: "IAM User", Status: "active", PolicyIDs: []string{"pol-fwread00000000"}})
	_ = s.Store.SaveIAMAccessKey(store.IAMAccessKey{AccessKeyID: "CFXFWREAD000001", UserID: "usr-fwread00000000", SecretHash: hashToken("secret"), Active: true})
	reader := "CFXFWREAD000001.secret"
	if w := do(h, "GET", "/api/firewall", "", reader); w.Code != 200 {
		t.Fatal("security:read GET", w.Code, w.Body.String())
	}
	if w := do(h, "PUT", "/api/firewall/workers/w-fw", `{"restrict":false,"rules":[]}`, reader); w.Code != 403 {
		t.Fatal("security:read PUT", w.Code, w.Body.String())
	}

	restored := loadFirewall(dir)
	if restored.Workers["w-fw"].Restrict {
		t.Fatal("open worker policy should persist")
	}
	if !restored.Instances["vps-fw01"].Restrict {
		t.Fatal("instance restrict missing")
	}
	var listedBody map[string]any
	if err := json.Unmarshal(listed.Body.Bytes(), &listedBody); err != nil {
		t.Fatal(err)
	}
}
