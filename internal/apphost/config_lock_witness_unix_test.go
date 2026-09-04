//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos

package apphost

import (
	"os"
	"syscall"
)

// tryLockFile makes one non-blocking attempt at the given flock side and
// reports whether another holder refused it. A refused attempt leaves the
// handle unlocked; a granted attempt is released immediately, so the probe
// observes contention without taking part in the coordination it examines.
// It exists only for the cross-process tests' lock witness.
func tryLockFile(file *os.File, exclusive bool) (refused bool, err error) {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	switch err := syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB); err {
	case nil:
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return false, nil
	case syscall.EWOULDBLOCK:
		return true, nil
	default:
		return false, err
	}
}
