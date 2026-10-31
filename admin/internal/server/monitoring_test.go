package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitoringDownRecoveryAndRedactedChannels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, _ := newSrv(t)
	var down atomic.Bool
	down.Store(true)
	var delivered atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer target.Close()
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["text"] == nil || payload["event_id"] == nil {
			t.Error("invalid webhook payload")
		}
		delivered.Add(1)
		w.WriteHeader(204)
	}))
	defer webhook.Close()
	h := s.AdminHandler()
	raw, _ := json.Marshal(alertChannel{Name: "Ops", Type: "webhook", Webhook: webhook.URL + "/?secret=hidden", Enabled: true})
	w := do(h, "POST", "/api/monitoring/channels", string(raw), tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	raw, _ = json.Marshal(healthMonitor{Name: "Application", Type: "http", Target: target.URL, Enabled: true, Threshold: 2})
	w = do(h, "POST", "/api/monitoring/monitors", string(raw), tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s.runHealthChecks(context.Background())
	if delivered.Load() != 0 {
		t.Fatal("not debounced")
	}
	s.runHealthChecks(context.Background())
	if delivered.Load() != 1 {
		t.Fatal("down alert not delivered")
	}
	s.runHealthChecks(context.Background())
	if delivered.Load() != 1 {
		t.Fatal("duplicate down alert")
	}
	down.Store(false)
	s.runHealthChecks(context.Background())
	if delivered.Load() != 2 {
		t.Fatal("recovery not delivered")
	}
	response := do(h, "GET", "/api/monitoring", "", tok)
	if strings.Contains(response.Body.String(), "hidden") || strings.Contains(response.Body.String(), webhook.URL) {
		t.Fatal("webhook secret exposed")
	}
	if !strings.Contains(response.Body.String(), `"delivered"`) {
		t.Fatal(response.Body.String())
	}
	restored := loadMonitoring(dir)
	if len(restored.Channels) != 1 || len(restored.Monitors) != 1 || len(restored.Events) != 2 {
		t.Fatal("monitoring state not persisted")
	}
	if code := do(h, "GET", "/api/monitoring", "", "").Code; code != 401 {
		t.Fatal("missing auth", code)
	}
}
func TestMonitoringRetryAndNoCredentialLeak(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	var count atomic.Int32
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) == 1 {
			w.WriteHeader(500)
			w.Write([]byte("secret-url-must-not-leak"))
		} else {
			w.WriteHeader(200)
		}
	}))
	defer webhook.Close()
	d := s.monitoring
	d.Channels["ops"] = alertChannel{ID: "ops", Name: "Ops", Type: "webhook", Webhook: webhook.URL, Enabled: true}
	d.recordState(healthState{ID: "service", Name: "Service", Status: "DOWN", Checked: time.Now()}, 1)
	s.deliverHealthEvents(context.Background())
	if count.Load() != 1 || d.Events[0].Deliveries["ops"].Attempts != 1 {
		t.Fatal("failed attempt not tracked")
	}
	s.deliverHealthEvents(context.Background())
	if count.Load() != 1 {
		t.Fatal("retry ignored backoff")
	}
	delivery := d.Events[0].Deliveries["ops"]
	delivery.Next = time.Now().Add(-time.Second)
	d.Events[0].Deliveries["ops"] = delivery
	s.deliverHealthEvents(context.Background())
	if count.Load() != 2 || d.Events[0].Deliveries["ops"].Status != "delivered" {
		t.Fatal("retry not delivered")
	}
	raw, _ := json.Marshal(d.Events)
	if strings.Contains(string(raw), "secret-url-must-not-leak") {
		t.Fatal("response body leaked")
	}
}
func TestMonitoringValidationAndUI(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	for _, test := range []struct{ path, body string }{{"channels", `{"name":"Slack","type":"slack","webhook":"http://evil.example/services/abc"}`}, {"monitors", `{"name":"bad","type":"http","target":"file:///etc/passwd","threshold":2}`}, {"monitors", `{"name":"bad","type":"tcp","target":"host:99999","threshold":2}`}} {
		if w := do(h, "POST", "/api/monitoring/"+test.path, test.body, tok); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/health-checks", "/settings"} {
		w := do(h, "GET", path, "", "")
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
	if permissionResource("/api/monitoring/channels") != "health-checks" {
		t.Fatal("missing permission scope")
	}
}

func TestPingMonitorValidation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	for _, target := range []string{"127.0.0.1", "::1", "worker", "server.example.com"} {
		raw, _ := json.Marshal(healthMonitor{Name: "Server", Type: "ping", Target: target, Enabled: true, Threshold: 2})
		w := do(h, "POST", "/api/monitoring/monitors", string(raw), tok)
		if w.Code != 200 {
			t.Fatal(target, w.Body.String())
		}
	}
	for _, target := range []string{"-c", "host;echo hi", "https://example.com", "example.com:80", "x\ny"} {
		raw, _ := json.Marshal(healthMonitor{Name: "Bad", Type: "ping", Target: target, Enabled: true, Threshold: 2})
		if w := do(h, "POST", "/api/monitoring/monitors", string(raw), tok); w.Code != 400 {
			t.Fatal("invalid ping target accepted", target)
		}
	}
	state := checkHealthTarget(context.Background(), healthMonitor{Type: "ping", Target: "-c", Enabled: true})
	if state.Status != "ERROR" {
		t.Fatal(state)
	}
}
