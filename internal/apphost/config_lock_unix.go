//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos

package apphost

import (
	"os"
	"syscall"
)

// lockFileExclusively blocks until this file handle holds the exclusive
// advisory lock, and lockFileShared until it holds the shared one. flock is
// not restarted by the runtime after a signal — the runtime's own preemption
// signals included — so EINTR is retried here.
//
// The build tag enumerates exactly the platforms whose syscall package
// provides Flock. The unix tag alone would also pull in solaris and aix,
// where it does not exist and this file would not compile; there, and on
// every other platform without advisory locking, config_lock_other.go
// refuses instead.
func lockFileExclusively(file *os.File) error { return flock(file, syscall.LOCK_EX) }

func lockFileShared(file *os.File) error { return flock(file, syscall.LOCK_SH) }

func flock(file *os.File, how int) error {
	for {
		err := syscall.Flock(int(file.Fd()), how)
		if err != syscall.EINTR {
			return err
		}
	}
}

func unlockFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
