package fim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FileMeta struct {
	Path    string      `json:"path"`
	SHA256  string      `json:"sha256"`
	Size    int64       `json:"size"`
	Mode    os.FileMode `json:"mode"`
	UID     int         `json:"uid"`
	GID     int         `json:"gid"`
	ModTime time.Time   `json:"mod_time"`
}

type Change struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // created|modified|deleted|perm|owner
	Detail string `json:"detail"`
}

type Baseline struct {
	Version   int                 `json:"version"`
	CreatedAt time.Time           `json:"created_at"`
	Files     map[string]FileMeta `json:"files"`
}

type Engine struct {
	Paths   []string
	Exclude []string
	Store   string // baseline path
}

func (e *Engine) baselinePath() string {
	if e.Store != "" {
		return e.Store
	}
	return "/var/lib/clofix/guard/fim-baseline.json"
}

func (e *Engine) excluded(path string) bool {
	for _, x := range e.Exclude {
		if path == x || strings.HasPrefix(path, strings.TrimSuffix(x, "/")+"/") {
			return true
		}
	}
	// Skip volatile runtime
	if strings.HasPrefix(path, "/etc/ssh/ssh_host_") {
		return true
	}
	return false
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (e *Engine) Scan() (map[string]FileMeta, error) {
	out := make(map[string]FileMeta)
	for _, root := range e.Paths {
		fi, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			if meta, err := e.meta(root); err == nil {
				out[root] = meta
			}
			continue
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if base == "proc" || base == "sys" || base == "dev" || base == "run" {
					if path != root {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if e.excluded(path) {
				return nil
			}
			// Bound: skip huge files (>32MiB) for baseline cost
			if info.Size() > 32<<20 {
				return nil
			}
			if meta, err := e.meta(path); err == nil {
				out[path] = meta
			}
			return nil
		})
	}
	return out, nil
}

func (e *Engine) meta(path string) (FileMeta, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return FileMeta{}, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return FileMeta{}, fmt.Errorf("symlink")
	}
	sum, size, err := hashFile(path)
	if err != nil {
		return FileMeta{}, err
	}
	uid, gid := fileOwners(st)
	return FileMeta{
		Path: path, SHA256: sum, Size: size, Mode: st.Mode(),
		UID: uid, GID: gid, ModTime: st.ModTime().UTC(),
	}, nil
}

func (e *Engine) WriteBaseline(files map[string]FileMeta) error {
	b := Baseline{Version: 1, CreatedAt: time.Now().UTC(), Files: files}
	dir := filepath.Dir(e.baselinePath())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// Versioned copy
	ver := filepath.Join(dir, fmt.Sprintf("fim-baseline-%s.json", time.Now().UTC().Format("20060102T150405")))
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(ver, raw, 0o640); err != nil {
		return err
	}
	return os.WriteFile(e.baselinePath(), raw, 0o640)
}

func (e *Engine) LoadBaseline() (Baseline, error) {
	var b Baseline
	raw, err := os.ReadFile(e.baselinePath())
	if err != nil {
		return b, err
	}
	err = json.Unmarshal(raw, &b)
	if b.Files == nil {
		b.Files = map[string]FileMeta{}
	}
	return b, err
}

func (e *Engine) Diff(current map[string]FileMeta) ([]Change, error) {
	base, err := e.LoadBaseline()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no baseline: run fim baseline first")
		}
		return nil, err
	}
	var changes []Change
	for path, cur := range current {
		old, ok := base.Files[path]
		if !ok {
			changes = append(changes, Change{Path: path, Kind: "created", Detail: "new file"})
			continue
		}
		if old.SHA256 != cur.SHA256 {
			changes = append(changes, Change{Path: path, Kind: "modified", Detail: "sha256 changed"})
		}
		if old.Mode != cur.Mode {
			changes = append(changes, Change{Path: path, Kind: "perm", Detail: fmt.Sprintf("%v -> %v", old.Mode, cur.Mode)})
		}
		if old.UID != cur.UID || old.GID != cur.GID {
			changes = append(changes, Change{Path: path, Kind: "owner", Detail: fmt.Sprintf("%d:%d -> %d:%d", old.UID, old.GID, cur.UID, cur.GID)})
		}
	}
	for path := range base.Files {
		if _, ok := current[path]; !ok {
			changes = append(changes, Change{Path: path, Kind: "deleted", Detail: "missing"})
		}
	}
	return changes, nil
}
