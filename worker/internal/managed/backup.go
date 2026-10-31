//go:build linux

package managed

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// A stopped application cannot be started/deleted while its files are exported.
func (m *Manager) downloadBackup(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", 405)
		return
	}
	m.Lock()
	a := m.apps[id]
	if a == nil || a.Status.Desired != "stopped" || m.CG.Alive(id) || a.backupActive {
		m.Unlock()
		http.Error(w, "Stop the application before exporting its data", 409)
		return
	}
	if err := m.ensureData(id); err != nil {
		m.Unlock()
		http.Error(w, "Cannot mount application data", 500)
		return
	}
	a.backupActive = true
	m.Unlock()
	defer func() { m.Lock(); a.backupActive = false; m.Unlock() }()
	root := filepath.Join(m.path(id), "data")
	type entry struct {
		path, name string
		size       int64
	}
	files := []entry{}
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()
		if len(files) >= 10000 || total > 1<<30 {
			return fmt.Errorf("backup exceeds 10000 files or 1 GiB")
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, entry{path, filepath.ToSlash(name), info.Size()})
		return nil
	})
	if err != nil {
		http.Error(w, "Cannot export data: "+err.Error(), 422)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`-data.zip"`)
	archive := zip.NewWriter(w)
	defer archive.Close()
	for _, file := range files {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		source, err := os.Open(file.path)
		if err != nil {
			return
		}
		target, err := archive.Create(file.name)
		if err != nil {
			source.Close()
			return
		}
		_, err = io.Copy(target, io.LimitReader(source, file.size))
		source.Close()
		if err != nil {
			return
		}
	}
}
