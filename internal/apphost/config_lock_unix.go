//go:build unix

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
const configLockFile = ".config.lock"

// withConfigLock runs fn while holding an exclusive advisory lock in
// metaDir, so concurrent updaters serialise instead of racing a lost update
// through the gap between reading ConfigFile and renaming over it.
func withConfigLock[T any](metaDir string, fn func() (T, error)) (T, error) {
	file, err := os.OpenFile(filepath.Join(metaDir, configLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		var zero T
		return zero, err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		var zero T
		return zero, err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}
