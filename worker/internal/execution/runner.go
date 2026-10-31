//go:build linux

// Package execution runs artifacts using CloFix namespaces, cgroups and seccomp.
package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/internal/engine/cgroups"
	"github.com/clofix/filevps/worker/pkg/proto"
)

const OutputLimit = 128 << 10

type limitedBuffer struct {
	mu        sync.Mutex
	b         bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := OutputLimit - b.b.Len()
	if len(p) > left {
		p = p[:left]
		b.truncated = true
	}
	b.b.Write(p)
	return n, nil
}

func Validate(e proto.Execution) error { return proto.ValidateExecution(e) }

func Run(parent context.Context, e proto.Execution) (proto.ExecutionResult, error) {
	return RunWithProgress(parent, e, nil)
}
func RunWithProgress(parent context.Context, e proto.Execution, progress func(proto.ExecutionProgress)) (proto.ExecutionResult, error) {
	result := proto.ExecutionResult{ExitCode: -1}
	if err := Validate(e); err != nil {
		return result, err
	}
	runtimeDir := RuntimeDirectory()
	lower := filepath.Join(runtimeDir, e.Runtime)
	if _, err := os.Stat(filepath.Join(lower, "usr")); err != nil {
		return result, fmt.Errorf("CloFix %s runtime rootfs is not provisioned", e.Runtime)
	}
	if err := os.MkdirAll(ExecutionDirectory(), 0700); err != nil {
		return result, err
	}
	dir, err := os.MkdirTemp(ExecutionDirectory(), "clofix-run-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "root")
	for _, name := range []string{"root", "upper", "work"} {
		if err = os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			return result, err
		}
	}
	opts := "lowerdir=" + lower + ",upperdir=" + filepath.Join(dir, "upper") + ",workdir=" + filepath.Join(dir, "work")
	if err = syscall.Mount("overlay", root, "overlay", syscall.MS_NODEV|syscall.MS_NOSUID, opts); err != nil {
		return result, fmt.Errorf("runtime overlay: %w", err)
	}
	defer syscall.Unmount(root, syscall.MNT_DETACH)
	if err = os.MkdirAll(filepath.Join(root, "app"), 0755); err != nil {
		return result, err
	}
	if err = ExtractProject(filepath.Join(root, "app"), e); err != nil {
		return result, err
	}
	name := filepath.Base(dir)
	cg := cgroups.New()
	defer func() { cg.Kill(name); _ = cg.Remove(name) }()
	if err = cg.Create(name, cgroups.Limits{MemMax: uint64(e.RAMMB) << 20, CPUQuota: e.CPU, Pids: 64}); err != nil {
		return result, err
	}
	fd, err := cg.Open(name)
	if err != nil {
		return result, err
	}
	defer fd.Close()
	ctx, cancel := context.WithTimeout(parent, time.Duration(e.Timeout)*time.Second)
	defer cancel()
	outputDir := filepath.Join(dir, "output")
	if err = os.Mkdir(outputDir, 0755); err != nil {
		return result, err
	}
	if err = syscall.Mount("tmpfs", outputDir, "tmpfs", syscall.MS_NODEV|syscall.MS_NOSUID|syscall.MS_NOEXEC, "size=4m,mode=1777"); err != nil {
		return result, err
	}
	defer syscall.Unmount(outputDir, syscall.MNT_DETACH)
	env := map[string]string{"PYTHONPATH": "/app:/tmp/deps", "PYTHONUNBUFFERED": "1", "OUTPUT_DIR": "/output", "DENO_DIR": "/tmp/deno"}
	for k, v := range e.Env {
		env[k] = v
	}
	envJSON, _ := json.Marshal(env)
	if err = os.WriteFile(filepath.Join(root, "app", ".clofix-env.json"), envJSON, 0600); err != nil {
		return result, err
	}
	command := proto.ExecutionCommand(e)
	if e.InstallDependencies {
		script := ""
		switch e.Runtime {
		case "python":
			script = "set -eu\n/usr/bin/python3 -m pip install --no-index --only-binary=:all: --find-links=/app/wheels --target=/tmp/deps -r /app/requirements.txt\nexec \"$@\"\n"
		case "nodejs":
			script = "set -eu\nmkdir -p /tmp/project\ncp -R /app/. /tmp/project/\nchmod -R u+rwX /tmp/project\ncd /tmp/project\n/usr/bin/npm ci --offline --ignore-scripts --no-audit --no-fund --cache /tmp/project/.npm\nexec \"$@\"\n"
			for i, arg := range command {
				if strings.HasPrefix(arg, "/app/") {
					command[i] = "/tmp/project/" + strings.TrimPrefix(arg, "/app/")
				}
			}
		default:
			return result, fmt.Errorf("automatic offline dependency installation supports Python and Node.js; vendor other dependencies")
		}
		if err = os.WriteFile(filepath.Join(root, "app", ".clofix-launch"), []byte(script), 0555); err != nil {
			return result, err
		}
		command = append([]string{"/bin/sh", "/app/.clofix-launch"}, command...)
	}
	args := []string{"fx-init", root, "clofix-job", "--protection-mode", "--unprivileged", "--bind-mount", outputDir, "/output", "--"}
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, "/proc/self/exe", args...)
	cmd.Stdin = bytes.NewBufferString(e.Input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET, UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 65536}}, GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 65536}}, GidMappingsEnableSetgroups: true, UseCgroupFD: true, CgroupFD: int(fd.Fd())}
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	done := make(chan struct{})
	finished := make(chan struct{})
	if progress != nil {
		go func() {
			defer close(finished)
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					stdout, t1 := out.snapshot(e.Env)
					stderrText, t2 := stderr.snapshot(e.Env)
					progress(proto.ExecutionProgress{Stdout: stdout, Stderr: stderrText, Truncated: t1 || t2})
				}
			}
		}()
	} else {
		close(finished)
	}
	err = cmd.Run()
	close(done)
	<-finished
	cg.Kill(name)
	for i := 0; i < 100 && cg.Alive(name); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	result.Stdout, _ = out.snapshot(e.Env)
	result.Stderr, _ = stderr.snapshot(e.Env)
	result.Truncated = out.truncated || stderr.truncated
	var errOutput error
	result.OutputZIP, errOutput = CollectOutput(outputDir)
	if errOutput != nil {
		result.OutputError = errOutput.Error()
	}
	result.Cancelled = parent.Err() != nil
	result.TimedOut = ctx.Err() != nil && !result.Cancelled
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if result.Cancelled {
		return result, fmt.Errorf("execution cancelled")
	}
	if result.TimedOut {
		return result, fmt.Errorf("execution timed out after %d seconds", e.Timeout)
	}
	if err != nil {
		return result, fmt.Errorf("execution failed: %w", err)
	}
	return result, nil
}

func (b *limitedBuffer) snapshot(env map[string]string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := b.b.String()
	for _, v := range env {
		if len(v) > 0 {
			text = strings.ReplaceAll(text, v, "[REDACTED]")
		}
	}
	return text, b.truncated
}
