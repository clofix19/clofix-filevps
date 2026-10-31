package execution

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func projectZIP(t *testing.T, names []string, link bool) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range names {
		h := &zip.FileHeader{Name: name}
		if link {
			h.SetMode(os.ModeSymlink | 0777)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte("print('hello')"))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestProjectExtractionRejectsUnsafeEntries(t *testing.T) {
	for _, test := range []struct {
		name  string
		names []string
		link  bool
	}{{"traversal", []string{"../outside"}, false}, {"absolute", []string{"/etc/passwd"}, false}, {"backslash", []string{`..\outside`}, false}, {"duplicate", []string{"main.py", "main.py"}, false}, {"symlink", []string{"main.py"}, true}, {"reserved", []string{".clofix-env.json"}, false}} {
		t.Run(test.name, func(t *testing.T) {
			e := proto.Execution{Format: "zip", Entry: "main.py", Artifact: projectZIP(t, test.names, test.link)}
			if ExtractProject(t.TempDir(), e) == nil {
				t.Fatal("unsafe ZIP accepted")
			}
		})
	}
}
func TestProjectAndOutputArchive(t *testing.T) {
	root := t.TempDir()
	e := proto.Execution{Format: "zip", Entry: "src/main.py", Artifact: projectZIP(t, []string{"src/main.py", "requirements.txt"}, false)}
	if err := ExtractProject(root, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "src/main.py")); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "answer.txt"), []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := CollectOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 1 || z.File[0].Name != "answer.txt" {
		t.Fatal("output archive invalid")
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(out, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err = CollectOutput(out); err == nil {
		t.Fatal("output symlink accepted")
	}
}
func TestRuntimeExecutableResolution(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "usr/bin"), 0755)
	_ = os.MkdirAll(filepath.Join(root, "etc/alternatives"), 0755)
	_ = os.MkdirAll(filepath.Join(root, "usr/lib/jvm"), 0755)
	_ = os.WriteFile(filepath.Join(root, "usr/lib/jvm/java"), []byte("x"), 0755)
	_ = os.Symlink("/etc/alternatives/java", filepath.Join(root, "usr/bin/java"))
	_ = os.Symlink("/usr/lib/jvm/java", filepath.Join(root, "etc/alternatives/java"))
	if !RuntimeReady(root, "java") {
		t.Fatal("absolute runtime symlink resolved against host instead of rootfs")
	}
	if RuntimeReady(root, "bun") {
		t.Fatal("missing runtime advertised")
	}
}
