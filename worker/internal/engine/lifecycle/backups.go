package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/clofix/filevps/worker/pkg/proto"
)

type backupManifest struct {
	Info  proto.BackupInfo `json:"info"`
	Image proto.ImageRef   `json:"image"`
}

func (e *Engine) backupDir(id string) string { return filepath.Join(e.StateDir, "backups", id) }

func (e *Engine) CreateBackup(instanceID, backupID, workerID string) error {
	if !validID.MatchString(instanceID) || !validID.MatchString(backupID) {
		return errors.New("invalid VPS or backup ID")
	}
	m, err := e.get(instanceID)
	if err != nil {
		return err
	}
	if e.CG.Alive(instanceID) {
		return errors.New("stop the VPS before creating a consistent backup")
	}
	dir := e.backupDir(backupID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info := proto.BackupInfo{ID: backupID, InstanceID: instanceID, WorkerID: workerID, Created: time.Now().UTC(), RAMMB: m.Spec.RAMMB, CPU: m.Spec.CPU, SwapMB: proto.SwapLimitMB(m.Spec), DiskMB: m.Spec.DiskMB, Image: m.Spec.Image.Name + ":" + m.Spec.Image.Version, State: "CREATING"}
	manifest := backupManifest{Info: info, Image: proto.ImageRef{Name: m.Spec.Image.Name, Version: m.Spec.Image.Version}}
	writeManifest := func() error {
		b, _ := json.MarshalIndent(manifest, "", "  ")
		return os.WriteFile(filepath.Join(dir, "backup.json"), b, 0o600)
	}
	if err := writeManifest(); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	archive := filepath.Join(dir, "upper.tar.gz")
	cmd := exec.Command("tar", "--xattrs", "--xattrs-include=*", "--acls", "--numeric-owner", "-czf", archive, "-C", filepath.Join(e.idir(instanceID), "upper"), ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		info.State, info.Error = "FAILED", fmt.Sprintf("tar backup failed: %s: %v", string(out), err)
		manifest.Info = info
		_ = writeManifest()
		return errors.New(info.Error)
	}
	st, err := os.Stat(archive)
	if err != nil {
		return err
	}
	info.SizeBytes, info.State = uint64(st.Size()), "READY"
	manifest.Info = info
	return writeManifest()
}

func (e *Engine) DeleteBackup(id string) error {
	if !validID.MatchString(id) {
		return errors.New("invalid backup ID")
	}
	return os.RemoveAll(e.backupDir(id))
}

func (e *Engine) BackupStatuses() []proto.BackupInfo {
	root := filepath.Join(e.StateDir, "backups")
	ents, err := os.ReadDir(root)
	if err != nil {
		return []proto.BackupInfo{}
	}
	out := make([]proto.BackupInfo, 0, len(ents))
	for _, ent := range ents {
		if !ent.IsDir() || !validID.MatchString(ent.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, ent.Name(), "backup.json"))
		if err != nil {
			continue
		}
		var m backupManifest
		if json.Unmarshal(b, &m) == nil {
			out = append(out, m.Info)
		}
	}
	return out
}

func (e *Engine) restoreBackup(id, backupID string) error {
	if !validID.MatchString(backupID) {
		return errors.New("invalid backup ID")
	}
	dir := e.backupDir(backupID)
	b, err := os.ReadFile(filepath.Join(dir, "backup.json"))
	if err != nil {
		return fmt.Errorf("backup not found on this worker: %w", err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return errors.New("backup metadata is invalid")
	}
	if manifest.Info.State != "READY" {
		return errors.New("backup is not ready")
	}
	cmd := exec.Command("tar", "--xattrs", "--xattrs-include=*", "--acls", "--numeric-owner", "--same-owner", "-xzf", filepath.Join(dir, "upper.tar.gz"), "-C", filepath.Join(e.idir(id), "upper"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract backup: %s: %w", string(out), err)
	}
	return nil
}
