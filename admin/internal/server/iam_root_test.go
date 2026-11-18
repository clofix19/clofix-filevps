package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/clofix/filevps/admin/internal/store"
)

func TestRootIAMSelfRestrictedAndOwnAccountUsers(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-iam-self@client.test","password":"client-password-123","account_name":"Root IAM Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	var regOut struct {
		Account store.Account `json:"account"`
		User    store.IAMUser `json:"user"`
	}
	if err := json.Unmarshal(reg.Body.Bytes(), &regOut); err != nil || regOut.User.ID == "" {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)

	// Cannot edit/suspend/delete own root login via IAM APIs.
	if got := doCookie(h, "PUT", "/api/iam/users/"+regOut.User.ID, `{"username":"hijacked","email":"root-iam-self@client.test","role":"Full Administrator","status":"active","group_ids":[],"policy_ids":[]}`, cookie); got.Code != 404 {
		t.Fatalf("root cannot edit self via IAM: %d %s", got.Code, got.Body.String())
	}
	if got := doCookie(h, "POST", "/api/iam/users/"+regOut.User.ID+"/suspend", "", cookie); got.Code != 400 && got.Code != 403 {
		t.Fatalf("root cannot suspend self: %d %s", got.Code, got.Body.String())
	}
	if got := doCookie(h, "DELETE", "/api/iam/users/"+regOut.User.ID, "", cookie); got.Code != 409 {
		t.Fatalf("root cannot delete self: %d %s", got.Code, got.Body.String())
	}

	// Can create IAM user, group, policy, and access key for own account.
	pol := doCookie(h, "POST", "/api/iam/policies", `{"name":"VPSOps","description":"own","permissions":["instances:manage"]}`, cookie)
	if pol.Code != 200 {
		t.Fatal(pol.Body.String())
	}
	var policy store.IAMPolicy
	if err := json.Unmarshal(pol.Body.Bytes(), &policy); err != nil || policy.ID == "" {
		t.Fatal(pol.Body.String())
	}
	grp := doCookie(h, "POST", "/api/iam/groups", `{"name":"Operators","description":"ops","policy_ids":["`+policy.ID+`"]}`, cookie)
	if grp.Code != 200 {
		t.Fatal(grp.Body.String())
	}
	var group store.IAMGroup
	if err := json.Unmarshal(grp.Body.Bytes(), &group); err != nil || group.ID == "" {
		t.Fatal(grp.Body.String())
	}
	iam := doCookie(h, "POST", "/api/iam/users", `{"username":"deploy-ops","email":"deploy-ops@client.test","password":"iam-password-123","role":"IAM User","status":"active","group_ids":["`+group.ID+`"],"policy_ids":[]}`, cookie)
	if iam.Code != 200 {
		t.Fatal(iam.Body.String())
	}
	var iamUser store.IAMUser
	if err := json.Unmarshal(iam.Body.Bytes(), &iamUser); err != nil || iamUser.ID == "" || iamUser.AccountID != regOut.Account.ID {
		t.Fatalf("iam user: %s", iam.Body.String())
	}

	keySelf := doCookie(h, "POST", "/api/iam/users/"+regOut.User.ID+"/keys", `{}`, cookie)
	if keySelf.Code != 201 || !strings.Contains(keySelf.Body.String(), "access_key_id") {
		t.Fatalf("root may generate own access key: %d %s", keySelf.Code, keySelf.Body.String())
	}
	keyIAM := doCookie(h, "POST", "/api/iam/users/"+iamUser.ID+"/keys", `{}`, cookie)
	if keyIAM.Code != 201 {
		t.Fatalf("root may generate IAM user key: %d %s", keyIAM.Code, keyIAM.Body.String())
	}

	// Can suspend/edit/delete own-account IAM users.
	if got := doCookie(h, "POST", "/api/iam/users/"+iamUser.ID+"/suspend", "", cookie); got.Code != 200 {
		t.Fatalf("suspend iam: %d %s", got.Code, got.Body.String())
	}
	if got := doCookie(h, "POST", "/api/iam/users/"+iamUser.ID+"/resume", "", cookie); got.Code != 200 {
		t.Fatalf("resume iam: %d %s", got.Code, got.Body.String())
	}
	if got := doCookie(h, "DELETE", "/api/iam/users/"+iamUser.ID, "", cookie); got.Code != 200 {
		t.Fatalf("delete iam: %d %s", got.Code, got.Body.String())
	}
}

func TestIAMAccessKeySuspendAndDelete(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-keys@client.test","password":"client-password-123","account_name":"Keys Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)
	iam := doCookie(h, "POST", "/api/iam/users", `{"username":"key-ops","email":"key-ops@client.test","password":"iam-password-123","role":"IAM User","status":"active","group_ids":[],"policy_ids":[]}`, cookie)
	if iam.Code != 200 {
		t.Fatal(iam.Body.String())
	}
	var iamUser store.IAMUser
	if err := json.Unmarshal(iam.Body.Bytes(), &iamUser); err != nil || iamUser.ID == "" {
		t.Fatal(iam.Body.String())
	}
	created := doCookie(h, "POST", "/api/iam/users/"+iamUser.ID+"/keys", `{}`, cookie)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var key struct {
		AccessKeyID string `json:"access_key_id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil || key.AccessKeyID == "" {
		t.Fatal(created.Body.String())
	}
	sus := doCookie(h, "POST", "/api/iam/users/"+iamUser.ID+"/keys/"+key.AccessKeyID+"/suspend", "", cookie)
	if sus.Code != 200 || !strings.Contains(sus.Body.String(), `"active":false`) {
		t.Fatalf("suspend key: %d %s", sus.Code, sus.Body.String())
	}
	res := doCookie(h, "POST", "/api/iam/users/"+iamUser.ID+"/keys/"+key.AccessKeyID+"/resume", "", cookie)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"active":true`) {
		t.Fatalf("resume key: %d %s", res.Code, res.Body.String())
	}
	del := doCookie(h, "DELETE", "/api/iam/users/"+iamUser.ID+"/keys/"+key.AccessKeyID, "", cookie)
	if del.Code != 200 {
		t.Fatalf("delete key: %d %s", del.Code, del.Body.String())
	}
}

func TestOwnKeysAllowedButOwnUserSuspendDeleteBlocked(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-ownkey@client.test","password":"client-password-123","account_name":"Own Key Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	var out struct {
		User store.IAMUser `json:"user"`
	}
	if err := json.Unmarshal(reg.Body.Bytes(), &out); err != nil || out.User.ID == "" {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)
	created := doCookie(h, "POST", "/api/iam/users/"+out.User.ID+"/keys", `{}`, cookie)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var key struct {
		AccessKeyID string `json:"access_key_id"`
	}
	json.Unmarshal(created.Body.Bytes(), &key)
	if key.AccessKeyID == "" {
		t.Fatal(created.Body.String())
	}
	if sus := doCookie(h, "POST", "/api/iam/users/"+out.User.ID+"/keys/"+key.AccessKeyID+"/suspend", "", cookie); sus.Code != 200 {
		t.Fatalf("own key suspend should work: %d %s", sus.Code, sus.Body.String())
	}
	if res := doCookie(h, "POST", "/api/iam/users/"+out.User.ID+"/keys/"+key.AccessKeyID+"/resume", "", cookie); res.Code != 200 {
		t.Fatalf("own key resume: %d %s", res.Code, res.Body.String())
	}
	if del := doCookie(h, "DELETE", "/api/iam/users/"+out.User.ID+"/keys/"+key.AccessKeyID, "", cookie); del.Code != 200 {
		t.Fatalf("own key delete should work: %d %s", del.Code, del.Body.String())
	}
	if got := doCookie(h, "POST", "/api/iam/users/"+out.User.ID+"/suspend", "", cookie); got.Code != 400 && got.Code != 403 {
		t.Fatalf("own user suspend blocked: %d %s", got.Code, got.Body.String())
	}
	if got := doCookie(h, "DELETE", "/api/iam/users/"+out.User.ID, "", cookie); got.Code != 409 && got.Code != 403 {
		t.Fatalf("own user delete blocked: %d %s", got.Code, got.Body.String())
	}
}

func TestRootCanListWorkersForPlacement(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()
	_ = m.UpsertWorker(store.Worker{ID: "wrk-place", Hostname: "place-1", Status: "active"})
	reg := do(h, "POST", "/api/auth/register", `{"email":"root-place@client.test","password":"client-password-123","account_name":"Place Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)
	list := doCookie(h, "GET", "/api/workers", "", cookie)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "wrk-place") {
		t.Fatalf("root needs workers for placement: %d %s", list.Code, list.Body.String())
	}
	if got := doCookie(h, "POST", "/api/workers/wrk-place/drain", "", cookie); got.Code != 403 {
		t.Fatalf("root cannot drain workers: %d %s", got.Code, got.Body.String())
	}
	if got := doCookie(h, "POST", "/api/enrollment-tokens", `{"ttl_minutes":60}`, cookie); got.Code != 403 {
		t.Fatalf("root cannot enroll workers: %d %s", got.Code, got.Body.String())
	}
}
