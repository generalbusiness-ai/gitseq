//go:build unix

package apphost

import (
	"os"
	"syscall"
)

// configInode reports the file's inode number, or 0 where the platform does
// not expose one, so a test can tell a rewritten file from an untouched one
// even when its modification time did not visibly move.
func configInode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
