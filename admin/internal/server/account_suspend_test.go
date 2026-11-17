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

func TestAdminSuspendRootCascadesIAMAndBlocksLogin(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-suspend@client.test","password":"client-password-123","account_name":"Suspend Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	var regOut struct {
		Account store.Account `json:"account"`
		User    store.IAMUser `json:"user"`
	}
	if err := json.Unmarshal(reg.Body.Bytes(), &regOut); err != nil || regOut.Account.ID == "" {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)
	iam := doCookie(h, "POST", "/api/iam/users", `{"username":"ops-suspend","email":"ops-suspend@client.test","password":"iam-password-123","role":"IAM User","status":"active","group_ids":[],"policy_ids":[]}`, cookie)
	if iam.Code != 200 {
		t.Fatal(iam.Body.String())
	}
	var iamUser store.IAMUser
	if err := json.Unmarshal(iam.Body.Bytes(), &iamUser); err != nil || iamUser.ID == "" {
		t.Fatal(iam.Body.String())
	}
	sus := do(h, "POST", "/api/accounts/"+regOut.Account.ID+"/suspend", "", tok)
	if sus.Code != 200 || !strings.Contains(sus.Body.String(), `"status":"suspended"`) {
		t.Fatal(sus.Code, sus.Body.String())
	}
	acc, err := m.GetAccount(regOut.Account.ID)
	if err != nil || acc.Status != "suspended" {
		t.Fatalf("account status: %+v %v", acc, err)
	}
	root, _ := m.GetIAMUser(regOut.User.ID)
	child, _ := m.GetIAMUser(iamUser.ID)
	if root.Status != "inactive" || child.Status != "inactive" {
		t.Fatalf("expected cascade inactive, root=%s iam=%s", root.Status, child.Status)
	}
	login := do(h, "POST", "/api/auth/login", `{"email":"root-suspend@client.test","password":"client-password-123"}`, "")
	if login.Code != 401 && login.Code != 403 {
		t.Fatalf("suspended root must not login: %d %s", login.Code, login.Body.String())
	}
	iamLogin := do(h, "POST", "/api/auth/login", `{"email":"ops-suspend@client.test","password":"iam-password-123"}`, "")
	if iamLogin.Code != 401 && iamLogin.Code != 403 {
		t.Fatalf("suspended IAM must not login: %d %s", iamLogin.Code, iamLogin.Body.String())
	}
	me := doCookie(h, "GET", "/api/auth/me", "", cookie)
	if me.Code != 401 {
		t.Fatalf("existing session must die after suspend: %d %s", me.Code, me.Body.String())
	}
	resumeIAM := do(h, "POST", "/api/iam/users/"+iamUser.ID+"/resume", "", tok)
	if resumeIAM.Code != 409 {
		t.Fatalf("cannot resume IAM while account suspended: %d %s", resumeIAM.Code, resumeIAM.Body.String())
	}
	res := do(h, "POST", "/api/accounts/"+regOut.Account.ID+"/resume", "", tok)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"status":"active"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	root, _ = m.GetIAMUser(regOut.User.ID)
	child, _ = m.GetIAMUser(iamUser.ID)
	if root.Status != "active" {
		t.Fatalf("root should resume active, got %s", root.Status)
	}
	if child.Status != "inactive" {
		t.Fatalf("IAM should stay inactive until re-enabled, got %s", child.Status)
	}
	okLogin := do(h, "POST", "/api/auth/login", `{"email":"root-suspend@client.test","password":"client-password-123"}`, "")
	if okLogin.Code != 200 {
		t.Fatal(okLogin.Body.String())
	}
}

func TestAdminSuspendIAMUserOnly(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-iam@client.test","password":"client-password-123","account_name":"IAM Suspend Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)
	iam := doCookie(h, "POST", "/api/iam/users", `{"username":"ops-only","email":"ops-only@client.test","password":"iam-password-123","role":"IAM User","status":"active","group_ids":[],"policy_ids":[]}`, cookie)
	if iam.Code != 200 {
		t.Fatal(iam.Body.String())
	}
	var iamUser store.IAMUser
	json.Unmarshal(iam.Body.Bytes(), &iamUser)
	sus := do(h, "POST", "/api/iam/users/"+iamUser.ID+"/suspend", "", tok)
	if sus.Code != 200 {
		t.Fatal(sus.Body.String())
	}
	got, _ := m.GetIAMUser(iamUser.ID)
	if got.Status != "inactive" {
		t.Fatalf("expected inactive, got %s", got.Status)
	}
	login := do(h, "POST", "/api/auth/login", `{"email":"ops-only@client.test","password":"iam-password-123"}`, "")
	if login.Code != 401 {
		t.Fatalf("suspended IAM login: %d %s", login.Code, login.Body.String())
	}
	rootLogin := do(h, "POST", "/api/auth/login", `{"email":"root-iam@client.test","password":"client-password-123"}`, "")
	if rootLogin.Code != 200 {
		t.Fatal("root should still login", rootLogin.Body.String())
	}
	en := do(h, "POST", "/api/iam/users/"+iamUser.ID+"/resume", "", tok)
	if en.Code != 200 {
		t.Fatal(en.Body.String())
	}
}

func TestAdminStopAccountServices(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()
	_ = m.UpsertWorker(store.Worker{ID: "w-stop", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{
		RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-stop@client.test","password":"client-password-123","account_name":"Stop Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	var regOut struct {
		Account store.Account `json:"account"`
	}
	json.Unmarshal(reg.Body.Bytes(), &regOut)
	cookie := sessionCookieFrom(reg)
	created := doCookie(h, "POST", "/api/instances", `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":512,"swap_mb":0,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`, cookie)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Instance store.Instance `json:"instance"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	inst, _ := m.GetInstance(out.Instance.ID)
	inst.State = proto.StateRunning
	_ = m.SaveInstance(inst)
	stop := do(h, "POST", "/api/accounts/"+regOut.Account.ID+"/stop-services", "", tok)
	if stop.Code != 200 || !strings.Contains(stop.Body.String(), `"vps":1`) {
		t.Fatal(stop.Code, stop.Body.String())
	}
	ops, _ := m.TakeOps(inst.WorkerID)
	found := false
	for _, op := range ops {
		if op.InstanceID == inst.ID && op.Action == proto.ActionStop {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stop op queued for %s on worker %s, got %+v", inst.ID, inst.WorkerID, ops)
	}
}
