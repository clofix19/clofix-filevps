package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	dock "github.com/clofix/filevps/worker/pkg/docker"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestDockerUIPageAndIAM(t *testing.T) {
	if page, title, _, ok := uiPage("/docker"); !ok || page != "docker" || title != "Docker" {
		t.Fatalf("docker page: %s %s %v", page, title, ok)
	}
	if permissionResource("/api/docker") != "docker" || permissionResource("/api/docker/containers") != "docker" {
		t.Fatal("docker IAM resource")
	}
	if !iamPermissionSet["docker:read"] || !iamPermissionSet["docker:manage"] {
		t.Fatal("docker permissions missing from catalog")
	}
}

func TestValidateComposePolicy(t *testing.T) {
	if err := validateComposePolicy(""); err == nil {
		t.Fatal("empty yaml")
	}
	if err := validateComposePolicy("services:\n  web:\n    image: nginx\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"privileged: true",
		"network_mode: host",
		"/var/run/docker.sock",
		"pid: host",
	} {
		if err := validateComposePolicy("services:\n  x:\n    " + bad + "\n"); err == nil {
			t.Fatalf("expected reject for %s", bad)
		}
	}
}

func TestDockerContainerSpecSecurityDefaults(t *testing.T) {
	id := dockerRandID("dck")
	ok := dock.ContainerSpec{ID: id, Name: "web", Image: "nginx", RAMMB: 128, CPU: 0.5, Ports: []dock.PortPublish{{ContainerPort: 80, HostPort: 18080}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	badPriv := ok
	badPriv.Privileged = true
	if err := badPriv.Validate(); err == nil {
		t.Fatal("privileged must fail")
	}
	badHost := ok
	badHost.HostNetwork = true
	if err := badHost.Validate(); err == nil {
		t.Fatal("host network must fail")
	}
	badPort := ok
	badPort.Ports = []dock.PortPublish{{ContainerPort: 80, HostPort: 80}}
	if err := badPort.Validate(); err == nil {
		t.Fatal("out-of-range host port must fail")
	}
}

func TestDockerOverviewAuthAndTemplates(t *testing.T) {
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	if w := do(h, "GET", "/api/docker/overview", "", ""); w.Code != 401 {
		t.Fatalf("unauth overview %d", w.Code)
	}
	ov := do(h, "GET", "/api/docker/overview", "", tok)
	if ov.Code != 200 {
		t.Fatal(ov.Body.String())
	}
	tpl := do(h, "GET", "/api/docker/templates", "", tok)
	if tpl.Code != 200 {
		t.Fatal(tpl.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(tpl.Body.Bytes(), &items); err != nil || len(items) < 10 {
		t.Fatalf("templates: %s", tpl.Body.String())
	}
	html := do(h, "GET", "/docker", "", tok)
	if html.Code != 200 || !strings.Contains(html.Body.String(), "Docker") || !strings.Contains(html.Body.String(), `id="docker-containers"`) {
		t.Fatalf("docker ui: %d", html.Code)
	}
}

func TestDockerSchedulingRejectWhenEngineNotReady(t *testing.T) {
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-docker-off", Hostname: "d1", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: false},
	})
	h := s.AdminHandler()
	w := do(h, "POST", "/api/docker/containers", `{"name":"nginx","image":"nginx","ram_mb":128,"cpu":0.5}`, tok)
	if w.Code != 409 {
		t.Fatalf("want 409 when docker not ready, got %d %s", w.Code, w.Body.String())
	}
}

func TestDockerPortConflictAndPlace(t *testing.T) {
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-docker-on", Hostname: "d2", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true, DockerVersion: "27.0"},
	})
	_ = m.SaveInstance(store.Instance{
		ID: "vps-1", WorkerID: "wrk-docker-on", State: proto.StateRunning,
		Spec: proto.InstanceSpec{PortMappings: []proto.PortMapping{{HostPort: 18080, GuestPort: 80}}},
	})
	if err := s.checkPortConflicts("wrk-docker-on", []dock.PortPublish{{HostPort: 18080, ContainerPort: 80}}, ""); err == nil {
		t.Fatal("expected port conflict with FileVPS mapping")
	}
	h := s.AdminHandler()
	created := do(h, "POST", "/api/docker/containers", `{"name":"web","image":"nginx","ram_mb":128,"cpu":0.5,"ports":[{"container_port":80,"host_port":18081}]}`, tok)
	if created.Code != 202 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	conflict := do(h, "POST", "/api/docker/containers", `{"name":"web2","image":"nginx","ram_mb":128,"cpu":0.5,"ports":[{"container_port":80,"host_port":18081}]}`, tok)
	if conflict.Code != 409 {
		t.Fatalf("want port conflict 409 got %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestDockerCrossTenantDeny(t *testing.T) {
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-docker-on", Hostname: "d2", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true},
	})
	id := dockerRandID("dck")
	s.docker.mu.Lock()
	s.docker.Containers[id] = dockerContainer{
		ID: id, Owner: "acc-other", Name: "secret", WorkerID: "wrk-docker-on", State: "Running",
		Spec: dock.ContainerSpec{ID: id, Name: "secret", Image: "nginx", RAMMB: 128, CPU: 0.5},
	}
	_ = s.docker.save()
	s.docker.mu.Unlock()
	req, _ := http.NewRequest("GET", "/api/docker/containers/"+id, nil)
	r := req.WithContext(context.WithValue(context.Background(), iamPrincipalContextKey{}, iamPrincipal{UserID: "u-client", AccountID: "acc-client", Email: "c@x.test"}))
	if canAccessDocker(r, "acc-other") {
		t.Fatal("cross-tenant access must be denied")
	}
	_ = m
}

func TestDockerQuotaReject(t *testing.T) {
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-docker-on", Hostname: "d2", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true},
	})
	zero := 0
	s.license.Grant.Plan.MaxDockerContainers = &zero
	h := s.AdminHandler()
	w := do(h, "POST", "/api/docker/containers", `{"name":"nginx","image":"nginx","ram_mb":128,"cpu":0.5}`, tok)
	if w.Code != 403 && w.Code != 409 && w.Code != 402 {
		// license middleware returns 403 when quota exceeded
		if w.Code == 202 {
			t.Fatalf("quota 0 must reject create, got %d %s", w.Code, w.Body.String())
		}
	}
	if w.Code == 202 {
		t.Fatal("unexpected success under zero quota")
	}
}

func TestDockerComposeAndImageAppPaths(t *testing.T) {
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-docker-on", Hostname: "d2", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true},
	})
	h := s.AdminHandler()
	bad := do(h, "POST", "/api/docker/applications", `{"name":"bad","method":"compose","compose_yaml":"services:\n  x:\n    privileged: true\n    image: nginx\n","ram_mb":256,"cpu":0.5}`, tok)
	if bad.Code != 400 {
		t.Fatalf("compose policy: %d %s", bad.Code, bad.Body.String())
	}
	ok := do(h, "POST", "/api/docker/applications", `{"name":"stack","method":"compose","compose_yaml":"services:\n  web:\n    image: nginx:alpine\n","ram_mb":256,"cpu":0.5}`, tok)
	if ok.Code != 202 {
		t.Fatalf("compose deploy: %d %s", ok.Code, ok.Body.String())
	}
	img := do(h, "POST", "/api/docker/applications", `{"name":"nginx-app","method":"image","image":"nginx","tag":"alpine","ram_mb":128,"cpu":0.5,"ports":[{"container_port":80,"host_port":18090}]}`, tok)
	if img.Code != 202 {
		t.Fatalf("image app: %d %s", img.Code, img.Body.String())
	}
	quick := do(h, "POST", "/api/docker/applications", `{"name":"qa","method":"quickapp","template":"redis","ram_mb":256,"cpu":0.5}`, tok)
	if quick.Code != 202 {
		t.Fatalf("quickapp: %d %s", quick.Code, quick.Body.String())
	}
}

func TestDockerNetworksVolumesBackupsSecurity(t *testing.T) {
	s, m := newSrv(t)
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-docker-on", Hostname: "d2", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 7 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 50 << 30, DockerReady: true},
	})
	h := s.AdminHandler()
	net := do(h, "POST", "/api/docker/networks", `{"name":"app-net","internal":true}`, tok)
	if net.Code != 201 && net.Code != 202 && net.Code != 200 {
		t.Fatalf("network: %d %s", net.Code, net.Body.String())
	}
	vol := do(h, "POST", "/api/docker/volumes", `{"name":"data","delete_protection":true}`, tok)
	if vol.Code != 201 && vol.Code != 202 && vol.Code != 200 {
		t.Fatalf("volume: %d %s", vol.Code, vol.Body.String())
	}
	var v struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(vol.Body.Bytes(), &v)
	if v.ID == "" {
		// some handlers wrap object
		var wrap map[string]any
		_ = json.Unmarshal(vol.Body.Bytes(), &wrap)
		if id, _ := wrap["id"].(string); id != "" {
			v.ID = id
		}
	}
	if v.ID != "" {
		del := do(h, "DELETE", "/api/docker/volumes/"+v.ID, "", tok)
		if del.Code != 409 && del.Code != 400 {
			// delete protection should block
			if del.Code == 200 || del.Code == 204 {
				t.Fatal("delete protection should block")
			}
		}
		bak := do(h, "POST", "/api/docker/backups", `{"kind":"volume","target_id":"`+v.ID+`"}`, tok)
		if bak.Code != 202 && bak.Code != 201 && bak.Code != 200 {
			t.Fatalf("backup: %d %s", bak.Code, bak.Body.String())
		}
	}
	sec := do(h, "GET", "/api/docker/security", "", tok)
	if sec.Code != 200 {
		t.Fatal(sec.Body.String())
	}
	scan := do(h, "POST", "/api/docker/security/scan", `{"image":"nginx:alpine"}`, tok)
	if scan.Code != 202 && scan.Code != 200 {
		t.Fatalf("scan: %d %s", scan.Code, scan.Body.String())
	}
}

func TestDockerFileVPSRegressionSmoke(t *testing.T) {
	s, _ := newSrv(t)
	h := s.AdminHandler()
	if w := do(h, "GET", "/api/instances", "", tok); w.Code != 200 {
		t.Fatalf("instances: %d", w.Code)
	}
	if w := do(h, "GET", "/api/docker/overview", "", tok); w.Code != 200 {
		t.Fatalf("docker overview: %d", w.Code)
	}
}
