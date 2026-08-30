//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos

package apphost

import (
	"os"
	"path/filepath"
	"syscall"
)

// configLockFile guards read-modify-write updates of ConfigFile. It sits
// beside the file it protects and is never renamed: an updater holds the
// advisory lock on its own open description for one whole load-modify-store,
// and the kernel drops the lock if the process dies, so a crash cannot leave
// a stale lock behind.
//
// The build tag enumerates exactly the platforms whose syscall package
// provides Flock. The unix tag alone would also pull in solaris and aix,
// where it does not exist and this file would not compile; there, and on
// every other platform without advisory locking,
// config_lock_other.go refuses updates instead.
const configLockFile = ".config.lock"

// withConfigLock runs fn while holding an exclusive advisory lock in
// metaDir, so concurrent updaters serialise instead of racing a lost update
// through the gap between reading ConfigFile and renaming over it.
func withConfigLock[T any](metaDir string, fn func() (T, error)) (T, error) {
	return WithMetaLock(metaDir, configLockFile, fn)
}

// WithMetaLock runs fn while holding an exclusive advisory lock on the named
// lock file inside metaDir. It is the one advisory-lock primitive in this
// repository, and it names no application vocabulary: a caller says which
// file it is serialising on, and the host layer says how a lock is taken and
// released. A second helper elsewhere would be a second answer to the
// crash-safety question this one already answers.
//
// The lock file is created if it does not exist and is never renamed, so the
// kernel drops the lock when the process dies. Each distinct name is a
// distinct lock: config updates take configLockFile and nothing else, so a
// caller holding another name may still update its configuration inside fn.
// A caller must never nest two acquisitions of the same name in one process
// — flock is per open description, so the inner acquisition would block on
// the outer one forever.
func WithMetaLock[T any](metaDir, lockFile string, fn func() (T, error)) (T, error) {
	var zero T
	if err := validateLockFile(lockFile); err != nil {
		return zero, err
	}
	file, err := os.OpenFile(filepath.Join(metaDir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return zero, err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return zero, err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}
