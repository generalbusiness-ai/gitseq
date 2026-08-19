package app

import (
	"os"
	"path/filepath"
)

// writeFileAtomically replaces path with data written to a unique temporary
// file in the same directory. The shared directory keeps the rename atomic;
// the unique name lets concurrent writers complete independently.
func writeFileAtomically(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
