package network

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/clofix/filevps/worker/pkg/proto"
)

const firewallTable = "clofix_fw"

func ensureUserFirewall() error {
	if _, err := command("nft", "list", "table", "inet", firewallTable); err == nil {
		return nil
	}
	rules := `table inet clofix_fw {
 chain input { type filter hook input priority 10; policy accept; }
 chain forward { type filter hook forward priority 10; policy accept; }
}`
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		if _, again := command("nft", "list", "table", "inet", firewallTable); again == nil {
			return nil
		}
		return fmt.Errorf("initialize user firewall: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (m *Manager) SyncHostFirewall(restrict bool, rules []proto.FirewallRule) error {
	if err := proto.NormalizeFirewallSpec(&proto.FirewallSpec{Scope: "host", Restrict: restrict, Rules: rules}, true); err != nil {
		return err
	}
	if err := ensureUserFirewall(); err != nil {
		return err
	}
	removeTaggedRules("input", hostComment())
	cmds := hostFirewallCommands(restrict, rules)
	for _, args := range cmds {
		if _, err := command(args[0], args[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) SyncVPSFirewall(id, guestIP string, restrict bool, rules []proto.FirewallRule) error {
	spec := proto.FirewallSpec{Scope: "vps", InstanceID: id, GuestIP: guestIP, Restrict: restrict, Rules: rules}
	if err := proto.NormalizeFirewallSpec(&spec, false); err != nil {
		return err
	}
	ip := net.ParseIP(guestIP).To4()
	if ip == nil {
		return fmt.Errorf("invalid guest address")
	}
	if err := ensureUserFirewall(); err != nil {
		return err
	}
	removeTaggedRules("forward", vpsComment(id))
	cmds := vpsFirewallCommands(id, ip.String(), restrict, spec.Rules)
	for _, args := range cmds {
		if _, err := command(args[0], args[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) RemoveVPSFirewall(id string) {
	removeTaggedRules("forward", vpsComment(id))
}

func (m *Manager) RemoveHostFirewall() {
	removeTaggedRules("input", hostComment())
}

func hostComment() string { return "clofix-fw-host" }
func vpsComment(id string) string {
	return "clofix-fw-vps-" + id
}

func hostFirewallCommands(restrict bool, rules []proto.FirewallRule) [][]string {
	out := [][]string{nftAdd("input", []string{"ct", "state", "established,related", "accept"}, hostComment())}
	for _, rule := range orderedRules(rules) {
		args := matchArgs(rule, true)
		args = append(args, verdict(rule.Action))
		out = append(out, nftAdd("input", args, hostComment()))
	}
	if restrict {
		for _, protoName := range []string{"tcp", "udp"} {
			out = append(out, nftAdd("input", append(clofixPortArgs(protoName), "drop"), hostComment()))
		}
	}
	return out
}

func vpsFirewallCommands(id, guestIP string, restrict bool, rules []proto.FirewallRule) [][]string {
	base := []string{"ct", "status", "dnat", "ip", "daddr", guestIP}
	out := [][]string{nftAdd("forward", append(append([]string{}, base...), "ct", "state", "established,related", "accept"), vpsComment(id))}
	for _, rule := range orderedRules(rules) {
		args := append([]string{}, base...)
		args = append(args, matchArgs(rule, false)...)
		args = append(args, verdict(rule.Action))
		out = append(out, nftAdd("forward", args, vpsComment(id)))
	}
	if restrict {
		out = append(out, nftAdd("forward", append(append([]string{}, base...), "drop"), vpsComment(id)))
	}
	return out
}

func orderedRules(rules []proto.FirewallRule) []proto.FirewallRule {
	var deny, allow []proto.FirewallRule
	for _, rule := range rules {
		if rule.Action == "deny" {
			deny = append(deny, rule)
		} else {
			allow = append(allow, rule)
		}
	}
	return append(deny, allow...)
}

func matchArgs(rule proto.FirewallRule, host bool) []string {
	args := []string{"ip", "saddr", rule.Source}
	if rule.Protocol == "icmp" {
		return append(args, "ip", "protocol", "icmp")
	}
	if rule.Protocol == "any" {
		if host && rule.Port == 0 {
			return args
		}
		if rule.Port == 0 {
			return args
		}
		args = append(args, "th", "dport", portExpr(rule))
		return args
	}
	args = append(args, rule.Protocol)
	if rule.Port == 0 {
		if host {
			args = append(args, "dport", clofixPortSet())
		}
		return args
	}
	args = append(args, "dport", portExpr(rule))
	return args
}

func portExpr(rule proto.FirewallRule) string {
	if rule.PortEnd > rule.Port {
		return strconv.Itoa(rule.Port) + "-" + strconv.Itoa(rule.PortEnd)
	}
	return strconv.Itoa(rule.Port)
}

func clofixPortSet() string {
	parts := make([]string, 0, len(proto.CloFixHostPortRanges))
	for _, r := range proto.CloFixHostPortRanges {
		parts = append(parts, strconv.Itoa(r[0])+"-"+strconv.Itoa(r[1]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func clofixPortArgs(protocol string) []string {
	return []string{protocol, "dport", clofixPortSet()}
}

func verdict(action string) string {
	if action == "deny" {
		return "drop"
	}
	return "accept"
}

func nftAdd(chain string, args []string, comment string) []string {
	out := []string{"nft", "add", "rule", "inet", firewallTable, chain}
	out = append(out, args...)
	out = append(out, "comment", comment)
	return out
}

func removeTaggedRules(chain, comment string) {
	out, err := command("nft", "-a", "list", "chain", "inet", firewallTable, chain)
	if err != nil {
		return
	}
	for _, handle := range taggedHandles(string(out), comment) {
		command("nft", "delete", "rule", "inet", firewallTable, chain, "handle", handle)
	}
}

func taggedHandles(listed, comment string) []string {
	var handles []string
	for _, line := range strings.Split(listed, "\n") {
		if !strings.Contains(line, comment) {
			continue
		}
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "handle" {
				handles = append(handles, fields[i+1])
				break
			}
		}
	}
	return handles
}
