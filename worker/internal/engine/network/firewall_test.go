package network

import (
	"strings"
	"testing"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func TestHostFirewallCommandsRestrictAndSource(t *testing.T) {
	cmds := hostFirewallCommands(true, []proto.FirewallRule{{
		Action: "allow", Protocol: "tcp", Port: 22022, Source: "10.0.0.0/8",
	}})
	joined := flatten(cmds)
	if !strings.Contains(joined, "10.0.0.0/8") || !strings.Contains(joined, "22022") {
		t.Fatalf("source match missing: %s", joined)
	}
	if !strings.Contains(joined, "drop") || !strings.Contains(joined, "22000-26999") || !strings.Contains(joined, "42000-46999") {
		t.Fatalf("restrict drop missing: %s", joined)
	}
	if !strings.Contains(joined, "clofix-fw-host") {
		t.Fatal("host comment missing")
	}
	open := flatten(hostFirewallCommands(false, nil))
	if strings.Contains(open, "drop") {
		t.Fatal("restrict=false must not install default-deny")
	}
}

func TestVPSFirewallCommandsDNATAndCleanupHandles(t *testing.T) {
	cmds := vpsFirewallCommands("vps-1", "10.231.0.10", true, []proto.FirewallRule{{
		Action: "allow", Protocol: "tcp", Port: 22, Source: "10.0.0.0/8",
	}})
	joined := flatten(cmds)
	if !strings.Contains(joined, "ct status dnat") || !strings.Contains(joined, "10.231.0.10") {
		t.Fatalf("DNAT guest match missing: %s", joined)
	}
	if !strings.Contains(joined, "clofix-fw-vps-vps-1") {
		t.Fatal("vps comment missing")
	}
	listed := "\tip saddr 10.0.0.0/8 tcp dport 22 accept comment \"clofix-fw-vps-vps-1\" # handle 9\n\tip daddr 1.2.3.4 accept comment \"other\" # handle 10\n"
	handles := taggedHandles(listed, "clofix-fw-vps-vps-1")
	if len(handles) != 1 || handles[0] != "9" {
		t.Fatalf("handles %v", handles)
	}
}

func flatten(cmds [][]string) string {
	parts := make([]string, 0, len(cmds))
	for _, c := range cmds {
		parts = append(parts, strings.Join(c, " "))
	}
	return strings.Join(parts, "\n")
}
