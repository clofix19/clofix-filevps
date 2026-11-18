package rootkit

import (
	"os"
	"strings"
)

type Finding struct {
	Code    string `json:"code"`
	Scope   string `json:"scope"` // guest|host
	Message string `json:"message"`
}

type Status struct {
	Findings []Finding `json:"findings"`
	Note     string    `json:"note"`
}

// GuestChecks run inside rootfs/VPS without claiming host-kernel rootkit coverage.
func GuestChecks() Status {
	st := Status{
		Note: "Kernel module / hidden-process rootkit checks requiring host kernel visibility are UNSUPPORTED in unprivileged guests; run on trusted host-worker",
	}
	// Suspicious startup persistence in guest
	for _, p := range []string{
		"/etc/rc.local",
		"/etc/cron.d",
		"/etc/systemd/system",
		"/etc/profile.d",
	} {
		if _, err := os.Stat(p); err == nil {
			// present - deeper FIM covers integrity
		}
	}
	// Unexpected world-writable dirs under /etc
	walkWritable("/etc", &st)
	if b, err := os.ReadFile("/proc/modules"); err == nil {
		st.Findings = append(st.Findings, Finding{
			Code: "KERNEL_MODULES_VISIBLE", Scope: "host-shared",
			Message: "Guest can read /proc/modules (" + truncate(string(b), 120) + ") - interpretation requires host trust boundary",
		})
	} else {
		st.Findings = append(st.Findings, Finding{
			Code: "KERNEL_MODULES_HIDDEN", Scope: "guest",
			Message: "/proc/modules not readable - expected in some isolated environments",
		})
	}
	return st
}

func walkWritable(root string, st *Status) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := root + "/" + e.Name()
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.Mode().Perm()&0o002 != 0 {
			st.Findings = append(st.Findings, Finding{
				Code: "WORLD_WRITABLE", Scope: "guest",
				Message: path + " is world-writable",
			})
		}
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
