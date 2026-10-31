package database

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type brokenStore struct{}

func (brokenStore) LoadDatabaseState() ([]byte, error) {
	return nil, errors.New("database unavailable")
}
func (brokenStore) SaveDatabaseState([]byte) error { return errors.New("database unavailable") }
func TestStatePersistenceAndFailClosed(t *testing.T) {
	root := t.TempDir()
	state, e := New(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	state.Jobs["job"] = Job{ID: "job", Owner: "tenant-a", State: "QUEUED", Payload: []byte("encrypted-payload")}
	state.Lock()
	e = state.Save()
	state.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	restored, e := New(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	if restored.Jobs["job"].Owner != "tenant-a" {
		t.Fatal("job ownership not persisted")
	}
	stat, e := os.Stat(filepath.Join(root, "database-state.json"))
	if e != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("state file permissions", e)
	}
	if e = os.WriteFile(filepath.Join(root, "database-state.json"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = New(root, nil); e == nil {
		t.Fatal("corrupt persisted state accepted")
	}
	if _, e = New(t.TempDir(), brokenStore{}); e == nil {
		t.Fatal("database outage silently fell back to empty state")
	}
}
