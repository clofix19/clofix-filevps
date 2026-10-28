//go:build linux

package namespaces

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	prSetNoNewPrivs       = 38
	prSetSeccomp          = 22
	seccompModeFilter     = 2
	seccompRetKillProcess = 0x80000000
	seccompRetErrno       = 0x00050000
	seccompRetAllow       = 0x7fff0000
	seccompDataNr         = 0
	seccompDataArch       = 4
	bpfLDWABS             = 0x20
	bpfJEQK               = 0x15
	bpfRETK               = 0x06
)

// installSeccomp installs a syscall deny-list after namespace and root setup. Calls not
// explicitly blocked remain available so ordinary guest distributions can boot.
func installSeccomp() error {
	var auditArch uint32
	var blocked []uint32
	switch runtime.GOARCH {
	case "amd64":
		auditArch = 0xc000003e
		blocked = []uint32{101, 155, 165, 166, 169, 175, 176, 246, 248, 249, 250, 272, 298, 304, 308, 313, 321, 323, 425, 426, 427, 428, 429, 430, 431, 432, 442}
	case "arm64":
		auditArch = 0xc00000b7
		blocked = []uint32{39, 40, 41, 97, 104, 105, 106, 117, 142, 217, 218, 219, 241, 265, 268, 273, 280, 282, 425, 426, 427, 428, 429, 430, 431, 432, 442}
	default:
		return fmt.Errorf("seccomp is unsupported on architecture %s", runtime.GOARCH)
	}

	// Verify the syscall ABI before checking syscall numbers; a mismatch kills the process.
	filters := []syscall.SockFilter{
		{Code: bpfLDWABS, K: seccompDataArch},
		{Code: bpfJEQK, Jt: 1, Jf: 0, K: auditArch},
		{Code: bpfRETK, K: seccompRetKillProcess},
		{Code: bpfLDWABS, K: seccompDataNr},
		// Return ENOSYS for clone3 so libc can fall back to clone. Returning
		// EPERM prevents tools such as apt from spawning their helper processes.
		{Code: bpfJEQK, Jt: 0, Jf: 1, K: 435},
		{Code: bpfRETK, K: seccompRetErrno | uint32(syscall.ENOSYS)},
	}
	for _, nr := range blocked {
		filters = append(filters,
			syscall.SockFilter{Code: bpfJEQK, Jt: 0, Jf: 1, K: nr},
			syscall.SockFilter{Code: bpfRETK, K: seccompRetErrno | uint32(syscall.EPERM)},
		)
	}
	filters = append(filters, syscall.SockFilter{Code: bpfRETK, K: seccompRetAllow})
	program := syscall.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}

	if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("set no_new_privs: %w", errno)
	}
	if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetSeccomp, seccompModeFilter, uintptr(unsafe.Pointer(&program)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("install seccomp filter: %w", errno)
	}
	return nil
}
