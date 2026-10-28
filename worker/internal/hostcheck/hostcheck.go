// Package hostcheck validates required kernel features at worker startup (spec section 8).
package hostcheck

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Result struct {
	Name     string
	OK       bool
	Required bool
	Detail   string
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func fileContains(p, sub string) bool {
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), sub)
}

func Run() []Result {
	var rs []Result
	add := func(name string, ok, req bool, detail string) {
		rs = append(rs, Result{name, ok, req, detail})
	}
	add("root privileges", os.Geteuid() == 0, true, fmt.Sprintf("euid=%d", os.Geteuid()))
	for _, ns := range []string{"user", "mnt", "pid", "net", "ipc", "uts"} {
		add("namespace "+ns, exists("/proc/self/ns/"+ns), true, "/proc/self/ns/"+ns)
	}
	add("cgroup v2", exists("/sys/fs/cgroup/cgroup.controllers"), true, "/sys/fs/cgroup/cgroup.controllers")
	for _, c := range []string{"cpu", "memory", "pids"} {
		add("cgroup controller "+c, fileContains("/sys/fs/cgroup/cgroup.controllers", c), true, "")
	}
	add("overlayfs", fileContains("/proc/filesystems", "overlay"), true, "/proc/filesystems")
	add("PSI", exists("/proc/pressure/memory"), true, "/proc/pressure/memory")
	_, nftErr := exec.LookPath("nft")
	add("nftables", nftErr == nil, false, "nft binary")
	add("seccomp", fileContains("/proc/self/status", "Seccomp:"), false, "")
	add("AppArmor", exists("/sys/module/apparmor"), false, "")
	add("zram", exists("/sys/block/zram0") || exists("/sys/module/zram"), false, "optional")
	return rs
}

// Failed returns required checks that did not pass.
func Failed(rs []Result) []Result {
	var f []Result
	for _, r := range rs {
		if r.Required && !r.OK {
			f = append(f, r)
		}
	}
	return f
}
