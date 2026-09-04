//go:build windows

package apphost

import (
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile makes one non-blocking attempt at the given lock side, over the
// same whole-file range the blocking helpers use, and reports whether another
// holder refused it. A refused attempt leaves the handle unlocked; a granted
// attempt is released immediately, so the probe observes contention without
// taking part in the coordination it examines. It exists only for the
// cross-process tests' lock witness.
func tryLockFile(file *os.File, exclusive bool) (refused bool, err error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	switch err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped)); err {
	case nil:
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
		return false, nil
	case windows.ERROR_LOCK_VIOLATION:
		return true, nil
	default:
		return false, err
	}
}
