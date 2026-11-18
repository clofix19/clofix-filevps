package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type imageEntry struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	URL         string    `json:"url,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	Description string    `json:"description,omitempty"`
	Enabled     bool      `json:"enabled"`
	Created     time.Time `json:"created"`
}

type imageCatalog struct {
	sync.Mutex
	path  string
	Items map[string]imageEntry `json:"items"`
}

func loadImageCatalog(dir string) *imageCatalog {
	c := &imageCatalog{path: filepath.Join(dir, "image-catalog.json"), Items: map[string]imageEntry{}}
	b, err := os.ReadFile(c.path)
	if err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.Items == nil {
		c.Items = map[string]imageEntry{}
	}
	now := time.Now().UTC()
	if len(c.Items) == 0 {
		c.Items["ubuntu-24.04"] = imageEntry{ID: "ubuntu-24.04", Name: "ubuntu", Version: "24.04", Description: "Bundled Ubuntu 24.04 · root-only", Enabled: true, Created: now}
		c.Items["ubuntu-22.04"] = imageEntry{ID: "ubuntu-22.04", Name: "ubuntu", Version: "22.04", Description: "Bundled Ubuntu 22.04 · root-only", Enabled: true, Created: now}
		c.Items["clofix-linux-1.0"] = imageEntry{ID: "clofix-linux-1.0", Name: "clofix-linux", Version: "1.0", Description: "CloFix Linux 1.0 · branded Ubuntu base · root-only", Enabled: true, Created: now}
	} else {
		// Keep catalog current when upgrading older controllers that already have image-catalog.json.
		if e, ok := c.Items["ubuntu-22.04"]; ok && strings.Contains(e.Description, "requires bundled or custom") {
			e.Description = "Bundled Ubuntu 22.04 · root-only"
			c.Items["ubuntu-22.04"] = e
		}
		if _, ok := c.Items["clofix-linux-1.0"]; !ok {
			c.Items["clofix-linux-1.0"] = imageEntry{ID: "clofix-linux-1.0", Name: "clofix-linux", Version: "1.0", Description: "CloFix Linux 1.0 · branded Ubuntu base · root-only", Enabled: true, Created: now}
		}
	}
	return c
}

func (c *imageCatalog) save() error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (s *Server) imageCatalogRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/images", s.auth(s.listImages))
	m.HandleFunc("POST /api/images", s.auth(s.saveImage))
	m.HandleFunc("POST /api/images/from-backup", s.auth(s.imageFromBackup))
	m.HandleFunc("DELETE /api/images/{id}", s.auth(s.deleteImage))
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	c := s.images
	c.Lock()
	defer c.Unlock()
	items := make([]imageEntry, 0, len(c.Items))
	for _, item := range c.Items {
		if item.Enabled || isAdmin(r) {
			items = append(items, item)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) saveImage(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "image catalog changes require administrator access")
		return
	}
	var in imageEntry
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		fail(w, 400, "invalid image payload")
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Version = strings.TrimSpace(in.Version)
	if in.Name == "" || in.Version == "" {
		fail(w, 400, "name and version required")
		return
	}
	if in.ID == "" {
		in.ID = in.Name + "-" + in.Version
	}
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	in.Enabled = true
	if in.Created.IsZero() {
		in.Created = time.Now().UTC()
	}
	c := s.images
	c.Lock()
	defer c.Unlock()
	c.Items[in.ID] = in
	if err := c.save(); err != nil {
		fail(w, 500, "could not save image catalog")
		return
	}
	writeJSON(w, 200, in)
}

func (s *Server) imageFromBackup(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "snapshot-as-image requires administrator access")
		return
	}
	var in struct {
		BackupID    string `json:"backup_id"`
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid payload")
		return
	}
	s.usageMu.RLock()
	backup, ok := s.backups[in.BackupID]
	s.usageMu.RUnlock()
	if !ok || backup.State != "READY" {
		fail(w, 404, "ready backup not found")
		return
	}
	name, version := restoreImage(backup.Image)
	if in.Name != "" {
		name = strings.ToLower(strings.TrimSpace(in.Name))
	}
	if in.Version != "" {
		version = strings.TrimSpace(in.Version)
	}
	id := name + "-" + version + "-snap-" + backup.ID[len(backup.ID)-6:]
	entry := imageEntry{
		ID: id, Name: name, Version: version, Description: in.Description,
		Enabled: true, Created: time.Now().UTC(),
	}
	if entry.Description == "" {
		entry.Description = "Snapshot image from backup " + backup.ID + " (restore still uses the source backup ID on create)."
	}
	c := s.images
	c.Lock()
	c.Items[id] = entry
	err := c.save()
	c.Unlock()
	if err != nil {
		fail(w, 500, "could not save image catalog")
		return
	}
	writeJSON(w, 200, map[string]any{"image": entry, "backup_id": backup.ID})
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "image catalog changes require administrator access")
		return
	}
	id := r.PathValue("id")
	c := s.images
	c.Lock()
	defer c.Unlock()
	if _, ok := c.Items[id]; !ok {
		fail(w, 404, "image not found")
		return
	}
	delete(c.Items, id)
	if err := c.save(); err != nil {
		fail(w, 500, "could not save image catalog")
		return
	}
	writeJSON(w, 200, map[string]string{"deleted": id})
}
