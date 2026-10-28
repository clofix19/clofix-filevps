// Package image fetches, verifies, caches and extracts base images (spec sections 5 and 7).
// Images can come from a bundled trusted rootfs or a short-lived URL with mandatory SHA-256.
package image

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type Manager struct {
	CacheDir   string
	BuiltinDir string
	UIDShift   uint32 // host uid/gid base that container root maps to; applied on extraction
	MaxBytes   int64  // download and extracted size cap
	Client     *http.Client
	mu         sync.Mutex
}

func New(cacheDir string, uidShift uint32) *Manager {
	builtinDir := os.Getenv("CLOFIX_BUILTIN_IMAGE_DIR")
	if builtinDir == "" {
		builtinDir = "/opt/clofix/builtin"
	}
	return &Manager{CacheDir: cacheDir, BuiltinDir: builtinDir, UIDShift: uidShift, MaxBytes: 8 << 30, Client: &http.Client{Timeout: 30 * time.Minute}}
}

type manifest struct {
	FormatVersion int       `json:"format_version"`
	SHA256        string    `json:"sha256"`
	UIDShift      uint32    `json:"uid_shift"`
	Pulled        time.Time `json:"pulled"`
}

func (m *Manager) imageDir(name, version string) string {
	return filepath.Join(m.CacheDir, name, version)
}

// Ensure returns the extracted rootfs directory, pulling and verifying it when missing.
func (m *Manager) Ensure(ref proto.ImageRef) (string, error) {
	if !safeName.MatchString(ref.Name) || !safeName.MatchString(ref.Version) {
		return "", errors.New("invalid image name or version")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := m.imageDir(ref.Name, ref.Version)
	root := filepath.Join(dir, "rootfs")
	builtin := ref.URL == "" && ref.SHA256 == ""
	archive := ""
	builtinSum := ""
	if builtin {
		archive = filepath.Join(m.BuiltinDir, ref.Name, ref.Version, "rootfs.tar.gz")
		if fi, err := os.Stat(archive); err != nil || !fi.Mode().IsRegular() {
			return "", fmt.Errorf("image %s:%s is not bundled; provide an image URL and sha256", ref.Name, ref.Version)
		}
		f, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		builtinSum = hex.EncodeToString(h.Sum(nil))
	} else {
		if ref.URL == "" || ref.SHA256 == "" {
			return "", errors.New("custom images require both url and sha256")
		}
		if !strings.HasPrefix(ref.URL, "https://") && !strings.HasPrefix(ref.URL, "http://") {
			return "", errors.New("image url must be http(s)")
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		var mf manifest
		valid := json.Unmarshal(b, &mf) == nil
		if valid {
			valid = mf.FormatVersion == 2 && mf.UIDShift == m.UIDShift
		}
		if valid && builtin {
			valid = strings.EqualFold(mf.SHA256, builtinSum)
		}
		if valid && !builtin && ref.SHA256 != "" {
			valid = strings.EqualFold(mf.SHA256, ref.SHA256)
		}
		if valid {
			return root, nil // cache hit
		}
	}
	if err := os.MkdirAll(m.CacheDir, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(m.CacheDir, ".pull-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	sum := ""
	if !builtin {
		archive = filepath.Join(tmp, "image.tar.gz")
		sum, err = m.download(ref.URL, archive)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(sum, ref.SHA256) {
			return "", fmt.Errorf("sha256 mismatch: got %s", sum)
		}
	}
	staged := filepath.Join(tmp, "rootfs")
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if builtin {
		h := sha256.New()
		if err := Extract(io.TeeReader(f, h), staged, m.UIDShift, m.MaxBytes); err != nil {
			return "", err
		}
		sum = hex.EncodeToString(h.Sum(nil))
	} else if err := Extract(f, staged, m.UIDShift, m.MaxBytes); err != nil {
		return "", err
	}
	mb, _ := json.Marshal(manifest{FormatVersion: 2, SHA256: sum, UIDShift: m.UIDShift, Pulled: time.Now()})
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), mb, 0o640); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return "", err
	}
	os.RemoveAll(dir)
	// stage into place: rootfs first, manifest last (manifest marks a complete image)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if err := os.Rename(staged, root); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(tmp, "manifest.json"), filepath.Join(dir, "manifest.json")); err != nil {
		return "", err
	}
	return root, nil
}

func (m *Manager) download(url, dest string) (string, error) {
	resp, err := m.Client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("image download: http %d", resp.StatusCode)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer out.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, m.MaxBytes+1))
	if err != nil {
		return "", err
	}
	if n > m.MaxBytes {
		return "", errors.New("image exceeds size limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// noSymlinkParents refuses to write through a symlink anywhere between root and dir.
func noSymlinkParents(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return errors.New("path escapes root")
	}
	cur := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to traverse symlink %s", cur)
		}
	}
	return nil
}

// Extract unpacks a tar.gz into dest with path-traversal and symlink-escape protection.
// Device nodes and fifos are skipped (the engine provides /dev itself).
func Extract(r io.Reader, dest string, shift uint32, maxBytes int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	chown := func(p string, uid, gid int) error {
		return os.Lchown(p, int(shift)+uid, int(shift)+gid)
	}
	var total int64
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean("/" + h.Name)
		if name == "/" {
			continue
		}
		target := filepath.Join(dest, name)
		if err := noSymlinkParents(dest, filepath.Dir(target)); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := chown(target, h.Uid, h.Gid); err != nil {
				return fmt.Errorf("set ownership on %s: %w", name, err)
			}
			if err := os.Chmod(target, os.FileMode(h.Mode)&0o7777); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, maxBytes-total+1))
			f.Close()
			if err != nil {
				return err
			}
			if total += n; total > maxBytes {
				return errors.New("extracted image exceeds size limit")
			}
			if err := chown(target, h.Uid, h.Gid); err != nil {
				return fmt.Errorf("set ownership on %s: %w", name, err)
			}
			if err := os.Chmod(target, os.FileMode(h.Mode)&0o7777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
			if err := chown(target, h.Uid, h.Gid); err != nil {
				return fmt.Errorf("set ownership on %s: %w", name, err)
			}
		case tar.TypeLink:
			src := filepath.Join(dest, filepath.Clean("/"+h.Linkname))
			if err := noSymlinkParents(dest, filepath.Dir(src)); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Link(src, target); err != nil {
				return err
			}
		}
	}
}
