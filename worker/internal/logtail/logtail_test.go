package logtail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanPathRejectsEscape(t *testing.T) {
	if _, err := CleanPath("../etc/passwd"); err == nil {
		t.Fatal("parent path accepted")
	}
	if _, err := CleanPath("/var/log/../etc/passwd"); err == nil {
		t.Fatal("dot-dot path accepted")
	}
	if got, err := CleanPath("/var/log/nginx/error.log"); err != nil || got != "/var/log/nginx/error.log" {
		t.Fatalf("abs path: %q %v", got, err)
	}
}

func TestAllowHostPath(t *testing.T) {
	if err := AllowHostPath("/var/log/clofix-filevps/app.log"); err != nil {
		t.Fatal(err)
	}
	if err := AllowHostPath("/etc/passwd"); err == nil {
		t.Fatal("host path outside /var/log accepted")
	}
	if err := AllowHostPath("/var/log/../etc/shadow"); err == nil {
		t.Fatal("escaped host path accepted")
	}
}

func TestGuestPathStaysInProcRoot(t *testing.T) {
	got, err := GuestPath(12, "/var/log/nginx/error.log")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/proc/12/root/var/log/nginx/error.log" {
		t.Fatal(got)
	}
	if _, err := GuestPath(12, "../etc/passwd"); err == nil {
		t.Fatal("guest escape accepted")
	}
}

func TestConfineAndTailFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "nested", "app.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 80)+"hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Confine(root, "nested/app.log")
	if err != nil || got != path {
		t.Fatalf("confine: %q %v", got, err)
	}
	if _, err := Confine(root, "../secret"); err == nil {
		t.Fatal("confine escape accepted")
	}
	text := string(TailFile(path, 16))
	if !strings.Contains(text, "hello") || len(text) > 16 {
		t.Fatalf("tail: %q", text)
	}
}
