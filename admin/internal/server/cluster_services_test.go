package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestClusterServiceCreateValidation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	_ = s.Store.UpsertWorker(store.Worker{
		ID: "wrk-cluster", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{
			RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 6 << 30, RAMCommitted: 0,
			CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30, DiskCommitted: 0,
			SwapTotal: 2 << 30, SwapFree: 2 << 30,
		},
	})

	badApp := do(h, "POST", "/api/cluster-services", `{"name":"x","app":"wildfly","topology":"master_master","storage":"temp","node_count":3}`, tok)
	if badApp.Code != 400 {
		t.Fatalf("wildfly should be rejected: %d %s", badApp.Code, badApp.Body.String())
	}
	tooFew := do(h, "POST", "/api/cluster-services", `{"name":"x","app":"redis","topology":"master_master","storage":"temp","node_count":2}`, tok)
	if tooFew.Code != 400 {
		t.Fatalf("master_master needs 3: %d %s", tooFew.Code, tooFew.Body.String())
	}
	ok := do(h, "POST", "/api/cluster-services", `{"name":"redis-lab","app":"redis","topology":"master_master","storage":"temp","node_count":3,"ram_mb":256,"cpu":0.5,"disk_mb":2048}`, tok)
	if ok.Code != 202 {
		t.Fatalf("create: %d %s", ok.Code, ok.Body.String())
	}
	var created clusterService
	if err := json.Unmarshal(ok.Body.Bytes(), &created); err != nil || created.ID == "" || created.Status != clusterStatusProvisioning || len(created.Nodes) != 3 {
		t.Fatalf("created payload: %+v err=%v", created, err)
	}
	list := do(h, "GET", "/api/cluster-services", "", tok)
	if list.Code != 200 {
		t.Fatalf("list: %s", list.Body.String())
	}
	var wrap struct {
		Clusters []clusterService `json:"clusters"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range wrap.Clusters {
		if c.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("created cluster missing from list")
	}
	got := do(h, "GET", "/api/cluster-services/"+created.ID, "", tok)
	if got.Code != 200 {
		t.Fatalf("get: %d %s", got.Code, got.Body.String())
	}
	del := do(h, "DELETE", "/api/cluster-services/"+created.ID, "", tok)
	if del.Code != 202 {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
}

func TestRedisClusterLicenseQuota(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	_ = s.Store.UpsertWorker(store.Worker{
		ID: "wrk-redis-quota", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{
			RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 6 << 30,
			CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30,
			SwapTotal: 2 << 30, SwapFree: 2 << 30,
		},
	})
	zero := 0
	s.license.Grant.Plan.MaxRedisClusters = &zero
	blocked := do(h, "POST", "/api/cluster-services", `{"name":"blocked","app":"redis","topology":"master_master","storage":"temp","node_count":3,"ram_mb":256,"cpu":0.5,"disk_mb":2048}`, tok)
	if blocked.Code != 403 {
		t.Fatalf("zero redis cluster quota must block create: %d %s", blocked.Code, blocked.Body.String())
	}
	one := 1
	s.license.Grant.Plan.MaxRedisClusters = &one
	ok := do(h, "POST", "/api/cluster-services", `{"name":"allowed","app":"redis","topology":"master_master","storage":"temp","node_count":3,"ram_mb":256,"cpu":0.5,"disk_mb":2048}`, tok)
	if ok.Code != 202 {
		t.Fatalf("create under limit: %d %s", ok.Code, ok.Body.String())
	}
	again := do(h, "POST", "/api/cluster-services", `{"name":"second","app":"redis","topology":"master_master","storage":"temp","node_count":3,"ram_mb":256,"cpu":0.5,"disk_mb":2048}`, tok)
	if again.Code != 403 {
		t.Fatalf("second redis cluster must hit quota: %d %s", again.Code, again.Body.String())
	}
	usage := do(h, "GET", "/api/license/usage", "", tok)
	if usage.Code != 200 {
		t.Fatalf("usage: %d %s", usage.Code, usage.Body.String())
	}
	var body struct {
		Quotas map[string]struct {
			Used  int64 `json:"used"`
			Limit int64 `json:"limit"`
		} `json:"quotas"`
	}
	if err := json.Unmarshal(usage.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	q := body.Quotas["redis-clusters"]
	if q.Used != 1 || q.Limit != 1 {
		t.Fatalf("redis-clusters usage want used=1 limit=1 got %+v", q)
	}
}
