//go:build linux

package managed

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const ManagedDiskBytes = uint64(2 << 30)

func (m *Manager) ensureData(id string) error {
	root := m.path(id)
	data := filepath.Join(root, "data")
	image := filepath.Join(root, "data.img")
	if err := os.MkdirAll(data, 0755); err != nil {
		return err
	}
	if _, err := os.Stat(image); os.IsNotExist(err) {
		file, err := os.OpenFile(image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = file.Truncate(int64(ManagedDiskBytes))
		file.Close()
		if err != nil {
			return err
		}
		if out, err := exec.Command("mkfs.ext4", "-q", "-F", image).CombinedOutput(); err != nil {
			_ = os.Remove(image)
			return fmt.Errorf("format managed storage: %s: %w", out, err)
		}
	}
	if !mountedData(data) {
		if out, err := exec.Command("mount", "-t", "ext4", "-o", "loop,nosuid,nodev", image, data).CombinedOutput(); err != nil {
			return fmt.Errorf("mount managed storage: %s: %w", out, err)
		}
	}
	if err := os.Chown(data, 65534, 65534); err != nil {
		return err
	}
	return os.Chmod(data, 0755)
}
func mountedData(path string) bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 4 && f[4] == path {
			return true
		}
	}
	return false
}
