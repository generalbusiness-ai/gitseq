//go:build !unix

package apphost

import "errors"

// withConfigLock has no implementation here: this platform offers no advisory
// lock to hold across a read-modify-write, so an update could lose a
// concurrent writer's change exactly as before. Refusing loudly keeps that
// failure out of the record; creating a first configuration with SaveConfig
// still works.
func withConfigLock[T any](metaDir string, fn func() (T, error)) (T, error) {
	var zero T
	return zero, errors.New("this platform has no advisory file locking, so gitseq cannot update its configuration safely here")
}
