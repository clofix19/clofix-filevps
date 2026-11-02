package proto

import "testing"

func TestCloFixHostPortAndNormalize(t *testing.T) {
	if CloFixHostPort(22) || CloFixHostPort(8080) || CloFixHostPort(8443) {
		t.Fatal("host SSH and admin ports must stay unfiltered")
	}
	if !CloFixHostPort(22022) || !CloFixHostPort(HostCustomBase) || !CloFixHostPort(HostManagedBase+1) || !CloFixHostPort(HostDatabaseBase+50) {
		t.Fatal("CloFix published ports must be allowed")
	}
	host := &FirewallSpec{Rules: []FirewallRule{{Action: "allow", Protocol: "tcp", Port: 22, Source: "0.0.0.0/0"}}}
	if err := NormalizeFirewallSpec(host, true); err == nil {
		t.Fatal("host rule on port 22 must be rejected")
	}
	ok := &FirewallSpec{Restrict: false, Rules: []FirewallRule{{Action: "allow", Protocol: "tcp", Port: 22022, Source: "10.0.0.0/8"}}}
	if err := NormalizeFirewallSpec(ok, true); err != nil {
		t.Fatal(err)
	}
	if ok.Restrict {
		t.Fatal("restrict=false must stay open")
	}
	badCIDR := &FirewallSpec{Rules: []FirewallRule{{Action: "allow", Protocol: "tcp", Port: 22022, Source: "10.0.0.0/../8"}}}
	if err := NormalizeFirewallSpec(badCIDR, true); err == nil {
		t.Fatal("path traversal CIDR must fail")
	}
	vps := &FirewallSpec{InstanceID: "vps-1", Rules: []FirewallRule{{Action: "deny", Protocol: "any", Source: "0.0.0.0/0"}}}
	if err := NormalizeFirewallSpec(vps, false); err != nil {
		t.Fatal(err)
	}
}
