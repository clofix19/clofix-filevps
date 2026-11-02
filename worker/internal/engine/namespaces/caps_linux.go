//go:build linux

package namespaces

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	linuxCapVersion3     = 0x20080522
	prCapBsetDrop        = 24
	prCapAmbient         = 47
	prCapAmbientClearAll = 4
	capChown             = 0
	capDACOverride       = 1
	capDACReadSearch     = 2
	capFowner            = 3
	capFsetid            = 4
	capKill              = 5
	capSetgid            = 6
	capSetuid            = 7
	capSetpcap           = 8
	capNetBindService    = 10
	capSysChroot         = 18
	capAuditWrite        = 29
)

type capHeader struct {
	version uint32
	pid     int32
}

type capData struct {
	effective   uint32
	permitted   uint32
	inheritable uint32
}

// guestCaps is the allowlist needed for sshd and apt. Dangerous caps (SYS_MODULE,
// SYS_RAWIO, SYS_PTRACE, SYS_ADMIN, SYS_BOOT, SYS_TIME, …) are omitted.
var guestCaps = []int{
	capChown, capDACOverride, capDACReadSearch, capFowner, capFsetid,
	capKill, capSetgid, capSetuid, capSetpcap, capNetBindService,
	capSysChroot, capAuditWrite,
}

func dropCaps() error {
	allowed := map[int]bool{}
	for _, c := range guestCaps {
		allowed[c] = true
	}
	last := lastCap()
	for cap := 0; cap <= last; cap++ {
		if allowed[cap] {
			continue
		}
		_, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prCapBsetDrop, uintptr(cap), 0)
		if errno != 0 && errno != syscall.EINVAL && errno != syscall.EPERM {
			return fmt.Errorf("drop bounding cap %d: %w", cap, errno)
		}
	}
	syscall.RawSyscall(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientClearAll, 0)

	var data [2]capData
	// After --unprivileged setuid(nobody), permitted caps are already gone.
	// Raising the FileVPS sshd allowlist then fails with EPERM and kills
	// managed apps before network setup. Keep the allowlist for ns-root only.
	if os.Geteuid() == 0 {
		for _, cap := range guestCaps {
			if cap < 32 {
				data[0].effective |= 1 << cap
				data[0].permitted |= 1 << cap
			} else if cap < 64 {
				data[1].effective |= 1 << (cap - 32)
				data[1].permitted |= 1 << (cap - 32)
			}
		}
	}
	hdr := capHeader{version: linuxCapVersion3, pid: 0}
	_, _, errno := syscall.RawSyscall(syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0)
	if errno != 0 {
		if os.Geteuid() != 0 && errno == syscall.EPERM {
			return nil
		}
		return fmt.Errorf("capset: %w", errno)
	}
	return nil
}

func lastCap() int {
	b, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return 40
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 || n > 63 {
		return 40
	}
	return n
}
