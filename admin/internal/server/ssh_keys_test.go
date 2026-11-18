package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSSHKeysAreAccountScoped(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()

	adminKey := do(h, "POST", "/api/ssh-keys", `{"name":"admin-laptop"}`, tok)
	if adminKey.Code != 201 {
		t.Fatal(adminKey.Body.String())
	}
	var adminOut struct {
		Key struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
		} `json:"key"`
	}
	if err := json.Unmarshal(adminKey.Body.Bytes(), &adminOut); err != nil || adminOut.Key.ID == "" {
		t.Fatal(adminKey.Body.String())
	}

	regA := do(h, "POST", "/api/auth/register", `{"email":"pem-a@client.test","password":"client-password-123","account_name":"PEM A"}`, "")
	regB := do(h, "POST", "/api/auth/register", `{"email":"pem-b@client.test","password":"client-password-123","account_name":"PEM B"}`, "")
	if regA.Code != 200 || regB.Code != 200 {
		t.Fatalf("register: %s %s", regA.Body.String(), regB.Body.String())
	}
	cookieA, cookieB := sessionCookieFrom(regA), sessionCookieFrom(regB)

	createA := doCookie(h, "POST", "/api/ssh-keys", `{"name":"client-a-key"}`, cookieA)
	if createA.Code != 201 {
		t.Fatal(createA.Body.String())
	}
	var keyA struct {
		Key struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
		} `json:"key"`
	}
	if err := json.Unmarshal(createA.Body.Bytes(), &keyA); err != nil || keyA.Key.ID == "" || keyA.Key.AccountID == "" {
		t.Fatalf("client A key: %s", createA.Body.String())
	}

	listB := doCookie(h, "GET", "/api/ssh-keys", "", cookieB)
	if listB.Code != 200 {
		t.Fatal(listB.Body.String())
	}
	if strings.Contains(listB.Body.String(), keyA.Key.ID) || strings.Contains(listB.Body.String(), adminOut.Key.ID) {
		t.Fatalf("client B must not see other accounts' PEM keys: %s", listB.Body.String())
	}

	listA := doCookie(h, "GET", "/api/ssh-keys", "", cookieA)
	if listA.Code != 200 || !strings.Contains(listA.Body.String(), keyA.Key.ID) {
		t.Fatalf("client A should see own key: %s", listA.Body.String())
	}
	if strings.Contains(listA.Body.String(), adminOut.Key.ID) {
		t.Fatalf("client A must not see admin PEM keys: %s", listA.Body.String())
	}

	if w := doCookie(h, "DELETE", "/api/ssh-keys/"+adminOut.Key.ID, "", cookieA); w.Code != 404 {
		t.Fatalf("client cannot delete admin key: %d %s", w.Code, w.Body.String())
	}
	if w := doCookie(h, "DELETE", "/api/ssh-keys/"+keyA.Key.ID, "", cookieB); w.Code != 404 {
		t.Fatalf("client B cannot delete client A key: %d %s", w.Code, w.Body.String())
	}
}
