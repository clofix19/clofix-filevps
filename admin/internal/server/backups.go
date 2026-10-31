package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	s.usageMu.RLock()
	items := make([]proto.BackupInfo, 0, len(s.backups))
	for _, b := range s.backups {
		items = append(items, b)
	}
	s.usageMu.RUnlock()
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
