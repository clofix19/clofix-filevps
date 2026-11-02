package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/internal/engine/lifecycle"
	"github.com/clofix/filevps/worker/pkg/proto"
)

func fxUsage() {
	fmt.Fprintln(os.Stderr, `usage: clofix-worker fx <command>
  create <id>   create a local FileVPS instance
  start <id>    start a created instance
  stop <id>     stop a running instance
  exec <id> -- <cmd...>   run a command inside a running instance
  backup <id> <backup-id>   snapshot a stopped instance writable layer
  delete <id>   stop and delete a local instance

create flags:
  --image --version --ram-mb --cpu --disk-mb --pids --swap-mb
  --ssh-key PATH   required public key
  --hostname NAME
  --backup-id ID   restore this backup into the new instance
  --network / --no-network
  --protection / --no-protection`)
}

func runFX(args []string) error {
	if len(args) < 1 {
		fxUsage()
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	eng := lifecycle.New(cfg)
	if err := eng.Load(); err != nil {
		return err
	}
	switch args[0] {
	case "create":
		return fxCreate(eng, args[1:])
	case "start":
		return fxNeedID(eng.Start, args[1:])
	case "stop":
		return fxNeedID(eng.Stop, args[1:])
	case "backup":
		return fxBackup(eng, args[1:])
	case "exec":
		return fxExec(eng, args[1:])
	case "delete", "destroy":
		return fxNeedID(eng.Delete, args[1:])
	case "help", "-h", "--help":
		fxUsage()
		return nil
	default:
		fxUsage()
		os.Exit(2)
		return nil
	}
}

func fxNeedID(fn func(string) error, args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("instance id required")
	}
	return fn(args[0])
}

func fxBackup(eng *lifecycle.Engine, args []string) error {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return fmt.Errorf("usage: fx backup <instance-id> <backup-id>")
	}
	return eng.CreateBackup(args[0], args[1], "local")
}

func fxExec(eng *lifecycle.Engine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("instance id required")
	}
	id := args[0]
	cmd := args[1:]
	if len(cmd) > 0 && cmd[0] == "--" {
		cmd = cmd[1:]
	}
	return eng.Exec(id, cmd, os.Stdout, os.Stderr)
}

func fxCreate(eng *lifecycle.Engine, args []string) error {
	fs := flag.NewFlagSet("fx create", flag.ContinueOnError)
	image := fs.String("image", "ubuntu", "image name")
	version := fs.String("version", "24.04", "image version")
	ram := fs.Int64("ram-mb", 256, "memory limit in MiB")
	cpu := fs.Float64("cpu", 0.5, "CPU quota in cores")
	disk := fs.Int64("disk-mb", 2048, "disk accounting in MiB")
	pids := fs.Int64("pids", 256, "process limit")
	swap := fs.Int64("swap-mb", 0, "swap limit in MiB")
	sshKeyPath := fs.String("ssh-key", "", "path to an SSH public key")
	hostname := fs.String("hostname", "", "guest hostname")
	backupID := fs.String("backup-id", "", "restore this backup into the new instance")
	if err := fs.Parse(stripCreateSwitches(reorderCreateArgs(args))); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("instance id required")
	}
	if *sshKeyPath == "" {
		return fmt.Errorf("--ssh-key is required")
	}
	key, err := os.ReadFile(*sshKeyPath)
	if err != nil {
		return fmt.Errorf("read ssh key: %w", err)
	}
	id := fs.Arg(0)
	protect := createSwitch(args, "protection", false)
	useNet := createSwitch(args, "network", true)
	spec := proto.InstanceSpec{
		Image:            proto.ImageRef{Name: *image, Version: *version},
		RAMMB:            *ram,
		CPU:              *cpu,
		DiskMB:           *disk,
		Pids:             *pids,
		Hostname:         *hostname,
		SSHAuthorizedKey: strings.TrimSpace(string(key)),
		ProtectionMode:   &protect,
		BackupID:         *backupID,
	}
	swapMB := *swap
	spec.SwapMB = &swapMB
	if spec.Hostname == "" {
		spec.Hostname = id
	}
	if useNet {
		ip, port, err := allocateFXNetwork(eng)
		if err != nil {
			return err
		}
		spec.IPAddress, spec.SSHPort = ip, port
	}
	return eng.Create(id, spec)
}

// reorderCreateArgs moves the instance id after flags so flag.Parse sees --ssh-key
// when callers use the documented form: fx create <id> --ssh-key PATH ...
func reorderCreateArgs(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				switch a {
				case "--network", "--no-network", "--protection", "--no-protection":
				default:
					i++
					flags = append(flags, args[i])
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func stripCreateSwitches(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--network", "--no-network", "--protection", "--no-protection":
			continue
		default:
			out = append(out, a)
		}
	}
	return out
}

func createSwitch(args []string, name string, def bool) bool {
	on, off := "--"+name, "--no-"+name
	v := def
	for _, a := range args {
		if a == on {
			v = true
		}
		if a == off {
			v = false
		}
	}
	return v
}

func allocateFXNetwork(eng *lifecycle.Engine) (string, int, error) {
	usedIP, usedPort := eng.UsedNetwork()
	mergeLiveNetwork(usedIP, usedPort)
	var ip string
	for third := 0; third < proto.ManagedNetThirdBase && ip == ""; third++ {
		for host := 2; host <= 254; host++ {
			cand := fmt.Sprintf("10.231.%d.%d", third, host)
			if !usedIP[cand] {
				ip = cand
				break
			}
		}
	}
	if ip == "" {
		return "", 0, fmt.Errorf("no free FileVPS address in 10.231.0.0/16 below managed-app space")
	}
	for port := proto.HostSSHBase; port <= proto.HostSSHEnd(); port++ {
		if !usedPort[port] {
			return ip, port, nil
		}
	}
	return "", 0, fmt.Errorf("no free SSH host port in %d-%d", proto.HostSSHBase, proto.HostSSHEnd())
}

// mergeLiveNetwork marks IPs/ports already used by the compose agent's state
// so an isolated CLOFIX_STATE_DIR does not collide with live VPS.
func mergeLiveNetwork(ips map[string]bool, ports map[int]bool) {
	live := "/var/lib/clofix-filevps"
	if os.Getenv("CLOFIX_STATE_DIR") == "" || os.Getenv("CLOFIX_STATE_DIR") == live {
		return
	}
	ents, err := os.ReadDir(filepath.Join(live, "instances"))
	if err != nil {
		return
	}
	type spec struct {
		IPAddress string `json:"ip_address"`
		SSHPort   int    `json:"ssh_port"`
	}
	type meta struct {
		Spec spec `json:"spec"`
	}
	for _, ent := range ents {
		b, err := os.ReadFile(filepath.Join(live, "instances", ent.Name(), "metadata.json"))
		if err != nil {
			continue
		}
		var m meta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.Spec.IPAddress != "" {
			ips[m.Spec.IPAddress] = true
		}
		if m.Spec.SSHPort > 0 {
			ports[m.Spec.SSHPort] = true
		}
	}
}
