package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/clofix/filevps/admin/internal/objectstore"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

type efsAttachment struct {
	InstanceID string `json:"instance_id"`
	GuestPath  string `json:"guest_path"`
}

type efsShare struct {
	ID           string          `json:"id"`
	AccountID    string          `json:"account_id,omitempty"`
	Name         string          `json:"name"`
	ConnectionID string          `json:"connection_id"`
	Prefix       string          `json:"prefix"`
	Attachments  []efsAttachment `json:"attachments,omitempty"`
}

type efsStore struct {
	sync.Mutex
	Shares map[string]efsShare `json:"shares"`
	file   string
}

func loadEFS(dir string) *efsStore {
	d := &efsStore{file: filepath.Join(dir, "efs.json")}
	if raw, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(raw, d) != nil {
			panic("invalid efs.json")
		}
	}
	if d.Shares == nil {
		d.Shares = map[string]efsShare{}
	}
	return d
}

func (d *efsStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err = os.WriteFile(d.file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (s *Server) efsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/efs", s.auth(s.listEFS))
	m.HandleFunc("POST /api/efs", s.auth(s.createEFS))
	m.HandleFunc("PUT /api/efs/{id}", s.auth(s.updateEFS))
	m.HandleFunc("DELETE /api/efs/{id}", s.auth(s.deleteEFS))
	m.HandleFunc("POST /api/efs/{id}/attach", s.auth(s.attachEFS))
	m.HandleFunc("POST /api/efs/{id}/detach", s.auth(s.detachEFS))
	m.HandleFunc("GET /api/efs/{id}/files", s.auth(s.listEFSFiles))
	m.HandleFunc("GET /api/efs/{id}/file", s.auth(s.getEFSFile))
	m.HandleFunc("PUT /api/efs/{id}/file", s.auth(s.putEFSFile))
	m.HandleFunc("DELETE /api/efs/{id}/file", s.auth(s.deleteEFSFile))
	m.HandleFunc("POST /api/efs/{id}/folder", s.auth(s.createEFSFolder))
	m.HandleFunc("GET /api/efs/{id}/archive", s.auth(s.archiveEFSFolder))
	m.HandleFunc("POST /api/efs/{id}/move", s.auth(s.moveEFSObject))
}

func (s *Server) listEFS(w http.ResponseWriter, r *http.Request) {
	instances, _ := s.Store.ListInstances()
	byID := map[string]store.Instance{}
	for _, inst := range instances {
		byID[inst.ID] = inst
	}
	p := principalFrom(r)
	s.efs.Lock()
	out := make([]map[string]any, 0, len(s.efs.Shares))
	for _, share := range s.efs.Shares {
		if p.Kind != "admin" && !s.canAccessAccount(p, share.AccountID) {
			continue
		}
		atts := make([]map[string]any, 0, len(share.Attachments))
		for _, a := range share.Attachments {
			row := map[string]any{"instance_id": a.InstanceID, "guest_path": a.GuestPath}
			if inst, ok := byID[a.InstanceID]; ok {
				row["worker_id"] = inst.WorkerID
				row["state"] = inst.State
			}
			atts = append(atts, row)
		}
		out = append(out, map[string]any{
			"id": share.ID, "name": share.Name, "connection_id": share.ConnectionID,
			"prefix": share.Prefix, "attachments": atts,
		})
	}
	s.efs.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	writeJSON(w, 200, map[string]any{"shares": out})
}

func (s *Server) createEFS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name         string `json:"name"`
		ConnectionID string `json:"connection_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		fail(w, http.StatusBadRequest, "name must be 1–80 characters")
		return
	}
	conn, err := s.efsConnection(in.ConnectionID)
	if err != nil {
		fail(w, http.StatusConflict, "configure S3 storage first")
		return
	}
	id := "efs-" + randHex(8)
	share := efsShare{ID: id, AccountID: s.stampAccount(principalFrom(r)), Name: name, ConnectionID: conn.ID, Prefix: efsPrefix(conn.Prefix, id)}
	s.efs.Lock()
	s.efs.Shares[id] = share
	err = s.efs.save()
	s.efs.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "efs.create", id, name)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name, "connection_id": conn.ID, "prefix": share.Prefix})
}

func (s *Server) updateEFS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		fail(w, http.StatusBadRequest, "name must be 1–80 characters")
		return
	}
	s.efs.Lock()
	share, ok := s.efs.Shares[id]
	if !ok {
		s.efs.Unlock()
		fail(w, http.StatusNotFound, "EFS share not found")
		return
	}
	share.Name = name
	s.efs.Shares[id] = share
	err := s.efs.save()
	s.efs.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "efs.update", id, name)
	writeJSON(w, 200, map[string]any{"id": id, "name": name})
}

func (s *Server) deleteEFS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.efs.Lock()
	share, ok := s.efs.Shares[id]
	if !ok {
		s.efs.Unlock()
		fail(w, http.StatusNotFound, "EFS share not found")
		return
	}
	if len(share.Attachments) > 0 {
		s.efs.Unlock()
		fail(w, http.StatusConflict, "detach all VPS instances before deleting this share")
		return
	}
	delete(s.efs.Shares, id)
	err := s.efs.save()
	s.efs.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "efs.delete", id, share.Name)
	writeJSON(w, 200, map[string]any{"deleted": true, "id": id})
}

func (s *Server) attachEFS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		InstanceID string `json:"instance_id"`
		GuestPath  string `json:"guest_path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || !apiInstanceID.MatchString(in.InstanceID) || !validS3MountPath(in.GuestPath) {
		fail(w, http.StatusBadRequest, "instance_id and a clean guest path are required")
		return
	}
	if strings.Contains(in.GuestPath, "..") {
		fail(w, http.StatusBadRequest, "guest path cannot contain ..")
		return
	}
	inst, err := s.Store.GetInstance(in.InstanceID)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	if err := s.attachEFSShare(id, in.InstanceID, in.GuestPath); err != nil {
		code := http.StatusConflict
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		fail(w, code, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "efs.attach", id, in.InstanceID)
	writeJSON(w, 200, map[string]any{"id": id, "instance_id": in.InstanceID, "guest_path": in.GuestPath, "queued": true, "restart_required": inst.State == proto.StateRunning})
}

func (s *Server) attachEFSShare(shareID, instanceID, guestPath string) error {
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(instanceID)
	if err != nil {
		return err
	}
	if inst.State == proto.StateDeleting {
		return fmt.Errorf("cannot attach while VPS is deleting")
	}
	for _, m := range inst.Spec.EFSMounts {
		if m.ID == shareID && m.GuestPath == guestPath {
			return nil
		}
	}
	if storagePathsConflict(guestPath, inst.Spec.S3MountPath) {
		return fmt.Errorf("EFS path overlaps the S3 mount")
	}
	for _, m := range inst.Spec.StorageMounts {
		if storagePathsConflict(guestPath, m.GuestPath) {
			return fmt.Errorf("EFS path overlaps a volume mount")
		}
	}
	for _, m := range inst.Spec.EFSMounts {
		if m.ID != shareID && storagePathsConflict(guestPath, m.GuestPath) {
			return fmt.Errorf("EFS path overlaps another EFS mount")
		}
	}
	s.efs.Lock()
	share, ok := s.efs.Shares[shareID]
	if !ok {
		s.efs.Unlock()
		return fmt.Errorf("EFS share not found")
	}
	payload, err := s.efsWorkerShare(share, guestPath, false)
	if err != nil {
		s.efs.Unlock()
		return err
	}
	previousAtts := append([]efsAttachment(nil), share.Attachments...)
	found := false
	for i, a := range share.Attachments {
		if a.InstanceID == inst.ID {
			share.Attachments[i].GuestPath = guestPath
			found = true
			break
		}
	}
	if !found {
		share.Attachments = append(share.Attachments, efsAttachment{InstanceID: inst.ID, GuestPath: guestPath})
	}
	s.efs.Shares[shareID] = share
	if err := s.efs.save(); err != nil {
		s.efs.Unlock()
		return err
	}
	s.efs.Unlock()
	previous := inst
	inst.Spec.EFSMounts = upsertInstanceEFS(inst.Spec.EFSMounts, proto.StorageMount{ID: shareID, GuestPath: guestPath})
	if err := s.Store.SaveInstance(inst); err != nil {
		s.efs.Lock()
		share.Attachments = previousAtts
		s.efs.Shares[shareID] = share
		_ = s.efs.save()
		s.efs.Unlock()
		return err
	}
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: inst.ID + "/" + proto.ActionUpdateEFS + "/" + randHex(4), Action: proto.ActionUpdateEFS, InstanceID: inst.ID, EFS: &payload,
	}); err != nil {
		_ = s.Store.SaveInstance(previous)
		s.efs.Lock()
		share.Attachments = previousAtts
		s.efs.Shares[shareID] = share
		_ = s.efs.save()
		s.efs.Unlock()
		return err
	}
	return nil
}

func (s *Server) detachEFS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || !apiInstanceID.MatchString(in.InstanceID) {
		fail(w, http.StatusBadRequest, "instance_id required")
		return
	}
	s.placeMu.Lock()
	defer s.placeMu.Unlock()
	inst, err := s.Store.GetInstance(in.InstanceID)
	if err != nil {
		fail(w, http.StatusNotFound, "instance not found")
		return
	}
	s.efs.Lock()
	share, ok := s.efs.Shares[id]
	if !ok {
		s.efs.Unlock()
		fail(w, http.StatusNotFound, "EFS share not found")
		return
	}
	filtered := make([]efsAttachment, 0, len(share.Attachments))
	for _, a := range share.Attachments {
		if a.InstanceID != inst.ID {
			filtered = append(filtered, a)
		}
	}
	share.Attachments = filtered
	s.efs.Shares[id] = share
	if err := s.efs.save(); err != nil {
		s.efs.Unlock()
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.efs.Unlock()
	previous := inst
	inst.Spec.EFSMounts = removeInstanceEFS(inst.Spec.EFSMounts, id)
	if err := s.Store.SaveInstance(inst); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload := proto.EFSShare{ID: id, Detach: true}
	if err := s.Store.QueueOp(inst.WorkerID, proto.Operation{
		ID: inst.ID + "/" + proto.ActionUpdateEFS + "/" + randHex(4), Action: proto.ActionUpdateEFS, InstanceID: inst.ID, EFS: &payload,
	}); err != nil {
		_ = s.Store.SaveInstance(previous)
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "efs.detach", id, inst.ID)
	writeJSON(w, 200, map[string]any{"id": id, "instance_id": inst.ID, "queued": true})
}

func (s *Server) listEFSFiles(w http.ResponseWriter, r *http.Request) {
	cfg, rel, ok := s.efsObjectContext(w, r, r.URL.Query().Get("path"), true)
	if !ok {
		return
	}
	listing, err := objectstore.List(cfg, rel, r.URL.Query().Get("cursor"))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, listing)
}

func (s *Server) getEFSFile(w http.ResponseWriter, r *http.Request) {
	cfg, key, ok := s.efsObjectContext(w, r, r.URL.Query().Get("path"), false)
	if !ok {
		return
	}
	if r.URL.Query().Get("text") == "1" {
		b, contentType, err := objectstore.Get(cfg, key)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if !utf8.Valid(b) || !(strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "json") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "xml")) {
			fail(w, http.StatusUnsupportedMediaType, "object is not editable text")
			return
		}
		writeJSON(w, 200, map[string]string{"content": string(b), "content_type": contentType})
		return
	}
	resp, err := objectstore.Open(cfg, key)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filepath.Base(key), `"`, "")+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) putEFSFile(w http.ResponseWriter, r *http.Request) {
	cfg, key, ok := s.efsObjectContext(w, r, r.URL.Query().Get("path"), false)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "file exceeds the 4 MB upload limit")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(key))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := objectstore.Put(cfg, key, contentType, body); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"saved": true, "path": key, "size": len(body)})
}

func (s *Server) createEFSFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "path required")
		return
	}
	cfg, key, ok := s.efsObjectContext(w, r, in.Path, false)
	if !ok {
		return
	}
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	if err := objectstore.Put(cfg, key, "application/x-directory", nil); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"created": true, "path": key})
}

func (s *Server) archiveEFSFolder(w http.ResponseWriter, r *http.Request) {
	cfg, rel, ok := s.efsObjectContext(w, r, r.URL.Query().Get("path"), true)
	if !ok {
		return
	}
	writeObjectArchive(w, cfg, rel, "efs")
}

func (s *Server) moveEFSObject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Folder bool   `json:"folder"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "from and to paths are required")
		return
	}
	if strings.HasSuffix(strings.TrimSpace(in.From), "/") {
		in.Folder = true
	}
	dest, err := efsMoveDest(in.From, in.To, in.Folder)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, src, ok := s.efsObjectContext(w, r, strings.Trim(in.From, "/"), false)
	if !ok {
		return
	}
	if code, moveErr := moveObjectPrefix(cfg, src, dest, in.Folder); moveErr != nil {
		fail(w, code, moveErr.Error())
		return
	}
	s.Store.Audit(applicationOwner(r), "efs.move", r.PathValue("id"), src+" -> "+dest)
	writeJSON(w, 200, map[string]any{"moved": true, "from": src, "to": dest})
}

func (s *Server) deleteEFSFile(w http.ResponseWriter, r *http.Request) {
	cfg, key, ok := s.efsObjectContext(w, r, r.URL.Query().Get("path"), false)
	if !ok {
		return
	}
	folder := strings.HasSuffix(key, "/")
	if err := objectstore.Delete(cfg, key, folder); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "path": key})
}

func (s *Server) efsObjectContext(w http.ResponseWriter, r *http.Request, rel string, allowEmpty bool) (objectstore.Config, string, bool) {
	id := r.PathValue("id")
	s.efs.Lock()
	share, ok := s.efs.Shares[id]
	s.efs.Unlock()
	if !ok {
		fail(w, http.StatusNotFound, "EFS share not found")
		return objectstore.Config{}, "", false
	}
	if !validEFSRelPath(rel, allowEmpty) {
		fail(w, http.StatusBadRequest, "invalid path")
		return objectstore.Config{}, "", false
	}
	conn, err := s.efsConnection(share.ConnectionID)
	if err != nil {
		fail(w, http.StatusConflict, "S3 connection for this share is missing")
		return objectstore.Config{}, "", false
	}
	conn.Prefix = share.Prefix
	return conn, strings.TrimPrefix(rel, "/"), true
}

func (s *Server) efsSharesForWorker(workerID string) []proto.EFSShare {
	instances, err := s.Store.ListInstances()
	if err != nil {
		return nil
	}
	local := map[string]bool{}
	onWorker := map[string]bool{}
	for _, inst := range instances {
		if inst.WorkerID != workerID {
			continue
		}
		onWorker[inst.ID] = true
		for _, m := range inst.Spec.EFSMounts {
			local[m.ID] = true
		}
	}
	s.efs.Lock()
	defer s.efs.Unlock()
	out := []proto.EFSShare{}
	for id, share := range s.efs.Shares {
		if !local[id] {
			for _, a := range share.Attachments {
				if onWorker[a.InstanceID] {
					local[id] = true
					break
				}
			}
		}
		if !local[id] {
			continue
		}
		payload, err := s.efsWorkerShare(share, "", false)
		if err != nil {
			continue
		}
		out = append(out, payload)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) efsWorkerShare(share efsShare, guestPath string, detach bool) (proto.EFSShare, error) {
	conn, err := s.efsConnection(share.ConnectionID)
	if err != nil {
		return proto.EFSShare{}, err
	}
	return proto.EFSShare{
		ID: share.ID, GuestPath: guestPath, Endpoint: conn.Endpoint, Bucket: conn.Bucket,
		Region: conn.Region, Prefix: share.Prefix, AccessKey: conn.AccessKey, SecretKey: conn.SecretKey,
		ConnectionID: conn.ID, Detach: detach,
	}, nil
}

func (s *Server) efsConnection(id string) (objectstore.Config, error) {
	if id != "" {
		return s.S3.GetConnection(id)
	}
	items, err := s.S3.ListConnections()
	if err != nil {
		return objectstore.Config{}, err
	}
	if len(items) == 0 {
		return objectstore.Config{}, os.ErrNotExist
	}
	return items[0], nil
}

func (s *Server) dropEFSInstance(id string) {
	s.efs.Lock()
	defer s.efs.Unlock()
	changed := false
	for sid, share := range s.efs.Shares {
		atts := make([]efsAttachment, 0, len(share.Attachments))
		for _, a := range share.Attachments {
			if a.InstanceID != id {
				atts = append(atts, a)
			} else {
				changed = true
			}
		}
		if len(atts) != len(share.Attachments) {
			share.Attachments = atts
			s.efs.Shares[sid] = share
		}
	}
	if changed {
		_ = s.efs.save()
	}
}

func (s *Server) efsUsesConnection(id string) bool {
	s.efs.Lock()
	defer s.efs.Unlock()
	for _, share := range s.efs.Shares {
		if share.ConnectionID == id {
			return true
		}
	}
	return false
}

func efsPrefix(connPrefix, id string) string {
	base := strings.Trim(connPrefix, "/")
	if base != "" {
		return base + "/efs/" + id
	}
	return "efs/" + id
}

const (
	efsArchiveMaxFiles = 2000
	efsArchiveMaxBytes = 64 << 20
)

func efsArchiveName(rel string) string {
	name := path.Base(strings.Trim(rel, "/"))
	if name == "" || name == "." {
		name = "efs"
	}
	return strings.ReplaceAll(name, `"`, "") + ".zip"
}

func efsMoveDest(from, to string, folder bool) (string, error) {
	from = strings.Trim(strings.TrimPrefix(strings.TrimSpace(from), "/"), "/")
	to = strings.TrimPrefix(strings.TrimSpace(to), "/")
	if strings.HasSuffix(to, "/") && strings.Trim(to, "/") != "" {
		to = strings.Trim(to, "/") + "/" + path.Base(from)
	} else {
		to = strings.Trim(to, "/")
	}
	if from == "" || to == "" {
		return "", fmt.Errorf("source and destination paths are required")
	}
	if from == to {
		return "", fmt.Errorf("destination must be different from the source")
	}
	if folder && strings.HasPrefix(to+"/", from+"/") {
		return "", fmt.Errorf("cannot move a folder into itself")
	}
	if !validEFSRelPath(from, false) || !validEFSRelPath(to, false) {
		return "", fmt.Errorf("invalid path")
	}
	return to, nil
}

func writeObjectArchive(w http.ResponseWriter, cfg objectstore.Config, rel, defaultBase string) {
	items, err := objectstore.ListAll(cfg, rel)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(items) == 0 {
		fail(w, http.StatusNotFound, "folder is empty or not found")
		return
	}
	var files, bytes int64
	for _, item := range items {
		if item.Folder {
			continue
		}
		files++
		bytes += item.Size
	}
	if files > efsArchiveMaxFiles || bytes > efsArchiveMaxBytes {
		fail(w, http.StatusRequestEntityTooLarge, "folder is too large to zip; download files individually")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+efsArchiveName(rel)+`"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	root := strings.Trim(rel, "/")
	base := path.Base(root)
	if base == "" || base == "." {
		base = defaultBase
	}
	for _, item := range items {
		entry := item.Key
		if root != "" {
			entry = base + "/" + strings.TrimPrefix(strings.TrimPrefix(item.Key, root), "/")
		}
		entry = strings.TrimPrefix(entry, "/")
		if item.Folder || strings.HasSuffix(item.Key, "/") {
			if _, err := zw.Create(strings.TrimSuffix(entry, "/") + "/"); err != nil {
				return
			}
			continue
		}
		h := &zip.FileHeader{Name: entry, Method: zip.Deflate}
		if item.Modified != "" {
			if t, err := time.Parse(time.RFC3339, item.Modified); err == nil {
				h.Modified = t
			} else if t, err := time.Parse(time.RFC3339Nano, item.Modified); err == nil {
				h.Modified = t
			}
		}
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return
		}
		resp, err := objectstore.Open(cfg, item.Key)
		if err != nil {
			return
		}
		_, copyErr := io.Copy(fw, resp.Body)
		resp.Body.Close()
		if copyErr != nil {
			return
		}
	}
}

func moveObjectPrefix(cfg objectstore.Config, src, dest string, folder bool) (int, error) {
	src = strings.Trim(src, "/")
	dest = strings.Trim(dest, "/")
	if folder {
		items, err := objectstore.ListAll(cfg, src)
		if err != nil {
			return http.StatusBadGateway, err
		}
		if len(items) == 0 {
			return http.StatusNotFound, fmt.Errorf("folder not found")
		}
		if exists, existErr := efsDestTaken(cfg, dest, true); existErr != nil {
			return http.StatusBadGateway, existErr
		} else if exists {
			return http.StatusConflict, fmt.Errorf("destination already exists")
		}
		for _, item := range items {
			rel := strings.TrimPrefix(strings.TrimPrefix(item.Key, src), "/")
			next := dest
			if rel != "" {
				next = dest + "/" + rel
			}
			if item.Folder || strings.HasSuffix(item.Key, "/") {
				if !strings.HasSuffix(next, "/") {
					next += "/"
				}
				if err := objectstore.Put(cfg, next, "application/x-directory", nil); err != nil {
					return http.StatusBadGateway, err
				}
				continue
			}
			if err := objectstore.Copy(cfg, item.Key, next); err != nil {
				return http.StatusBadGateway, err
			}
		}
		if err := objectstore.Delete(cfg, src, true); err != nil {
			return http.StatusBadGateway, err
		}
		return http.StatusOK, nil
	}
	found, err := objectstore.Head(cfg, src)
	if err != nil {
		return http.StatusBadGateway, err
	}
	if !found {
		return http.StatusNotFound, fmt.Errorf("file not found")
	}
	if exists, existErr := efsDestTaken(cfg, dest, false); existErr != nil {
		return http.StatusBadGateway, existErr
	} else if exists {
		return http.StatusConflict, fmt.Errorf("destination already exists")
	}
	if err := objectstore.Copy(cfg, src, dest); err != nil {
		return http.StatusBadGateway, err
	}
	if err := objectstore.Delete(cfg, src, false); err != nil {
		return http.StatusBadGateway, err
	}
	return http.StatusOK, nil
}

func efsDestTaken(cfg objectstore.Config, dest string, folder bool) (bool, error) {
	found, err := objectstore.Head(cfg, dest)
	if err != nil || found {
		return found, err
	}
	if !folder {
		return false, nil
	}
	items, err := objectstore.ListAll(cfg, dest)
	if err != nil {
		return false, err
	}
	return len(items) > 0, nil
}

func validEFSRelPath(p string, allowEmpty bool) bool {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return allowEmpty
	}
	if strings.ContainsAny(p, "\\\x00\r\n") || len(p) > 1024 {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func upsertInstanceEFS(mounts []proto.StorageMount, next proto.StorageMount) []proto.StorageMount {
	out := make([]proto.StorageMount, 0, len(mounts)+1)
	found := false
	for _, m := range mounts {
		if m.ID == next.ID {
			out = append(out, next)
			found = true
			continue
		}
		out = append(out, m)
	}
	if !found {
		out = append(out, next)
	}
	return out
}

func removeInstanceEFS(mounts []proto.StorageMount, id string) []proto.StorageMount {
	out := make([]proto.StorageMount, 0, len(mounts))
	for _, m := range mounts {
		if m.ID != id {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
