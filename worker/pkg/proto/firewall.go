package proto

import (
	"fmt"
	"net"
	"strings"
)

var CloFixHostPortRanges = [][2]int{
	{HostSSHBase, HostSSHEnd()},
	{HostSSHBase + HostHTTPOffset, HostSSHEnd() + HostHTTPOffset},
	{HostSSHBase + HostHTTPSOffset, HostSSHEnd() + HostHTTPSOffset},
	{HostCustomBase, HostCustomEnd()},
	{HostManagedBase, HostManagedEnd()},
	{HostDatabaseBase, HostDatabaseEnd()},
}

func CloFixHostPort(port int) bool {
	for _, r := range CloFixHostPortRanges {
		if port >= r[0] && port <= r[1] {
			return true
		}
	}
	return false
}

func NormalizeFirewallSpec(spec *FirewallSpec, host bool) error {
	if spec == nil {
		return fmt.Errorf("firewall spec required")
	}
	spec.Scope = strings.ToLower(strings.TrimSpace(spec.Scope))
	if host {
		spec.Scope = "host"
		spec.InstanceID, spec.GuestIP = "", ""
	} else {
		spec.Scope = "vps"
	}
	if len(spec.Rules) > 50 {
		return fmt.Errorf("maximum 50 rules")
	}
	out := make([]FirewallRule, 0, len(spec.Rules))
	for _, rule := range spec.Rules {
		normalized, err := normalizeFirewallRule(rule, host)
		if err != nil {
			return err
		}
		out = append(out, normalized)
	}
	spec.Rules = out
	return nil
}

func normalizeFirewallRule(rule FirewallRule, host bool) (FirewallRule, error) {
	rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
	rule.Protocol = strings.ToLower(strings.TrimSpace(rule.Protocol))
	rule.Source = strings.TrimSpace(rule.Source)
	if rule.Action != "allow" && rule.Action != "deny" {
		return rule, fmt.Errorf("action must be allow or deny")
	}
	if rule.Protocol != "tcp" && rule.Protocol != "udp" && rule.Protocol != "icmp" && rule.Protocol != "any" {
		return rule, fmt.Errorf("protocol must be tcp, udp, icmp or any")
	}
	if !validIPv4CIDR(rule.Source) {
		return rule, fmt.Errorf("source must be an IPv4 CIDR")
	}
	if rule.Protocol == "icmp" {
		rule.Port, rule.PortEnd = 0, 0
		return rule, nil
	}
	if rule.Port == 0 && rule.PortEnd == 0 {
		return rule, nil
	}
	if rule.PortEnd == 0 {
		rule.PortEnd = rule.Port
	}
	if rule.Port < 1 || rule.Port > 65535 || rule.PortEnd < rule.Port || rule.PortEnd > 65535 {
		return rule, fmt.Errorf("invalid port range")
	}
	if host {
		for p := rule.Port; p <= rule.PortEnd; p++ {
			if !CloFixHostPort(p) {
				return rule, fmt.Errorf("host firewall ports must stay in CloFix published ranges")
			}
		}
	}
	return rule, nil
}

func validIPv4CIDR(value string) bool {
	ip, network, err := net.ParseCIDR(value)
	if err != nil || ip == nil || ip.To4() == nil || network == nil {
		return false
	}
	return network.IP.Equal(ip.To4()) || network.Contains(ip)
}
