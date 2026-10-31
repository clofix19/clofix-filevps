package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/database"
	db "github.com/clofix/filevps/worker/pkg/database"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

type databaseDownloadGrant struct {
	BackupID string
	Expires  time.Time
}

func (s *Server) initDatabases(root string) {
	var backend model.SnapshotStore
	if candidate, ok := s.Store.(model.SnapshotStore); ok {
		backend = candidate
	}
	s.databases, s.databaseError = model.New(filepath.Join(root, "managed-databases"), backend)
	if s.databaseError == nil && s.databases != nil && (len(s.databases.Jobs) > 0 || len(s.databases.Backups) > 0 || len(s.databases.Uploads) > 0) {
		if _, err := os.Stat(filepath.Join(root, "application-secrets.key")); err != nil {
			s.databaseError = fmt.Errorf("database encryption key is unavailable: %w", err)
		}
	}
	s.databaseDownloads = map[string]databaseDownloadGrant{}
	s.databaseTransfers = map[string]bool{}
}
func (s *Server) databaseAvailable(w http.ResponseWriter) bool {
	if s.databaseError != nil || s.databases == nil {
		fail(w, 503, "managed database metadata is unavailable; restore storage before retrying")
		return false
	}
	return true
}
func (s *Server) databaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases", s.auth(s.listDatabases))
	mux.HandleFunc("POST /api/databases/projects", s.auth(s.createDatabaseProject))
	mux.HandleFunc("DELETE /api/databases/projects/{id}", s.auth(s.deleteDatabaseProject))
	mux.HandleFunc("GET /api/databases/projects/{id}/ca.pem", s.auth(s.downloadDatabaseCA))
	mux.HandleFunc("POST /api/databases/projects/{id}/operations", s.auth(s.databaseOperation))
	mux.HandleFunc("POST /api/databases/projects/{id}/backups", s.auth(s.createDatabaseBackup))
	mux.HandleFunc("POST /api/databases/projects/{id}/restore", s.auth(s.restoreDatabase))
	mux.HandleFunc("POST /api/databases/jobs/{id}/cancel", s.auth(s.cancelDatabaseJob))
	mux.HandleFunc("DELETE /api/databases/backups/{id}", s.auth(s.deleteDatabaseBackup))
	mux.HandleFunc("POST /api/databases/backups/{id}/retry", s.auth(s.retryDatabaseBackup))
	mux.HandleFunc("GET /api/databases/backups/{id}/download", s.auth(s.downloadDatabaseBackup))
	mux.HandleFunc("GET /api/databases/backups/{id}/download-token", s.auth(s.databaseDownloadToken))
	mux.HandleFunc("GET /database-download/{id}", s.databaseTokenDownload)
	mux.HandleFunc("POST /api/databases/schedules", s.auth(s.saveDatabaseSchedule))
	mux.HandleFunc("PUT /api/databases/schedules/{id}", s.auth(s.saveDatabaseSchedule))
	mux.HandleFunc("DELETE /api/databases/schedules/{id}", s.auth(s.deleteDatabaseSchedule))
	s.databaseUploadRoutes(mux)
}

func (s *Server) downloadDatabaseCA(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	project, ok := d.Projects[r.PathValue("id")]
	d.Unlock()
	if !ok || !canAccessApplication(r, project.Owner) || project.CA == "" {
		fail(w, 404, "database TLS CA is not available")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="`+project.Spec.ID+`-ca.pem"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(project.CA))
}
func dbTerminal(state string) bool {
	return state == "SUCCEEDED" || state == "FAILED" || state == "CANCELLED" || state == "INTERRUPTED" || state == "RETRIED"
}
func dbCleanup(action string) bool { return action == "delete-project" || action == "delete-backup" }

func (s *Server) databaseAlert(id, name, status string) {
	d := s.monitoring
	d.Lock()
	defer d.Unlock()
	deliveries := map[string]alertDelivery{}
	for channelID, channel := range d.Channels {
		if channel.Enabled {
			deliveries[channelID] = alertDelivery{Status: "PENDING", Next: time.Now().UTC()}
		}
	}
	d.Events = append(d.Events, healthEvent{ID: "evt-" + randHex(8), MonitorID: id, Name: name, Status: status, At: time.Now().UTC(), Deliveries: deliveries})
	if len(d.Events) > 100 {
		d.Events = d.Events[len(d.Events)-100:]
	}
	_ = d.save()
}
func (s *Server) sealDatabase(id string, value any) ([]byte, error) {
	plain, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	c, e := s.applicationCipher()
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, c.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return c.Seal(nonce, nonce, plain, []byte("database:"+id)), nil
}
func (s *Server) openDatabase(id string, encrypted []byte, value any) error {
	c, e := s.applicationCipher()
	if e != nil {
		return e
	}
	n := c.NonceSize()
	if len(encrypted) < n {
		return fmt.Errorf("invalid database secret")
	}
	plain, e := c.Open(nil, encrypted[:n], encrypted[n:], []byte("database:"+id))
	if e != nil {
		return fmt.Errorf("database secrets cannot be decrypted")
	}
	return json.Unmarshal(plain, value)
}

// Persist only with the metadata mutex held. Failure restores the prior snapshot.
func (s *Server) saveDatabase(previous []byte) error {
	if e := s.databases.Save(); e != nil {
		json.Unmarshal(previous, s.databases)
		return fmt.Errorf("database metadata could not be persisted")
	}
	return nil
}
func (s *Server) databaseCheckpoint() []byte { raw, _ := json.Marshal(s.databases); return raw }
func (s *Server) queueDatabase(p model.Project, operation db.Operation, after string) (model.Job, error) {
	operation.Project = p.Spec
	operation.JobID = "dbj-" + randHex(16)
	payload, e := s.sealDatabase(operation.JobID, operation)
	if e != nil {
		return model.Job{}, e
	}
	now := time.Now().UTC()
	job := model.Job{ID: operation.JobID, Owner: p.Owner, ProjectID: p.Spec.ID, WorkerID: p.WorkerID, Action: operation.Action, State: "QUEUED", Payload: payload, After: after, Created: now, Updated: now, Attempts: 1}
	if operation.Backup != nil {
		job.BackupID = operation.Backup.ID
	}
	if operation.Restore != nil {
		job.SourceBackupID = operation.Restore.BackupID
	}
	s.databases.Jobs[job.ID] = job
	return job, nil
}
func (s *Server) databaseProjectBusy(id string) bool {
	for _, j := range s.databases.Jobs {
		if j.ProjectID == id && !dbTerminal(j.State) {
			return true
		}
	}
	return false
}
func (s *Server) databaseQuotaUsed(resource string) int64 {
	if s.databases == nil {
		return 0
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	n := int64(0)
	switch resource {
	case "database-projects":
		return int64(len(d.Projects))
	case "database-schedules":
		return int64(len(d.Schedules))
	case "backups":
		return int64(len(d.Backups) + len(d.Uploads))
	case "databases", "database-users":
		for _, p := range d.Projects {
			if resource == "databases" {
				n += int64(len(p.Databases))
			} else {
				n += int64(len(p.Users))
			}
		}
		for _, j := range d.Jobs {
			if !dbTerminal(j.State) && (resource == "databases" && j.Action == "create-database" || resource == "database-users" && j.Action == "create-user") {
				n++
			}
		}
	}
	return n
}
func (s *Server) databaseStorageUsed() int64 {
	if s.databases == nil {
		return 0
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	n := int64(0)
	for _, p := range d.Projects {
		n += p.Spec.DiskMB << 20
	}
	for _, b := range d.Backups {
		size := b.Bytes
		if b.ReservedBytes > size {
			size = b.ReservedBytes
		}
		n += size
	}
	for _, u := range d.Uploads {
		n += u.Total + (u.Total/db.ChunkBytes+2)*20 + 16
	}
	return n
}
func (s *Server) databaseLicense() error {
	p, e := s.licensedPlan()
	if e != nil {
		return e
	}
	if quotaLimit(p, "database-projects") == 0 {
		return fmt.Errorf("managed databases are not included in this license")
	}
	return s.checkQuota("database-projects", 0)
}
func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	projects := []model.Project{}
	backups := []model.Backup{}
	jobs := []model.Job{}
	schedules := []model.Schedule{}
	uploads := []model.Upload{}
	for _, p := range d.Projects {
		if canAccessApplication(r, p.Owner) {
			projects = append(projects, p)
		}
	}
	for _, b := range d.Backups {
		if canAccessApplication(r, b.Owner) {
			b.EncryptedKey = nil
			backups = append(backups, b)
		}
	}
	for _, j := range d.Jobs {
		if canAccessApplication(r, j.Owner) {
			j.Payload = nil
			jobs = append(jobs, j)
		}
	}
	for _, v := range d.Schedules {
		if canAccessApplication(r, v.Owner) {
			schedules = append(schedules, v)
		}
	}
	for _, u := range d.Uploads {
		if canAccessApplication(r, u.Owner) {
			u.EncryptedKey = nil
			uploads = append(uploads, u)
		}
	}
	d.Unlock()
	sort.Slice(projects, func(i, j int) bool { return projects[i].Created.Before(projects[j].Created) })
	sort.Slice(backups, func(i, j int) bool { return backups[i].Created.After(backups[j].Created) })
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Created.After(jobs[j].Created) })
	if len(jobs) > 200 {
		jobs = jobs[:200]
	}
	workers, _ := s.Store.ListWorkers()
	ready := []map[string]any{}
	for _, worker := range workers {
		if worker.Status == "active" && time.Since(worker.LastHeartbeat) < time.Minute && len(worker.Capacity.DatabaseEngines) > 0 && worker.Capacity.DatabasePublicHost != "" {
			ready = append(ready, map[string]any{"id": worker.ID, "hostname": worker.Hostname, "engines": worker.Capacity.DatabaseEngines})
		}
	}
	connections, _ := s.S3.PublicConnections()
	writeJSON(w, 200, map[string]any{"projects": projects, "backups": backups, "jobs": jobs, "schedules": schedules, "uploads": uploads, "workers": ready, "connections": connections})
}
func (s *Server) createDatabaseProject(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	if e := s.checkQuota("database-projects", 1); e != nil {
		fail(w, 403, e.Error())
		return
	}
	var in struct {
		Name     string     `json:"name"`
		WorkerID string     `json:"worker_id"`
		Spec     db.Project `json:"spec"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || len(in.Name) > 80 {
		fail(w, 400, "project name and resource specification required")
		return
	}
	in.Spec.ID = "dbp-" + randHex(16)
	in.Spec.Port = 26000
	if e := in.Spec.Validate(); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e := s.checkStorage(in.Spec.DiskMB<<20, false); e != nil {
		fail(w, 403, e.Error())
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	workers, e := s.Store.ListWorkers()
	if e != nil {
		fail(w, 503, "worker inventory unavailable")
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	before := s.databaseCheckpoint()
	candidates := []scheduler.Candidate{}
	for _, worker := range workers {
		cp := worker.Capacity
		if in.WorkerID != "" && worker.ID != in.WorkerID || cp.DatabaseEngines[in.Spec.Engine] != in.Spec.Version || cp.DatabasePublicHost == "" {
			continue
		}
		host := cp.DatabasePublicHost
		if net.ParseIP(host) == nil && (strings.ContainsAny(host, "/: \t\r\n") || host == "") {
			continue
		}
		reported := map[string]bool{}
		for _, id := range cp.DatabaseProjects {
			reported[id] = true
		}
		for _, project := range d.Projects {
			if project.WorkerID == worker.ID && !reported[project.Spec.ID] {
				cp.RAMCommitted += uint64(project.Spec.RAMMB) << 20
				cp.CPUAllocated += project.Spec.CPU
				cp.DiskCommitted += uint64(project.Spec.DiskMB) << 20
			}
		}
		candidates = append(candidates, scheduler.Candidate{ID: worker.ID, Active: worker.Status == "active", Draining: worker.Status == "draining", LastHeartbeat: worker.LastHeartbeat, Capacity: cp})
	}
	decision, e := scheduler.Place(candidates, scheduler.Request{RAM: uint64(in.Spec.RAMMB) << 20, CPU: in.Spec.CPU, Disk: uint64(in.Spec.DiskMB) << 20}, s.Sched, time.Now())
	if e != nil {
		fail(w, 409, "no ready database worker has safe CPU, RAM and disk capacity")
		return
	}
	usedPorts := map[int]bool{}
	for _, p := range d.Projects {
		if p.WorkerID == decision.WorkerID {
			usedPorts[p.Spec.Port] = true
		}
	}
	for usedPorts[in.Spec.Port] && in.Spec.Port <= 26099 {
		in.Spec.Port++
	}
	if in.Spec.Port > 26099 {
		fail(w, 409, "database listener ports exhausted")
		return
	}
	for _, worker := range workers {
		if worker.ID == decision.WorkerID {
			in.Spec.Host = worker.Capacity.DatabasePublicHost
		}
	}
	project := model.Project{Spec: in.Spec, Owner: applicationOwner(r), Name: strings.TrimSpace(in.Name), WorkerID: decision.WorkerID, State: "PROVISIONING", Created: time.Now().UTC(), Databases: map[string]bool{}, Users: map[string]map[string]string{}}
	d.Projects[project.Spec.ID] = project
	job, e := s.queueDatabase(project, db.Operation{Action: "provision"}, "")
	if e == nil {
		e = s.saveDatabase(before)
	}
	if e != nil {
		json.Unmarshal(before, d)
		fail(w, 503, e.Error())
		return
	}
	s.Store.Audit(project.Owner, "database.provision", project.Spec.ID, job.ID)
	writeJSON(w, 202, map[string]any{"project": project, "job_id": job.ID})
}
func validateDatabaseAction(p model.Project, op db.Operation) error {
	switch op.Action {
	case "list-databases":
		return nil
	case "list-tables", "list-users", "create-database", "delete-database", "rename-database", "create-table", "delete-table", "create-index", "delete-index":
		if op.Action != "list-users" {
			if e := db.Name(op.Database); e != nil {
				return e
			}
		}
	case "create-user", "update-user", "delete-user":
		if e := db.Name(op.Username); e != nil {
			return e
		}
		if strings.EqualFold(op.Username, "root") || strings.EqualFold(op.Username, "clofixadmin") || strings.HasPrefix(strings.ToLower(op.Username), "pg_") || strings.HasPrefix(strings.ToLower(op.Username), "restore_") {
			return fmt.Errorf("reserved database user")
		}
		if op.Action == "create-user" && p.Users[op.Username] != nil {
			return fmt.Errorf("user already exists")
		}
		if op.Action != "create-user" && p.Users[op.Username] == nil {
			return fmt.Errorf("user not found")
		}
		if op.Action == "create-user" && len(op.Password) < 16 || op.Password != "" && (len(op.Password) < 16 || len(op.Password) > 128 || strings.ContainsAny(op.Password, "\x00\r\n")) {
			return fmt.Errorf("password must contain 16–128 characters without line breaks")
		}
		for name, role := range op.Grants {
			if !p.Databases[name] || role != "read" && role != "write" {
				return fmt.Errorf("grants must reference an existing database and read/write permission")
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported database action")
	}
	if op.Action == "create-database" {
		if p.Databases[op.Database] {
			return fmt.Errorf("database already exists")
		}
		return nil
	}
	if op.Action != "list-users" && !p.Databases[op.Database] {
		return fmt.Errorf("database not found")
	}
	switch op.Action {
	case "rename-database":
		if p.Spec.Engine != "postgresql" {
			return fmt.Errorf("only PostgreSQL supports database rename")
		}
		return db.Name(op.NewName)
	case "create-table":
		if p.Spec.Engine == "mongodb" {
			return db.Name(op.Table)
		}
		_, e := db.TableDDL(p.Spec.Engine, op.Table, op.Columns)
		return e
	case "delete-table":
		return db.Name(op.Table)
	case "create-index", "delete-index":
		if e := db.Name(op.Table); e != nil {
			return e
		}
		if op.Index == nil {
			return fmt.Errorf("index required")
		}
		if e := db.Name(op.Index.Name); e != nil {
			return e
		}
		if op.Action == "create-index" {
			if len(op.Index.Columns) < 1 || len(op.Index.Columns) > 16 {
				return fmt.Errorf("provide 1–16 index columns")
			}
			for _, col := range op.Index.Columns {
				if e := db.Name(col); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *Server) databaseOperation(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	if e := s.databaseLicense(); e != nil {
		fail(w, 403, e.Error())
		return
	}
	var op db.Operation
	var input struct {
		db.Operation
		Confirm string `json:"confirm"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&input) != nil {
		fail(w, 400, "invalid operation")
		return
	}
	op = input.Operation
	// Credentials, job IDs, runtime images, backup keys and restore URLs are server controlled.
	op.Project = db.Project{}
	op.JobID = ""
	op.Backup = nil
	op.Restore = nil
	resource := ""
	if op.Action == "create-database" {
		resource = "databases"
	}
	if op.Action == "create-user" {
		resource = "database-users"
	}
	if resource != "" {
		if e := s.checkQuota(resource, 1); e != nil {
			fail(w, 403, e.Error())
			return
		}
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	p, ok := d.Projects[r.PathValue("id")]
	if !ok || !canAccessApplication(r, p.Owner) {
		fail(w, 404, "project not found")
		return
	}
	if p.State != "READY" || s.databaseProjectBusy(p.Spec.ID) {
		fail(w, 409, "project is not ready or another job is active")
		return
	}
	if e := validateDatabaseAction(p, op); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if strings.HasPrefix(op.Action, "delete-") && input.Confirm != "DELETE" {
		fail(w, 400, "type DELETE to confirm this destructive operation")
		return
	}
	if op.Action == "list-users" {
		writeJSON(w, 200, map[string]any{"users": p.Users})
		return
	}
	before := s.databaseCheckpoint()
	job, e := s.queueDatabase(p, op, "")
	if e == nil {
		e = s.saveDatabase(before)
	}
	if e != nil {
		json.Unmarshal(before, d)
		fail(w, 503, e.Error())
		return
	}
	s.Store.Audit(p.Owner, "database."+op.Action, p.Spec.ID, job.ID)
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}
func (s *Server) deleteDatabaseProject(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in) != nil || in.Confirm != "DELETE" {
		fail(w, 400, "type DELETE to delete this database instance")
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	p, ok := d.Projects[r.PathValue("id")]
	if !ok || !canAccessApplication(r, p.Owner) {
		fail(w, 404, "project not found")
		return
	}
	if s.databaseProjectBusy(p.Spec.ID) {
		fail(w, 409, "cancel or finish active jobs first")
		return
	}
	before := s.databaseCheckpoint()
	job, e := s.queueDatabase(p, db.Operation{Action: "delete-project"}, "")
	if e == nil {
		p.State = "DELETING"
		d.Projects[p.Spec.ID] = p
		e = s.saveDatabase(before)
	}
	if e != nil {
		json.Unmarshal(before, d)
		fail(w, 503, e.Error())
		return
	}
	s.Store.Audit(p.Owner, "database.delete", p.Spec.ID, job.ID)
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}
func (s *Server) cancelDatabaseJob(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	j, ok := d.Jobs[r.PathValue("id")]
	if !ok || !canAccessApplication(r, j.Owner) {
		fail(w, 404, "job not found")
		return
	}
	if dbTerminal(j.State) {
		writeJSON(w, 200, map[string]string{"state": j.State})
		return
	}
	before := s.databaseCheckpoint()
	j.Cancellation = true
	j.Updated = time.Now().UTC()
	if j.State == "QUEUED" {
		j.State = "CANCELLED"
	}
	d.Jobs[j.ID] = j
	if e := s.saveDatabase(before); e != nil {
		fail(w, 503, e.Error())
		return
	}
	s.Store.Audit(j.Owner, "database.job.cancel", j.ID, "")
	writeJSON(w, 202, map[string]string{"state": j.State})
}

func (s *Server) databaseWorker(r *http.Request) (string, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
		return "", fmt.Errorf("verified worker certificate required")
	}
	id := r.TLS.PeerCertificates[0].Subject.CommonName
	worker, e := s.Store.GetWorker(id)
	if e != nil || worker.Status == "revoked" || worker.Status == "pending" {
		return "", fmt.Errorf("worker is not authorized")
	}
	return id, nil
}
func (s *Server) databaseWorkerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/database/jobs/{id}", s.fetchDatabaseJob)
	mux.HandleFunc("PUT /v1/database/jobs/{id}/backup", s.receiveDatabaseBackup)
	mux.HandleFunc("GET /v1/database/jobs/{id}/backup", s.sendDatabaseBackup)
}
func (s *Server) fetchDatabaseJob(w http.ResponseWriter, r *http.Request) {
	worker, e := s.databaseWorker(r)
	if e != nil {
		fail(w, 403, e.Error())
		return
	}
	if !s.databaseAvailable(w) {
		return
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	d := s.databases
	d.Lock()
	defer d.Unlock()
	j, ok := d.Jobs[r.PathValue("id")]
	if !ok || j.WorkerID != worker {
		fail(w, 404, "job not found")
		return
	}
	if j.Cancellation || dbTerminal(j.State) {
		fail(w, 409, "job is cancelled or completed")
		return
	}
	if !dbCleanup(j.Action) {
		p, e := s.licensedPlan()
		if e != nil || quotaLimit(p, "database-projects") == 0 {
			fail(w, 403, "managed database license required")
			return
		}
	}
	if j.After != "" {
		dependency, ok := d.Jobs[j.After]
		if !ok || dependency.State != "SUCCEEDED" {
			fail(w, 409, "safety backup has not completed")
			return
		}
		b := d.Backups[dependency.BackupID]
		if b.State != "READY" {
			fail(w, 409, "safety backup is not durably stored")
			return
		}
	}
	var operation db.Operation
	if e = s.openDatabase(j.ID, j.Payload, &operation); e != nil {
		fail(w, 503, e.Error())
		return
	}
	if operation.Backup != nil && j.Action == "backup" {
		operation.Backup.CentralURL = "/v1/database/jobs/" + j.ID + "/backup"
	}
	if operation.Restore != nil {
		operation.Restore.SourceURL = "/v1/database/jobs/" + j.ID + "/backup"
	}
	before := s.databaseCheckpoint()
	j.State = "RUNNING"
	j.Updated = time.Now().UTC()
	d.Jobs[j.ID] = j
	if e = s.saveDatabase(before); e != nil {
		fail(w, 503, e.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, operation)
}
func (s *Server) databaseHeartbeat(worker string, results []db.Result, statuses []db.Status) ([]proto.Operation, []string) {
	if s.databases == nil || s.databaseError != nil {
		return nil, nil
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	before := s.databaseCheckpoint()
	ack := []string{}
	operations := []proto.Operation{}
	for _, status := range statuses {
		p, ok := d.Projects[status.ProjectID]
		if !ok || p.WorkerID != worker {
			continue
		}
		p.Metrics = status
		if status.State == "NEEDS_INSPECTION" {
			p.State = status.State
			p.Error = "worker requires inspection after interrupted work"
		}
		d.Projects[status.ProjectID] = p
	}
	for _, result := range results {
		j, ok := d.Jobs[result.JobID]
		if !ok || j.WorkerID != worker || j.ProjectID != result.ProjectID {
			continue
		}
		if dbTerminal(j.State) {
			ack = append(ack, j.ID)
			continue
		}
		if result.State == "RUNNING" {
			if j.State != "QUEUED" {
				j.State = "RUNNING"
				j.Progress = result.Progress
				j.Updated = time.Now().UTC()
				d.Jobs[j.ID] = j
			}
			continue
		}
		if result.State != "SUCCEEDED" && result.State != "FAILED" && result.State != "CANCELLED" && result.State != "INTERRUPTED" {
			continue
		}
		var op db.Operation
		if s.openDatabase(j.ID, j.Payload, &op) != nil {
			continue
		}
		j.State = result.State
		j.Error = result.Error
		j.Progress = result.Progress
		j.Result = result.Data
		j.Updated = time.Now().UTC()
		p, projectExists := d.Projects[j.ProjectID]
		if result.State == "SUCCEEDED" {
			switch j.Action {
			case "provision":
				var data struct {
					CA string `json:"ca"`
				}
				if json.Unmarshal(result.Data, &data) != nil || !strings.Contains(data.CA, "BEGIN CERTIFICATE") {
					j.State = "FAILED"
					j.Error = "worker did not return a database TLS certificate"
				} else {
					p.CA = data.CA
					p.State = "READY"
					p.Error = ""
				}
			case "create-database":
				p.Databases[op.Database] = true
			case "delete-database":
				delete(p.Databases, op.Database)
				for user, grants := range p.Users {
					copy := map[string]string{}
					for name, role := range grants {
						if name != op.Database {
							copy[name] = role
						}
					}
					p.Users[user] = copy
				}
			case "rename-database":
				delete(p.Databases, op.Database)
				p.Databases[op.NewName] = true
				for user, grants := range p.Users {
					copy := map[string]string{}
					for name, role := range grants {
						if name == op.Database {
							name = op.NewName
						}
						copy[name] = role
					}
					p.Users[user] = copy
				}
			case "create-user", "update-user":
				if op.Grants == nil {
					op.Grants = map[string]string{}
				}
				p.Users[op.Username] = op.Grants
			case "delete-user":
				delete(p.Users, op.Username)
			case "list-databases", "list-tables":
				p.Inventory = result.Data
			case "restore":
				p.Databases[op.Database] = true
				p.Inventory = nil
			case "backup":
				b, ok := d.Backups[j.BackupID]
				if !ok {
					j.State = "FAILED"
					j.Error = "backup metadata is missing"
					break
				}
				var meta struct {
					Size   int64  `json:"size_bytes"`
					Plain  int64  `json:"plain_bytes"`
					SHA    string `json:"sha256"`
					Format string `json:"format"`
				}
				if json.Unmarshal(result.Data, &meta) != nil || b.SHA256 == "" || b.SHA256 != meta.SHA || b.Bytes != meta.Size {
					j.State = "FAILED"
					j.Error = "worker backup result does not match verified controller storage"
				} else {
					if b.Destination == "s3" {
						b.State = "TRANSFERRING"
					} else {
						b.State = "READY"
						b.ReservedBytes = b.Bytes
					}
					b.Format = meta.Format
					b.PlainBytes = meta.Plain
					b.Error = ""
					d.Backups[b.ID] = b
				}
			case "delete-backup":
				b, ok := d.Backups[j.BackupID]
				if ok {
					b.State = "DELETING_STORAGE"
					d.Backups[b.ID] = b
				}
			case "delete-project":
				delete(d.Projects, j.ProjectID)
				for id, schedule := range d.Schedules {
					if schedule.ProjectID == j.ProjectID {
						delete(d.Schedules, id)
					}
				}
				projectExists = false
			}
		}
		if j.State != "SUCCEEDED" {
			if j.Action == "provision" {
				p.State = "FAILED"
				p.Error = j.Error
			}
			if j.State == "INTERRUPTED" || j.State == "CANCELLED" || j.Action == "restore" && strings.Contains(j.Error, "inspection") {
				p.State = "NEEDS_INSPECTION"
				p.Error = j.Error
			}
			if j.Action == "backup" {
				b := d.Backups[j.BackupID]
				b.State = "FAILED"
				b.Error = j.Error
				b.NextAttempt = time.Now().Add(30 * time.Second)
				d.Backups[b.ID] = b
			}
			if j.Action == "delete-project" {
				p.State = "FAILED"
				p.Error = j.Error
			}
			s.databaseAlert(j.ID, "Database "+j.Action, "DOWN")
		}
		if j.Action == "restore" && dbTerminal(j.State) {
			for id, backup := range d.Backups {
				if backup.ProtectedBy == j.ID {
					backup.ProtectedBy = ""
					d.Backups[id] = backup
				}
			}
		}
		if projectExists {
			d.Projects[p.Spec.ID] = p
		}
		d.Jobs[j.ID] = j
		ack = append(ack, j.ID)
	}
	// One database job per worker; never schedule a second operation while a native
	// process is running, including on a different project.
	busy := false
	for _, j := range d.Jobs {
		if j.WorkerID == worker && (j.State == "RUNNING" || j.State == "DISPATCHED") {
			busy = true
			if j.Cancellation {
				operations = append(operations, proto.Operation{ID: j.ID + "/cancel", Action: "cancel-database", DatabaseJobID: j.ID})
			}
		}
	}
	if !busy {
		queued := []model.Job{}
		for _, j := range d.Jobs {
			if j.WorkerID == worker && j.State == "QUEUED" && !j.Cancellation {
				queued = append(queued, j)
			}
		}
		sort.Slice(queued, func(i, j int) bool { return queued[i].Created.Before(queued[j].Created) })
		for _, j := range queued {
			if j.After != "" {
				dep, ok := d.Jobs[j.After]
				if !ok || dbTerminal(dep.State) && dep.State != "SUCCEEDED" {
					j.State = "FAILED"
					j.Error = "safety backup failed; restore was not started"
					d.Jobs[j.ID] = j
					continue
				}
				if dep.State != "SUCCEEDED" || d.Backups[dep.BackupID].State != "READY" {
					continue
				}
			}
			j.State = "DISPATCHED"
			j.Updated = time.Now().UTC()
			d.Jobs[j.ID] = j
			operations = append(operations, proto.Operation{ID: j.ID, Action: "database-job", DatabaseJobID: j.ID})
			break
		}
	} else {
		for _, j := range d.Jobs {
			if j.WorkerID == worker && j.State == "DISPATCHED" && !j.Cancellation {
				operations = append(operations, proto.Operation{ID: j.ID, Action: "database-job", DatabaseJobID: j.ID})
			}
		}
	}
	if e := s.saveDatabase(before); e != nil {
		return nil, nil
	}
	return operations, ack
}

// Leases use durable job IDs and worker journals. RUNNING jobs are deliberately
// never replayed with a new ID after a timeout or controller restart.
func (s *Server) RunDatabaseScheduler(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if s.databaseError == nil && s.databases != nil {
			s.runDatabaseSchedules(time.Now().UTC())
			s.processDatabaseStorage(ctx)
			s.cleanupDatabaseUploads(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
