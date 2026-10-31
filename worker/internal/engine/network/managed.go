package network

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// Managed app ports are served by an authenticated worker-side gateway, not DNAT.
func (m *Manager) SetupManaged(id string, pid int, address string) error {
	ip := net.ParseIP(address).To4()
	if ip == nil || ip[0] != 10 || ip[1] != 231 || ip[2] != 128 || ip[3] < 2 || pid <= 0 {
		return fmt.Errorf("invalid managed network")
	}
	m.CleanupManaged(id)
	if err := ensureBridge(); err != nil {
		return err
	}
	if err := managedFirewall(); err != nil {
		return err
	}
	host, peer := names(id)
	if _, err := command("ip", "link", "add", host, "type", "veth", "peer", "name", peer); err != nil {
		return err
	}
	steps := [][]string{{"ip", "link", "set", host, "master", bridgeName}, {"bridge", "link", "set", "dev", host, "isolated", "on"}, {"ip", "link", "set", host, "up"}, {"ip", "link", "set", peer, "netns", strconv.Itoa(pid)}, {"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", peer, "name", "eth0"}, {"nsenter", "-t", strconv.Itoa(pid), "-n", "sh", "-c", "if [ -e /proc/sys/net/ipv6/conf/all/disable_ipv6 ]; then echo 1 > /proc/sys/net/ipv6/conf/all/disable_ipv6; echo 1 > /proc/sys/net/ipv6/conf/default/disable_ipv6; fi"},
		{"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", "lo", "up"}, {"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "addr", "add", address + "/16", "dev", "eth0"}, {"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", "eth0", "up"}, {"nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "route", "replace", "default", "via", "10.231.0.1"}}
	for _, step := range steps {
		if _, err := command(step[0], step[1:]...); err != nil {
			m.CleanupManaged(id)
			return err
		}
	}
	return nil
}
func (m *Manager) CleanupManaged(id string) {
	host, _ := names(id)
	_, _ = command("ip", "link", "del", host)
}
func managedFirewall() error {
	if _, err := command("nft", "list", "table", "inet", "clofix_managed"); err == nil {
		return nil
	}
	rules := `table inet clofix_managed {
 chain input { type filter hook input priority -20; policy accept;
  ip saddr 10.231.128.0/24 ct state established,related accept
  ip saddr 10.231.128.0/24 drop
 }
 chain forward { type filter hook forward priority -20; policy accept;
  ip saddr 10.231.128.0/24 ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 224.0.0.0/4, 240.0.0.0/4 } drop
 }
}`
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("managed network isolation: %s: %w", out, err)
	}
	return nil
}
