//go:build linux

package lifecycle

import (
	"io"
	"testing"

	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestExecRejectsUnknownAndEmpty(t *testing.T) {
	e := New(config.Default())
	if err := e.Exec("missing", []string{"true"}, io.Discard, io.Discard); err == nil {
		t.Fatal("expected missing instance to fail")
	}
	if err := e.Exec("missing", nil, io.Discard, io.Discard); err == nil {
		t.Fatal("expected empty command to fail")
	}
	e.meta["idle"] = &Meta{ID: "idle", Pid: 0, Spec: proto.InstanceSpec{}}
	if err := e.Exec("idle", []string{"true"}, io.Discard, io.Discard); err == nil {
		t.Fatal("expected stopped instance to fail")
	}
}

func TestUsedNetwork(t *testing.T) {
	e := New(config.Default())
	e.meta["a"] = &Meta{Spec: proto.InstanceSpec{IPAddress: "10.231.0.2", SSHPort: 22000}}
	ips, ports := e.UsedNetwork()
	if !ips["10.231.0.2"] || !ports[22000] {
		t.Fatalf("used network = %v %v", ips, ports)
	}
}
