//go:build unix

package fim

import (
	"os"
	"syscall"
)

func fileOwners(st os.FileInfo) (uid, gid int) {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(sys.Uid), int(sys.Gid)
	}
	return -1, -1
}
