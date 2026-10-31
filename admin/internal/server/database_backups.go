package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/database"
	"github.com/clofix/filevps/admin/internal/objectstore"
	db "github.com/clofix/filevps/worker/pkg/database"
)

func (s *Server) databaseBackupPath(id string) string {
	return filepath.Join(s.databases.Root(), "backups", id+".enc")
}

func newDatabaseBackupKey() ([]byte, error) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	return key, err
}

func (s *Server) createBackupLocked(project model.Project, database string, tables []string, destination, connectionID, scheduleID string, limitMB int64) (model.Backup, model.Job, error) {
	if !project.Databases[database] {
		return model.Backup{}, model.Job{}, fmt.Errorf("database not found")
	}
	if destination != "central" && destination != "s3" {
		return model.Backup{}, model.Job{}, fmt.Errorf("backup destination must be central or s3")
	}
	if destination == "s3" {
		if connectionID == "" {
			return model.Backup{}, model.Job{}, fmt.Errorf("S3 connection required")
		}
		if _, err := s.S3.GetConnection(connectionID); err != nil {
			return model.Backup{}, model.Job{}, fmt.Errorf("S3 connection not found")
		}
	}
	if limitMB < 16 || limitMB > project.Spec.DiskMB {
		return model.Backup{}, model.Job{}, fmt.Errorf("backup reservation must be 16 MB through the project disk limit")
	}
	for _, table := range tables {
		if err := db.Name(table); err != nil {
			return model.Backup{}, model.Job{}, err
		}
	}
	key, err := newDatabaseBackupKey()
	if err != nil {
		return model.Backup{}, model.Job{}, err
	}
	id := "dbb-" + randHex(16)
	encrypted, err := s.sealDatabase(id, key)
	if err != nil {
		return model.Backup{}, model.Job{}, err
	}
	backup := model.Backup{ID: id, Owner: project.Owner, ProjectID: project.Spec.ID, ProjectSpec: project.Spec, Database: database, Engine: project.Spec.Engine, EngineVersion: project.Spec.Version, Tables: tables, State: "CREATING", Destination: destination, ConnectionID: connectionID, WorkerID: project.WorkerID, EncryptedKey: encrypted, ReservedBytes: limitMB << 20, Created: time.Now().UTC(), ScheduleID: scheduleID}
	job, err := s.queueDatabase(project, db.Operation{Action: "backup", Database: database, Backup: &db.BackupSpec{ID: id, Tables: tables, Key: key, LimitBytes: backup.ReservedBytes}}, "")
	if err != nil {
		return model.Backup{}, model.Job{}, err
	}
	job.BackupID = id
	backup.JobID = job.ID
	s.databases.Jobs[job.ID] = job
	s.databases.Backups[id] = backup
	return backup, job, nil
}

func (s *Server) createDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	if err := s.checkQuota("backups", 1); err != nil {
		fail(w, 403, err.Error())
		return
	}
	var in struct {
		Database     string   `json:"database"`
		Tables       []string `json:"tables"`
		Destination  string   `json:"destination"`
		ConnectionID string   `json:"connection_id"`
		LimitMB      int64    `json:"limit_mb"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || db.Name(in.Database) != nil {
		fail(w, 400, "valid database backup request required")
		return
	}
	if err := s.checkStorage(in.LimitMB<<20, false); err != nil {
		fail(w, 403, err.Error())
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	project, ok := d.Projects[r.PathValue("id")]
	if !ok || !canAccessApplication(r, project.Owner) {
		fail(w, 404, "project not found")
		return
	}
	if project.State != "READY" || s.databaseProjectBusy(project.Spec.ID) {
		fail(w, 409, "project is not ready or another job is active")
		return
	}
	before := s.databaseCheckpoint()
	backup, job, err := s.createBackupLocked(project, in.Database, in.Tables, in.Destination, in.ConnectionID, "", in.LimitMB)
	if err == nil {
		err = s.saveDatabase(before)
	}
	if err != nil {
		json.Unmarshal(before, d)
		fail(w, 400, err.Error())
		return
	}
	s.Store.Audit(project.Owner, "database.backup", backup.ID, job.ID)
	writeJSON(w, 202, map[string]any{"backup": backup, "job_id": job.ID})
}

func (s *Server) restoreDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	var in struct {
		BackupID string   `json:"backup_id"`
		Database string   `json:"database"`
		Tables   []string `json:"tables"`
		Confirm  string   `json:"confirm"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in) != nil || in.Confirm != "RESTORE" || db.Name(in.Database) != nil {
		fail(w, 400, "valid backup, destination and RESTORE confirmation required")
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	project, ok := d.Projects[r.PathValue("id")]
	backup, backupOK := d.Backups[in.BackupID]
	if !ok || !backupOK || !canAccessApplication(r, project.Owner) || !canAccessApplication(r, backup.Owner) {
		fail(w, 404, "project or backup not found")
		return
	}
	if project.State != "READY" || s.databaseProjectBusy(project.Spec.ID) || backup.State != "READY" || backup.Engine != project.Spec.Engine || backup.EngineVersion != project.Spec.Version {
		fail(w, 409, "project and backup must be ready with matching engine versions")
		return
	}
	for _, table := range in.Tables {
		if db.Name(table) != nil {
			fail(w, 400, "invalid selected table")
			return
		}
	}
	var key []byte
	if err := s.openDatabase(backup.ID, backup.EncryptedKey, &key); err != nil || len(key) != 32 {
		fail(w, 503, "backup key cannot be decrypted")
		return
	}
	before := s.databaseCheckpoint()
	after, safetyID := "", ""
	if project.Databases[in.Database] {
		plan, planErr := s.licensedPlan()
		backupLimit := quotaLimit(plan, "backups")
		usedBackups := int64(len(d.Backups) + len(d.Uploads))
		if planErr != nil || (backupLimit >= 0 && usedBackups >= backupLimit) {
			fail(w, 403, "a safety backup is required but the retained-backup license quota is full")
			return
		}
		safety, job, err := s.createBackupLocked(project, in.Database, nil, "central", "", "", project.Spec.DiskMB)
		if err != nil {
			json.Unmarshal(before, d)
			fail(w, 409, "safety backup could not be queued: "+err.Error())
			return
		}
		after, safetyID = job.ID, safety.ID
	}
	grants := map[string]map[string]string{}
	for user, userGrants := range project.Users {
		copyGrants := map[string]string{}
		for database, role := range userGrants {
			copyGrants[database] = role
		}
		grants[user] = copyGrants
	}
	operation := db.Operation{Action: "restore", Database: in.Database, Restore: &db.RestoreSpec{SafetyBackupID: safetyID, UserGrants: grants, BackupID: backup.ID, Key: key, SHA256: backup.SHA256, Format: backup.Format, SourceDatabase: backup.Database, EngineVersion: backup.EngineVersion, Tables: in.Tables, LimitBytes: max64(backup.PlainBytes, backup.ProjectSpec.DiskMB<<20), ConfirmOverwrite: true}}
	job, err := s.queueDatabase(project, operation, after)
	if err == nil && safetyID != "" {
		safety := d.Backups[safetyID]
		safety.ProtectedBy = job.ID
		d.Backups[safetyID] = safety
	}
	if err == nil {
		err = s.saveDatabase(before)
	}
	if err != nil {
		json.Unmarshal(before, d)
		fail(w, 503, err.Error())
		return
	}
	s.Store.Audit(project.Owner, "database.restore", project.Spec.ID, job.ID)
	writeJSON(w, 202, map[string]any{"job_id": job.ID, "safety_backup_id": safetyID})
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Server) receiveDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	worker, err := s.databaseWorker(r)
	if err != nil || !s.databaseAvailable(w) {
		fail(w, 403, "worker not authorized")
		return
	}
	d := s.databases
	d.Lock()
	job, ok := d.Jobs[r.PathValue("id")]
	backup := d.Backups[job.BackupID]
	if !ok || job.WorkerID != worker || job.Action != "backup" || job.State != "RUNNING" || backup.State != "CREATING" || s.databaseTransfers[job.ID] {
		d.Unlock()
		fail(w, 409, "backup transfer is not expected")
		return
	}
	s.databaseTransfers[job.ID] = true
	limit := backup.ReservedBytes + (backup.ReservedBytes/db.ChunkBytes+2)*20 + 16
	d.Unlock()
	defer func() { d.Lock(); delete(s.databaseTransfers, job.ID); d.Unlock() }()
	if err := os.MkdirAll(filepath.Dir(s.databaseBackupPath(backup.ID)), 0700); err != nil {
		fail(w, 503, "backup storage unavailable")
		return
	}
	temporary := s.databaseBackupPath(backup.ID) + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail(w, 409, "backup transfer already exists")
		return
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(r.Body, limit+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || n <= 0 || n > limit {
		os.Remove(temporary)
		fail(w, 413, "backup transfer failed or exceeded its reservation")
		return
	}
	if err = os.Rename(temporary, s.databaseBackupPath(backup.ID)); err != nil {
		os.Remove(temporary)
		fail(w, 503, "backup commit failed")
		return
	}
	d.Lock()
	before := s.databaseCheckpoint()
	backup = d.Backups[backup.ID]
	backup.Bytes, backup.SHA256 = n, hex.EncodeToString(hash.Sum(nil))
	d.Backups[backup.ID] = backup
	err = s.saveDatabase(before)
	d.Unlock()
	if err != nil {
		os.Remove(s.databaseBackupPath(backup.ID))
		fail(w, 503, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sendDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	worker, err := s.databaseWorker(r)
	if err != nil || !s.databaseAvailable(w) {
		fail(w, 403, "worker not authorized")
		return
	}
	d := s.databases
	d.Lock()
	job, ok := d.Jobs[r.PathValue("id")]
	backup := d.Backups[job.SourceBackupID]
	allowed := ok && job.WorkerID == worker && job.Action == "restore" && job.State == "RUNNING" && backup.State == "READY"
	d.Unlock()
	if !allowed {
		fail(w, 404, "restore source unavailable")
		return
	}
	s.serveEncryptedBackup(w, r, backup)
}

func (s *Server) serveEncryptedBackup(w http.ResponseWriter, r *http.Request, backup model.Backup) {
	file, err := os.Open(s.databaseBackupPath(backup.ID))
	if err != nil {
		fail(w, 404, "backup file unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != backup.Bytes {
		fail(w, 409, "backup file metadata mismatch")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, backup.ID+".enc", backup.Created, file)
}

func (s *Server) serveDatabaseBackupDownload(w http.ResponseWriter, r *http.Request, backup model.Backup) {
	file, err := os.Open(s.databaseBackupPath(backup.ID))
	if err != nil {
		fail(w, 404, "backup file unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	var key []byte
	if err != nil || info.Size() != backup.Bytes || s.openDatabase(backup.ID, backup.EncryptedKey, &key) != nil || backup.PlainBytes < 0 {
		fail(w, 409, "backup metadata or encryption key is invalid")
		return
	}
	reader, err := db.OpenReader(file, backup.Bytes, backup.PlainBytes, key, backup.ID)
	if err != nil {
		fail(w, 409, "backup integrity verification failed")
		return
	}
	name := backup.Database + "-" + backup.Created.UTC().Format("20060102-150405") + "." + backup.Format
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, backup.Created, reader)
}

func (s *Server) databaseDownloadToken(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	backup, ok := d.Backups[r.PathValue("id")]
	d.Unlock()
	if !ok || !canAccessApplication(r, backup.Owner) || backup.State != "READY" {
		fail(w, 404, "backup not found")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		fail(w, 500, "token creation failed")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	s.quotaMu.Lock()
	for key, grant := range s.databaseDownloads {
		if time.Now().After(grant.Expires) {
			delete(s.databaseDownloads, key)
		}
	}
	s.databaseDownloads[token] = databaseDownloadGrant{BackupID: backup.ID, Expires: time.Now().Add(5 * time.Minute)}
	s.quotaMu.Unlock()
	writeJSON(w, 200, map[string]any{"url": "/database-download/" + backup.ID + "?token=" + token, "expires_in": 300})
}

func (s *Server) databaseTokenDownload(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	s.quotaMu.Lock()
	grant, ok := s.databaseDownloads[token]
	if ok {
		delete(s.databaseDownloads, token)
	}
	s.quotaMu.Unlock()
	if !ok || grant.BackupID != r.PathValue("id") || time.Now().After(grant.Expires) {
		fail(w, 403, "download link expired or invalid")
		return
	}
	d := s.databases
	d.Lock()
	backup, exists := d.Backups[grant.BackupID]
	d.Unlock()
	if !exists || backup.State != "READY" {
		fail(w, 404, "backup unavailable")
		return
	}
	s.serveDatabaseBackupDownload(w, r, backup)
}

func (s *Server) downloadDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	backup, ok := d.Backups[r.PathValue("id")]
	d.Unlock()
	if !ok || !canAccessApplication(r, backup.Owner) || backup.State != "READY" {
		fail(w, 404, "backup not found")
		return
	}
	s.serveDatabaseBackupDownload(w, r, backup)
}

func (s *Server) deleteDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	backup, ok := d.Backups[r.PathValue("id")]
	if !ok || !canAccessApplication(r, backup.Owner) {
		fail(w, 404, "backup not found")
		return
	}
	for _, job := range d.Jobs {
		if !dbTerminal(job.State) && (job.BackupID == backup.ID || job.SourceBackupID == backup.ID) {
			fail(w, 409, "backup is used by an active job")
			return
		}
	}
	if backup.ProtectedBy != "" {
		fail(w, 409, "backup is protected by a restore workflow")
		return
	}
	project, projectOK := d.Projects[backup.ProjectID]
	before := s.databaseCheckpoint()
	if projectOK {
		job, err := s.queueDatabase(project, db.Operation{Action: "delete-backup", Backup: &db.BackupSpec{ID: backup.ID}}, "")
		if err != nil {
			fail(w, 503, err.Error())
			return
		}
		job.BackupID = backup.ID
		d.Jobs[job.ID] = job
		backup.JobID = job.ID
		backup.State = "DELETING"
		d.Backups[backup.ID] = backup
	} else {
		backup.State = "DELETING_STORAGE"
		d.Backups[backup.ID] = backup
	}
	if err := s.saveDatabase(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 202, map[string]string{"state": backup.State})
}

func (s *Server) retryDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	backup, ok := d.Backups[r.PathValue("id")]
	project, projectOK := d.Projects[backup.ProjectID]
	if !ok || !projectOK || !canAccessApplication(r, backup.Owner) {
		fail(w, 404, "backup not found")
		return
	}
	if backup.State != "FAILED" || backup.Attempts >= 3 {
		fail(w, 409, "backup cannot be retried")
		return
	}
	if s.databaseProjectBusy(project.Spec.ID) {
		fail(w, 409, "project has another active job")
		return
	}
	var key []byte
	if s.openDatabase(backup.ID, backup.EncryptedKey, &key) != nil {
		fail(w, 503, "backup key unavailable")
		return
	}
	os.Remove(s.databaseBackupPath(backup.ID))
	before := s.databaseCheckpoint()
	job, err := s.queueDatabase(project, db.Operation{Action: "backup", Database: backup.Database, Backup: &db.BackupSpec{ID: backup.ID, Tables: backup.Tables, Key: key, LimitBytes: backup.ReservedBytes}}, "")
	if err == nil {
		backup.Attempts++
		backup.State = "CREATING"
		backup.Error = ""
		backup.JobID = job.ID
		d.Backups[backup.ID] = backup
		err = s.saveDatabase(before)
	}
	if err != nil {
		json.Unmarshal(before, d)
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}

func (s *Server) processDatabaseStorage(ctx context.Context) {
	d := s.databases
	d.Lock()
	backups := []model.Backup{}
	for _, backup := range d.Backups {
		if (backup.State == "TRANSFERRING" && (backup.NextAttempt.IsZero() || !time.Now().Before(backup.NextAttempt))) || backup.State == "DELETING_STORAGE" {
			backups = append(backups, backup)
		}
	}
	d.Unlock()
	for _, backup := range backups {
		if ctx.Err() != nil {
			return
		}
		if backup.State == "TRANSFERRING" {
			s.transferDatabaseBackup(backup)
		} else {
			s.removeDatabaseBackupStorage(backup)
		}
	}
}

func (s *Server) transferDatabaseBackup(backup model.Backup) {
	config, err := s.S3.GetConnection(backup.ConnectionID)
	file, openErr := os.Open(s.databaseBackupPath(backup.ID))
	if err == nil && openErr == nil {
		err = objectstore.PutReader(config, "database-backups/"+backup.ID+".enc", "application/octet-stream", file, backup.Bytes, backup.SHA256)
		file.Close()
	}
	if openErr != nil {
		err = openErr
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	current, ok := d.Backups[backup.ID]
	if !ok || current.State != "TRANSFERRING" {
		return
	}
	before := s.databaseCheckpoint()
	current.TransferAttempts++
	if err == nil {
		current.State = "READY"
		current.Error = ""
		current.ReservedBytes = current.Bytes
	} else if current.TransferAttempts >= 5 {
		current.State = "FAILED"
		current.Error = "S3 transfer failed after 5 attempts"
	} else {
		current.Error = "S3 transfer failed; retrying"
		current.NextAttempt = time.Now().Add(time.Duration(current.TransferAttempts) * time.Minute)
	}
	d.Backups[current.ID] = current
	_ = s.saveDatabase(before)
}

func (s *Server) removeDatabaseBackupStorage(backup model.Backup) {
	var err error
	if backup.Destination == "s3" && backup.ConnectionID != "" {
		if config, e := s.S3.GetConnection(backup.ConnectionID); e == nil {
			err = objectstore.Delete(config, "database-backups/"+backup.ID+".enc", false)
		}
	}
	if err == nil {
		err = os.Remove(s.databaseBackupPath(backup.ID))
		if os.IsNotExist(err) {
			err = nil
		}
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	current, ok := d.Backups[backup.ID]
	if !ok || current.State != "DELETING_STORAGE" {
		return
	}
	before := s.databaseCheckpoint()
	if err == nil {
		delete(d.Backups, backup.ID)
	} else {
		current.Error = "backup storage deletion failed"
		d.Backups[backup.ID] = current
	}
	_ = s.saveDatabase(before)
}

func (s *Server) saveDatabaseSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	var in model.Schedule
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil || db.Name(in.Database) != nil || in.Retention < 1 || in.Retention > 365 || in.LimitMB < 16 {
		fail(w, 400, "valid schedule, retention and reservation required")
		return
	}
	if _, err := parseApplicationCron(in.Cron); err != nil {
		fail(w, 400, err.Error())
		return
	}
	creating := r.Method == http.MethodPost
	if creating {
		if err := s.checkQuota("database-schedules", 1); err != nil {
			fail(w, 403, err.Error())
			return
		}
		in.ID = "dbs-" + randHex(16)
	} else {
		in.ID = r.PathValue("id")
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	project, ok := d.Projects[in.ProjectID]
	if !ok || !canAccessApplication(r, project.Owner) || !project.Databases[in.Database] {
		fail(w, 404, "project or database not found")
		return
	}
	if in.Destination != "central" && in.Destination != "s3" {
		fail(w, 400, "destination must be central or s3")
		return
	}
	if in.Destination == "s3" {
		if _, e := s.S3.GetConnection(in.ConnectionID); e != nil {
			fail(w, 400, "S3 connection not found")
			return
		}
	}
	if !creating {
		old, ok := d.Schedules[in.ID]
		if !ok || !canAccessApplication(r, old.Owner) {
			fail(w, 404, "schedule not found")
			return
		}
	}
	in.Owner = project.Owner
	before := s.databaseCheckpoint()
	d.Schedules[in.ID] = in
	if err := s.saveDatabase(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, in)
}

func (s *Server) deleteDatabaseSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	schedule, ok := d.Schedules[r.PathValue("id")]
	if !ok || !canAccessApplication(r, schedule.Owner) {
		fail(w, 404, "schedule not found")
		return
	}
	before := s.databaseCheckpoint()
	delete(d.Schedules, schedule.ID)
	if err := s.saveDatabase(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) runDatabaseSchedules(now time.Time) {
	plan, licenseErr := s.licensedPlan()
	if licenseErr != nil {
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	minute := now.UTC().Format("2006-01-02T15:04")
	before := s.databaseCheckpoint()
	changed := false
	backupLimit := quotaLimit(plan, "backups")
	for id, schedule := range d.Schedules {
		if !schedule.Enabled || schedule.LastMinute == minute {
			continue
		}
		cron, err := parseApplicationCron(schedule.Cron)
		if err != nil || !cron.matches(now) {
			continue
		}
		project, ok := d.Projects[schedule.ProjectID]
		if !ok || project.State != "READY" || s.databaseProjectBusy(project.Spec.ID) || (backupLimit >= 0 && int64(len(d.Backups)+len(d.Uploads)) >= backupLimit) {
			continue
		}
		if _, _, err = s.createBackupLocked(project, schedule.Database, nil, schedule.Destination, schedule.ConnectionID, schedule.ID, schedule.LimitMB); err != nil {
			schedule.Error = err.Error()
		} else {
			schedule.Error = ""
		}
		schedule.LastMinute = minute
		d.Schedules[id] = schedule
		changed = true
	}
	// Retention deletes only successful, unprotected scheduled backups.
	for _, schedule := range d.Schedules {
		items := []model.Backup{}
		for _, backup := range d.Backups {
			if backup.ScheduleID == schedule.ID && backup.State == "READY" && backup.ProtectedBy == "" {
				items = append(items, backup)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
		for _, backup := range items[schedule.Retention:] {
			backup.State = "DELETING_STORAGE"
			d.Backups[backup.ID] = backup
			changed = true
		}
	}
	if changed {
		_ = s.saveDatabase(before)
	}
}

func parseContentRange(value string) (int64, int64, int64, error) {
	if !strings.HasPrefix(value, "bytes ") {
		return 0, 0, 0, fmt.Errorf("Content-Range required")
	}
	rangePart, totalPart, ok := strings.Cut(strings.TrimPrefix(value, "bytes "), "/")
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range")
	}
	startText, endText, ok := strings.Cut(rangePart, "-")
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range")
	}
	start, e1 := strconv.ParseInt(startText, 10, 64)
	end, e2 := strconv.ParseInt(endText, 10, 64)
	total, e3 := strconv.ParseInt(totalPart, 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || start < 0 || end < start || total <= end {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range")
	}
	return start, end, total, nil
}
