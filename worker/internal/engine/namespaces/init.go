// Package namespaces holds the in-namespace setup step. The engine re-executes the worker binary as
// "clofix-worker fx-init ..." inside new user/mount/PID/UTS/IPC/net namespaces; RunInit then builds the
// instance's root (private mounts, /proc, /dev, hostname), pivots into it and execs the instance's init.
package namespaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// RunInit args: <rootfs> <hostname> [--protection-mode] -- <init command...>.
// It does not return on success.
func RunInit(args []string) error {
	// Keep namespace setup and seccomp on the same OS thread through exec.
	runtime.LockOSThread()
	if len(args) < 4 {
		return errors.New("usage: fx-init <rootfs> <hostname> [--protection-mode] -- <cmd...>")
	}
	root, hostname := args[0], args[1]
	argStart := 2
	protected := false
	unprivileged := false
	type bindMount struct{ source, target string }
	var binds []bindMount
	for argStart < len(args) && args[argStart] != "--" {
		switch args[argStart] {
		case "--unprivileged":
			unprivileged = true
			argStart++
		case "--protection-mode":
			protected = true
			argStart++
		case "--bind-mount":
			if argStart+2 >= len(args) {
				return errors.New("--bind-mount requires source and guest target")
			}
			binds = append(binds, bindMount{source: args[argStart+1], target: args[argStart+2]})
			argStart += 3
		default:
			return errors.New("unknown fx-init option: " + args[argStart])
		}
	}
	if argStart >= len(args) || args[argStart] != "--" {
		return errors.New("usage: fx-init <rootfs> <hostname> [--protection-mode] -- <cmd...>")
	}
	argv := args[argStart+1:]

	// keep every mount we make private to this namespace
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}
	// pivot_root needs the new root to be a mount point
	if err := syscall.Mount(root, root, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind rootfs: %w", err)
	}
	if err := syscall.Mount("proc", filepath.Join(root, "proc"), "proc", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, ""); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}
	if err := setupDev(root); err != nil {
		return fmt.Errorf("setup /dev: %w", err)
	}
	if protected {
		for _, mount := range []struct{ path, opts string }{
			{"run", "mode=755,size=16m"},
			{"var/log", "mode=755,size=32m"},
			{"var/log/nginx", "mode=755,size=16m"},
			{"tmp", "mode=1777,size=64m"},
			{"var/lib/apt/lists", "mode=755,size=64m"},
			{"var/cache/apt", "mode=755,size=64m"},
			{"var/cache/nginx", "mode=755,size=32m"},
		} {
			path := filepath.Join(root, mount.path)
			if err := os.MkdirAll(path, 0o755); err != nil {
				return fmt.Errorf("prepare writable /%s: %w", mount.path, err)
			}
			if err := syscall.Mount("tmpfs", path, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, mount.opts); err != nil {
				return fmt.Errorf("mount volatile /%s: %w", mount.path, err)
			}
		}
	}
	for _, bind := range binds {
		if !validBindTarget(bind.target) {
			return fmt.Errorf("invalid bind mount target %q", bind.target)
		}
		dest := filepath.Join(root, strings.TrimPrefix(bind.target, "/"))
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("prepare bind mount target %s: %w", bind.target, err)
		}
		if err := syscall.Mount(bind.source, dest, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("bind mount %s to %s: %w", bind.source, bind.target, err)
		}
	}
	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("sethostname: %w", err)
	}

	old := filepath.Join(root, ".oldroot")
	if err := os.MkdirAll(old, 0o700); err != nil {
		return err
	}
	if err := syscall.PivotRoot(root, old); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}
	if err := syscall.Unmount("/.oldroot", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old root: %w", err)
	}
	os.Remove("/.oldroot")
	var appEnv map[string]string
	if unprivileged {
		data, err := os.ReadFile("/app/.clofix-env.json")
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &appEnv); err != nil {
			return err
		}
		if err := os.Remove("/app/.clofix-env.json"); err != nil {
			return err
		}
	}

	if protected {
		if err := syscall.Mount("", "/", "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
			return fmt.Errorf("remount guest root read-only: %w", err)
		}
	}
	if unprivileged {
		if err := syscall.Setgroups([]int{}); err != nil {
			return err
		}
		if err := syscall.Setgid(65534); err != nil {
			return err
		}
		if err := syscall.Setuid(65534); err != nil {
			return err
		}
		if err := os.Chdir("/tmp"); err != nil {
			return err
		}
	}
	if err := installSeccomp(); err != nil {
		return fmt.Errorf("install seccomp: %w", err)
	}

	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "TERM=xterm", "container=clofix-fx"}
	if unprivileged {
		env[1] = "HOME=/tmp"
		for k, v := range appEnv {
			replaced := false
			for i, value := range env {
				if strings.HasPrefix(value, k+"=") {
					env[i] = k + "=" + v
					replaced = true
					break
				}
			}
			if !replaced {
				env = append(env, k+"="+v)
			}
		}
	}
	if len(argv) == 0 || argv[0] == "" {
		argv = []string{"/sbin/init"}
		if _, err := os.Stat("/sbin/init"); err != nil {
			argv = []string{"/bin/sh"}
		}
	}
	path := argv[0]
	if filepath.Base(path) == path {
		p, err := exec.LookPath(path)
		if err != nil {
			return err
		}
		path = p
	}
	return syscall.Exec(path, argv, env)
}

func validBindTarget(p string) bool {
	if !filepath.IsAbs(p) || p == "/" || filepath.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, root := range []string{"/proc", "/sys", "/dev", "/run", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/root", "/var/lib", "/var/log", "/var/cache", "/var/run"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return false
		}
	}
	return true
}

func setupDev(root string) error {
	dev := filepath.Join(root, "dev")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		return err
	}
	if err := syscall.Mount("tmpfs", dev, "tmpfs", syscall.MS_NOSUID|syscall.MS_STRICTATIME, "mode=755,size=1m"); err != nil {
		return err
	}
	// device nodes cannot be created inside a user namespace: bind the harmless host ones
	for _, n := range []string{"null", "zero", "full", "random", "urandom", "tty"} {
		t := filepath.Join(dev, n)
		f, err := os.OpenFile(t, os.O_CREATE|os.O_RDONLY, 0o666)
		if err != nil {
			return err
		}
		f.Close()
		if err := syscall.Mount("/dev/"+n, t, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind /dev/%s: %w", n, err)
		}
	}
	// pts and shm are best effort
	for _, d := range []string{"pts", "shm"} {
		os.MkdirAll(filepath.Join(dev, d), 0o755)
	}
	if err := syscall.Mount("devpts", filepath.Join(dev, "pts"), "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=620"); err == nil {
		os.Symlink("pts/ptmx", filepath.Join(dev, "ptmx"))
	}
	syscall.Mount("tmpfs", filepath.Join(dev, "shm"), "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=1777,size=64m")
	os.Symlink("/proc/self/fd", filepath.Join(dev, "fd"))
	os.Symlink("/proc/self/fd/0", filepath.Join(dev, "stdin"))
	os.Symlink("/proc/self/fd/1", filepath.Join(dev, "stdout"))
	os.Symlink("/proc/self/fd/2", filepath.Join(dev, "stderr"))
	return nil
}
