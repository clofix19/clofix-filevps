package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSecretsVaultCRUDRevealRuntimeAndInject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()

	if page, title, _, ok := uiPage("/secrets"); !ok || page != "secrets" || title != "Secrets Manager" {
		t.Fatalf("secrets page: %s %s %v", page, title, ok)
	}
	if permissionResource("/api/secrets") != "secrets" || permissionResource("/api/secrets/sec-1/value") != "secrets" {
		t.Fatal("secrets must use secrets IAM")
	}
	html := do(h, "GET", "/secrets", "", tok)
	if html.Code != 200 || !strings.Contains(html.Body.String(), "Secrets Manager") {
		t.Fatalf("page: %d %s", html.Code, html.Body.String())
	}
	if w := do(h, "GET", "/api/secrets", "", ""); w.Code != 401 {
		t.Fatal("missing auth", w.Code)
	}

	created := do(h, "POST", "/api/secrets", `{"name":"db-password","description":"primary","type":"string","value":"s3cret!"}`, tok)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var sec struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Value   string `json:"value"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &sec); err != nil || !strings.HasPrefix(sec.ID, "sec-") || sec.Value != "s3cret!" || sec.Warning == "" {
		t.Fatalf("create: %s", created.Body.String())
	}

	listed := do(h, "GET", "/api/secrets", "", tok)
	if listed.Code != 200 {
		t.Fatal(listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), "s3cret!") || strings.Contains(listed.Body.String(), `"ciphertext"`) {
		t.Fatalf("list must not include plaintext or ciphertext: %s", listed.Body.String())
	}

	reveal := do(h, "POST", "/api/secrets/"+sec.ID+"/reveal", `{}`, tok)
	if reveal.Code != 200 || !strings.Contains(reveal.Body.String(), "s3cret!") {
		t.Fatalf("reveal: %s", reveal.Body.String())
	}

	byID := do(h, "GET", "/api/secrets/"+sec.ID+"/value", "", tok)
	if byID.Code != 200 || !strings.Contains(byID.Body.String(), `"value":"s3cret!"`) {
		t.Fatalf("runtime by id: %s", byID.Body.String())
	}
	byName := do(h, "GET", "/api/secrets/by-name/db-password/value", "", tok)
	if byName.Code != 200 || !strings.Contains(byName.Body.String(), `"value":"s3cret!"`) {
		t.Fatalf("runtime by name: %s", byName.Body.String())
	}

	owner := s.secrets.Secrets[sec.ID].Owner
	resolved, err := s.resolveSecretRefs(owner, map[string]string{
		"PLAIN": "ok",
		"PASS":  "sec:" + sec.ID,
		"ALT":   "{{secret:db-password}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["PLAIN"] != "ok" || resolved["PASS"] != "s3cret!" || resolved["ALT"] != "s3cret!" {
		t.Fatalf("resolve: %+v", resolved)
	}
	if _, err = s.resolveSecretRefs("acc-other", map[string]string{"PASS": "sec:" + sec.ID}); err == nil {
		t.Fatal("cross-account resolve must fail")
	}
	// Docker platform owner should resolve admin/root vault secrets.
	if owner == "root" {
		platformResolved, err := s.resolveSecretRefs("acc-platform", map[string]string{"PASS": "sec:" + sec.ID})
		if err != nil || platformResolved["PASS"] != "s3cret!" {
			t.Fatalf("platform resolve: %+v %v", platformResolved, err)
		}
	}

	kv := do(h, "POST", "/api/secrets", `{"name":"app-creds","type":"key_value","values":{"USER":"demo","PASS":"pw"}}`, tok)
	if kv.Code != 201 {
		t.Fatal(kv.Body.String())
	}
	var kvSec struct {
		ID     string            `json:"id"`
		Values map[string]string `json:"values"`
	}
	if err := json.Unmarshal(kv.Body.Bytes(), &kvSec); err != nil || kvSec.Values["USER"] != "demo" {
		t.Fatalf("kv create: %s", kv.Body.String())
	}
	data, err := s.secretDataMap(s.secrets.Secrets[kvSec.ID])
	if err != nil || data["USER"] != "demo" || data["PASS"] != "pw" {
		t.Fatalf("secret data map: %+v %v", data, err)
	}

	del := do(h, "DELETE", "/api/secrets/"+sec.ID, "", tok)
	if del.Code != 200 {
		t.Fatal(del.Body.String())
	}
	if w := do(h, "GET", "/api/secrets/"+sec.ID+"/value", "", tok); w.Code != 404 {
		t.Fatalf("deleted value: %d", w.Code)
	}
}

func TestSecretsCKSSyncRequiresCluster(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, _ := newSrv(t)
	h := s.AdminHandler()
	created := do(h, "POST", "/api/secrets", `{"name":"cks-sync","type":"string","value":"tok"}`, tok)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var sec struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &sec)
	w := do(h, "POST", "/api/secrets/"+sec.ID+"/sync-cks", `{"cluster_id":"cks-missing","namespace":"default","name":"db-secret"}`, tok)
	if w.Code != 409 {
		t.Fatalf("missing cluster: %d %s", w.Code, w.Body.String())
	}
}
