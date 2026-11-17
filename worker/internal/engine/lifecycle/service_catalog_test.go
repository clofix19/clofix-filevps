package lifecycle

import "testing"

func TestServiceCatalogDevOpsPackages(t *testing.T) {
	for _, key := range []string{
		"docker", "podman", "buildah", "skopeo", "kubectl", "helm", "terraform", "ansible",
		"fail2ban", "ufw", "wireguard", "openvpn", "nmap", "tcpdump", "mtr", "iperf3", "clamav",
		"netdata", "node-exporter", "collectd", "rabbitmq",
		"caddy", "varnish", "squid",
		"rclone", "restic", "awscli",
		"jq", "httpie", "htop", "tmux", "vim", "neovim", "zip-tools", "tree", "cmake",
		"ripgrep", "fd", "bat", "ncdu", "sysstat", "dns-tools", "apache-utils", "zsh", "fish",
	} {
		pkgs, ok := serviceCatalogPackages(key)
		if !ok {
			t.Fatalf("missing install catalog entry %s", key)
		}
		if key != "kubectl" && key != "helm" && key != "terraform" && len(pkgs) == 0 {
			t.Fatalf("empty packages for %s", key)
		}
		if _, ok := serviceCatalogUninstallPackages(key); !ok {
			t.Fatalf("missing uninstall catalog entry %s", key)
		}
		if key == "kubectl" || key == "helm" || key == "terraform" {
			if devopsToolSetup(key) == "" || devopsToolCleanup(key) == "" {
				t.Fatalf("missing pinned setup/cleanup for %s", key)
			}
		}
	}
}
