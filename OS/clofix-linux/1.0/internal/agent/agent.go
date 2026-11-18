package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/clofix/clofix-linux/internal/config"
	"github.com/clofix/clofix-linux/internal/events"
	"github.com/clofix/clofix-linux/internal/fim"
	"github.com/clofix/clofix-linux/internal/firewall"
	"github.com/clofix/clofix-linux/internal/malware"
	"github.com/clofix/clofix-linux/internal/network"
	"github.com/clofix/clofix-linux/internal/policy"
	"github.com/clofix/clofix-linux/internal/privilege"
	"github.com/clofix/clofix-linux/internal/process"
	"github.com/clofix/clofix-linux/internal/response"
	"github.com/clofix/clofix-linux/internal/rootkit"
	"github.com/clofix/clofix-linux/internal/scoring"
	"github.com/clofix/clofix-linux/internal/sshguard"
	"github.com/clofix/clofix-linux/internal/vulnerability"
)

type Agent struct {
	Cfg    config.Config
	Store  *events.Store
	Policy policy.State
}

func New(cfg config.Config) (*Agent, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	st, err := events.NewStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	ps, _ := policy.Load(cfg.DataDir)
	return &Agent{Cfg: cfg, Store: st, Policy: ps}, nil
}

func (a *Agent) log(source, typ string, sev events.Severity, msg string, data map[string]any) {
	_ = a.Store.Append(events.Event{Source: source, Type: typ, Severity: sev, Message: msg, Data: data})
}

// FullScan runs all guest-safe security modules and persists a report.
func (a *Agent) FullScan() (map[string]any, error) {
	out := map[string]any{"time": time.Now().UTC()}

	sshSt, _ := sshguard.Assess(a.Cfg.SSH.ConfigPath, a.Cfg.SSH.AuthLogPaths, a.Cfg.SSH.FailThreshold, a.Cfg.SSH.FailWindowSeconds, a.Cfg.SSH.PreserveEmergency, a.Cfg.AllowlistIPs)
	out["ssh"] = sshSt
	for _, f := range sshSt.Findings {
		sev := events.Medium
		if f.Severity == "high" {
			sev = events.High
		}
		a.log("ssh", f.Code, sev, f.Message, nil)
	}

	fw := firewall.StatusNow(filepath.Join(a.Cfg.DataDir, "firewall"))
	out["firewall"] = fw

	eng := fim.Engine{Paths: a.Cfg.FIM.Paths, Exclude: a.Cfg.FIM.Exclude, Store: filepath.Join(a.Cfg.DataDir, "fim-baseline.json")}
	files, _ := eng.Scan()
	if _, err := eng.LoadBaseline(); err != nil {
		_ = eng.WriteBaseline(files)
		out["fim"] = map[string]any{"baseline": "created", "files": len(files)}
	} else {
		ch, _ := eng.Diff(files)
		out["fim"] = map[string]any{"changes": ch, "files": len(files)}
		for _, c := range ch {
			a.log("fim", c.Kind, events.Medium, c.Path+": "+c.Detail, nil)
		}
	}

	proc, _ := process.Snapshot(a.Cfg.Process.AllowTerminate && a.Policy.ProcessTerminate)
	out["process"] = proc

	netSt, _ := network.Inventory(a.Cfg.DataDir)
	out["network"] = netSt
	for _, n := range netSt.NewListeners {
		a.log("network", "new_listener", events.Medium, n, nil)
	}

	priv, _ := privilege.Assess()
	out["privilege"] = priv

	mal, _ := malware.ScanPaths([]string{"/tmp", "/var/tmp"}, a.Cfg.Malware.HashDBPath, a.Cfg.Malware.AllowQuarantine && a.Policy.AutoQuarantine, a.Cfg.Malware.QuarantineDir)
	out["malware"] = mal

	out["rootkit"] = rootkit.GuestChecks()

	vuln, _ := vulnerability.Assess(a.Cfg.Vuln.UseOSV)
	out["vulnerability"] = vuln

	// APT codename pin check
	out["apt_codename"] = checkAPTCodename(vuln.UbuntuCodename)

	score := a.computeScore(sshSt, fw, vuln, eng, files)
	out["score"] = score
	_ = writeJSON(filepath.Join(a.Cfg.DataDir, "last-scan.json"), out)
	_ = writeJSON(filepath.Join(a.Cfg.DataDir, "score.json"), score)
	return out, nil
}

func (a *Agent) computeScore(ssh sshguard.Status, fw firewall.Status, vuln vulnerability.Report, eng fim.Engine, files map[string]fim.FileMeta) scoring.Result {
	checks := []scoring.Check{}
	sshStatus := "pass"
	if len(ssh.Findings) > 0 {
		sshStatus = "fail"
	}
	if !ssh.ConfigOK {
		sshStatus = "unknown"
	}
	checks = append(checks, scoring.Check{ID: "ssh", Category: "ssh", Status: sshStatus, Weight: 15, Detail: fmt.Sprintf("%d findings", len(ssh.Findings)), Fix: "clofix security ssh status"})

	fwStatus := "unknown"
	if fw.Available && fw.Capable {
		if fw.Applied {
			fwStatus = "pass"
		} else {
			fwStatus = "fail"
		}
	}
	checks = append(checks, scoring.Check{ID: "firewall", Category: "firewall", Status: fwStatus, Weight: 10, Detail: fw.SkippedReason, Fix: "Install nftables on capable hosts; do not force from unprivileged VPS"})

	patchStatus := "pass"
	if vuln.Upgradeable > 0 {
		patchStatus = "fail"
	}
	checks = append(checks, scoring.Check{ID: "patch", Category: "patch", Status: patchStatus, Weight: 15, Detail: fmt.Sprintf("%d upgradeable", vuln.Upgradeable), Fix: "clofix update"})

	vulnStatus := "pass"
	if len(vuln.Findings) > 0 {
		vulnStatus = "fail"
	}
	checks = append(checks, scoring.Check{ID: "vuln", Category: "vuln", Status: vulnStatus, Weight: 10, Detail: fmt.Sprintf("%d findings", len(vuln.Findings))})

	checks = append(checks, scoring.Check{ID: "access", Category: "access", Status: "pass", Weight: 10, Detail: "privilege assess ran"})

	fimStatus := "unknown"
	if _, err := eng.LoadBaseline(); err == nil {
		fimStatus = "pass"
		if ch, err := eng.Diff(files); err == nil && len(ch) > 0 {
			fimStatus = "fail"
		}
	}
	checks = append(checks, scoring.Check{ID: "fim", Category: "fim", Status: fimStatus, Weight: 10, Detail: "baseline presence"})

	checks = append(checks, scoring.Check{ID: "audit", Category: "audit", Status: "pass", Weight: 5, Detail: "clofix event store"})
	checks = append(checks, scoring.Check{ID: "confinement", Category: "confinement", Status: "unknown", Weight: 10, Detail: "AppArmor/seccomp require host or packages"})
	checks = append(checks, scoring.Check{ID: "isolation", Category: "isolation", Status: "unknown", Weight: 10, Detail: "shared-kernel isolation is host-worker scope"})
	checks = append(checks, scoring.Check{ID: "backup", Category: "backup", Status: "unknown", Weight: 5, Detail: "backup readiness not verified"})

	return scoring.Compute(checks)
}

func (a *Agent) Run(ctx context.Context) error {
	a.log("agent", "start", events.Info, "clofix-guard starting", map[string]any{"mode": a.Policy.Mode})
	ticker := time.NewTicker(a.Cfg.Interval(5*time.Minute, a.Cfg.Intervals.ScoreSeconds))
	defer ticker.Stop()
	_, _ = a.FullScan()
	for {
		select {
		case <-ctx.Done():
			a.log("agent", "stop", events.Info, "graceful shutdown", nil)
			return nil
		case <-ticker.C:
			_, _ = a.FullScan()
		}
	}
}

func (a *Agent) AutoPilotStatus() (map[string]any, error) {
	eng := response.Engine{DataDir: a.Cfg.DataDir, Cfg: a.Cfg.AutoPilot}
	return eng.Status()
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o640)
}

func checkAPTCodename(expect string) map[string]any {
	out := map[string]any{"expected": expect, "ok": true, "detail": "no foreign release suites detected in quick scan"}
	paths := []string{
		"/etc/apt/sources.list",
		"/etc/apt/sources.list.d",
	}
	foreign := []string{}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			ents, _ := os.ReadDir(p)
			for _, e := range ents {
				b, err := os.ReadFile(filepath.Join(p, e.Name()))
				if err != nil {
					continue
				}
				foreign = append(foreign, findForeignSuites(string(b), expect)...)
			}
		} else {
			b, _ := os.ReadFile(p)
			foreign = append(foreign, findForeignSuites(string(b), expect)...)
		}
	}
	if len(foreign) > 0 {
		out["ok"] = false
		out["detail"] = foreign
	}
	return out
}

func findForeignSuites(content, expect string) []string {
	var bad []string
	suites := []string{"jammy", "focal", "bionic", "xenial", "plucky", "oracular", "mantic", "lunar", "kinetic", "devel"}
	for _, s := range suites {
		if s == expect {
			continue
		}
		if strings.Contains(content, "/"+s) || strings.Contains(content, " "+s+" ") || strings.Contains(content, "Suites: "+s) {
			bad = append(bad, s)
		}
	}
	return bad
}
