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

func TestWAFBindingsAreAccountScoped(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-waf-iso", Hostname: "waf-iso", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{
			RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4,
			DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true, WAFReady: true,
		},
	})
	h := s.AdminHandler()

	adminCID := dockerRandID("dck")
	s.docker.mu.Lock()
	s.docker.Containers[adminCID] = dockerContainer{
		ID: adminCID, Owner: "root", Name: "admin-edge", WorkerID: "wrk-waf-iso", State: "Running",
		Spec: dock.ContainerSpec{ID: adminCID, Name: "admin-edge", Image: "nginx", Tag: "alpine", RAMMB: 128, CPU: 0.5},
	}
	s.docker.mu.Unlock()
	// Stale Owner="root" on a binding whose live target belongs to another account must not leak.
	staleID := "waf-" + strings.Repeat("b", 32)
	_ = s.waf.put(wafBinding{
		ID: staleID, Name: "stale-root-owner", TargetKind: "docker_container", TargetID: adminCID,
		WorkerID: "wrk-waf-iso", State: "INACTIVE", Owner: "root", Created: time.Now().UTC(), Updated: time.Now().UTC(),
	})

	reg := do(h, "POST", "/api/auth/register", `{"email":"waf-client@client.test","password":"client-password-123","account_name":"WAF Client"}`, "")
	if reg.Code != 200 {
		t.Fatalf("register: %s", reg.Body.String())
	}
	cookie := sessionCookieFrom(reg)

	clientCID := dockerRandID("dck")
	me := doCookie(h, "GET", "/api/auth/me", "", cookie)
	if me.Code != 200 {
		t.Fatal(me.Body.String())
	}
	var meOut struct {
		AccountID string `json:"account_id"`
		Account   struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	_ = json.Unmarshal(me.Body.Bytes(), &meOut)
	accID := meOut.AccountID
	if accID == "" {
		accID = meOut.Account.ID
	}
	if accID == "" {
		t.Fatalf("could not resolve client account id: %s", me.Body.String())
	}
	s.docker.mu.Lock()
	s.docker.Containers[clientCID] = dockerContainer{
		ID: clientCID, Owner: accID, Name: "client-edge", WorkerID: "wrk-waf-iso", State: "Running",
		Spec: dock.ContainerSpec{ID: clientCID, Name: "client-edge", Image: "nginx", Tag: "alpine", RAMMB: 128, CPU: 0.5},
	}
	s.docker.mu.Unlock()
	clientBind := doCookie(h, "POST", "/api/waf/bindings", `{"name":"client-waf","target_kind":"docker_container","target_id":"`+clientCID+`","activate":false}`, cookie)
	if clientBind.Code != 201 {
		t.Fatalf("client create: %d %s", clientBind.Code, clientBind.Body.String())
	}
	var cb wafBinding
	_ = json.Unmarshal(clientBind.Body.Bytes(), &cb)

	overview := doCookie(h, "GET", "/api/waf", "", cookie)
	if overview.Code != 200 {
		t.Fatal(overview.Body.String())
	}
	body := overview.Body.String()
	if strings.Contains(body, staleID) || strings.Contains(body, adminCID) {
		t.Fatalf("client must not see admin/stale-root WAF bindings: %s", body)
	}
	if !strings.Contains(body, cb.ID) {
		t.Fatalf("client should see own binding: %s", body)
	}
	if w := doCookie(h, "GET", "/api/waf/bindings/"+staleID+"/stats", "", cookie); w.Code != 404 {
		t.Fatalf("client cannot read admin binding stats: %d %s", w.Code, w.Body.String())
	}
}

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
