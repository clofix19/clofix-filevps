package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestVPCDefaultLockOverlapAndAllocation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	if page, title, _, ok := uiPage("/vpc"); !ok || page != "vpc" || title != "VPC" {
		t.Fatalf("vpc page route missing: %s %s %v", page, title, ok)
	}
	if permissionResource("/api/vpcs") != "vpc" || permissionResource("/api/vpcs/vpc-default") != "vpc" {
		t.Fatal("vpc must be its own IAM resource")
	}
	h := s.AdminHandler()
	listed := do(h, "GET", "/api/vpcs", "", tok)
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), `"id":"vpc-default"`) || !strings.Contains(listed.Body.String(), `"cidr":"10.231.0.0/16"`) {
		t.Fatal(listed.Body.String())
	}
	if got := do(h, "DELETE", "/api/vpcs/vpc-default", "", tok); got.Code != 409 {
		t.Fatalf("delete default: %d %s", got.Code, got.Body.String())
	}
	if got := do(h, "PUT", "/api/vpcs/vpc-default", `{"name":"Default VPC","cidr":"10.231.20.0/24"}`, tok); got.Code != 409 {
		t.Fatalf("change default cidr: %d %s", got.Code, got.Body.String())
	}
	created := do(h, "POST", "/api/vpcs", `{"name":"Apps","cidr":"10.231.20.0/24"}`, tok)
	if created.Code != 201 {
		t.Fatalf("create extra: %d %s", created.Code, created.Body.String())
	}
	var extra vpcView
	if json.Unmarshal(created.Body.Bytes(), &extra) != nil || extra.ID == "" || extra.CIDR != "10.231.20.0/24" || extra.Gateway != "10.231.0.1" {
		t.Fatalf("extra vpc: %+v", extra)
	}
	if got := do(h, "POST", "/api/vpcs", `{"name":"Overlap","cidr":"10.231.20.0/24"}`, tok); got.Code != 409 {
		t.Fatalf("overlap: %d %s", got.Code, got.Body.String())
	}
	if got := do(h, "POST", "/api/vpcs", `{"name":"Outside","cidr":"10.20.0.0/16"}`, tok); got.Code != 400 {
		t.Fatalf("outside 10.231/16: %d %s", got.Code, got.Body.String())
	}
	removed := do(h, "DELETE", "/api/vpcs/"+extra.ID, "", tok)
	if removed.Code != 200 {
		t.Fatalf("delete unused extra: %d %s", removed.Code, removed.Body.String())
	}
	created = do(h, "POST", "/api/vpcs", `{"name":"Apps","cidr":"10.231.20.0/24"}`, tok)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	json.Unmarshal(created.Body.Bytes(), &extra)
	m.UpsertWorker(store.Worker{ID: "w1", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{
		RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	ok := `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":512,"swap_mb":0,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`
	first := do(h, "POST", "/api/instances", ok, tok)
	if first.Code != 201 {
		t.Fatalf("default alloc: %d %s", first.Code, first.Body.String())
	}
	var createdInst struct {
		Instance store.Instance `json:"instance"`
	}
	json.Unmarshal(first.Body.Bytes(), &createdInst)
	if createdInst.Instance.Spec.IPAddress != "10.231.0.2" || createdInst.Instance.Spec.VPCID != vpcDefaultID {
		t.Fatalf("default VPS want 10.231.0.2 vpc-default, got %+v", createdInst.Instance.Spec)
	}
	extraBody := `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":512,"swap_mb":0,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB user@test","vpc_id":"` + extra.ID + `"}}`
	second := do(h, "POST", "/api/instances", extraBody, tok)
	if second.Code != 201 {
		t.Fatalf("extra alloc: %d %s", second.Code, second.Body.String())
	}
	json.Unmarshal(second.Body.Bytes(), &createdInst)
	if createdInst.Instance.Spec.IPAddress != "10.231.20.2" || createdInst.Instance.Spec.VPCID != extra.ID {
		t.Fatalf("extra VPS want 10.231.20.2, got %+v", createdInst.Instance.Spec)
	}
	if got := do(h, "DELETE", "/api/vpcs/"+extra.ID, "", tok); got.Code != 409 {
		t.Fatalf("delete in-use: %d %s", got.Code, got.Body.String())
	}
}

func TestReserveNetworkSkipsExtraSubnet(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.vpcs.Lock()
	s.vpcs.ensure()
	s.vpcs.VPCs["vpc-apps"] = vpc{ID: "vpc-apps", Name: "Apps", CIDR: "10.231.20.0/24", Gateway: vpcDefaultGW, Created: time.Now().UTC(), Updated: time.Now().UTC()}
	s.vpcs.Unlock()
	spec := proto.InstanceSpec{}
	if !s.reserveNetwork(&spec, nil) || spec.IPAddress != "10.231.0.2" || spec.VPCID != vpcDefaultID {
		t.Fatalf("empty vpc_id want 10.231.0.2, got %+v", spec)
	}
	used := []store.Instance{{Spec: proto.InstanceSpec{IPAddress: "10.231.0.2", SSHPort: 22000}}}
	spec = proto.InstanceSpec{}
	if !s.reserveNetwork(&spec, used) || spec.IPAddress != "10.231.0.3" {
		t.Fatalf("next default IP should skip extra 10.231.20.0/24, got %+v", spec)
	}
	spec = proto.InstanceSpec{VPCID: "vpc-apps"}
	if !s.reserveNetwork(&spec, used) || spec.IPAddress != "10.231.20.2" {
		t.Fatalf("extra vpc alloc: %+v", spec)
	}
}
