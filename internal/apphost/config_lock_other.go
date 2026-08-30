//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos)

package apphost

import "errors"

// withConfigLock has no implementation here: this platform offers no advisory
// lock to hold across a read-modify-write — windows, plan9, js, wasip1, and
// the unix systems whose syscall package lacks Flock (solaris, aix) all land
// here — so an update could lose a concurrent writer's change exactly as
// before. Refusing loudly keeps that failure out of the record; creating a
// first configuration with SaveConfig still works.
func withConfigLock[T any](metaDir string, fn func() (T, error)) (T, error) {
	var zero T
	return zero, errors.New("this platform has no advisory file locking, so gitseq cannot update its configuration safely here")
}

// WithMetaLock refuses here for the same reason, and for every caller rather
// than only the configuration writer: a caller that cannot be serialised must
// be told so, not handed an unsynchronised run that looks like it worked.
func WithMetaLock[T any](metaDir, lockFile string, fn func() (T, error)) (T, error) {
	var zero T
	if err := validateLockFile(lockFile); err != nil {
		return zero, err
	}
	return zero, errors.New("this platform has no advisory file locking, so gitseq cannot serialise this operation here")
}
