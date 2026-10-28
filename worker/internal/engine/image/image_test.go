package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func makeTar(t *testing.T, entries []tar.Header, data map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		body := data[h.Name]
		h.Size = int64(len(body))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestEnsurePullVerifyCache(t *testing.T) {
	archive := makeTar(t, []tar.Header{{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755}, {Name: "etc/os-release", Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{"etc/os-release": "ID=test\n"})
	sum := sha256.Sum256(archive)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write(archive) }))
	defer srv.Close()
	m := New(t.TempDir(), 0)
	ref := proto.ImageRef{Name: "ubuntu", Version: "24.04", URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}
	root, err := m.Ensure(ref)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/os-release")); string(b) != "ID=test\n" {
		t.Fatalf("bad content %q", b)
	}
	if _, err := m.Ensure(ref); err != nil || hits != 1 {
		t.Fatalf("expected cache hit, hits=%d err=%v", hits, err)
	}
}

func TestHashMismatchRejected(t *testing.T) {
	archive := makeTar(t, nil, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
	defer srv.Close()
	m := New(t.TempDir(), 0)
	_, err := m.Ensure(proto.ImageRef{Name: "x", Version: "1", URL: srv.URL, SHA256: "00"})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestTraversalContained(t *testing.T) {
	archive := makeTar(t, []tar.Header{{Name: "../../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{"../../escape.txt": "x"})
	base := t.TempDir()
	dest := filepath.Join(base, "a", "b", "rootfs")
	if err := Extract(bytes.NewReader(archive), dest, 0, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "escape.txt")); err == nil {
		t.Fatal("file escaped extraction root")
	}
	if _, err := os.Stat(filepath.Join(dest, "escape.txt")); err != nil {
		t.Fatal("expected file contained inside root")
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	archive := makeTar(t, []tar.Header{
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc", Mode: 0o777},
		{Name: "link/pwned", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"link/pwned": "x"})
	if err := Extract(bytes.NewReader(archive), t.TempDir(), 0, 1<<20); err == nil {
		t.Fatal("expected symlink traversal to be rejected")
	}
}
