//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos || windows)

package apphost

import "os"

// tryLockFile has no lock to attempt on this platform: the blocking helpers
// already fail closed, so the witness reports the same refusal. It exists
// only for the cross-process tests' lock witness.
func tryLockFile(*os.File, bool) (bool, error) { return false, errConfigLockUnsupported }
