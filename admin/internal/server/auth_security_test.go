package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

func TestTOTPAndLoginIPAndMFA(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil || secret == "" {
		t.Fatal(err)
	}
	code, err := totpCode(secret, time.Now().UTC())
	if err != nil || !verifyTOTP(secret, code, time.Now().UTC()) {
		t.Fatal("totp verify failed", err, code)
	}
	user := store.IAMUser{AllowIPs: []string{"203.0.113.10"}, DenyIPs: []string{"198.51.100.0/24"}}
	if loginIPAllowed(user, "198.51.100.20") == nil {
		t.Fatal("deny CIDR ignored")
	}
	if loginIPAllowed(user, "203.0.113.11") == nil {
		t.Fatal("allow list ignored")
	}
	if err := loginIPAllowed(user, "203.0.113.10"); err != nil {
		t.Fatal(err)
	}

	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"mfa@client.test","password":"client-password-123","account_name":"MFA Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)
	setup := doCookie(h, "POST", "/api/profile/mfa/setup", `{}`, cookie)
	if setup.Code != 200 {
		t.Fatal(setup.Body.String())
	}
	var secretOut struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(setup.Body.Bytes(), &secretOut)
	enableCode, _ := totpCode(secretOut.Secret, time.Now().UTC())
	if enabled := doCookie(h, "POST", "/api/profile/mfa/enable", `{"code":"`+enableCode+`"}`, cookie); enabled.Code != 200 {
		t.Fatal(enabled.Body.String())
	}
	userRec, _ := m.GetIAMUserByEmail("mfa@client.test")
	userRec.DenyIPs = []string{"203.0.113.0/24"}
	_ = m.SaveIAMUser(userRec)

	login := do(h, "POST", "/api/auth/login", `{"email":"mfa@client.test","password":"client-password-123"}`, "")
	if login.Code != 200 || !strings.Contains(login.Body.String(), `"mfa_required":true`) {
		t.Fatal("expected MFA challenge", login.Body.String())
	}
	var challenge struct {
		Token string `json:"mfa_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &challenge)
	verifyCode, _ := totpCode(secretOut.Secret, time.Now().UTC())
	done := do(h, "POST", "/api/auth/login/mfa", `{"mfa_token":"`+challenge.Token+`","code":"`+verifyCode+`"}`, "")
	if done.Code != 200 || sessionCookieFrom(done) == "" {
		t.Fatal(done.Code, done.Body.String())
	}
	profile := doCookie(h, "GET", "/api/profile", "", sessionCookieFrom(done))
	if profile.Code != 200 || !strings.Contains(profile.Body.String(), `"mfa_enabled":true`) {
		t.Fatal(profile.Body.String())
	}
	if page, title, _, ok := uiPage("/profile"); !ok || page != "profile" || title != "My Profile" {
		t.Fatal("profile page missing")
	}
	if got := do(h, "GET", "/profile", "", ""); got.Code != http.StatusSeeOther {
		t.Fatalf("profile must require auth, got %d", got.Code)
	}
}
