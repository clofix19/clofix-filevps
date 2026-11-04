package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	dock "github.com/clofix/filevps/worker/pkg/docker"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestWAFUIPagesAndIAM(t *testing.T) {
	if page, title, _, ok := uiPage("/waf"); !ok || page != "waf" || title != "WAF" {
		t.Fatalf("waf page: %s %s %v", page, title, ok)
	}
	if page, title, id, ok := uiPage("/waf/bindings/waf-" + strings.Repeat("a", 32)); !ok || page != "waf-binding" || title != "WAF Binding" || !strings.HasPrefix(id, "waf-") {
		t.Fatalf("waf binding page: %s %s %s %v", page, title, id, ok)
	}
	if permissionResource("/api/waf") != "security" || permissionResource("/api/waf/bindings/x") != "security" {
		t.Fatal("waf IAM resource")
	}
}

func TestWAFBindingCreateStatsErrorPagesDelete(t *testing.T) {
	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-waf", Hostname: "waf1", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{
			RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4,
			DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true, WAFReady: true,
		},
	})
	h := s.AdminHandler()
	html := do(h, "GET", "/waf", "", tok)
	if html.Code != 200 || !strings.Contains(html.Body.String(), "WAF") || !strings.Contains(html.Body.String(), `id="waf-bindings"`) {
		t.Fatalf("waf ui: %d", html.Code)
	}

	cid := dockerRandID("dck")
	s.docker.mu.Lock()
	s.docker.Containers[cid] = dockerContainer{
		ID: cid, Owner: "acc-platform", Name: "edge-web", WorkerID: "wrk-waf", State: "Running",
		Spec: dock.ContainerSpec{ID: cid, Name: "edge-web", Image: "nginx", Tag: "alpine", RAMMB: 128, CPU: 0.5,
			Ports: []dock.PortPublish{{ContainerPort: 80, HostPort: 18081}},
		},
	}
	s.docker.mu.Unlock()

	overview := do(h, "GET", "/api/waf", "", tok)
	if overview.Code != 200 {
		t.Fatalf("get waf: %d %s", overview.Code, overview.Body.String())
	}
	var ov map[string]any
	_ = json.Unmarshal(overview.Body.Bytes(), &ov)
	if _, ok := ov["bindings"]; !ok {
		t.Fatal("bindings missing from /api/waf")
	}

	create := do(h, "POST", "/api/waf/bindings", `{"name":"edge-web-waf","target_kind":"docker_container","target_id":"`+cid+`","activate":false}`, tok)
	if create.Code != 201 {
		t.Fatalf("create binding: %d %s", create.Code, create.Body.String())
	}
	var b wafBinding
	if err := json.Unmarshal(create.Body.Bytes(), &b); err != nil || b.ID == "" {
		t.Fatalf("binding body: %s", create.Body.String())
	}
	if b.State != "INACTIVE" {
		t.Fatalf("state=%s want INACTIVE", b.State)
	}
	dup := do(h, "POST", "/api/waf/bindings", `{"target_kind":"docker_container","target_id":"`+cid+`","activate":false}`, tok)
	if dup.Code != 409 {
		t.Fatalf("duplicate want 409 got %d", dup.Code)
	}

	stats := do(h, "GET", "/api/waf/bindings/"+b.ID+"/stats", "", tok)
	if stats.Code != 200 || !strings.Contains(stats.Body.String(), `"stats":null`) {
		t.Fatalf("empty stats: %d %s", stats.Code, stats.Body.String())
	}

	pages := do(h, "PUT", "/api/waf/bindings/"+b.ID+"/error-pages", `{"status":"403","html":"<!DOCTYPE html><html><body>blocked</body></html>"}`, tok)
	if pages.Code != 202 {
		t.Fatalf("error pages: %d %s", pages.Code, pages.Body.String())
	}
	got, ok := s.waf.get(b.ID)
	if !ok || got.WAFErrorPages["403"] == "" {
		t.Fatalf("error page not stored: %+v", got.WAFErrorPages)
	}

	cat := do(h, "POST", "/api/waf/bindings/"+b.ID+"/category", `{"category":"sqli","enabled":true}`, tok)
	if cat.Code != 202 && cat.Code != 200 {
		t.Fatalf("category: %d %s", cat.Code, cat.Body.String())
	}

	detail := do(h, "GET", "/waf/bindings/"+b.ID, "", tok)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "wafb-stats-kpis") {
		t.Fatalf("binding detail ui: %d", detail.Code)
	}

	del := do(h, "DELETE", "/api/waf/bindings/"+b.ID, "", tok)
	if del.Code != 200 && del.Code != 202 && del.Code != 204 {
		t.Fatalf("delete binding: %d %s", del.Code, del.Body.String())
	}
	if _, ok := s.waf.get(b.ID); ok {
		t.Fatal("binding still present after delete")
	}
}
