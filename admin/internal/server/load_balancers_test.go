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

	"github.com/clofix/filevps/admin/internal/objectstore"
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
func TestLoadBalancerHitAutoscaling(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	p, _ := strconv.Atoi(port)
	t.Setenv("CLOFIX_LB_WORKER_HOST", host)
	m.UpsertWorker(store.Worker{ID: "w1", Hostname: "old-container-hostname.invalid", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	payload := strings.Replace(lbPayload, `"target_cpu":70`, `"target_cpu":70,"target_rps":8`, 1)
	response := do(s.AdminHandler(), "POST", "/api/load-balancers", payload, tok)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var created loadBalancer
	json.Unmarshal(response.Body.Bytes(), &created)
	if created.TargetRPS != 8 {
		t.Fatal("target_rps not saved", response.Body.String())
	}
	s.reconcileLoadBalancers()
	saved := s.balancers.Items[created.ID]
	iid := saved.Instances[0]
	m.TakeOps("w1")
	inst, _ := m.GetInstance(iid)
	inst.State = proto.StateRunning
	inst.Spec.SSHPort = p - 1000
	m.SaveInstance(inst)
	s.usage[iid] = proto.InstanceMetrics{CPUPercent: 10}
	saved.ensureHits()
	for i := 0; i < 20; i++ {
		saved.hits.add(time.Now())
	}
	saved.LastScale = time.Now().Add(-3 * time.Minute)
	s.reconcileLoadBalancers()
	if len(saved.Instances) != 2 || saved.ScaleReason != "hits above target" {
		t.Fatalf("hit scale out: replicas=%d reason=%s", len(saved.Instances), saved.ScaleReason)
	}
	m.TakeOps("w1")
	second, _ := m.GetInstance(saved.Instances[1])
	second.State = proto.StateRunning
	second.Spec.SSHPort = p - 1000
	m.SaveInstance(second)
	s.usage[second.ID] = proto.InstanceMetrics{CPUPercent: 10}
	saved.hits = &hitMinute{}
	for i := 0; i < 12; i++ {
		saved.hits.add(time.Now())
	}
	saved.LastScale = time.Now().Add(-3 * time.Minute)
	s.reconcileLoadBalancers()
	if len(saved.Instances) != 2 {
		t.Fatal("hits between half and target must not scale")
	}
	saved.hits = &hitMinute{}
	saved.LastScale = time.Now().Add(-3 * time.Minute)
	s.reconcileLoadBalancers()
	ops, _ := m.TakeOps("w1")
	if len(ops) != 1 || ops[0].Action != proto.ActionDelete || saved.ScaleReason != "CPU and hits below half target" {
		t.Fatalf("low hit scale in: ops=%+v reason=%s", ops, saved.ScaleReason)
	}

	cpuOnly := do(s.AdminHandler(), "POST", "/api/load-balancers", lbPayload, tok)
	if cpuOnly.Code != 200 {
		t.Fatal(cpuOnly.Body.String())
	}
	var other loadBalancer
	json.Unmarshal(cpuOnly.Body.Bytes(), &other)
	if other.TargetRPS != 0 {
		t.Fatal("omitted target_rps must stay CPU-only")
	}
	s.reconcileLoadBalancers()
	only := s.balancers.Items[other.ID]
	oid := only.Instances[0]
	inst2, _ := m.GetInstance(oid)
	inst2.State = proto.StateRunning
	inst2.Spec.SSHPort = p - 1000
	m.SaveInstance(inst2)
	s.usage[oid] = proto.InstanceMetrics{CPUPercent: 10}
	only.ensureHits()
	for i := 0; i < 40; i++ {
		only.hits.add(time.Now())
	}
	only.LastScale = time.Now().Add(-3 * time.Minute)
	before := len(only.Instances)
	s.reconcileLoadBalancers()
	if len(only.Instances) != before {
		t.Fatal("CPU-only balancer scaled on hits")
	}
	if w := do(s.AdminHandler(), "POST", "/api/load-balancers", strings.Replace(lbPayload, `"target_cpu":70`, `"target_cpu":70,"target_rps":-1`, 1), tok); w.Code != 400 {
		t.Fatal("negative target_rps accepted", w.Code)
	}
}

func TestLoadBalancerLiveHitsOnList(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer backend.Close()
	lb := &loadBalancer{ID: "lb-live", Name: "Live", Algorithm: "round_robin", Healthy: map[string]string{"a": backend.URL}, active: map[string]int{}, latency: map[string]time.Duration{}}
	s.balancers.Items[lb.ID] = lb
	if do(s.AdminHandler(), "GET", "/lb/lb-live/", "", "").Code != 200 {
		t.Fatal("proxy failed")
	}
	w := do(s.AdminHandler(), "GET", "/api/load-balancers", "", tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var items []loadBalancer
	json.Unmarshal(w.Body.Bytes(), &items)
	if len(items) != 1 || items[0].Hits1m < 1 || items[0].AvgLatencyMS < 0 {
		t.Fatalf("live hits missing: %s", w.Body.String())
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
	w := do(s.AdminHandler(), "GET", "/load-balancers", "", tok)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `id="lb-form"`) || !strings.Contains(body, "/assets/load-balancers.js") || !strings.Contains(body, `id="lb-target-rps"`) || !strings.Contains(body, `value="2"`) {
		t.Fatalf("UI failed: %d %s", w.Code, body)
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
	detail := do(h, "GET", "/load-balancers/"+b.ID, "", tok)
	page := detail.Body.String()
	if detail.Code != 200 || !strings.Contains(page, `id="lb-detail"`) || !strings.Contains(page, `id="lb-efs"`) || !strings.Contains(page, `id="lb-efs-path"`) || !strings.Contains(page, `data-chart-range="1h"`) || !strings.Contains(page, `data-chart-range="7d"`) || !strings.Contains(page, `data-chart-range="30d"`) || !strings.Contains(page, `data-chart-range="custom"`) || !strings.Contains(page, `id="chart-lb-replicas"`) || !strings.Contains(page, `id="lb-stat-replicas"`) {
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

func TestALBInternalDNSNameRouting(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok %s", r.URL.Path)
	}))
	defer upstream.Close()
	b := &loadBalancer{ID: "lb-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Mode: "http", Name: "clofix-lb", Algorithm: "round_robin", Healthy: map[string]string{"one": upstream.URL}, active: map[string]int{}, latency: map[string]time.Duration{}}
	s.balancers.Items[b.ID] = b
	r := httptest.NewRequest("GET", "http://clofix-lb.alb.clofix.internal/health", nil)
	w := httptest.NewRecorder()
	s.serveNamedHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "ok /health" {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "http://clofix-lb.alb.clofix.internal/health", nil)
	w = httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "ok /health" {
		t.Fatal("admin host route", w.Code, w.Body.String())
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

func TestLoadBalancerEFSIDAutoAttachesMembers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, m := newSrv(t)
	if err := s.S3.SaveConnection(objectstore.Config{
		ID: "s3-default", Name: "CloFix S3", Provider: "clofix",
		Endpoint: "http://s3pool:8333", Bucket: "clofix", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	created := do(s.AdminHandler(), "POST", "/api/efs", `{"name":"web-root"}`, tok)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var share struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &share); err != nil || share.ID == "" {
		t.Fatal(created.Body.String())
	}
	if w := do(s.AdminHandler(), "POST", "/api/load-balancers", strings.Replace(lbPayload, `"health_path":"/health"`, `"health_path":"/health","efs_id":"efs-missing"`, 1), tok); w.Code != 400 {
		t.Fatalf("missing share: %d %s", w.Code, w.Body.String())
	}
	if w := do(s.AdminHandler(), "POST", "/api/load-balancers", strings.Replace(lbPayload, `"health_path":"/health"`, `"health_path":"/health","efs_id":"`+share.ID+`","efs_path":"/etc/nginx"`, 1), tok); w.Code != 400 {
		t.Fatalf("blocked path: %d %s", w.Code, w.Body.String())
	}
	payload := strings.Replace(lbPayload, `"health_path":"/health"`, `"health_path":"/health","efs_id":"`+share.ID+`"`, 1)
	response := do(s.AdminHandler(), "POST", "/api/load-balancers", payload, tok)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var b loadBalancer
	if err := json.Unmarshal(response.Body.Bytes(), &b); err != nil || b.EFSID != share.ID || b.EFSPath != "/var/www/html" {
		t.Fatalf("saved efs: %s", response.Body.String())
	}
	m.UpsertWorker(store.Worker{ID: "w1", Hostname: "127.0.0.1", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	s.reconcileLoadBalancers()
	saved := s.balancers.Items[b.ID]
	if saved == nil || len(saved.Instances) != 1 {
		t.Fatalf("no backend: %+v", saved)
	}
	inst, err := m.GetInstance(saved.Instances[0])
	if err != nil || len(inst.Spec.EFSMounts) != 1 || inst.Spec.EFSMounts[0].ID != share.ID || inst.Spec.EFSMounts[0].GuestPath != "/var/www/html" {
		t.Fatalf("auto mount: %+v %v", inst.Spec.EFSMounts, err)
	}
	ops, _ := m.TakeOps("w1")
	foundCreate, foundEFS := false, false
	for _, op := range ops {
		if op.Action == proto.ActionCreate {
			foundCreate = true
		}
		if op.Action == proto.ActionUpdateEFS && op.EFS != nil && op.EFS.ID == share.ID && op.EFS.GuestPath == "/var/www/html" {
			foundEFS = true
		}
	}
	if !foundCreate || !foundEFS {
		t.Fatalf("queued ops: %+v", ops)
	}
	s.reconcileLoadBalancers()
	ops, _ = m.TakeOps("w1")
	for _, op := range ops {
		if op.Action == proto.ActionUpdateEFS {
			t.Fatal("already attached members must not re-queue update-efs")
		}
	}
	moved := strings.Replace(payload, `"efs_id":"`+share.ID+`"`, `"efs_id":"`+share.ID+`","efs_path":"/mnt/site"`, 1)
	if w := do(s.AdminHandler(), "PUT", "/api/load-balancers/"+b.ID, moved, tok); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s.reconcileLoadBalancers()
	inst, err = m.GetInstance(saved.Instances[0])
	if err != nil || len(inst.Spec.EFSMounts) != 1 || inst.Spec.EFSMounts[0].GuestPath != "/mnt/site" {
		t.Fatalf("path change: %+v %v", inst.Spec.EFSMounts, err)
	}
	ops, _ = m.TakeOps("w1")
	foundMove := false
	for _, op := range ops {
		if op.Action == proto.ActionUpdateEFS && op.EFS != nil && op.EFS.GuestPath == "/mnt/site" {
			foundMove = true
		}
	}
	if !foundMove {
		t.Fatalf("path change not queued: %+v", ops)
	}
}
