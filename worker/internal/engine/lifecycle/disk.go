package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/clofix/filevps/worker/pkg/proto"
)

// linuxkit ships 8 loop nodes. Extra mknod entries let losetup -f scale
// past that without changing the per-guest layer.img design.
const maxLoopDevs = 128

func ensureLoopNodes() {
	for i := 0; i < maxLoopDevs; i++ {
		path := "/dev/loop" + strconv.Itoa(i)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		_ = exec.Command("mknod", path, "b", "7", strconv.Itoa(i)).Run()
	}
}

func mountLoopExt4(img, mnt, opts string) error {
	ensureLoopNodes()
	out, err := exec.Command("losetup", "-f", "--show", img).CombinedOutput()
	if err != nil {
		return fmt.Errorf("attach loop device: %s: %w", strings.TrimSpace(string(out)), err)
	}
	dev := strings.TrimSpace(string(out))
	if opts == "" {
		opts = "nosuid,nodev"
	}
	out, err = exec.Command("mount", "-t", "ext4", "-o", opts, dev, mnt).CombinedOutput()
	if err != nil {
		_ = exec.Command("losetup", "-d", dev).Run()
		return fmt.Errorf("mount guest disk image: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

const minGuestDiskMB int64 = 64

func (e *Engine) layerMount(id string) string {
	return filepath.Join(e.idir(id), "layer")
}

func (e *Engine) layerImage(id string) string {
	return filepath.Join(e.idir(id), "layer.img")
}

func (e *Engine) upperDir(id string) string {
	if isMounted(e.layerMount(id)) {
		return filepath.Join(e.layerMount(id), "upper")
	}
	return filepath.Join(e.idir(id), "upper")
}

func (e *Engine) workDir(id string) string {
	if isMounted(e.layerMount(id)) {
		return filepath.Join(e.layerMount(id), "work")
	}
	return filepath.Join(e.idir(id), "work")
}

func (e *Engine) meminfoPath(id string) string {
	return filepath.Join(e.idir(id), "proc-meminfo")
}

// ensureDiskQuota puts overlay upper/work on a loop+ext4 image sized to disk_mb
// so guest df / shows the limit and writes past it return ENOSPC.
// Existing guests keep their current upper until the next start, then migrate.
func (e *Engine) ensureDiskQuota(m *Meta) error {
	if m.Spec.DiskMB < minGuestDiskMB {
		return nil
	}
	img := e.layerImage(m.ID)
	mnt := e.layerMount(m.ID)
	if _, err := os.Stat(img); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(img), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(img, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("create guest disk image: %w", err)
		}
		if err := f.Truncate(m.Spec.DiskMB << 20); err != nil {
			f.Close()
			_ = os.Remove(img)
			return fmt.Errorf("size guest disk image: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(img)
			return err
		}
		out, err := exec.Command("mkfs.ext4", "-F", "-m", "0", img).CombinedOutput()
		if err != nil {
			_ = os.Remove(img)
			return fmt.Errorf("format guest disk image: %s: %w", strings.TrimSpace(string(out)), err)
		}
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(mnt, 0o750); err != nil {
		return err
	}
	if !isMounted(mnt) {
		if err := mountLoopExt4(img, mnt, "nosuid,nodev"); err != nil {
			return err
		}
	}
	for _, name := range []string{"upper", "work"} {
		if err := os.MkdirAll(filepath.Join(mnt, name), 0o755); err != nil {
			return err
		}
	}
	legacy := filepath.Join(e.idir(m.ID), "upper")
	dest := filepath.Join(mnt, "upper")
	if err := migrateLegacyUpper(legacy, dest); err != nil {
		return err
	}
	owner := e.guestOwner(m)
	if err := os.Chown(dest, owner, owner); err != nil {
		return fmt.Errorf("set quota upper ownership: %w", err)
	}
	return e.writeGuestMeminfo(m)
}

func migrateLegacyUpper(legacy, dest string) error {
	st, err := os.Stat(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !st.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(legacy)
	if err != nil || len(entries) == 0 {
		return err
	}
	destEntries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(destEntries) > 0 {
		return nil
	}
	out, err := exec.Command("cp", "-a", legacy+"/.", dest+"/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("migrate upper layer onto quota disk: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return os.Rename(legacy, legacy+".migrated")
}

func (e *Engine) writeGuestMeminfo(m *Meta) error {
	kb := m.Spec.RAMMB << 10
	if kb <= 0 {
		kb = 256 << 10
	}
	swapKB := proto.SwapLimitMB(m.Spec) << 10
	body := fmt.Sprintf("MemTotal:       %d kB\nMemFree:        %d kB\nMemAvailable:   %d kB\nBuffers:              0 kB\nCached:               0 kB\nSwapCached:           0 kB\nSwapTotal:      %d kB\nSwapFree:       %d kB\n", kb, kb, kb, swapKB, swapKB)
	return os.WriteFile(e.meminfoPath(m.ID), []byte(body), 0o644)
}

func backingLoop(mnt string) string {
	b, _ := os.ReadFile("/proc/self/mounts")
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == mnt && strings.HasPrefix(f[0], "/dev/loop") {
			return f[0]
		}
	}
	return ""
}

func (e *Engine) unmountLayer(id string) {
	mnt := e.layerMount(id)
	dev := backingLoop(mnt)
	if isMounted(mnt) {
		if err := exec.Command("umount", mnt).Run(); err != nil {
			_ = exec.Command("umount", "-l", mnt).Run()
		}
	}
	if dev != "" {
		_ = exec.Command("losetup", "-d", dev).Run()
	}
}
