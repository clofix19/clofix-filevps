package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	s.usageMu.RLock()
	copied := make([]proto.BackupInfo, 0, len(s.backups))
	for _, b := range s.backups {
		copied = append(copied, b)
	}
	s.usageMu.RUnlock()
	p := principalFrom(r)
	items := make([]proto.BackupInfo, 0, len(copied))
	for _, b := range copied {
		inst, err := s.Store.GetInstance(b.InstanceID)
		if err != nil || (p.Kind != "admin" && !s.canAccessAccount(p, inst.AccountID)) {
			continue
		}
		items = append(items, b)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, err := s.Store.GetInstance(id)
	if err != nil {
		fail(w, http.StatusNotFound, "VPS not found")
		return
	}
	if inst.State != proto.StateStopped {
		fail(w, http.StatusConflict, "stop the VPS before creating a consistent backup")
		return
	}
	if e := s.checkStorage(inst.Spec.DiskMB<<20, false); e != nil {
		fail(w, 403, e.Error())
		return
	}
	wk, err := s.Store.GetWorker(inst.WorkerID)
	if err != nil || wk.Status != "active" {
		fail(w, http.StatusConflict, "the VPS worker must be active to create a backup")
		return
	}
	backupID := "bkp-" + randHex(6)
	info := proto.BackupInfo{ID: backupID, InstanceID: id, WorkerID: inst.WorkerID, Created: time.Now().UTC(), RAMMB: inst.Spec.RAMMB, CPU: inst.Spec.CPU, SwapMB: proto.SwapLimitMB(inst.Spec), DiskMB: inst.Spec.DiskMB, Image: inst.Spec.Image.Name + ":" + inst.Spec.Image.Version, State: "CREATING"}
	s.usageMu.Lock()
	s.backups[backupID] = info
	s.usageMu.Unlock()
	op := proto.Operation{ID: "backup/" + backupID + "/create/" + randHex(4), Action: proto.ActionCreateBackup, InstanceID: id, BackupID: backupID, WorkerID: inst.WorkerID}
	if err := s.Store.QueueOp(inst.WorkerID, op); err != nil {
		s.usageMu.Lock()
		delete(s.backups, backupID)
		s.usageMu.Unlock()
		fail(w, http.StatusInternalServerError, "could not queue VPS backup")
		return
	}
	s.Store.Audit("admin", "backup.create", backupID, "queued")
	writeJSON(w, http.StatusAccepted, map[string]any{"backup": info, "queued": true})
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.usageMu.Lock()
	b, ok := s.backups[id]
	wasDeleting := ok && b.State == "DELETING"
	if ok && !wasDeleting {
		b.State = "DELETING"
		s.backups[id] = b
	}
	s.usageMu.Unlock()
	if !ok {
		fail(w, http.StatusNotFound, "backup not found")
		return
	}
	if wasDeleting {
		fail(w, http.StatusConflict, "backup deletion is already queued")
		return
	}
	wk, err := s.Store.GetWorker(b.WorkerID)
	if err != nil || wk.Status != "active" {
		fail(w, http.StatusConflict, "backup worker must be active to delete this backup")
		return
	}
	op := proto.Operation{ID: "backup/" + id + "/delete/" + randHex(4), Action: proto.ActionDeleteBackup, BackupID: id, WorkerID: b.WorkerID}
	if err := s.Store.QueueOp(b.WorkerID, op); err != nil {
		s.usageMu.Lock()
		b.State = "READY"
		s.backups[id] = b
		s.usageMu.Unlock()
		fail(w, http.StatusInternalServerError, "could not queue backup deletion")
		return
	}
	s.Store.Audit("admin", "backup.delete", id, "queued")
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "queued": true})
}

func restoreImage(image string) (string, string) {
	name, version, ok := strings.Cut(image, ":")
	if !ok {
		return name, ""
	}
	return name, version
}

func (s *Server) migrateBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		WorkerID string `json:"worker_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || strings.TrimSpace(in.WorkerID) == "" {
		fail(w, 400, "target worker_id required")
		return
	}
	s.usageMu.Lock()
	backup, ok := s.backups[id]
	if ok && backup.State == "READY" {
		backup.State = "MIGRATING"
		s.backups[id] = backup
	}
	s.usageMu.Unlock()
	if !ok {
		fail(w, 404, "backup not found")
		return
	}
	if backup.State != "MIGRATING" {
		fail(w, 409, "backup must be READY to migrate")
		return
	}
	if backup.WorkerID == in.WorkerID {
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		writeJSON(w, 200, map[string]any{"backup": backup, "note": "already on target worker"})
		return
	}
	src, err := s.Store.GetWorker(backup.WorkerID)
	dst, err2 := s.Store.GetWorker(in.WorkerID)
	if err != nil || err2 != nil || src.Status != "active" || dst.Status != "active" {
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 409, "source and target workers must be active")
		return
	}
	exportID := "backup/" + id + "/export/" + randHex(4)
	exportCh := make(chan proto.OpResult, 1)
	s.volumeFileMu.Lock()
	s.volumeFileWait[exportID] = volumeFileWaiter{workerID: backup.WorkerID, result: exportCh}
	s.volumeFileMu.Unlock()
	if err := s.Store.QueueOp(backup.WorkerID, proto.Operation{ID: exportID, Action: proto.ActionExportBackup, BackupID: id}); err != nil {
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, exportID)
		s.volumeFileMu.Unlock()
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 500, "could not queue backup export")
		return
	}
	var exported proto.OpResult
	select {
	case exported = <-exportCh:
	case <-time.After(2 * time.Minute):
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, exportID)
		s.volumeFileMu.Unlock()
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 504, "backup export timed out")
		return
	case <-r.Context().Done():
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, exportID)
		s.volumeFileMu.Unlock()
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 408, "request cancelled")
		return
	}
	if !exported.OK || len(exported.BackupArtifact) == 0 {
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		msg := exported.Error
		if msg == "" {
			msg = "worker returned empty backup artifact"
		}
		fail(w, 502, msg)
		return
	}
	importID := "backup/" + id + "/import/" + randHex(4)
	importCh := make(chan proto.OpResult, 1)
	s.volumeFileMu.Lock()
	s.volumeFileWait[importID] = volumeFileWaiter{workerID: in.WorkerID, result: importCh}
	s.volumeFileMu.Unlock()
	if err := s.Store.QueueOp(in.WorkerID, proto.Operation{ID: importID, Action: proto.ActionImportBackup, BackupID: id, BackupArtifact: exported.BackupArtifact}); err != nil {
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, importID)
		s.volumeFileMu.Unlock()
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 500, "could not queue backup import")
		return
	}
	var imported proto.OpResult
	select {
	case imported = <-importCh:
	case <-time.After(2 * time.Minute):
		s.volumeFileMu.Lock()
		delete(s.volumeFileWait, importID)
		s.volumeFileMu.Unlock()
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 504, "backup import timed out")
		return
	}
	if !imported.OK {
		s.usageMu.Lock()
		backup.State = "READY"
		s.backups[id] = backup
		s.usageMu.Unlock()
		fail(w, 502, imported.Error)
		return
	}
	s.usageMu.Lock()
	backup.WorkerID = in.WorkerID
	backup.State = "READY"
	s.backups[id] = backup
	s.usageMu.Unlock()
	s.Store.Audit(principalFrom(r).Email, "backup.migrate", id, in.WorkerID)
	writeJSON(w, 200, map[string]any{"backup": backup, "migrated": true})
}
