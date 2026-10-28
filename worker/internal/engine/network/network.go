// Package network configures a private bridge for instances and forwards selected service ports.
package network

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/clofix/filevps/worker/pkg/proto"
)

const (
	bridgeName = "cfbr0"
	bridgeAddr = "10.231.0.1/16"
	subnet     = "10.231.0.0/16"
)

type Manager struct{}

func New() *Manager { return &Manager{} }

func command(name string, args ...string) ([]byte, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

func names(id string) (string, string) {
	h := sha256.Sum256([]byte(id))
	s := fmt.Sprintf("%x", h[:3])
	return "cfh" + s, "cfp" + s
}

func firewall() error {
	if _, err := command("nft", "list", "table", "ip", "clofix"); err == nil {
		return nil
	}
	rules := `table ip clofix {
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "cfbr0" ip daddr 169.254.169.254 drop
  iifname "cfbr0" oifname != "cfbr0" accept
  iifname != "cfbr0" oifname "cfbr0" ct status dnat accept
  iifname != "cfbr0" oifname "cfbr0" ct state established,related accept
 }
 chain prerouting { type nat hook prerouting priority dstnat; policy accept; }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  oifname != "cfbr0" ip saddr 10.231.0.0/16 masquerade
 }
}`
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		if _, again := command("nft", "list", "table", "ip", "clofix"); again == nil {
			return nil
		}
		return fmt.Errorf("initialize nftables: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func ensureBridge() error {
	if _, err := command("ip", "link", "show", bridgeName); err != nil {
		if _, err = command("ip", "link", "add", bridgeName, "type", "bridge"); err != nil {
			return err
		}
	}
	if _, err := command("ip", "addr", "replace", bridgeAddr, "dev", bridgeName); err != nil {
		return err
	}
	if _, err := command("ip", "link", "set", bridgeName, "up"); err != nil {
		return err
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	return firewall()
}

// Setup attaches a veth pair, assigns the private address and installs service port mappings.
func (m *Manager) Setup(id string, pid int, address string, sshPort int, mappings []proto.PortMapping) error {
	parsed := net.ParseIP(address).To4()
	_, network, _ := net.ParseCIDR(subnet)
	if parsed == nil || !network.Contains(parsed) || parsed[3] < 2 || sshPort < 22000 || sshPort > 22099 || pid <= 0 {
		return fmt.Errorf("invalid instance network settings")
	}
	// Reconciliation may be recreating an instance after the worker process restarted.
	m.Cleanup(id, sshPort)
	if err := ensureBridge(); err != nil {
		return err
	}
	hostIf, peerIf := names(id)
	if _, err := command("ip", "link", "show", hostIf); err == nil {
		command("ip", "link", "del", hostIf)
	}
	if _, err := command("ip", "link", "add", hostIf, "type", "veth", "peer", "name", peerIf); err != nil {
		return err
	}
	cleanupLink := true
	defer func() {
		if cleanupLink {
			command("ip", "link", "del", hostIf)
		}
	}()
	steps := [][]string{
		{"ip", "link", "set", hostIf, "master", bridgeName},
		{"bridge", "link", "set", "dev", hostIf, "isolated", "on"},
		{"ip", "link", "set", hostIf, "up"},
		{"ip", "link", "set", peerIf, "netns", strconv.Itoa(pid)},
		{"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", peerIf, "name", "eth0"},
		{"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", "lo", "up"},
		{"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "addr", "add", address + "/16", "dev", "eth0"},
		{"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", "eth0", "up"},
		{"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "route", "replace", "default", "via", "10.231.0.1"},
	}
	for _, step := range steps {
		if _, err := command(step[0], step[1:]...); err != nil {
			return err
		}
	}
	if err := m.SyncPorts(id, address, sshPort, mappings); err != nil {
		return err
	}
	cleanupLink = false
	return nil
}

func (m *Manager) SyncPorts(id, address string, sshPort int, mappings []proto.PortMapping) error {
	for _, mapping := range mappings {
		protocol := strings.ToLower(mapping.Protocol)
		if (protocol != "tcp" && protocol != "udp") || mapping.HostPort < 24000 || mapping.HostPort > 24099 || mapping.GuestPort < 1 || mapping.GuestPort > 65535 {
			return fmt.Errorf("invalid port mapping %s %d:%d", protocol, mapping.HostPort, mapping.GuestPort)
		}
	}
	m.removeRules(id)
	comment := "clofix-vps-" + id
	add := func(protocol string, hostPort, guestPort int) error {
		_, err := command("nft", "add", "rule", "ip", "clofix", "prerouting", protocol, "dport", strconv.Itoa(hostPort), "dnat", "to", net.JoinHostPort(address, strconv.Itoa(guestPort)), "comment", comment)
		return err
	}
	for _, mapping := range []struct {
		protocol    string
		host, guest int
	}{
		{"tcp", sshPort, 22},
		{"tcp", sshPort + 1000, 80},
		{"tcp", sshPort + 1100, 443},
	} {
		if err := add(mapping.protocol, mapping.host, mapping.guest); err != nil {
			return err
		}
	}
	for _, mapping := range mappings {
		protocol := strings.ToLower(mapping.Protocol)
		if err := add(protocol, mapping.HostPort, mapping.GuestPort); err != nil {
			return err
		}
	}
	return nil
}

// Cleanup removes an instance's DNAT rules and host-side veth.
func (m *Manager) Cleanup(id string, sshPort int) {
	hostIf, _ := names(id)
	m.removeRules(id)
	command("ip", "link", "del", hostIf)
}

func (m *Manager) removeRules(id string) {
	if out, err := command("nft", "-a", "list", "chain", "ip", "clofix", "prerouting"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.Contains(line, "clofix-vps-"+id) {
				continue
			}
			fields := strings.Fields(line)
			for i := 0; i+1 < len(fields); i++ {
				if fields[i] == "handle" {
					command("nft", "delete", "rule", "ip", "clofix", "prerouting", "handle", fields[i+1])
					break
				}
			}
		}
	}
}
