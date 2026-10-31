package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestApplicationsPersistAndHideArtifacts(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	body := `{"name":"hello","kind":"job","execution":{"runtime":"python","artifact":"cHJpbnQoJ2hpJyk=","ram_mb":128,"cpu":0.5,"timeout_seconds":10}}`
	if r := do(h, "POST", "/api/applications", body, ""); r.Code != 401 {
		t.Fatalf("anonymous create: %d", r.Code)
	}
	r := do(h, "POST", "/api/applications", body, tok)
	if r.Code != 201 {
		t.Fatalf("create %d: %s", r.Code, r.Body.String())
	}
	var a application
	_ = json.Unmarshal(r.Body.Bytes(), &a)
	if len(a.Execution.Artifact) != 0 {
		t.Fatal("artifact leaked in create response")
	}
	reloaded := loadServerless(s.applications.file[:strings.LastIndex(s.applications.file, "/")])
	if len(reloaded.Apps[a.ID].Execution.Artifact) == 0 {
		t.Fatal("artifact not persisted")
	}
	list := do(h, "GET", "/api/applications", "", tok)
	if strings.Contains(list.Body.String(), "cHJpbnQ") {
		t.Fatal("artifact leaked in list")
	}
	malformed := strings.Replace(body, `"cpu":0.5`, `"cpu":0`, 1)
	if r = do(h, "POST", "/api/applications", malformed, tok); r.Code != 400 {
		t.Fatalf("invalid limits accepted: %d", r.Code)
	}
}
func TestApplicationQueueAndWorkerResultOwnership(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	a := application{ID: "app-test", Owner: "root", Name: "test", Kind: "job", Execution: proto.Execution{Runtime: "python", Artifact: []byte("print(1)"), RAMMB: 64, CPU: 0.5, Timeout: 10}}
	s.applications.Apps[a.ID] = a
	// Eligible worker with room for the scheduler's host reserve.
	_ = m.UpsertWorker(store.Worker{ID: "worker-test", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{Serverless: true, ServerlessRuntimes: []string{"python"}, RAMTotal: 8 << 30, MemAvailable: 7 << 30, CPUCores: 8, DiskTotal: 20 << 30, DiskFree: 10 << 30}})
	r := do(s.AdminHandler(), "POST", "/api/applications/app-test/run", `{"input":"hello"}`, tok)
	if r.Code != 202 {
		t.Fatalf("queue %d: %s", r.Code, r.Body.String())
	}
	var run applicationRun
	_ = json.Unmarshal(r.Body.Bytes(), &run)
	result := proto.OpResult{OpID: "execution/" + run.ID, OK: true, Execution: &proto.ExecutionResult{Stdout: "hello", ExitCode: 0}}
	s.completeExecution("other-worker", result)
	if s.applications.Runs[run.ID].State != "QUEUED" {
		t.Fatal("foreign worker completed invocation")
	}
	s.completeExecution(run.WorkerID, result)
	if s.applications.Runs[run.ID].State != "SUCCEEDED" {
		t.Fatal("result not recorded")
	}
	// A job cannot be invoked through the function endpoint.
	if r = do(s.AdminHandler(), "POST", "/api/functions/app-test/invoke", `{"input":""}`, tok); r.Code != 409 {
		t.Fatalf("job invoked as function: %d", r.Code)
	}
}
func TestApplicationOwnerIsolation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.applications.Apps["private"] = application{ID: "private", Owner: "alice"}
	req := httptest.NewRequest(http.MethodGet, "/api/applications", nil).WithContext(context.WithValue(context.Background(), iamPrincipalContextKey{}, iamPrincipal{UserID: "bob"}))
	w := httptest.NewRecorder()
	s.listApplications(w, req)
	if strings.Contains(w.Body.String(), "private") {
		t.Fatal("another owner's application visible")
	}
}

func TestFunctionHTTPInvocationAndPage(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	page := do(s.AdminHandler(), "GET", "/applications", "", "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Jobs &amp; Functions") {
		t.Fatalf("page: %d %s", page.Code, page.Body.String())
	}
	a := application{ID: "app-function", Owner: "root", Name: "echo", Kind: "function", Execution: proto.Execution{Runtime: "python", Artifact: []byte("print(1)"), RAMMB: 64, CPU: 0.5, Timeout: 5}}
	s.applications.Apps[a.ID] = a
	_ = m.UpsertWorker(store.Worker{ID: "worker-function", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{Serverless: true, ServerlessRuntimes: []string{"python"}, RAMTotal: 8 << 30, MemAvailable: 7 << 30, CPUCores: 8, DiskTotal: 20 << 30, DiskFree: 10 << 30}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.applications.Lock()
				var id string
				for key := range s.applications.Runs {
					id = key
					break
				}
				s.applications.Unlock()
				if id != "" {
					s.completeExecution("worker-function", proto.OpResult{OpID: "execution/" + id, OK: true, Execution: &proto.ExecutionResult{Stdout: "hello", ExitCode: 0}})
					return
				}
			}
		}
	}()
	req := httptest.NewRequest("POST", "/api/functions/app-function/invoke", strings.NewReader(`{"input":"hello"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+tok)
	response := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(response, req)
	<-done
	var run applicationRun
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || response.Code != 200 || run.State != "SUCCEEDED" || run.Result == nil || run.Result.Stdout != "hello" {
		t.Fatalf("function response %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-CloFix-Run-ID") != run.ID {
		t.Fatal("missing invocation tracking header")
	}
}

func TestApplicationSecretsVersionsAndRollback(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	body := `{"name":"versioned","kind":"job","secrets":{"API_KEY":"do-not-leak-this-value"},"execution":{"runtime":"nodejs","artifact":"eA==","ram_mb":128,"cpu":0.5,"timeout_seconds":10}}`
	response := do(h, "POST", "/api/applications", body, tok)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var a application
	_ = json.Unmarshal(response.Body.Bytes(), &a)
	if strings.Contains(response.Body.String(), "do-not-leak") || strings.Contains(response.Body.String(), "encrypted_secrets") {
		t.Fatal("secret disclosed")
	}
	disk, err := os.ReadFile(s.applications.file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), "do-not-leak-this-value") {
		t.Fatal("plaintext secret persisted")
	}
	secret, err := s.decryptApplicationSecrets(s.applications.Apps[a.ID].EncryptedSecrets)
	if err != nil || secret["API_KEY"] != "do-not-leak-this-value" {
		t.Fatal("secret round trip failed", err)
	}
	update := `{"name":"versioned","kind":"job","execution":{"runtime":"php","artifact":"eQ==","ram_mb":128,"cpu":0.5,"timeout_seconds":10}}`
	if response = do(h, "PUT", "/api/applications/"+a.ID, update, tok); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response = do(h, "POST", "/api/applications/"+a.ID+"/rollback/1", `{}`, tok); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if got := s.applications.Apps[a.ID].Execution.Runtime; got != "nodejs" {
		t.Fatalf("rollback runtime: %s", got)
	}
	if response = do(h, "PUT", "/api/applications/"+a.ID, update, tok); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if s.applications.Apps[a.ID].Version != 3 {
		t.Fatal("version number reused after rollback")
	}
}
func TestApplicationCronValidationAndUTC(t *testing.T) {
	for _, bad := range []string{"* * * *", "60 * * * *", "*/0 * * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *"} {
		if _, err := parseApplicationCron(bad); err == nil {
			t.Fatal("invalid cron accepted", bad)
		}
	}
	cron, err := parseApplicationCron("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	if !cron.matches(time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)) || cron.matches(time.Date(2026, 10, 29, 9, 31, 0, 0, time.UTC)) {
		t.Fatal("schedule minute matching")
	}
	domOrDow, _ := parseApplicationCron("0 0 1 * 1")
	if !domOrDow.matches(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("cron day-of-month/weekday OR behavior")
	}
}
func TestApplicationWebhookAndLiveProgress(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	h := s.AdminHandler()
	a := application{ID: "app-hook", Owner: "root", Kind: "job", Name: "hook", Execution: proto.Execution{Runtime: "python", Artifact: []byte("x"), RAMMB: 64, CPU: 0.5, Timeout: 10}}
	s.applications.Apps[a.ID] = a
	response := do(h, "POST", "/api/applications/app-hook/webhook", `{}`, tok)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var hook map[string]string
	_ = json.Unmarshal(response.Body.Bytes(), &hook)
	if got := do(h, "POST", "/hooks/applications/app-hook", `event`, ""); got.Code != 401 {
		t.Fatal("anonymous webhook accepted")
	}
	_ = m.UpsertWorker(store.Worker{ID: "hook-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{ServerlessRuntimes: []string{"python"}, RAMTotal: 8 << 30, MemAvailable: 7 << 30, CPUCores: 8, DiskTotal: 20 << 30, DiskFree: 10 << 30}})
	request := httptest.NewRequest("POST", "/hooks/applications/app-hook", strings.NewReader(`{"hello":1}`))
	request.Header.Set("X-CloFix-Webhook-Key", hook["secret"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var run applicationRun
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	ops, _ := m.TakeOps("hook-worker")
	if len(ops) != 1 || ops[0].Execution.Input != `{"hello":1}` {
		t.Fatal("webhook body not passed to stdin")
	}
	s.recordExecutionProgress("foreign", []proto.ExecutionProgress{{OpID: "execution/" + run.ID, Stdout: "bad"}})
	if s.applications.Runs[run.ID].State != "QUEUED" {
		t.Fatal("foreign progress accepted")
	}
	s.recordExecutionProgress("hook-worker", []proto.ExecutionProgress{{OpID: "execution/" + run.ID, Stdout: "live"}})
	if s.applications.Runs[run.ID].State != "RUNNING" {
		t.Fatal("live state not recorded")
	}
	s.completeExecution("hook-worker", proto.OpResult{OpID: "execution/" + run.ID, OK: true, Execution: &proto.ExecutionResult{Stdout: "done", OutputZIP: []byte("zip")}})
	output := do(h, "GET", "/api/application-runs/"+run.ID+"/output", "", tok)
	if output.Code != 200 || output.Body.String() != "zip" {
		t.Fatal("output download failed")
	}
	if got := do(h, "GET", "/api/application-runs/"+run.ID+"/output", "", ""); got.Code != 401 {
		t.Fatal("anonymous output download")
	}
}

func TestApplicationScheduleDeduplicatesMinute(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	s.applications.Apps["app-scheduled"] = application{ID: "app-scheduled", Owner: "root", Name: "scheduled", Kind: "job", Schedule: "* * * * *", Execution: proto.Execution{Runtime: "python", Artifact: []byte("x"), RAMMB: 64, CPU: 0.5, Timeout: 10}}
	_ = m.UpsertWorker(store.Worker{ID: "schedule-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{ServerlessRuntimes: []string{"python"}, RAMTotal: 8 << 30, MemAvailable: 7 << 30, CPUCores: 8, DiskTotal: 20 << 30, DiskFree: 10 << 30}})
	now := time.Now().UTC()
	s.runApplicationSchedules(now)
	first, _ := m.TakeOps("schedule-worker")
	if len(first) != 1 {
		t.Fatalf("scheduled operation count: %d; error: %s", len(first), s.applications.Apps["app-scheduled"].ScheduleError)
	}
	s.runApplicationSchedules(now)
	second, _ := m.TakeOps("schedule-worker")
	if len(second) != 0 {
		t.Fatal("schedule repeated within the same minute")
	}
}
