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
	dock "github.com/clofix/filevps/worker/pkg/docker"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

// Suites 19–22 + DB09–DB16 production-grade unit coverage.

func TestProductionVPSControls(t *testing.T) {
	s, m := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	_ = m.UpsertWorker(store.Worker{
		ID: "wrk-vp", Hostname: "vp1", Status: "active", LastHeartbeat: time.Now(),
		Capacity: scheduler.Capacity{
			RAMTotal: 16 << 30, HostReserve: 512 << 20, MemAvailable: 12 << 30, CPUCores: 8,
			DiskTotal: 200 << 30, DiskFree: 100 << 30, DockerReady: true, WAFReady: true, UserNS: true,
		},
	})

	// VP01 — Protection Mode default-on for new guests
	create := do(h, "POST", "/api/instances", `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":256,"swap_mb":0,"cpu":0.5,"disk_mb":1024,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`, tok)
	if create.Code != 201 {
		t.Fatalf("VP01 create: %d %s", create.Code, create.Body.String())
	}
	var created struct {
		Instance store.Instance `json:"instance"`
	}
	if json.Unmarshal(create.Body.Bytes(), &created) != nil || created.Instance.ID == "" {
		t.Fatal("VP01 missing instance")
	}
	if created.Instance.Spec.ProtectionMode == nil || !*created.Instance.Spec.ProtectionMode {
		t.Fatal("VP01 protection_mode must default on")
	}
	id := created.Instance.ID

	// Ensure stopped for backup + delete-protection probes (worker already heartbeating).
	inst, _ := m.GetInstance(id)
	inst.State = proto.StateStopped
	_ = m.SaveInstance(inst)

	// VP02 — SSH key-only / login block
	if w := do(h, "POST", "/api/instances/"+id+"/ssh-login", `{"enabled":true}`, tok); w.Code != 202 && w.Code != 200 {
		t.Fatalf("VP02 ssh-login: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/instances/"+id+"/ssh-key-only", `{"enabled":true}`, tok); w.Code != 202 && w.Code != 200 {
		t.Fatalf("VP02 ssh-key-only: %d %s", w.Code, w.Body.String())
	}

	// VP03 — Delete Protection blocks destroy
	if w := do(h, "POST", "/api/instances/"+id+"/delete-protection", `{"enabled":true}`, tok); w.Code != 202 && w.Code != 200 {
		t.Fatalf("VP03 enable: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "DELETE", "/api/instances/"+id, "", tok); w.Code != 409 {
		t.Fatalf("VP03 delete with protection want 409 got %d %s", w.Code, w.Body.String())
	}

	// VP04 — Security modes + quarantine dry-run
	modes := do(h, "GET", "/api/security/modes", "", tok)
	if modes.Code != 200 {
		t.Fatalf("VP04 modes: %d", modes.Code)
	}
	dry := do(h, "POST", "/api/security/modes/tpl-lockdown/dry-run", `{"instance_ids":["`+id+`"]}`, tok)
	if dry.Code != 200 {
		t.Fatalf("VP04 dry-run: %d %s", dry.Code, dry.Body.String())
	}

	// VP05 — cloud published port ranges
	ssh := 22010
	if !proto.ValidHostSSHPort(ssh) || proto.HostHTTPPort(ssh) != 27010 || proto.HostHTTPSPort(ssh) != 32010 {
		t.Fatalf("VP05 map wrong http=%d https=%d", proto.HostHTTPPort(ssh), proto.HostHTTPSPort(ssh))
	}
	if !proto.ValidHostCustomPort(proto.HostCustomBase) || !proto.ValidHostDatabasePort(proto.HostDatabaseBase) {
		t.Fatal("VP05 custom/database bands invalid")
	}

	// VP06 — Firewall CIDR allow on mapped ports
	fw := do(h, "PUT", "/api/firewall/workers/wrk-vp", `{"restrict":true,"rules":[{"action":"allow","protocol":"tcp","port":22010,"source":"10.0.0.0/8"}]}`, tok)
	if fw.Code != 200 {
		t.Fatalf("VP06 firewall: %d %s", fw.Code, fw.Body.String())
	}

	// VP07 — Edge WAF binding for FileVPS HTTP path
	waf := do(h, "POST", "/api/waf/bindings", `{"name":"vps-waf","target_kind":"vps","target_id":"`+id+`","activate":false}`, tok)
	if waf.Code != 201 && waf.Code != 202 {
		t.Fatalf("VP07 waf bind: %d %s", waf.Code, waf.Body.String())
	}

	// VP08 — writable-layer backup only when stopped
	running := inst
	running.State = proto.StateRunning
	_ = m.SaveInstance(running)
	if w := do(h, "POST", "/api/instances/"+id+"/backups", "{}", tok); w.Code != 409 {
		t.Fatalf("VP08 running backup want 409 got %d", w.Code)
	}
	_ = m.SaveInstance(inst) // stopped again
	if w := do(h, "POST", "/api/instances/"+id+"/backups", "{}", tok); w.Code != 202 {
		t.Fatalf("VP08 stopped backup: %d %s", w.Code, w.Body.String())
	}

	// VP09 — placement capacity / cgroup fields present on worker
	wk := do(h, "GET", "/api/workers", "", tok)
	if wk.Code != 200 || !strings.Contains(wk.Body.String(), "wrk-vp") {
		t.Fatalf("VP09 workers: %d", wk.Code)
	}

	// VP10 — cross-tenant isolation (reuse register pattern)
	regA := do(h, "POST", "/api/auth/register", `{"email":"vp-a@client.test","password":"client-password-123","account_name":"VP A"}`, "")
	regB := do(h, "POST", "/api/auth/register", `{"email":"vp-b@client.test","password":"client-password-123","account_name":"VP B"}`, "")
	if regA.Code != 200 || regB.Code != 200 {
		t.Fatal(regA.Body.String(), regB.Body.String())
	}
	own := doCookie(h, "POST", "/api/instances", `{"spec":{"image":{"name":"ubuntu","version":"24.04"},"ram_mb":256,"swap_mb":0,"cpu":0.5,"disk_mb":1024,"ssh_authorized_key":"ssh-ed25519 QUFB user@test"}}`, sessionCookieFrom(regA))
	if own.Code != 201 {
		t.Fatal(own.Body.String())
	}
	var tenant struct {
		Instance store.Instance `json:"instance"`
	}
	_ = json.Unmarshal(own.Body.Bytes(), &tenant)
	listed := doCookie(h, "GET", "/api/instances", "", sessionCookieFrom(regB))
	if listed.Code != 200 || strings.Contains(listed.Body.String(), tenant.Instance.ID) {
		t.Fatal("VP10 client B must not see client A VPS")
	}

	// VP11 — volume / EFS path conflict rejection
	if w := do(h, "GET", "/api/volumes/vol-missing01/archive?path=../secret", "", tok); w.Code != 400 {
		t.Fatalf("VP11 volume path: %d", w.Code)
	}

	// VP12 — IAM security:manage mapping for hardening mutations
	if permissionResource("/api/instances/"+id+"/protection") != "security" {
		t.Fatal("VP12 protection must map to security")
	}
	if permissionResource("/api/instances/"+id+"/ssh-login") != "security" {
		t.Fatal("VP12 ssh-login must map to security")
	}
	if permissionResource("/api/instances/"+id+"/delete-protection") != "security" {
		t.Fatal("VP12 delete-protection must map to security")
	}
}

func TestProductionManagedDatabase(t *testing.T) {
	s, memory := newSrv(t)
	worker := databaseWorkerFixture()
	worker.Capacity.DatabaseEngines["redis"] = "7.4"
	memory.UpsertWorker(worker)
	h := s.AdminHandler()

	// MDB02 — engines advertised (mysql/pg/mongo/redis)
	workers := do(h, "GET", "/api/workers", "", tok)
	body := workers.Body.String()
	for _, eng := range []string{"mysql", "postgresql", "mongodb", "redis"} {
		if !strings.Contains(body, eng) {
			t.Fatalf("MDB02 missing engine %s in %s", eng, body)
		}
	}

	// MDB01 + MDB03 — provision + TLS CA + no plaintext job payload
	response := do(h, "POST", "/api/databases/projects", `{"name":"orders-prod","spec":{"engine":"mysql","version":"8.4","ram_mb":1024,"cpu":1,"disk_mb":2048}}`, tok)
	if response.Code != 202 {
		t.Fatalf("MDB01 create: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Project struct {
			Spec db.Project `json:"spec"`
		} `json:"project"`
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(response.Body.Bytes(), &created) != nil || created.JobID == "" {
		t.Fatal("MDB01 missing project/job")
	}
	// MDB10 — published port band
	if !proto.ValidHostDatabasePort(created.Project.Spec.Port) {
		t.Fatalf("MDB10 port out of band: %d", created.Project.Spec.Port)
	}
	s.databases.Lock()
	job := s.databases.Jobs[created.JobID]
	if bytes.Contains(job.Payload, []byte(`"action"`)) || bytes.Contains(job.Payload, []byte("provision")) {
		s.databases.Unlock()
		t.Fatal("MDB03 database job payload stored in plaintext")
	}
	s.databases.Unlock()

	resultData, _ := json.Marshal(map[string]string{"ca": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----", "engine_version": "8.4"})
	_, ack := s.databaseHeartbeat("wrk-database", []db.Result{{JobID: created.JobID, ProjectID: created.Project.Spec.ID, State: "SUCCEEDED", Progress: 100, Data: resultData}}, nil)
	if len(ack) != 1 {
		t.Fatal("MDB01 completion not acknowledged")
	}
	s.databases.Lock()
	project := s.databases.Projects[created.Project.Spec.ID]
	s.databases.Unlock()
	if project.State != "READY" || !strings.Contains(project.CA, "BEGIN CERTIFICATE") {
		t.Fatalf("MDB01 not READY with CA: %+v", project)
	}
	// DB09 / DB11 — TLS CA download + engine version pin
	ca := do(h, "GET", "/api/databases/projects/"+created.Project.Spec.ID+"/ca.pem", "", tok)
	if ca.Code != 200 || !strings.Contains(ca.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("DB09 CA: %d", ca.Code)
	}
	if project.Spec.Version != "8.4" {
		t.Fatalf("DB11 version pin: %s", project.Spec.Version)
	}

	// MDB04 / DB14 — license deny without entitlement
	s2, mem2 := newSrv(t)
	mem2.UpsertWorker(databaseWorkerFixture())
	s2.license.Lock()
	s2.license.Grant.Plan.MaxDatabaseProjects = nil
	s2.license.Unlock()
	if denied := do(s2.AdminHandler(), "POST", "/api/databases/projects", `{"name":"x","spec":{"engine":"mysql","version":"8.4","ram_mb":512,"cpu":1,"disk_mb":512}}`, tok); denied.Code != 403 {
		t.Fatalf("MDB04/DB14 want 403 got %d", denied.Code)
	}

	pid := created.Project.Spec.ID
	// MDB05 / DB13 — reserved user + unknown inventory
	bad := do(h, "POST", "/api/databases/projects/"+pid+"/operations", `{"action":"create-user","username":"clofixadmin","password":"1234567890123456","grants":{"orders":"write"}}`, tok)
	if bad.Code != 400 {
		t.Fatalf("MDB05/DB13 reserved user: %d %s", bad.Code, bad.Body.String())
	}

	// MDB06 / DB12 — FAILED retry requeues once (no duplicate project id)
	failID := "dbp-" + strings.Repeat("c", 32)
	s.databases.Lock()
	fp := databaseProjectFixture(failID)
	fp.State, fp.Error = "FAILED", "timeout"
	s.databases.Projects[failID] = fp
	s.databases.Unlock()
	if w := do(h, "POST", "/api/databases/projects/"+failID+"/retry", "", tok); w.Code != 202 {
		t.Fatalf("MDB06/DB12 retry: %d", w.Code)
	}
	s.databases.Lock()
	retried := s.databases.Projects[failID]
	s.databases.Unlock()
	if retried.State != "PROVISIONING" || retried.Spec.ID != failID {
		t.Fatalf("MDB06 retry must keep same project id: %+v", retried)
	}

	// MDB07 / DB10 — READY backup carries sha256; restore gated by safety backup
	bakID := "dbb-" + strings.Repeat("d", 32)
	s.databases.Lock()
	s.databases.Backups[bakID] = model.Backup{
		ID: bakID, Owner: project.Owner, ProjectID: pid, Database: "orders", Engine: "mysql",
		EngineVersion: "8.4", Format: "sql", State: "READY", Destination: "central",
		WorkerID: "wrk-database", Bytes: 128, SHA256: strings.Repeat("a", 64), Created: time.Now().UTC(),
		ProjectSpec: project.Spec,
	}
	s.databases.Projects[pid] = func() model.Project {
		p := project
		if p.Databases == nil {
			p.Databases = map[string]bool{}
		}
		p.Databases["orders"] = true
		return p
	}()
	s.databases.Unlock()
	list := do(h, "GET", "/api/databases", "", tok)
	if list.Code != 200 || !strings.Contains(list.Body.String(), bakID) || !strings.Contains(list.Body.String(), strings.Repeat("a", 64)) {
		t.Fatalf("MDB07 sha256 surface: %d %s", list.Code, list.Body.String())
	}
	// DB10 — restore jobs wait on safety backup SUCCEEDED+READY (claim gate)
	s.databases.Lock()
	depJob := "dbj-" + strings.Repeat("e", 32)
	s.databases.Jobs[depJob] = model.Job{ID: depJob, ProjectID: pid, WorkerID: "wrk-database", Action: "backup", State: "RUNNING"}
	restoreJob := model.Job{ID: "dbj-" + strings.Repeat("f", 32), ProjectID: pid, WorkerID: "wrk-database", Action: "restore", State: "QUEUED", After: depJob}
	s.databases.Jobs[restoreJob.ID] = restoreJob
	dependency := s.databases.Jobs[restoreJob.After]
	safetyBlocked := dependency.State != "SUCCEEDED" || s.databases.Backups[dependency.BackupID].State != "READY"
	s.databases.Unlock()
	if !safetyBlocked {
		t.Fatal("DB10 safety gate should block restore while backup incomplete")
	}

	// MDB08 — schedules / jobs inventory
	sched := do(h, "POST", "/api/databases/schedules", `{"project_id":"`+pid+`","database":"orders","cron":"0 3 * * *","retention":7,"destination":"central","enabled":true}`, tok)
	if sched.Code != 200 && sched.Code != 201 && sched.Code != 202 {
		t.Fatalf("MDB08 schedule: %d %s", sched.Code, sched.Body.String())
	}
	inv := do(h, "GET", "/api/databases", "", tok)
	if inv.Code != 200 || !strings.Contains(inv.Body.String(), `"jobs"`) || !strings.Contains(inv.Body.String(), `"schedules"`) {
		t.Fatalf("MDB08 inventory: %s", inv.Body.String())
	}

	// MDB09 — tenant isolation
	regA := do(h, "POST", "/api/auth/register", `{"email":"db-a@client.test","password":"client-password-123","account_name":"DB A"}`, "")
	regB := do(h, "POST", "/api/auth/register", `{"email":"db-b@client.test","password":"client-password-123","account_name":"DB B"}`, "")
	if regA.Code != 200 || regB.Code != 200 {
		t.Fatal(regA.Body.String(), regB.Body.String())
	}
	s.databases.Lock()
	hidden := databaseProjectFixture("dbp-" + strings.Repeat("g", 32))
	hidden.Owner = "acc-hidden-tenant"
	s.databases.Projects[hidden.Spec.ID] = hidden
	s.databases.Unlock()
	listedB := doCookie(h, "GET", "/api/databases", "", sessionCookieFrom(regB))
	if listedB.Code != 200 || strings.Contains(listedB.Body.String(), hidden.Spec.ID) {
		t.Fatal("MDB09 tenant B must not see foreign project")
	}

	// DB15 — Redis prefix isolation; no native backup claim
	redisID := "dbp-" + strings.Repeat("h", 32)
	s.databases.Lock()
	rp := databaseProjectFixture(redisID)
	rp.Spec.Engine = "redis"
	rp.Spec.Version = "7.4"
	rp.Databases = map[string]bool{"cache": true}
	s.databases.Projects[redisID] = rp
	s.databases.Unlock()
	if w := do(h, "POST", "/api/databases/projects/"+redisID+"/backups", `{"database":"cache"}`, tok); w.Code != 400 {
		t.Fatalf("DB15 redis backup must be rejected: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/databases/projects/"+redisID+"/operations", `{"action":"create-table","database":"cache","table":"items","columns":[{"name":"id","type":"INTEGER"}]}`, tok); w.Code != 400 {
		t.Fatalf("DB15 redis SQL schema reject: %d", w.Code)
	}

	// DB16 — stopped DB bills storage only
	stoppedID := "dbp-" + strings.Repeat("i", 32)
	s.databases.Lock()
	sp := databaseProjectFixture(stoppedID)
	sp.State = proto.StateStopped
	sp.Spec.RAMMB = 1024
	sp.Spec.CPU = 2
	sp.Spec.DiskMB = 20480
	s.databases.Projects[stoppedID] = sp
	s.databases.Unlock()
	items := s.inventoryDatabases(nil, "USD", 1, map[string]string{}, map[string]string{"worker": "db-worker"})
	found := false
	for _, it := range items {
		if it.ID == stoppedID {
			found = true
			if !it.StorageOnly {
				t.Fatalf("DB16 stopped DB must be storage_only: %+v", it)
			}
			for _, line := range it.Lines {
				if line.UsageType == "cpu" || line.UsageType == "ram" {
					t.Fatalf("DB16 stopped DB billed compute: %+v", it.Lines)
				}
			}
		}
	}
	if !found {
		t.Fatal("DB16 stopped project missing from inventory")
	}
}

func TestProductionCKSAndDockerSurfaces(t *testing.T) {
	// CK01–CK12 / DCK01–DCK12 are primarily covered by kubernetes_test.go / docker_test.go.
	// This smoke ties production catalog expectations to live helper contracts.
	if dock.HostPortBase != 18000 || dock.HostPortCount != 4000 {
		t.Fatalf("DCK02 docker host band %d+%d", dock.HostPortBase, dock.HostPortCount)
	}
	if permissionResource("/api/v1/kubernetes/clusters") != "kubernetes" {
		t.Fatal("CK05 kubernetes IAM resource")
	}
	if permissionResource("/api/docker/containers") != "docker" {
		t.Fatal("DCK docker IAM resource")
	}
	t.Setenv("CLOFIX_K8S_E2E", "")
	s, m := newSrv(t)
	fake := kubernetesWorkerFixture()
	fake.Capacity.KubernetesFake = true
	m.UpsertWorker(fake)
	ov := do(s.AdminHandler(), "GET", "/api/v1/kubernetes", "", tok)
	if ov.Code != 200 || strings.Contains(ov.Body.String(), `"production_ready":true`) {
		t.Fatal("CK06 lab workers must not claim production_ready", ov.Body.String())
	}
	dov := do(s.AdminHandler(), "GET", "/api/docker/overview", "", tok)
	if dov.Code != 200 {
		t.Fatalf("DCK12 docker overview: %d", dov.Code)
	}
	inst := do(s.AdminHandler(), "GET", "/api/instances", "", tok)
	if inst.Code != 200 {
		t.Fatalf("DCK12 filevps API: %d", inst.Code)
	}
}
