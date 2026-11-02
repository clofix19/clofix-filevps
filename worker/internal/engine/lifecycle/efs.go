package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func efsHostPath(stateDir, id string) string {
	return filepath.Join(stateDir, "efs", id, "mount")
}

func efsBindArgs(stateDir string, mounts []proto.StorageMount) []string {
	var args []string
	for _, mount := range mounts {
		if mount.ID == "" || mount.GuestPath == "" {
			continue
		}
		args = append(args, "--bind-mount", efsHostPath(stateDir, mount.ID), mount.GuestPath)
	}
	return args
}

func (e *Engine) efsDir(id string) string {
	return filepath.Join(e.StateDir, "efs", id)
}

func (e *Engine) efsCredPath(id string) string {
	return filepath.Join(e.efsDir(id), "creds.json")
}

func (e *Engine) ApplyEFSShares(shares []proto.EFSShare) {
	wanted := map[string]bool{}
	for _, share := range shares {
		if err := e.saveEFSCreds(share); err != nil {
			log.Printf("efs creds %s: %v", share.ID, err)
			continue
		}
		if err := e.ensureEFSHost(share.ID); err != nil {
			log.Printf("efs mount %s: %v", share.ID, err)
			continue
		}
		wanted[share.ID] = true
	}
	e.mu.Lock()
	for _, m := range e.meta {
		for _, mount := range m.Spec.EFSMounts {
			wanted[mount.ID] = true
		}
	}
	e.mu.Unlock()
	e.efsMu.Lock()
	ids := make([]string, 0, len(e.efsMounts))
	for id := range e.efsMounts {
		ids = append(ids, id)
	}
	e.efsMu.Unlock()
	for _, id := range ids {
		if !wanted[id] {
			e.stopEFSHost(id)
		}
	}
}

func (e *Engine) UpdateEFS(id string, share proto.EFSShare) error {
	if !validID.MatchString(share.ID) {
		return errors.New("invalid EFS share id")
	}
	if id == "" {
		return e.saveEFSCreds(share)
	}
	m, err := e.get(id)
	if err != nil {
		if share.Detach {
			return nil
		}
		return err
	}
	if share.Detach {
		m.Spec.EFSMounts = removeStorageMount(m.Spec.EFSMounts, share.ID)
		if err := e.save(m); err != nil {
			return err
		}
		if e.CG.Alive(id) {
			if err := e.Restart(id); err != nil {
				return err
			}
		}
		if !e.localEFSUsers(share.ID) {
			e.stopEFSHost(share.ID)
		}
		return nil
	}
	if !validStorageGuestPath(share.GuestPath) {
		return errors.New("invalid EFS guest path")
	}
	if pathsOverlap(share.GuestPath, m.Spec.S3MountPath) {
		return errors.New("EFS path overlaps the S3 mount")
	}
	for _, mount := range m.Spec.StorageMounts {
		if pathsOverlap(share.GuestPath, mount.GuestPath) {
			return errors.New("EFS path overlaps a volume mount")
		}
	}
	for _, mount := range m.Spec.EFSMounts {
		if mount.ID != share.ID && pathsOverlap(share.GuestPath, mount.GuestPath) {
			return errors.New("EFS path overlaps another EFS mount")
		}
	}
	if err := e.saveEFSCreds(share); err != nil {
		return err
	}
	m.Spec.EFSMounts = upsertStorageMount(m.Spec.EFSMounts, proto.StorageMount{ID: share.ID, GuestPath: share.GuestPath})
	if err := e.save(m); err != nil {
		return err
	}
	if e.CG.Alive(id) {
		if err := e.ensureEFSHost(share.ID); err != nil {
			return err
		}
		return e.Restart(id)
	}
	return nil
}

func (e *Engine) saveEFSCreds(share proto.EFSShare) error {
	if !validID.MatchString(share.ID) || share.Endpoint == "" || share.Bucket == "" || share.AccessKey == "" || share.SecretKey == "" {
		return errors.New("invalid EFS share credentials")
	}
	if err := os.MkdirAll(e.efsDir(share.ID), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(proto.EFSShare{
		ID: share.ID, Endpoint: share.Endpoint, Bucket: share.Bucket, Region: share.Region,
		Prefix: share.Prefix, AccessKey: share.AccessKey, SecretKey: share.SecretKey, ConnectionID: share.ConnectionID,
	})
	if err != nil {
		return err
	}
	path := e.efsCredPath(share.ID)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (e *Engine) loadEFSCreds(id string) (proto.EFSShare, error) {
	var share proto.EFSShare
	raw, err := os.ReadFile(e.efsCredPath(id))
	if err != nil {
		return share, err
	}
	if err := json.Unmarshal(raw, &share); err != nil {
		return share, err
	}
	share.ID = id
	return share, nil
}

func (e *Engine) ensureEFSHost(id string) error {
	path := efsHostPath(e.StateDir, id)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if isMounted(path) {
		return nil
	}
	share, err := e.loadEFSCreds(id)
	if err != nil {
		return fmt.Errorf("EFS credentials missing: %w", err)
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		return fmt.Errorf("FUSE device /dev/fuse is unavailable on this worker: %w", err)
	}
	passFile := filepath.Join(e.efsDir(id), "s3fs.passwd")
	if err := os.WriteFile(passFile, []byte(share.AccessKey+":"+share.SecretKey+"\n"), 0o600); err != nil {
		return err
	}
	owner := 0
	if e.Cfg.UserNS {
		owner = int(e.Cfg.UIDBase)
	}
	region := share.Region
	if region == "" {
		region = "us-east-1"
	}
	logf, err := os.OpenFile(filepath.Join(e.LogDir, "efs-"+id+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	args := []string{share.Bucket, path, "-f", "-o", "passwd_file=" + passFile, "-o", "url=" + share.Endpoint,
		"-o", "endpoint=" + region, "-o", "use_path_request_style", "-o", "allow_other",
		"-o", fmt.Sprintf("uid=%d,gid=%d,mp_umask=0022", owner, owner)}
	if share.Prefix != "" {
		args = append(args, "-o", "prefix="+strings.Trim(share.Prefix, "/")+"/")
	}
	cmd := exec.Command("s3fs", args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("start EFS s3fs: %w", err)
	}
	proc := &s3MountProcess{cmd: cmd, done: make(chan error, 1), mountPath: path}
	e.efsMu.Lock()
	if e.efsMounts == nil {
		e.efsMounts = map[string]*s3MountProcess{}
	}
	e.efsMounts[id] = proc
	e.efsMu.Unlock()
	go func() {
		err := cmd.Wait()
		logf.Close()
		proc.done <- err
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if isMounted(path) {
			return nil
		}
		select {
		case err := <-proc.done:
			e.efsMu.Lock()
			delete(e.efsMounts, id)
			e.efsMu.Unlock()
			if err == nil {
				return errors.New("EFS s3fs exited before mounting")
			}
			return fmt.Errorf("EFS s3fs mount failed: %w", err)
		case <-ticker.C:
		case <-deadline.C:
			_ = cmd.Process.Kill()
			e.efsMu.Lock()
			delete(e.efsMounts, id)
			e.efsMu.Unlock()
			select {
			case <-proc.done:
			case <-time.After(time.Second):
			}
			return errors.New("timed out waiting for EFS mount")
		}
	}
}

func (e *Engine) stopEFSHost(id string) {
	path := efsHostPath(e.StateDir, id)
	e.efsMu.Lock()
	proc := e.efsMounts[id]
	delete(e.efsMounts, id)
	e.efsMu.Unlock()
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

func (e *Engine) localEFSUsers(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.meta {
		for _, mount := range m.Spec.EFSMounts {
			if mount.ID == id {
				return true
			}
		}
	}
	return false
}

func upsertStorageMount(mounts []proto.StorageMount, next proto.StorageMount) []proto.StorageMount {
	out := make([]proto.StorageMount, 0, len(mounts)+1)
	found := false
	for _, mount := range mounts {
		if mount.ID == next.ID {
			out = append(out, next)
			found = true
			continue
		}
		out = append(out, mount)
	}
	if !found {
		out = append(out, next)
	}
	return out
}

func removeStorageMount(mounts []proto.StorageMount, id string) []proto.StorageMount {
	out := make([]proto.StorageMount, 0, len(mounts))
	for _, mount := range mounts {
		if mount.ID != id {
			out = append(out, mount)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
