package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestLogsCatalogPathSafetyAndIAM(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	_ = m.UpsertWorker(store.Worker{ID: "w-logs", Hostname: "log-host", Status: "active"})
	_ = m.SaveInstance(store.Instance{ID: "vps-logsrc01", WorkerID: "w-logs", State: proto.StateRunning})
	h := s.AdminHandler()

	if w := do(h, "GET", "/api/logs", "", ""); w.Code != 401 {
		t.Fatal("missing auth", w.Code)
	}
	page := do(h, "GET", "/logs", "", tok)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Log Service") {
		t.Fatalf("page: %d %s", page.Code, page.Body.String())
	}

	listed := do(h, "GET", "/api/logs", "", tok)
	if listed.Code != 200 {
		t.Fatal(listed.Body.String())
	}
	if !strings.Contains(listed.Body.String(), `"id":"vps:vps-logsrc01"`) {
		t.Fatal("auto VPS source missing", listed.Body.String())
	}

	for _, body := range []string{
		`{"name":"escape","kind":"vps","resource_id":"vps-logsrc01","path":"../etc/passwd"}`,
		`{"name":"host","kind":"worker","resource_id":"w-logs","path":"/etc/passwd"}`,
		`{"name":"host-escape","kind":"worker","resource_id":"w-logs","path":"/var/log/../etc/shadow"}`,
	} {
		if w := do(h, "POST", "/api/logs/sources", body, tok); w.Code != 400 {
			t.Fatalf("wanted 400 for %s: %d %s", body, w.Code, w.Body.String())
		}
	}

	created := do(h, "POST", "/api/logs/sources", `{"name":"nginx","kind":"vps","resource_id":"vps-logsrc01","path":"/var/log/nginx/error.log"}`, tok)
	if created.Code != 200 {
		t.Fatal(created.Body.String())
	}
	var src logSource
	if err := json.Unmarshal(created.Body.Bytes(), &src); err != nil || !logCustomID.MatchString(src.ID) {
		t.Fatal(created.Body.String())
	}
	if src.Path != "/var/log/nginx/error.log" {
		t.Fatal(src.Path)
	}
	if deleted := do(h, "DELETE", "/api/logs/sources/"+src.ID, "", tok); deleted.Code != 200 {
		t.Fatal(deleted.Body.String())
	}
	if gone := do(h, "DELETE", "/api/logs/sources/"+src.ID, "", tok); gone.Code != 404 {
		t.Fatal(gone.Code, gone.Body.String())
	}

	_ = s.Store.SaveIAMPolicy(store.IAMPolicy{ID: "pol-aaaaaaaaaaaaaaaa", Name: "logs-read", Permissions: []string{"logs:read"}})
	_ = s.Store.SaveIAMUser(store.IAMUser{ID: "usr-aaaaaaaaaaaaaaaa", AccountID: platformAccount, Kind: "iam", Username: "logreader", Role: "IAM User", Status: "active", PolicyIDs: []string{"pol-aaaaaaaaaaaaaaaa"}})
	_ = s.Store.SaveIAMAccessKey(store.IAMAccessKey{AccessKeyID: "CFXLOGREAD0001", UserID: "usr-aaaaaaaaaaaaaaaa", SecretHash: hashToken("secret"), Active: true})
	reader := "CFXLOGREAD0001.secret"
	if w := do(h, "GET", "/api/logs", "", reader); w.Code != 200 {
		t.Fatal("logs:read GET", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/logs/sources", `{"name":"denied","kind":"vps","resource_id":"vps-logsrc01","path":"/var/log/app.log"}`, reader); w.Code != 403 {
		t.Fatal("logs:read POST", w.Code, w.Body.String())
	}
	if !hasIAMPermission(iamPrincipal{Permissions: []string{"logs:read"}}, "logs:read") || hasIAMPermission(iamPrincipal{Permissions: []string{"logs:read"}}, "logs:manage") {
		t.Fatal("IAM logs scope")
	}
	if permissionResource("/api/logs/vps:vps-logsrc01/tail") != "logs" {
		t.Fatal("tail permission resource")
	}
	restored := loadLogs(dir)
	if len(restored.Sources) != 0 {
		t.Fatal("deleted source persisted")
	}
}

func TestLogAlertMatchCooldownAndDisabledChannel(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	var delivered atomic.Int32
	var lastText atomic.Value
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["text"] == nil {
			t.Error("invalid webhook payload")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		lastText.Store(payload["text"].(string))
		delivered.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	s.monitoring.Channels["ops"] = alertChannel{ID: "ops", Name: "Ops", Type: "webhook", Webhook: webhook.URL, Enabled: true}
	s.monitoring.Channels["off"] = alertChannel{ID: "off", Name: "Off", Type: "webhook", Webhook: "http://127.0.0.1:9/disabled", Enabled: false}
	s.logs.Alerts["lga-aaaaaaaaaaaaaaaa"] = logAlert{ID: "lga-aaaaaaaaaaaaaaaa", Name: "Nginx errors", SourceID: "vps:vps-logsrc01", Match: "error", Level: "error", Enabled: true, Threshold: 1, CooldownSeconds: 300}

	s.ingestLogTail("vps:vps-logsrc01", "boot ok\n")
	if delivered.Load() != 0 || len(s.logs.Events) != 0 {
		t.Fatal("first observation must not alert")
	}
	s.ingestLogTail("vps:vps-logsrc01", "boot ok\nERROR failed request\n")
	if delivered.Load() != 1 {
		t.Fatal("matching line not delivered", delivered.Load())
	}
	if len(s.logs.Events) != 1 || s.logs.Events[0].Deliveries["off"].Status != "" {
		t.Fatal("disabled channel should be skipped", s.logs.Events)
	}
	if _, ok := s.logs.Events[0].Deliveries["ops"]; !ok {
		t.Fatal("enabled channel missing")
	}
	text, _ := lastText.Load().(string)
	if !strings.Contains(text, "CloFix log alert: Nginx errors") || !strings.Contains(text, "vps:vps-logsrc01") || !strings.Contains(text, "ERROR failed request") {
		t.Fatal(text)
	}

	s.ingestLogTail("vps:vps-logsrc01", "boot ok\nERROR failed request\n")
	if delivered.Load() != 1 {
		t.Fatal("identical tail re-fired", delivered.Load())
	}
	s.ingestLogTail("vps:vps-logsrc01", "boot ok\nERROR failed request\nERROR again\n")
	if delivered.Load() != 1 {
		t.Fatal("cooldown ignored", delivered.Load())
	}

	rule := s.logs.Alerts["lga-aaaaaaaaaaaaaaaa"]
	rule.LastFired = time.Now().Add(-10 * time.Minute)
	s.logs.Alerts[rule.ID] = rule
	s.ingestLogTail("vps:vps-logsrc01", "boot ok\nERROR failed request\nERROR again\nERROR later\n")
	if delivered.Load() != 2 {
		t.Fatal("cooldown expiry did not fire", delivered.Load())
	}
}

func TestLogTailDownloadAndAnalysis(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, m := newSrv(t)
	s.LogDir = t.TempDir()
	_ = m.SaveInstance(store.Instance{ID: "vps-logsrc01", WorkerID: "w-logs", State: proto.StateRunning})
	if err := os.WriteFile(s.LogDir+"/vps-logsrc01.log", []byte("info started\nWARN disk\nERROR boom\n"), 0600); err != nil {
		t.Fatal(err)
	}
	h := s.AdminHandler()
	tail := do(h, "GET", "/api/logs/vps:vps-logsrc01/tail?level=error", "", tok)
	if tail.Code != 200 || !strings.Contains(tail.Body.String(), "ERROR boom") || strings.Contains(tail.Body.String(), "WARN disk") {
		t.Fatal(tail.Body.String())
	}
	dl := do(h, "GET", "/api/logs/vps:vps-logsrc01/download", "", tok)
	if dl.Code != 200 || !strings.Contains(dl.Header().Get("Content-Disposition"), "vps-vps-logsrc01.log") || !strings.Contains(dl.Body.String(), "WARN disk") {
		t.Fatal(dl.Header().Get("Content-Disposition"), dl.Body.String())
	}
	analysis := do(h, "GET", "/api/logs/analysis?q=boom", "", tok)
	if analysis.Code != 200 || !strings.Contains(analysis.Body.String(), `"errors":1`) || !strings.Contains(analysis.Body.String(), `"matches":1`) {
		t.Fatal(analysis.Body.String())
	}
}
