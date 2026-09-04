//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos || windows)

package apphost

import (
	"errors"
	"os"
)

// This platform has no cross-process file lock this package implements —
// plan9, js, wasip1, and the unix systems whose syscall package lacks Flock
// (solaris, aix) all land here — so both sides fail closed: every coordinated
// path, update and read alike, refuses rather than proceed uncoordinated,
// because an unlocked writer could erase another process's stored changes and
// an unlocked reader could observe a replacement mid-rename. Creating a first
// configuration through CreateConfig, which coordinates through exclusive
// creation rather than through this lock, still works.
var errConfigLockUnsupported = errors.New("gitseq file locking is not implemented on this platform; refusing uncoordinated access")

func lockFileExclusively(*os.File) error { return errConfigLockUnsupported }

func lockFileShared(*os.File) error { return errConfigLockUnsupported }

// unlockFile pairs with the helpers above, which never grant a lock here.
func unlockFile(*os.File) {}
