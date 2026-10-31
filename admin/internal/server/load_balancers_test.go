package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

const lbPayload = `{"name":"Web pool","algorithm":"round_robin","min_vps":1,"max_vps":3,"target_cpu":70,"health_path":"/health","spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":512,"swap_mb":0,"cpu":1,"disk_mb":2048,"ssh_authorized_key":"ssh-ed25519 QUFB test"}}`

func TestLoadBalancerCRUDAndProvisioning(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	h := s.AdminHandler()
	if got := do(h, "POST", "/api/load-balancers", lbPayload, "").Code; got != 401 {
		t.Fatal(got)
	}
	invalid := strings.Replace(lbPayload, `"max_vps":3`, `"max_vps":0`, 1)
	if got := do(h, "POST", "/api/load-balancers", invalid, tok).Code; got != 400 {
		t.Fatal(got)
	}
	response := do(h, "POST", "/api/load-balancers", lbPayload, tok)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var b loadBalancer
	json.Unmarshal(response.Body.Bytes(), &b)
	m.UpsertWorker(store.Worker{ID: "w1", Hostname: "127.0.0.1", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	s.reconcileLoadBalancers()
	saved := s.balancers.Items[b.ID]
	if len(saved.Instances) != 1 {
		t.Fatalf("no backend: %+v", saved)
	}
	ops, _ := m.TakeOps("w1")
	if len(ops) != 1 || ops[0].Action != proto.ActionCreate {
		t.Fatalf("missing create: %+v", ops)
	}
	reloaded := loadBalancers(strings.TrimSuffix(s.balancers.file, "/load-balancers.json"))
	if len(reloaded.Items[b.ID].Instances) != 1 {
		t.Fatal("membership lost")
	}
	updated := do(h, "PUT", "/api/load-balancers/"+b.ID, strings.Replace(lbPayload, "round_robin", "least_connections", 1), tok)
	if updated.Code != 200 {
		t.Fatal(updated.Body.String())
	}
	if s.balancers.Items[b.ID].Algorithm != "least_connections" {
		t.Fatal("update not applied")
	}
	if got := do(h, "DELETE", "/api/load-balancers/"+b.ID, "", tok).Code; got != 202 {
		t.Fatal(got)
	}
	s.reconcileLoadBalancers()
	ops, _ = m.TakeOps("w1")
	if len(ops) != 1 || ops[0].Action != proto.ActionDelete {
		t.Fatalf("missing delete: %+v", ops)
	}
	m.DeleteInstance(saved.Instances[0])
	s.reconcileLoadBalancers()
	if len(s.balancers.Items) != 0 {
		t.Fatal("balancer not removed")
	}
}
func TestLoadBalancerAlgorithmsAndProxy(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	backend := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s:%s?%s:%s", label, r.URL.Path, r.URL.RawQuery, r.Header.Get("Cookie"))
		}))
	}
	a := backend("a")
	defer a.Close()
	b := backend("b")
	defer b.Close()
	lb := &loadBalancer{ID: "lb-test", Healthy: map[string]string{"a": a.URL, "b": b.URL}, active: map[string]int{}, latency: map[string]time.Duration{}}
	s.balancers.Items[lb.ID] = lb
	h := s.AdminHandler()
	for alg := range lbAlgorithms {
		lb.Algorithm = alg
		lb.sequence = 0
		request := httptest.NewRequest("GET", "/lb/lb-test/hello?x=1", nil)
		request.Header.Set("Cookie", "session=app")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != 200 || !strings.Contains(w.Body.String(), ":/hello?x=1:session=app") {
			t.Fatalf("%s: %d %s", alg, w.Code, w.Body.String())
		}
	}
	lb.Slug = "test-app"
	if w := do(h, "GET", "/lb/test-app/", "", ""); w.Code != 200 {
		t.Fatal("custom URL failed", w.Code)
	}
	lb.Algorithm = "round_robin"
	lb.sequence = 0
	first := do(h, "GET", "/lb/lb-test/", "", "").Body.String()
	second := do(h, "GET", "/lb/lb-test/", "", "").Body.String()
	if first == second {
		t.Fatal("round robin did not rotate")
	}
	lb.active["a"] = 5
	lb.Algorithm = "least_connections"
	if !strings.HasPrefix(do(h, "GET", "/lb/lb-test/", "", "").Body.String(), "b:") {
		t.Fatal("least connections ignored activity")
	}
	lb.Healthy = map[string]string{}
	if got := do(h, "GET", "/lb/lb-test/", "", "").Code; got != 503 {
		t.Fatal(got)
	}
}
func TestLoadBalancerCapacityFailure(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	do(h, "POST", "/api/load-balancers", lbPayload, tok)
	s.reconcileLoadBalancers()
	for _, b := range s.balancers.Items {
		if len(b.Instances) != 0 || b.Error == "" {
			t.Fatal("capacity failure not surfaced")
		}
	}
}

func TestLoadBalancerHealthAutoscalingAndCooldown(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	var unhealthy atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unhealthy.Load() {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	p, _ := strconv.Atoi(port)
	t.Setenv("CLOFIX_LB_WORKER_HOST", host)
	worker := store.Worker{ID: "w1", Hostname: "old-container-hostname.invalid", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}}
	m.UpsertWorker(worker)
	response := do(s.AdminHandler(), "POST", "/api/load-balancers", lbPayload, tok)
	var b loadBalancer
	json.Unmarshal(response.Body.Bytes(), &b)
	s.reconcileLoadBalancers()
	saved := s.balancers.Items[b.ID]
	iid := saved.Instances[0]
	m.TakeOps("w1")
	inst, _ := m.GetInstance(iid)
	inst.State = proto.StateRunning
	inst.Spec.SSHPort = p - 1000
	m.SaveInstance(inst)
	s.usage[iid] = proto.InstanceMetrics{CPUPercent: 90}
	saved.LastScale = time.Now().Add(-3 * time.Minute)
	s.reconcileLoadBalancers()
	if len(saved.Healthy) != 1 || len(saved.Instances) != 2 {
		t.Fatalf("no healthy scale out: %+v", saved)
	}
	// A fresh scale operation prevents another scale out.
	second, _ := m.GetInstance(saved.Instances[1])
	second.State = proto.StateRunning
	second.Spec.SSHPort = p - 1000
	m.SaveInstance(second)
	s.usage[second.ID] = proto.InstanceMetrics{CPUPercent: 90}
	s.reconcileLoadBalancers()
	if len(saved.Instances) != 2 {
		t.Fatal("cooldown ignored")
	}
	m.TakeOps("w1")
	s.usage[iid] = proto.InstanceMetrics{}
	s.usage[second.ID] = proto.InstanceMetrics{}
	saved.LastScale = time.Now().Add(-3 * time.Minute)
	s.reconcileLoadBalancers()
	ops, _ := m.TakeOps("w1")
	if len(ops) != 1 || ops[0].Action != proto.ActionDelete || ops[0].InstanceID != second.ID {
		t.Fatalf("no scale in: %+v", ops)
	}
	unhealthy.Store(true)
	s.reconcileLoadBalancers()
	if len(saved.Healthy) != 0 {
		t.Fatal("unhealthy backend received traffic")
	}
}
func TestLoadBalancerIAMScope(t *testing.T) {
	if permissionResource("/api/load-balancers") != "load-balancers" || !iamPermissionSet["load-balancers:manage"] {
		t.Fatal("missing IAM scope")
	}
	if hasIAMPermission(iamPrincipal{Permissions: []string{"load-balancers:read"}}, "load-balancers:manage") {
		t.Fatal("read scope grants writes")
	}
}

func TestLoadBalancerUI(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	w := do(s.AdminHandler(), "GET", "/load-balancers", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="lb-form"`) || !strings.Contains(w.Body.String(), "/assets/load-balancers.js") {
		t.Fatalf("UI failed: %d %s", w.Code, w.Body.String())
	}
}

func TestLoadBalancerBackupTemplate(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	s.backups["bkp-template"] = proto.BackupInfo{ID: "bkp-template", WorkerID: "source", Image: "ubuntu:24.04", State: "READY"}
	for _, id := range []string{"source", "other"} {
		m.UpsertWorker(store.Worker{ID: id, Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	}
	var payload map[string]any
	json.Unmarshal([]byte(lbPayload), &payload)
	spec := payload["spec"].(map[string]any)
	delete(spec, "image")
	spec["backup_id"] = "bkp-template"
	raw, _ := json.Marshal(payload)
	response := do(s.AdminHandler(), "POST", "/api/load-balancers", string(raw), tok)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	s.reconcileLoadBalancers()
	ops, _ := m.TakeOps("source")
	if len(ops) != 1 || ops[0].Spec.BackupID != "bkp-template" || ops[0].Spec.Image.Name != "ubuntu" {
		t.Fatalf("backup restore not queued on source worker: %+v", ops)
	}
	others, _ := m.TakeOps("other")
	if len(others) != 0 {
		t.Fatal("restore scheduled on wrong worker")
	}
	spec["backup_id"] = "bkp-missing"
	raw, _ = json.Marshal(payload)
	if got := do(s.AdminHandler(), "POST", "/api/load-balancers", string(raw), tok).Code; got != 400 {
		t.Fatal("missing backup accepted", got)
	}
	s.backups["bkp-template"] = proto.BackupInfo{ID: "bkp-template", State: "DELETING"}
	if _, e := s.lbLifecycle("create", "", proto.InstanceSpec{BackupID: "bkp-template"}); e == nil {
		t.Fatal("unavailable backup accepted during scale out")
	}
}

func TestLoadBalancerURLAndDetailPage(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	payload := strings.Replace(lbPayload, `"name":"Web pool"`, `"name":"Web pool","slug":"web-pool"`, 1)
	w := do(h, "POST", "/api/load-balancers", payload, tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var b loadBalancer
	json.Unmarshal(w.Body.Bytes(), &b)
	detail := do(h, "GET", "/load-balancers/"+b.ID, "", "")
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), `id="lb-detail"`) {
		t.Fatal("detail page unavailable", detail.Code)
	}
	if code := do(h, "POST", "/api/load-balancers", payload, tok).Code; code != 409 {
		t.Fatal("duplicate URL accepted", code)
	}
	bad := strings.Replace(payload, `"slug":"web-pool"`, `"slug":"lb-reserved"`, 1)
	if code := do(h, "POST", "/api/load-balancers", bad, tok).Code; code != 400 {
		t.Fatal("reserved URL accepted", code)
	}
	if code := do(h, "GET", "/lb/web-pool/", "", "").Code; code != 503 {
		t.Fatal("URL did not resolve to unready balancer", code)
	}
}

func TestLoadBalancerEditWithUnavailableBackup(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	s.backups["bkp-template"] = proto.BackupInfo{ID: "bkp-template", WorkerID: "source", Image: "ubuntu:24.04", State: "READY"}
	var payload map[string]any
	json.Unmarshal([]byte(lbPayload), &payload)
	spec := payload["spec"].(map[string]any)
	spec["backup_id"] = "bkp-template"
	raw, _ := json.Marshal(payload)
	w := do(h, "POST", "/api/load-balancers", string(raw), tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var b loadBalancer
	json.Unmarshal(w.Body.Bytes(), &b)
	delete(s.backups, "bkp-template")
	payload["slug"] = "clofix-lb"
	delete(spec, "image")
	raw, _ = json.Marshal(payload)
	w = do(h, "PUT", "/api/load-balancers/"+b.ID, string(raw), tok)
	if w.Code != 200 {
		t.Fatal("existing backup blocks URL edit", w.Body.String())
	}
	saved := s.balancers.Items[b.ID]
	if saved.Slug != "clofix-lb" || saved.Spec.Image.Name != "ubuntu" {
		t.Fatal("template or URL lost")
	}
	if _, e := s.lbLifecycle("create", "", saved.Spec); e == nil {
		t.Fatal("missing backup allowed provisioning")
	}
	if code := do(h, "POST", "/api/load-balancers", string(raw), tok).Code; code != 400 {
		t.Fatal("missing backup accepted for new balancer", code)
	}
}

func TestALBCustomDomainRouting(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s?%s", r.Host, r.URL.Path, r.URL.RawQuery)
	}))
	defer upstream.Close()
	b := &loadBalancer{ID: "test-alb", Mode: "http", Domains: []string{"app.example.test"}, Algorithm: "round_robin", Healthy: map[string]string{"one": upstream.URL}, active: map[string]int{}, latency: map[string]time.Duration{}}
	s.balancers.Items[b.ID] = b
	r := httptest.NewRequest("GET", "http://app.example.test/orders?sort=new", nil)
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "app.example.test /orders?sort=new" {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "http://other.example.test/orders", nil)
	w = httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("unmatched domain routed", w.Code)
	}
}
func TestALBDomainValidation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	payload := strings.Replace(lbPayload, `"name":"Web pool"`, `"mode":"alb","domains":["App.Example.Test"],"name":"Web pool"`, 1)
	w := do(h, "POST", "/api/load-balancers", payload, tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var b loadBalancer
	json.Unmarshal(w.Body.Bytes(), &b)
	if b.Mode != "http" || len(b.Domains) != 1 || b.Domains[0] != "app.example.test" {
		t.Fatal("invalid ALB response", w.Body.String())
	}
	if code := do(h, "POST", "/api/load-balancers", payload, tok).Code; code != 409 {
		t.Fatal("duplicate domain accepted", code)
	}
	for _, domain := range []string{"https://app.example.test", "localhost", "127.0.0.1", "app.example.test:8080", "-bad.example.test", "*.example.test"} {
		if validALBDomain(domain) {
			t.Fatal("invalid domain accepted", domain)
		}
	}
}
