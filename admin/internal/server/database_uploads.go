package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	model "github.com/clofix/filevps/admin/internal/database"
	db "github.com/clofix/filevps/worker/pkg/database"
)

func (s *Server) databaseUploadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/databases/uploads", s.auth(s.createDatabaseUpload))
	mux.HandleFunc("PATCH /api/databases/uploads/{id}", s.auth(s.appendDatabaseUpload))
	mux.HandleFunc("POST /api/databases/uploads/{id}/complete", s.auth(s.completeDatabaseUpload))
	mux.HandleFunc("DELETE /api/databases/uploads/{id}", s.auth(s.deleteDatabaseUpload))
}

func (s *Server) databaseUploadDir(id string) string {
	return filepath.Join(s.databases.Root(), "uploads", id)
}

func (s *Server) createDatabaseUpload(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	if err := s.checkQuota("backups", 1); err != nil {
		fail(w, 403, err.Error())
		return
	}
	var in struct {
		ProjectID string `json:"project_id"`
		Database  string `json:"database"`
		Filename  string `json:"filename"`
		SHA256    string `json:"sha256"`
		Total     int64  `json:"total"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || db.Name(in.Database) != nil || in.Total < 1 || in.Total > 1<<40 || (in.SHA256 != "" && len(in.SHA256) != 64) {
		fail(w, 400, "valid project, database, filename, size and SHA-256 required")
		return
	}
	d := s.databases
	d.Lock()
	defer d.Unlock()
	project, ok := d.Projects[in.ProjectID]
	if !ok || !canAccessApplication(r, project.Owner) || !project.Databases[in.Database] {
		fail(w, 404, "project or database not found")
		return
	}
	format, err := db.BackupFormat(project.Spec.Engine, in.Filename)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if in.Total > project.Spec.DiskMB<<20 {
		fail(w, 413, "upload exceeds project disk limit")
		return
	}
	key, err := newDatabaseBackupKey()
	if err != nil {
		fail(w, 500, "upload key creation failed")
		return
	}
	id := "dbu-" + randHex(16)
	encrypted, err := s.sealDatabase(id, key)
	if err != nil {
		fail(w, 500, "upload key encryption failed")
		return
	}
	upload := model.Upload{ID: id, Owner: project.Owner, ProjectID: project.Spec.ID, Database: in.Database, Filename: in.Filename, Format: format, Total: in.Total, SHA256: strings.ToLower(in.SHA256), EncryptedKey: encrypted, Created: time.Now().UTC()}
	before := s.databaseCheckpoint()
	d.Uploads[id] = upload
	if err = s.saveDatabase(before); err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 201, upload)
}

func (s *Server) appendDatabaseUpload(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	start, end, total, err := parseContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if end-start+1 > 8<<20 {
		fail(w, 413, "upload chunks are limited to 8 MiB")
		return
	}
	d := s.databases
	d.Lock()
	upload, ok := d.Uploads[r.PathValue("id")]
	if !ok || !canAccessApplication(r, upload.Owner) {
		d.Unlock()
		fail(w, 404, "upload not found")
		return
	}
	if start != upload.Offset || total != upload.Total {
		d.Unlock()
		fail(w, 409, "upload offset mismatch")
		return
	}
	var key []byte
	if s.openDatabase(upload.ID, upload.EncryptedKey, &key) != nil {
		d.Unlock()
		fail(w, 503, "upload key unavailable")
		return
	}
	d.Unlock()
	if err = os.MkdirAll(s.databaseUploadDir(upload.ID), 0700); err != nil {
		fail(w, 503, "upload storage unavailable")
		return
	}
	part := filepath.Join(s.databaseUploadDir(upload.ID), fmt.Sprintf("%012d.part", start))
	file, err := os.OpenFile(part+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail(w, 409, "chunk is already being received")
		return
	}
	n, encErr := db.Encrypt(file, io.LimitReader(r.Body, end-start+2), key, upload.ID+":"+strconv.FormatInt(start, 10), end-start+1)
	syncErr := file.Sync()
	closeErr := file.Close()
	if encErr != nil || syncErr != nil || closeErr != nil || n != end-start+1 {
		os.Remove(part + ".tmp")
		fail(w, 400, "chunk length or encryption failed")
		return
	}
	if err = os.Rename(part+".tmp", part); err != nil {
		os.Remove(part + ".tmp")
		fail(w, 503, "chunk commit failed")
		return
	}
	d.Lock()
	defer d.Unlock()
	current, ok := d.Uploads[upload.ID]
	if !ok || current.Offset != start {
		os.Remove(part)
		fail(w, 409, "upload changed during transfer")
		return
	}
	before := s.databaseCheckpoint()
	current.Parts = append(current.Parts, start)
	current.Offset = end + 1
	d.Uploads[current.ID] = current
	if err = s.saveDatabase(before); err != nil {
		os.Remove(part)
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int64{"offset": current.Offset})
}

func (s *Server) completeDatabaseUpload(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	upload, ok := d.Uploads[r.PathValue("id")]
	if !ok || !canAccessApplication(r, upload.Owner) {
		d.Unlock()
		fail(w, 404, "upload not found")
		return
	}
	if upload.Offset != upload.Total {
		d.Unlock()
		fail(w, 409, "upload is incomplete")
		return
	}
	project, projectOK := d.Projects[upload.ProjectID]
	var key []byte
	keyErr := s.openDatabase(upload.ID, upload.EncryptedKey, &key)
	d.Unlock()
	if !projectOK || keyErr != nil {
		fail(w, 503, "project or upload key unavailable")
		return
	}
	backupID := "dbb-" + randHex(16)
	backupKey, err := newDatabaseBackupKey()
	if err != nil {
		fail(w, 500, "backup key creation failed")
		return
	}
	encryptedKey, err := s.sealDatabase(backupID, backupKey)
	if err != nil {
		fail(w, 500, "backup key encryption failed")
		return
	}
	if err = os.MkdirAll(filepath.Dir(s.databaseBackupPath(backupID)), 0700); err != nil {
		fail(w, 503, "backup storage unavailable")
		return
	}
	output, err := os.OpenFile(s.databaseBackupPath(backupID)+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail(w, 503, "backup storage unavailable")
		return
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		hash := sha256.New()
		var streamErr error
		for _, offset := range upload.Parts {
			part, err := os.Open(filepath.Join(s.databaseUploadDir(upload.ID), fmt.Sprintf("%012d.part", offset)))
			if err != nil {
				streamErr = err
				break
			}
			_, err = db.Decrypt(io.MultiWriter(writer, hash), part, key, upload.ID+":"+strconv.FormatInt(offset, 10), min64(8<<20, upload.Total-offset))
			part.Close()
			if err != nil {
				streamErr = err
				break
			}
		}
		if streamErr == nil && upload.SHA256 != "" && hex.EncodeToString(hash.Sum(nil)) != upload.SHA256 {
			streamErr = fmt.Errorf("uploaded backup checksum mismatch")
		}
		writer.CloseWithError(streamErr)
		done <- streamErr
	}()
	plainBytes, encryptErr := db.Encrypt(output, reader, backupKey, backupID, project.Spec.DiskMB<<20)
	reader.CloseWithError(encryptErr)
	streamErr := <-done
	syncErr := output.Sync()
	closeErr := output.Close()
	if encryptErr != nil || streamErr != nil || syncErr != nil || closeErr != nil {
		os.Remove(s.databaseBackupPath(backupID) + ".tmp")
		fail(w, 400, "uploaded backup integrity validation failed")
		return
	}
	file, err := os.Open(s.databaseBackupPath(backupID) + ".tmp")
	if err != nil {
		fail(w, 503, "backup validation failed")
		return
	}
	decryptReader, decryptWriter := io.Pipe()
	verifyDone := make(chan error, 1)
	go func() {
		_, e := db.Decrypt(decryptWriter, file, backupKey, backupID, project.Spec.DiskMB<<20)
		decryptWriter.CloseWithError(e)
		verifyDone <- e
	}()
	contentErr := db.ValidateContent(project.Spec.Engine, upload.Format, decryptReader, project.Spec.DiskMB<<20)
	decryptReader.CloseWithError(contentErr)
	decryptErr := <-verifyDone
	file.Close()
	if contentErr != nil || decryptErr != nil {
		os.Remove(s.databaseBackupPath(backupID) + ".tmp")
		fail(w, 400, "backup format validation failed")
		return
	}
	if err = os.Rename(s.databaseBackupPath(backupID)+".tmp", s.databaseBackupPath(backupID)); err != nil {
		fail(w, 503, "backup commit failed")
		return
	}
	info, _ := os.Stat(s.databaseBackupPath(backupID))
	hashFile, _ := os.Open(s.databaseBackupPath(backupID))
	hash := sha256.New()
	io.Copy(hash, hashFile)
	hashFile.Close()
	backup := model.Backup{ID: backupID, Owner: upload.Owner, ProjectID: project.Spec.ID, ProjectSpec: project.Spec, Database: upload.Database, Engine: project.Spec.Engine, EngineVersion: project.Spec.Version, Format: upload.Format, State: "READY", Destination: "central", WorkerID: project.WorkerID, EncryptedKey: encryptedKey, Bytes: info.Size(), PlainBytes: plainBytes, ReservedBytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), Created: time.Now().UTC()}
	d.Lock()
	before := s.databaseCheckpoint()
	delete(d.Uploads, upload.ID)
	d.Backups[backup.ID] = backup
	err = s.saveDatabase(before)
	d.Unlock()
	if err != nil {
		os.Remove(s.databaseBackupPath(backup.ID))
		fail(w, 503, err.Error())
		return
	}
	os.RemoveAll(s.databaseUploadDir(upload.ID))
	writeJSON(w, 201, backup)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (s *Server) deleteDatabaseUpload(w http.ResponseWriter, r *http.Request) {
	if !s.databaseAvailable(w) {
		return
	}
	d := s.databases
	d.Lock()
	upload, ok := d.Uploads[r.PathValue("id")]
	if !ok || !canAccessApplication(r, upload.Owner) {
		d.Unlock()
		fail(w, 404, "upload not found")
		return
	}
	before := s.databaseCheckpoint()
	delete(d.Uploads, upload.ID)
	err := s.saveDatabase(before)
	d.Unlock()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	os.RemoveAll(s.databaseUploadDir(upload.ID))
	w.WriteHeader(204)
}

func (s *Server) cleanupDatabaseUploads(ctx context.Context) {
	if s.databases == nil {
		return
	}
	d := s.databases
	d.Lock()
	ids := []string{}
	before := s.databaseCheckpoint()
	for id, upload := range d.Uploads {
		if time.Since(upload.Created) > 24*time.Hour {
			delete(d.Uploads, id)
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		_ = s.saveDatabase(before)
	}
	d.Unlock()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		os.RemoveAll(s.databaseUploadDir(id))
	}
}
