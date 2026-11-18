package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripDefaultCloudUser(t *testing.T) {
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	home := filepath.Join(root, "home", "ubuntu")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := "root:x:0:0:root:/root:/bin/bash\nubuntu:x:1000:1000:Ubuntu:/home/ubuntu:/bin/bash\n"
	if err := os.WriteFile(filepath.Join(etc, "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StripDefaultCloudUser(root, "ubuntu"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(etc, "passwd"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ubuntu:") {
		t.Fatalf("ubuntu user still present: %s", b)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("home/ubuntu still exists: %v", err)
	}
	if err := StripDefaultCloudUser(root, "centos"); err != nil {
		t.Fatal(err)
	}
}
