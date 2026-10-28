// Package cgroups manages per-instance cgroup v2 subtrees.
package cgroups

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Limits struct {
	MemMax   uint64
	MemHigh  uint64
	SwapMax  uint64
	CPUQuota float64 // cores
	Pids     int64
}

type Manager struct{ Root string }

func New() *Manager { return &Manager{Root: "/sys/fs/cgroup/clofix-filevps"} }

func write(p, v string) error { return os.WriteFile(p, []byte(v), 0o644) }

// Init creates the parent cgroup and enables the controllers for children (best effort on the latter).
func (m *Manager) Init() error {
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return err
	}
	write(filepath.Join(filepath.Dir(m.Root), "cgroup.subtree_control"), "+cpu +memory +pids")
	write(filepath.Join(m.Root, "cgroup.subtree_control"), "+cpu +memory +pids")
	return nil
}

func (m *Manager) path(id string) string { return filepath.Join(m.Root, id) }

func (m *Manager) Create(id string, l Limits) error {
	p := m.path(id)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return err
	}
	set := func(f, v string) error {
		if err := write(filepath.Join(p, f), v); err != nil {
			return fmt.Errorf("cgroup %s=%s: %w", f, v, err)
		}
		return nil
	}
	if l.MemMax > 0 {
		if err := set("memory.max", strconv.FormatUint(l.MemMax, 10)); err != nil {
			return err
		}
		if l.MemHigh > 0 {
			if err := set("memory.high", strconv.FormatUint(l.MemHigh, 10)); err != nil {
				return err
			}
		}
		// swap.max includes ZRAM-backed swap; 0 means no swap use unless configured
		if err := set("memory.swap.max", strconv.FormatUint(l.SwapMax, 10)); err != nil {
			return err
		}
	}
	if l.CPUQuota > 0 {
		if err := set("cpu.max", fmt.Sprintf("%d 100000", int64(l.CPUQuota*100000))); err != nil {
			return err
		}
	}
	if l.Pids > 0 {
		if err := set("pids.max", strconv.FormatInt(l.Pids, 10)); err != nil {
			return err
		}
	}
	return nil
}

// Open returns a directory fd for exec.Cmd SysProcAttr.CgroupFD so the process starts inside the cgroup.
func (m *Manager) Open(id string) (*os.File, error) { return os.Open(m.path(id)) }

func (m *Manager) Pids(id string) []int {
	b, err := os.ReadFile(filepath.Join(m.path(id), "cgroup.procs"))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (m *Manager) Alive(id string) bool { return len(m.Pids(id)) > 0 }

// Signal sends sig to every process in the cgroup.
func (m *Manager) Signal(id string, sig syscall.Signal) {
	for _, p := range m.Pids(id) {
		syscall.Kill(p, sig)
	}
}

// Kill terminates every process in the cgroup (cgroup.kill needs kernel 5.14+, falls back to SIGKILL).
func (m *Manager) Kill(id string) {
	if write(filepath.Join(m.path(id), "cgroup.kill"), "1") != nil {
		m.Signal(id, syscall.SIGKILL)
	}
}

func (m *Manager) Remove(id string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.Remove(m.path(id)); err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

func (m *Manager) MemoryCurrent(id string) uint64 {
	b, _ := os.ReadFile(filepath.Join(m.path(id), "memory.current"))
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func (m *Manager) MemorySwapCurrent(id string) uint64 {
	b, _ := os.ReadFile(filepath.Join(m.path(id), "memory.swap.current"))
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func (m *Manager) CPUUsageUsec(id string) uint64 {
	b, err := os.ReadFile(filepath.Join(m.path(id), "cpu.stat"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			return value
		}
	}
	return 0
}
