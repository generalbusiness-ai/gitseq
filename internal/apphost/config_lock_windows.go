//go:build windows

package apphost

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusively blocks until this file handle holds the exclusive lock
// over the whole file, and lockFileShared until it holds the shared one. The
// same range a holder locks is what a waiter asks for, so the semantics match
// the Unix side: writers exclude each other and every reader, readers exclude
// writers only, released with the handle.
func lockFileExclusively(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
}

func lockFileShared(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), 0, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
}

func unlockFile(file *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
}
