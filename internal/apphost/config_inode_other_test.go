//go:build !unix

package apphost

import "os"

// configInode reports 0: this platform exposes no inode number, so tests
// relying on rewrite detection fall back to the modification time alone.
func configInode(os.FileInfo) uint64 { return 0 }
