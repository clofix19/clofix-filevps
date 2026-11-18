package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/clofix/clofix-linux/internal/agent"
	"github.com/clofix/clofix-linux/internal/config"
	"github.com/clofix/clofix-linux/internal/events"
	"github.com/clofix/clofix-linux/internal/firewall"
	"github.com/clofix/clofix-linux/internal/policy"
	"github.com/clofix/clofix-linux/internal/process"
	"github.com/clofix/clofix-linux/internal/response"
	"github.com/clofix/clofix-linux/internal/sshguard"
)

const version = "CloFix Linux 1.0.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cfg, _ := config.Load("/etc/clofix/guard/config.json")
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "version":
		printVersion()
	case "info":
		printInfo()
	case "status":
		err = printStatus(cfg)
	case "update":
		err = cmdUpdate(args)
	case "security":
		err = cmdSecurity(cfg, args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`Usage: clofix <command>

  version | info | status
  update | update check
  security status|score|scan|events
  security ssh|firewall|fim|process status
  security mode list|enable <standard|strict|immutable>
  security autopilot status|enable
  security rollback
`)
}

func printVersion() {
	fmt.Println(version)
	fmt.Println("Security Engine: CloFix Guard")
	fmt.Println("Website: https://clofix.com")
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "UBUNTU_CODENAME=") || strings.HasPrefix(line, "ID=") || strings.HasPrefix(line, "VERSION_ID=") {
				fmt.Println(line)
			}
		}
	}
}

func printInfo() {
	printVersion()
	fmt.Println("Kernel:", uname())
	fmt.Println("Arch:", runtimeGOARCH())
}

func printStatus(cfg config.Config) error {
	st, _ := policy.Load(cfg.DataDir)
	fmt.Printf("Mode: %s\n", st.Mode)
	fmt.Printf("AutoPilot: %v\n", st.AutoPilotEnabled)
	if b, err := os.ReadFile(filepath.Join(cfg.DataDir, "score.json")); err == nil {
		fmt.Println("Last score:")
		fmt.Println(string(b))
	} else {
		fmt.Println("Last score: none (run: clofix security scan)")
	}
	return nil
}

func cmdUpdate(args []string) error {
	if len(args) > 0 && args[0] == "check" {
		cmd := exec.Command("apt-get", "-s", "upgrade")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	cmds := [][]string{
		{"apt-get", "update"},
		{"apt-get", "-y", "upgrade"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	fmt.Println("Done. Release upgrades remain disabled by CloFix policy.")
	return nil
}

func cmdSecurity(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("security subcommand required")
	}
	switch args[0] {
	case "status":
		return printStatus(cfg)
	case "score":
		return printJSONFile(filepath.Join(cfg.DataDir, "score.json"))
	case "scan":
		a, err := agent.New(cfg)
		if err != nil {
			return err
		}
		rep, err := a.FullScan()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	case "events":
		st, err := events.NewStore(cfg.DataDir)
		if err != nil {
			return err
		}
		ev, err := st.Tail(50)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ev)
	case "ssh":
		return requireStatus(args, func() error {
			st, err := sshguard.Assess(cfg.SSH.ConfigPath, cfg.SSH.AuthLogPaths, cfg.SSH.FailThreshold, cfg.SSH.FailWindowSeconds, cfg.SSH.PreserveEmergency, cfg.AllowlistIPs)
			if err != nil {
				return err
			}
			return printJSON(st)
		})
	case "firewall":
		return requireStatus(args, func() error {
			return printJSON(firewall.StatusNow(filepath.Join(cfg.DataDir, "firewall")))
		})
	case "fim":
		return requireStatus(args, func() error {
			return printJSONFile(filepath.Join(cfg.DataDir, "last-scan.json"))
		})
	case "process":
		return requireStatus(args, func() error {
			st, err := process.Snapshot(false)
			if err != nil {
				return err
			}
			return printJSON(st)
		})
	case "mode":
		return cmdMode(cfg, args[1:])
	case "autopilot":
		return cmdAutopilot(cfg, args[1:])
	case "rollback":
		return firewall.Rollback(filepath.Join(cfg.DataDir, "firewall"))
	default:
		return fmt.Errorf("unknown security command: %s", args[0])
	}
}

func cmdMode(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("mode list|enable <name>")
	}
	switch args[0] {
	case "list":
		return printJSON(policy.List())
	case "enable":
		if len(args) < 2 {
			return fmt.Errorf("mode enable requires name")
		}
		st, err := policy.Enable(cfg.DataDir, policy.Mode(args[1]))
		if err != nil {
			return err
		}
		return printJSON(st)
	default:
		return fmt.Errorf("unknown mode command")
	}
}

func cmdAutopilot(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("autopilot status|enable")
	}
	switch args[0] {
	case "status":
		eng := response.Engine{DataDir: cfg.DataDir, Cfg: cfg.AutoPilot}
		st, err := eng.Status()
		if err != nil {
			return err
		}
		return printJSON(st)
	case "enable":
		st, err := policy.Load(cfg.DataDir)
		if err != nil {
			return err
		}
		st.AutoPilotEnabled = true
		cfg.AutoPilot.Enabled = true
		if err := policy.Save(cfg.DataDir, st); err != nil {
			return err
		}
		// Persist config flag
		cfg.AutoPilot.Enabled = true
		raw, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.MkdirAll("/etc/clofix/guard", 0o755)
		if err := os.WriteFile("/etc/clofix/guard/config.json", raw, 0o640); err != nil {
			return err
		}
		fmt.Println("AutoPilot enabled for READ_ONLY / policy-gated SAFE_MUTATION only; high-risk still requires approval")
		return nil
	default:
		return fmt.Errorf("unknown autopilot command")
	}
}

func requireStatus(args []string, fn func() error) error {
	if len(args) < 2 || args[1] != "status" {
		return fmt.Errorf("usage: clofix security %s status", args[0])
	}
	return fn()
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printJSONFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no data at %s (run: clofix security scan): %w", path, err)
	}
	fmt.Print(string(b))
	if len(b) == 0 || b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func uname() string {
	out, err := exec.Command("uname", "-sr").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func runtimeGOARCH() string {
	out, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
