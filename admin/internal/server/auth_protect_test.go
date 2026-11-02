package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthLoginRateLimitAndHoneypot(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"limit@client.test","password":"client-password-123","account_name":"Limit Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	honeypot := do(h, "POST", "/api/auth/login", `{"email":"limit@client.test","password":"client-password-123","website":"http://spam.example"}`, "")
	if honeypot.Code != 401 {
		t.Fatalf("honeypot must fail login, got %d %s", honeypot.Code, honeypot.Body.String())
	}
	locked := false
	for i := 0; i < authEmailFailLimit+2; i++ {
		bad := do(h, "POST", "/api/auth/login", `{"email":"limit@client.test","password":"wrong-password-xx"}`, "")
		if bad.Code == 429 {
			locked = true
			break
		}
		if bad.Code != 401 {
			t.Fatalf("attempt %d: want 401/429 got %d %s", i, bad.Code, bad.Body.String())
		}
	}
	if !locked {
		t.Fatal("expected email/IP lockout after repeated failures")
	}
	still := do(h, "POST", "/api/auth/login", `{"email":"limit@client.test","password":"client-password-123"}`, "")
	if still.Code != 429 {
		t.Fatalf("lockout must block correct password too, got %d %s", still.Code, still.Body.String())
	}

	bot := do(h, "POST", "/api/auth/register", `{"email":"bot@client.test","password":"client-password-123","account_name":"Bot Co","website":"http://spam.example"}`, "")
	if bot.Code != 200 {
		t.Fatal(bot.Body.String())
	}
	if sessionCookieFrom(bot) != "" {
		t.Fatal("honeypot register must not issue session")
	}
	if _, err := s.Store.GetIAMUserByEmail("bot@client.test"); err == nil {
		t.Fatal("honeypot must not create user")
	}

	badOrigin := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"email":"limit@client.test","password":"x"}`))
	badOrigin.Header.Set("Content-Type", "application/json")
	badOrigin.Header.Set("Origin", "https://evil.example")
	badOrigin.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, badOrigin)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login must be forbidden, got %d", w.Code)
	}
}

func TestAuthPageSecurityHeaders(t *testing.T) {
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	login := do(h, "GET", "/login", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	if login.Header().Get("X-Frame-Options") != "DENY" || login.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing auth security headers: %#v", login.Header())
	}
	if !strings.Contains(login.Body.String(), "CloFix Infotech") || !strings.Contains(login.Body.String(), "footer-clock") {
		t.Fatal("login footer missing")
	}
}
