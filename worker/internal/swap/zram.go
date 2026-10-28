// Package swap optionally configures a host ZRAM swap device for workers.
package swap

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// EnsureZRAM enables /dev/zram0 at the requested logical size. A zero size
// leaves host swap configuration untouched. ZRAM is host-wide worker capacity.
func EnsureZRAM(sizeMB int64) error {
	if sizeMB <= 0 {
		return nil
	}
	sys := "/sys/block/zram0"
	if _, err := os.Stat(sys + "/disksize"); err != nil {
		if out, modErr := exec.Command("modprobe", "zram", "num_devices=1").CombinedOutput(); modErr != nil {
			return fmt.Errorf("load zram module: %s: %w", strings.TrimSpace(string(out)), modErr)
		}
		for i := 0; i < 20; i++ {
			if _, err = os.Stat(sys + "/disksize"); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if _, err := os.Stat(sys + "/disksize"); err != nil {
			return fmt.Errorf("zram device did not appear: %w", err)
		}
	}
	device := "/dev/zram0"
	if _, err := os.Stat(device); err != nil {
		return fmt.Errorf("zram device node unavailable: %w", err)
	}
	desired := uint64(sizeMB) << 20
	currentBytes, err := os.ReadFile(sys + "/disksize")
	if err != nil {
		return err
	}
	current, _ := strconv.ParseUint(strings.TrimSpace(string(currentBytes)), 10, 64)
	active, err := activeZRAM()
	if err != nil {
		return err
	}
	if active {
		if current != desired {
			return fmt.Errorf("zram is already active at %d MiB; requested %d MiB", current>>20, sizeMB)
		}
		return nil
	}
	if current > 0 {
		if err := os.WriteFile(sys+"/reset", []byte("1"), 0o644); err != nil {
			return fmt.Errorf("reset inactive zram device: %w", err)
		}
	}
	if err := os.WriteFile(sys+"/disksize", []byte(strconv.FormatUint(desired, 10)), 0o644); err != nil {
		return fmt.Errorf("set zram size: %w", err)
	}
	if out, err := exec.Command("mkswap", "-f", device).CombinedOutput(); err != nil {
		return fmt.Errorf("initialize zram swap: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if out, err := exec.Command("swapon", "--priority", "100", device).CombinedOutput(); err != nil {
		return fmt.Errorf("enable zram swap: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func activeZRAM() (bool, error) {
	b, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return false, err
	}
	return strings.Contains(string(b), "/dev/zram0"), nil
}
