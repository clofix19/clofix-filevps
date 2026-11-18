package fim_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/clofix/clofix-linux/internal/fim"
)

func TestBaselineDiffDetectsModify(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := fim.Engine{Paths: []string{dir}, Store: filepath.Join(dir, "base.json")}
	files, err := eng.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.WriteBaseline(files); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := eng.Scan()
	if err != nil {
		t.Fatal(err)
	}
	ch, err := eng.Diff(cur)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range ch {
		if c.Kind == "modified" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected modified, got %#v", ch)
	}
}
