package apphost

import (
	"os"
	"path/filepath"
)

// WriteFileAtomically replaces path with data written to a unique temporary
// file in the same directory. The shared directory keeps the rename atomic;
// the unique name lets concurrent writers complete independently.
//
// It lives here because the repository-private state this package owns is
// what needs it, and the one caller above — the resident advertisement — is
// writing into the same metadata directory. One implementation, at the layer
// that owns the directory, rather than a copy on each side of the seam.
func WriteFileAtomically(path string, data []byte) error {
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
