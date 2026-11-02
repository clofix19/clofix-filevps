package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestBootstrapAdminDoesNotUseAdminToken(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	if do(h, "GET", "/api/workers", "", "change-this-admin-token-to-at-least-16-chars").Code != 401 {
		t.Fatal("CLOFIX_ADMIN_TOKEN must not authenticate")
	}
	if do(h, "GET", "/api/workers", "", tok).Code != 200 {
		t.Fatal("seeded admin access key should authenticate")
	}
	me := do(h, "GET", "/api/auth/me", "", tok)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"kind":"admin"`) {
		t.Fatal(me.Body.String())
	}
}

func TestRegisterLoginCookieAndMe(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"root@client.test","password":"client-password-123","account_name":"Client Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	if sessionCookieFrom(reg) == "" {
		t.Fatal("missing session cookie")
	}
	login := do(h, "POST", "/api/auth/login", `{"email":"root@client.test","password":"client-password-123"}`, "")
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	me := doCookie(h, "GET", "/api/auth/me", "", sessionCookieFrom(login))
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"kind":"root"`) {
		t.Fatal(me.Body.String())
	}
}

func TestIAMUserCannotReadOtherAccountInstance(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()
	_ = m.UpsertWorker(store.Worker{ID: "w1", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{
		RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	regA := do(h, "POST", "/api/auth/register", `{"email":"a@client.test","password":"client-password-123","account_name":"A Co"}`, "")
	regB := do(h, "POST", "/api/auth/register", `{"email":"b@client.test","password":"client-password-123","account_name":"B Co"}`, "")
	if regA.Code != 200 || regB.Code != 200 {
		t.Fatal(regA.Body.String(), regB.Body.String())
	}
	created := doCookie(h, "POST", "/api/instances", `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":512,"swap_mb":0,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`, sessionCookieFrom(regA))
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var out struct {
		Instance store.Instance `json:"instance"`
	}
	json.Unmarshal(created.Body.Bytes(), &out)
	listed := doCookie(h, "GET", "/api/instances", "", sessionCookieFrom(regB))
	if listed.Code != 200 || strings.Contains(listed.Body.String(), out.Instance.ID) {
		t.Fatal("client B must not see client A instance", listed.Body.String())
	}
	if got := doCookie(h, "POST", "/api/instances/"+out.Instance.ID+"/stop", "{}", sessionCookieFrom(regB)); got.Code != 404 {
		t.Fatal("client B must not mutate client A instance", got.Code, got.Body.String())
	}
}

func TestPartnerInvoiceScope(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	partner := do(h, "POST", "/api/accounts", `{"kind":"partner","name":"Northwind","email":"partner@co.test","password":"partner-pass-123"}`, tok)
	if partner.Code != 200 {
		t.Fatal(partner.Body.String())
	}
	login := do(h, "POST", "/api/auth/login", `{"email":"partner@co.test","password":"partner-pass-123"}`, "")
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie := sessionCookieFrom(login)
	own := doCookie(h, "POST", "/api/accounts", `{"kind":"client","name":"Own Client","email":"own@client.test","password":"client-password-123"}`, cookie)
	if own.Code != 200 {
		t.Fatal(own.Body.String())
	}
	var child struct {
		Account store.Account `json:"account"`
	}
	json.Unmarshal(own.Body.Bytes(), &child)
	other := do(h, "POST", "/api/auth/register", `{"email":"other@client.test","password":"client-password-123","account_name":"Other Co"}`, "")
	var stranger struct {
		Account store.Account `json:"account"`
	}
	json.Unmarshal(other.Body.Bytes(), &stranger)
	okInv := doCookie(h, "POST", "/api/billing/invoices", `{"owner":"`+child.Account.ID+`","description":"Due","amount_cents":500}`, cookie)
	if okInv.Code != 200 {
		t.Fatal("partner should invoice own client", okInv.Body.String())
	}
	denied := doCookie(h, "POST", "/api/billing/invoices", `{"owner":"`+stranger.Account.ID+`","description":"Due","amount_cents":500}`, cookie)
	if denied.Code != 403 {
		t.Fatal("partner must not invoice other client", denied.Code, denied.Body.String())
	}
}

func TestLoginPagesUnauthenticated(t *testing.T) {
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	login := do(h, "GET", "/login", "", "")
	if login.Code != 200 || !strings.Contains(login.Body.String(), "Sign in") {
		t.Fatal(login.Code, login.Body.String())
	}
	reg := do(h, "GET", "/register", "", "")
	if reg.Code != 200 || !strings.Contains(reg.Body.String(), "Create a client account") {
		t.Fatal(reg.Code, reg.Body.String())
	}
	home := do(h, "GET", "/", "", "")
	if home.Code != http.StatusSeeOther {
		t.Fatal("unauthenticated console should redirect", home.Code)
	}
}

func TestAdminSeedWithoutComposeToken(t *testing.T) {
	t.Setenv("CLOFIX_ADMIN_TOKEN", "")
	s, m := newSrv(t)
	if s.AdminToken != "" {
		t.Fatal("server must not keep an admin token")
	}
	admin, err := m.GetIAMUserByEmail("admin@clofix.test")
	if err != nil || admin.Kind != "admin" {
		t.Fatal(admin, err)
	}
}

func sessionCookieFrom(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func doCookie(h http.Handler, method, path, body, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
