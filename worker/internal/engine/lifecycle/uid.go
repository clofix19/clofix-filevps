package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// userNSAfterMount is true when user-ns is applied after pivot_root.
// Overlay rootfs bind-mount is denied inside CLONE_NEWUSER on linuxkit/Docker
// (a tmpdir probe is not enough). Default is the delayed path; set
// CLOFIX_USER_NS_AFTER_MOUNT=false to keep clone(CLONE_NEWUSER) on hosts
// where that bind works.
func userNSAfterMount() bool {
	return os.Getenv("CLOFIX_USER_NS_AFTER_MOUNT") != "false"
}

const (
	uidRangeSize   = 65536
	cloneNewCgroup = 0x2000000 // CLONE_NEWCGROUP
)

func applyUserNSMapping(pid int, hostUID int) error {
	if pid <= 0 || hostUID <= 0 {
		return fmt.Errorf("invalid pid or host uid for user ns mapping")
	}
	parent, err := os.Readlink("/proc/self/ns/user")
	if err != nil {
		return fmt.Errorf("read host user ns: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat("/proc/" + fmt.Sprint(pid)); err != nil {
			return fmt.Errorf("guest exited before entering a user namespace")
		}
		child, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/user", pid))
		comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		name := strings.TrimSpace(string(comm))
		if err == nil && child != parent && name != "" && name != "clofix-worker" {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest did not enter a user namespace")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// sshd preauth calls setgroups; "deny" resets the TCP handshake.
	if err := os.WriteFile(fmt.Sprintf("/proc/%d/setgroups", pid), []byte("allow\n"), 0o644); err != nil {
		return fmt.Errorf("write setgroups: %w", err)
	}
	mapping := fmt.Sprintf("0 %d %d\n", hostUID, uidRangeSize)
	if err := os.WriteFile(fmt.Sprintf("/proc/%d/uid_map", pid), []byte(mapping), 0o644); err != nil {
		return fmt.Errorf("write uid_map: %w", err)
	}
	if err := os.WriteFile(fmt.Sprintf("/proc/%d/gid_map", pid), []byte(mapping), 0o644); err != nil {
		return fmt.Errorf("write gid_map: %w", err)
	}
	return nil
}

func (e *Engine) guestOwner(m *Meta) int {
	if !e.Cfg.UserNS {
		return 0
	}
	if m != nil && m.HostUID != 0 {
		return int(m.HostUID)
	}
	return int(e.uidBase())
}

func (e *Engine) uidBase() uint32 {
	if e.Cfg.UIDBase != 0 {
		return e.Cfg.UIDBase
	}
	return 100000
}

func (e *Engine) allocateHostUID() (uint32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.allocateHostUIDLocked()
}

func (e *Engine) allocateHostUIDLocked() (uint32, error) {
	used := map[uint32]bool{}
	legacyShared := false
	for _, m := range e.meta {
		if m.HostUID != 0 {
			used[m.HostUID] = true
		} else if e.Cfg.UserNS {
			// Guests created before per-instance ranges still map to uid_base
			// until they restart and get remapped.
			legacyShared = true
		}
	}
	if legacyShared {
		used[e.uidBase()] = true
	}
	base := e.uidBase()
	for i := uint32(0); i < 4096; i++ {
		u := base + i*uidRangeSize
		if u < base {
			break
		}
		if !used[u] {
			return u, nil
		}
	}
	return 0, fmt.Errorf("no free host uid range")
}

// ensureHostUID assigns a unique host uid range when user namespaces are on.
// Existing guests that still share uid_base are remapped on the next start:
// overlay upper files in [uid_base, uid_base+65536) move to the new range.
// Running guests keep their current mapping until they restart.
func (e *Engine) ensureHostUID(m *Meta) error {
	if !e.Cfg.UserNS {
		return nil
	}
	if m.HostUID != 0 {
		return nil
	}
	uid, err := e.allocateHostUID()
	if err != nil {
		return err
	}
	old := e.uidBase()
	if uid != old {
		if err := remapUIDTree(e.upperDir(m.ID), old, uid); err != nil {
			return fmt.Errorf("remap upper layer to uid %d: %w", uid, err)
		}
	}
	m.HostUID = uid
	return e.save(m)
}

func remapUIDTree(root string, oldBase, newBase uint32) error {
	if oldBase == newBase {
		return nil
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		uid, gid := st.Uid, st.Gid
		changed := false
		if uid >= oldBase && uid < oldBase+uidRangeSize {
			uid = newBase + (uid - oldBase)
			changed = true
		}
		if gid >= oldBase && gid < oldBase+uidRangeSize {
			gid = newBase + (gid - oldBase)
			changed = true
		}
		if !changed {
			return nil
		}
		return os.Lchown(path, int(uid), int(gid))
	})
}
