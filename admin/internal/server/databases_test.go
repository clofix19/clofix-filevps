package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	model "github.com/clofix/filevps/admin/internal/database"
	"github.com/clofix/filevps/admin/internal/store"
	db "github.com/clofix/filevps/worker/pkg/database"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func databaseWorkerFixture() store.Worker {
	return store.Worker{ID: "wrk-database", Hostname: "db-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{DatabaseEngines: map[string]string{"mysql": "8.4", "postgresql": "17", "mongodb": "8.0"}, DatabasePublicHost: "192.0.2.20", RAMTotal: 8 << 30, HostReserve: 512 << 20, MemAvailable: 6 << 30, CPUCores: 4, DiskTotal: 100 << 30, DiskFree: 80 << 30}}
}

func TestDatabaseProvisioningMetadataAndCompletion(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(databaseWorkerFixture())
	body := `{"name":"orders","spec":{"engine":"mysql","version":"8.4","ram_mb":1024,"cpu":1,"disk_mb":2048}}`
	response := do(s.AdminHandler(), "POST", "/api/databases/projects", body, tok)
	if response.Code != 202 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Project struct {
			Spec db.Project `json:"spec"`
		} `json:"project"`
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(response.Body.Bytes(), &created) != nil || created.JobID == "" {
		t.Fatal("missing project or job")
	}
	s.databases.Lock()
	job := s.databases.Jobs[created.JobID]
	if bytes.Contains(job.Payload, []byte(`"action"`)) || bytes.Contains(job.Payload, []byte("provision")) {
		t.Fatal("database job payload stored in plaintext")
	}
	s.databases.Unlock()
	ops, acknowledged := s.databaseHeartbeat("wrk-database", nil, nil)
	if len(acknowledged) != 0 || len(ops) != 1 || ops[0].DatabaseJobID != created.JobID {
		t.Fatalf("dispatch mismatch: %+v", ops)
	}
	resultData, _ := json.Marshal(map[string]string{"ca": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----", "engine_version": "8.4"})
	_, acknowledged = s.databaseHeartbeat("wrk-database", []db.Result{{JobID: created.JobID, ProjectID: created.Project.Spec.ID, State: "SUCCEEDED", Progress: 100, Data: resultData}}, nil)
	if len(acknowledged) != 1 {
		t.Fatal("worker completion was not acknowledged")
	}
	s.databases.Lock()
	project := s.databases.Projects[created.Project.Spec.ID]
	s.databases.Unlock()
	if project.State != "READY" || !strings.Contains(project.CA, "BEGIN CERTIFICATE") {
		t.Fatalf("project not completed: %+v", project)
	}
}

func TestDatabaseLicenseDeniesLegacyPlanAndQuota(t *testing.T) {
	s, memory := newSrv(t)
	memory.UpsertWorker(databaseWorkerFixture())
	s.license.Lock()
	s.license.Grant.Plan.MaxDatabaseProjects = nil
	s.license.Unlock()
	response := do(s.AdminHandler(), "POST", "/api/databases/projects", `{"name":"denied","spec":{"engine":"mysql","version":"8.4","ram_mb":512,"cpu":1,"disk_mb":512}}`, tok)
	if response.Code != 403 {
		t.Fatalf("legacy plan must not imply database entitlement: %d %s", response.Code, response.Body.String())
	}
	one := 1
	s.license.Lock()
	s.license.Grant.Plan.MaxDatabaseProjects = &one
	s.license.Unlock()
	valid := `{"name":"one","spec":{"engine":"mysql","version":"8.4","ram_mb":512,"cpu":1,"disk_mb":512}}`
	if response = do(s.AdminHandler(), "POST", "/api/databases/projects", valid, tok); response.Code != 202 {
		t.Fatalf("first allocation: %d %s", response.Code, response.Body.String())
	}
	if response = do(s.AdminHandler(), "POST", "/api/databases/projects", valid, tok); response.Code != 403 {
		t.Fatalf("project quota not enforced: %d %s", response.Code, response.Body.String())
	}
}

func TestDatabaseOperationRejectsUnknownInventoryAndReservedUser(t *testing.T) {
	s, _ := newSrv(t)
	projectID := "dbp-" + strings.Repeat("a", 32)
	s.databases.Lock()
	s.databases.Projects[projectID] = databaseProjectFixture(projectID)
	s.databases.Unlock()
	for _, body := range []string{`{"action":"create-table","database":"missing","table":"items","columns":[{"name":"id","type":"INTEGER"}]}`, `{"action":"create-user","username":"clofixadmin","password":"1234567890123456","grants":{"orders":"write"}}`} {
		response := do(s.AdminHandler(), "POST", "/api/databases/projects/"+projectID+"/operations", body, tok)
		if response.Code != 400 {
			t.Fatalf("unsafe operation accepted: %d %s", response.Code, response.Body.String())
		}
	}
}

func databaseProjectFixture(id string) model.Project {
	return model.Project{Spec: db.Project{ID: id, Engine: "mysql", Version: "8.4", RAMMB: 512, CPU: 1, DiskMB: 512, Port: 26000, Host: "192.0.2.20"}, Owner: "root", Name: "test", WorkerID: "worker", State: "READY", Created: time.Now(), Databases: map[string]bool{"orders": true}, Users: map[string]map[string]string{}}
}
