package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

type s3MountProcess struct {
	cmd       *exec.Cmd
	done      chan error
	mountPath string
}

func (e *Engine) s3ConfigPath(id string) string {
	return filepath.Join(e.idir(id), "s3fs-credentials.json")
}
func (e *Engine) s3HostMountPath(id string) string { return filepath.Join(e.idir(id), "s3fs-mount") }

func validGuestMountPath(p string) bool {
	if !strings.HasPrefix(p, "/mnt/") || filepath.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/mnt/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return len(p) <= 256
}

// UpdateS3Mount saves the credential separately from public metadata. Running
// guests restart so the FUSE bind mount is installed in their mount namespace.
func (e *Engine) UpdateS3Mount(id string, mount *proto.S3Mount) error {
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if mount != nil {
		if !validGuestMountPath(mount.GuestPath) || mount.Bucket == "" || mount.AccessKey == "" || mount.SecretKey == "" {
			return errors.New("invalid S3 mount settings")
		}
	}
	running := e.CG.Alive(id)
	if running {
		if err := e.Stop(id); err != nil {
			return fmt.Errorf("stop guest to update S3 mount: %w", err)
		}
	}
	oldPath := m.S3MountPath
	oldSecret, _ := os.ReadFile(e.s3ConfigPath(id))
	if mount == nil {
		m.S3MountPath = ""
		m.Spec.S3MountPath = ""
		if err := os.Remove(e.s3ConfigPath(id)); err != nil && !os.IsNotExist(err) {
			m.S3MountPath = oldPath
			m.Spec.S3MountPath = oldPath
			return err
		}
	} else {
		b, err := json.Marshal(mount)
		if err != nil {
			return err
		}
		if err := os.WriteFile(e.s3ConfigPath(id), b, 0o600); err != nil {
			return fmt.Errorf("save S3 mount credentials: %w", err)
		}
		_ = os.Chmod(e.s3ConfigPath(id), 0o600)
		m.S3MountPath = mount.GuestPath
		m.Spec.S3MountPath = mount.GuestPath
	}
	if err := e.save(m); err != nil {
		m.S3MountPath = oldPath
		m.Spec.S3MountPath = oldPath
		if len(oldSecret) > 0 {
			_ = os.WriteFile(e.s3ConfigPath(id), oldSecret, 0o600)
		} else {
			_ = os.Remove(e.s3ConfigPath(id))
		}
		return fmt.Errorf("save S3 mount metadata: %w", err)
	}
	if running {
		if err := e.Start(id); err != nil {
			return fmt.Errorf("restart guest with S3 mount: %w", err)
		}
	}
	return nil
}

func (e *Engine) startS3Mount(id string, cgroupFD int, logf *os.File) (string, error) {
	m, err := e.get(id)
	if err != nil {
		return "", err
	}
	var cfg proto.S3Mount
	b, err := os.ReadFile(e.s3ConfigPath(id))
	if err != nil {
		return "", fmt.Errorf("S3 mount credentials missing: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return "", fmt.Errorf("read S3 mount credentials: %w", err)
	}
	if cfg.GuestPath != m.S3MountPath || !validGuestMountPath(cfg.GuestPath) {
		return "", errors.New("S3 mount metadata does not match saved credentials")
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		return "", fmt.Errorf("FUSE device /dev/fuse is unavailable on this worker: %w", err)
	}
	path := e.s3HostMountPath(id)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	if isMounted(path) {
		return path, nil
	}
	passFile := filepath.Join(e.idir(id), "s3fs.passwd")
	if err := os.WriteFile(passFile, []byte(cfg.AccessKey+":"+cfg.SecretKey+"\n"), 0o600); err != nil {
		return "", err
	}
	owner := 0
	if e.Cfg.UserNS {
		owner = int(e.Cfg.UIDBase)
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	args := []string{cfg.Bucket, path, "-f", "-o", "passwd_file=" + passFile, "-o", "url=" + cfg.Endpoint,
		"-o", "endpoint=" + region, "-o", "use_path_request_style", "-o", "allow_other",
		"-o", fmt.Sprintf("uid=%d,gid=%d,mp_umask=0022", owner, owner)}
	if cfg.Prefix != "" {
		args = append(args, "-o", "prefix="+strings.Trim(cfg.Prefix, "/")+"/")
	}
	cmd := exec.Command("s3fs", args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{CgroupFD: cgroupFD, UseCgroupFD: true, Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start s3fs: %w", err)
	}
	proc := &s3MountProcess{cmd: cmd, done: make(chan error, 1), mountPath: path}
	e.s3Mu.Lock()
	e.s3Mounts[id] = proc
	e.s3Mu.Unlock()
	go func() { proc.done <- cmd.Wait() }()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if isMounted(path) {
			return path, nil
		}
		select {
		case err := <-proc.done:
			e.s3Mu.Lock()
			delete(e.s3Mounts, id)
			e.s3Mu.Unlock()
			if err == nil {
				return "", errors.New("s3fs exited before mounting bucket")
			}
			return "", fmt.Errorf("s3fs mount failed: %w", err)
		case <-ticker.C:
		case <-deadline.C:
			_ = cmd.Process.Kill()
			e.s3Mu.Lock()
			delete(e.s3Mounts, id)
			e.s3Mu.Unlock()
			select {
			case <-proc.done:
			case <-time.After(time.Second):
			}
			return "", errors.New("timed out waiting for s3fs mount")
		}
	}
}

func (e *Engine) stopS3Mount(id string) {
	path := e.s3HostMountPath(id)
	e.s3Mu.Lock()
	proc := e.s3Mounts[id]
	delete(e.s3Mounts, id)
	e.s3Mu.Unlock()
	if proc != nil && proc.cmd.Process != nil {
		_ = proc.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-proc.done:
		case <-time.After(2 * time.Second):
			_ = proc.cmd.Process.Kill()
			select {
			case <-proc.done:
			case <-time.After(time.Second):
			}
		}
	}
	if isMounted(path) {
		_ = syscall.Unmount(path, syscall.MNT_DETACH)
	}
}
