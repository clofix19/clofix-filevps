//go:build linux

package lifecycle

import (
	"testing"

	"github.com/clofix/filevps/worker/internal/config"
)

func TestAllocateHostUIDIsUnique(t *testing.T) {
	e := New(config.Default())
	e.Cfg.UserNS = true
	e.Cfg.UIDBase = 100000
	e.meta["a"] = &Meta{ID: "a", HostUID: 100000}
	e.meta["b"] = &Meta{ID: "b", HostUID: 165536}
	got, err := e.allocateHostUID()
	if err != nil {
		t.Fatal(err)
	}
	if got != 231072 {
		t.Fatalf("allocateHostUID = %d, want 231072", got)
	}
}

func TestAllocateHostUIDSkipsBaseWhileLegacyExists(t *testing.T) {
	e := New(config.Default())
	e.Cfg.UserNS = true
	e.Cfg.UIDBase = 100000
	e.meta["legacy"] = &Meta{ID: "legacy"}
	got, err := e.allocateHostUID()
	if err != nil {
		t.Fatal(err)
	}
	if got != 165536 {
		t.Fatalf("allocateHostUID = %d, want 165536 while a pre-range guest still uses uid_base", got)
	}
}
