package apphost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		Version:      0,
		Genesis:      strings.Repeat("ab", 20),
		ObjectFormat: "sha1",
		ReadOnly:     true,
	}
}

var (
	errInjectedWrite      = errors.New("injected write failure")
	errInjectedClose      = errors.New("injected close failure")
	errInjectedRetryClose = errors.New("injected retry-close failure")
	errInjectedIdentity   = errors.New("injected identity-read failure")
)

// requireClosed proves CreateConfig closed the handle it opened: a leaked
// handle would hold the removed file open, and on Windows would block the
// removal itself.
func requireClosed(t *testing.T, file *os.File) {
	t.Helper()
	if file == nil {
		t.Fatal("the injected failure was never reached")
	}
	if _, err := file.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("CreateConfig left its file handle open after the failure: write error = %v, want os.ErrClosed", err)
	}
}

// A CreateConfig that fails mid-write must not poison its destination: the
// partial file it created never validates, so leaving it behind would make
// every later creation fail with os.ErrExist for a configuration nobody
// stored. The failure branch must close the handle it opened, remove the file
// it just created, and hand back the original failure, so a retry succeeds.
func TestCreateConfigWriteFailureLeavesPathFreeForRetry(t *testing.T) {
	metaDir := t.TempDir()
	var captured *os.File
	previousWrite := createConfigWrite
	createConfigWrite = func(file *os.File, _ []byte) (int, error) {
		captured = file
		return 0, errInjectedWrite
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want the original injected write failure", err)
	}
	requireClosed(t, captured)
	if _, statErr := os.Stat(filepath.Join(metaDir, ConfigFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed creation left a file at its destination: stat = %v", statErr)
	}
	if retryErr := CreateConfig(metaDir, testConfig()); retryErr != nil {
		t.Fatalf("retry after failed creation: %v", retryErr)
	}
	if _, loadErr := LoadConfig(metaDir); loadErr != nil {
		t.Fatalf("retried creation stored no valid configuration: %v", loadErr)
	}
}

// The injected close reports failure without closing, the way a real failed
// close can, so this test fails if CreateConfig does not itself close the
// still-open handle before removing the file — a pre-closed injection would
// prove nothing about that.
func TestCreateConfigCloseFailureLeavesPathFreeForRetry(t *testing.T) {
	metaDir := t.TempDir()
	var captured *os.File
	previousClose := createConfigClose
	createConfigClose = func(file *os.File) error {
		captured = file
		return errInjectedClose
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigClose = previousClose
	if !errors.Is(err, errInjectedClose) {
		t.Fatalf("CreateConfig error = %v, want the original injected close failure", err)
	}
	requireClosed(t, captured)
	if _, statErr := os.Stat(filepath.Join(metaDir, ConfigFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed creation left a file at its destination: stat = %v", statErr)
	}
	if retryErr := CreateConfig(metaDir, testConfig()); retryErr != nil {
		t.Fatalf("retry after failed creation: %v", retryErr)
	}
	if _, loadErr := LoadConfig(metaDir); loadErr != nil {
		t.Fatalf("retried creation stored no valid configuration: %v", loadErr)
	}
}

// When the cleanup itself fails, the caller must still see the original
// failure — the reason nothing was stored — and must also see that the
// partial file still occupies the path, because that occupation poisons every
// retry. Swallowing either half misreports what happened. The removal is made
// to fail by revoking write permission on the directory between the injected
// write failure and the cleanup.
func TestCreateConfigSurfacesCleanupFailureAlongsideOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the cleanup cannot be made to fail")
	}
	metaDir := t.TempDir()
	previousWrite := createConfigWrite
	createConfigWrite = func(*os.File, []byte) (int, error) {
		if err := os.Chmod(metaDir, 0o500); err != nil {
			t.Fatal(err)
		}
		return 0, errInjectedWrite
	}
	t.Cleanup(func() { os.Chmod(metaDir, 0o700) })
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want it to preserve the original injected write failure", err)
	}
	if err == nil || !strings.Contains(err.Error(), "still occupies its path") {
		t.Fatalf("CreateConfig error = %v, want the failed cleanup surfaced alongside the original failure", err)
	}
	if err := os.Chmod(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(metaDir, ConfigFile)); statErr != nil {
		t.Fatalf("the error reports the path still occupied, but stat = %v", statErr)
	}
}

// A pre-existing file makes the exclusive open fail before anything is
// written, and the failure handling must never remove or alter it: it is
// someone else's stored configuration, not this call's creation.
func TestCreateConfigNeverTouchesPreexistingFile(t *testing.T) {
	metaDir := t.TempDir()
	path := filepath.Join(metaDir, ConfigFile)
	stored := []byte("someone else's configuration\n")
	if err := os.WriteFile(path, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateConfig(metaDir, testConfig()); !errors.Is(err, os.ErrExist) {
		t.Fatalf("CreateConfig over an existing file = %v, want os.ErrExist", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pre-existing file no longer readable: %v", err)
	}
	if string(content) != string(stored) {
		t.Fatalf("pre-existing file changed: got %q, want %q", content, stored)
	}
}

// When the first close fails without releasing the handle, the cleanup closes
// it again, and that fallback close can itself fail. Both failures matter:
// the original close failure is why nothing was stored, and the fallback
// failure says the handle may still be held. Written the easy way — asserting
// only the original — this test would pass even if the fallback close's error
// were silently discarded, which is exactly the defect it exists to catch.
func TestCreateConfigCarriesFallbackCloseFailureAlongsideOriginal(t *testing.T) {
	metaDir := t.TempDir()
	var captured *os.File
	previousClose := createConfigClose
	previousRetryClose := createConfigRetryClose
	createConfigClose = func(file *os.File) error {
		captured = file
		return errInjectedClose
	}
	createConfigRetryClose = func(*os.File) error { return errInjectedRetryClose }
	err := CreateConfig(metaDir, testConfig())
	createConfigClose = previousClose
	createConfigRetryClose = previousRetryClose
	if captured == nil {
		t.Fatal("the injected close failure was never reached")
	}
	captured.Close()
	if !errors.Is(err, errInjectedClose) {
		t.Fatalf("CreateConfig error = %v, want the original injected close failure preserved", err)
	}
	if !errors.Is(err, errInjectedRetryClose) {
		t.Fatalf("CreateConfig error = %v, want the fallback close failure carried alongside the original", err)
	}
}

// A write failure followed by a close failure loses information if either is
// dropped: the write failure is why nothing was stored, and the close failure
// says the storage may be in a worse state than the write error alone admits.
// Written the easy way — asserting only the write failure — this test would
// pass even if the close failure were silently discarded, which is exactly
// the defect it exists to catch.
func TestCreateConfigCarriesCloseFailureAlongsideWriteFailure(t *testing.T) {
	metaDir := t.TempDir()
	var captured *os.File
	previousWrite := createConfigWrite
	previousClose := createConfigClose
	createConfigWrite = func(file *os.File, _ []byte) (int, error) {
		captured = file
		return 0, errInjectedWrite
	}
	createConfigClose = func(*os.File) error { return errInjectedClose }
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	createConfigClose = previousClose
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want the original injected write failure preserved", err)
	}
	if !errors.Is(err, errInjectedClose) {
		t.Fatalf("CreateConfig error = %v, want the close failure carried alongside the original", err)
	}
	requireClosed(t, captured)
	if _, statErr := os.Stat(filepath.Join(metaDir, ConfigFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed creation left a file at its destination: stat = %v", statErr)
	}
}

// O_EXCL proved the path empty at creation, not at cleanup. If another
// process removes the partial file and stores its own configuration in that
// window, a cleanup that removes by pathname deletes that process's file and
// reports it as this call's partial write. The injected close stages that
// race deterministically, after really closing the handle — the only order in
// which the racing process could remove the file on Windows, where an open
// handle blocks the removal. Written the easy way — with nothing racing —
// this test would pass even if the cleanup removed whatever stands at the
// path, which is exactly the defect it exists to catch.
func TestCreateConfigLeavesAReplacementAtItsDestinationAlone(t *testing.T) {
	metaDir := t.TempDir()
	path := filepath.Join(metaDir, ConfigFile)
	replacement := []byte("another process's configuration\n")
	previousWrite := createConfigWrite
	previousClose := createConfigClose
	createConfigWrite = func(*os.File, []byte) (int, error) { return 0, errInjectedWrite }
	createConfigClose = func(file *os.File) error {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, replacement, 0o600); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	createConfigClose = previousClose
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want the original injected write failure preserved", err)
	}
	if err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Fatalf("CreateConfig error = %v, want it to report the destination holds a file this call did not create", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("the replacement is no longer readable: %v", readErr)
	}
	if string(content) != string(replacement) {
		t.Fatalf("the replacement changed: got %q, want %q", content, replacement)
	}
}

// A symbolic link planted at the destination that points back at this call's
// file would pass an identity check that follows links: os.Stat would
// describe the created file itself, os.SameFile would match, and the cleanup
// would remove the link — an entry this call did not create. The identity
// check must describe the destination entry itself. The injected close stages
// the link after really closing the handle, the only order the racing process
// could achieve on Windows.
func TestCreateConfigLeavesASymlinkAtItsDestinationAlone(t *testing.T) {
	metaDir := t.TempDir()
	if err := os.Symlink("probe-target", filepath.Join(metaDir, "probe")); err != nil {
		t.Skipf("symbolic links are unavailable here: %v", err)
	}
	path := filepath.Join(metaDir, ConfigFile)
	moved := filepath.Join(metaDir, "moved-config.json")
	previousWrite := createConfigWrite
	previousClose := createConfigClose
	createConfigWrite = func(*os.File, []byte) (int, error) { return 0, errInjectedWrite }
	createConfigClose = func(file *os.File) error {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, path); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	createConfigClose = previousClose
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want the original injected write failure preserved", err)
	}
	if err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Fatalf("CreateConfig error = %v, want it to report the destination holds a file this call did not create", err)
	}
	info, lstatErr := os.Lstat(path)
	if lstatErr != nil {
		t.Fatalf("the link at the destination is gone: %v", lstatErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the destination entry is no longer a link: mode = %v", info.Mode())
	}
	if _, statErr := os.Stat(moved); statErr != nil {
		t.Fatalf("this call's file was removed through the link: %v", statErr)
	}
}

// When the identity read itself fails, the cleanup cannot know whose file
// occupies the path, so it must remove nothing — and it must tell the caller
// that specific cause, not a guess. Written the easy way — asserting only
// that something failed — this test would pass even if the identity read's
// error were silently discarded, which is exactly the defect it exists to
// catch.
func TestCreateConfigCarriesIdentityReadFailureAlongsideOriginal(t *testing.T) {
	metaDir := t.TempDir()
	previousWrite := createConfigWrite
	previousIdentity := createConfigIdentity
	createConfigWrite = func(*os.File, []byte) (int, error) { return 0, errInjectedWrite }
	createConfigIdentity = func(string) (os.FileInfo, error) { return nil, errInjectedIdentity }
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	createConfigIdentity = previousIdentity
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want the original injected write failure preserved", err)
	}
	if !errors.Is(err, errInjectedIdentity) {
		t.Fatalf("CreateConfig error = %v, want the identity read's failure carried alongside the original", err)
	}
	if err == nil || !strings.Contains(err.Error(), "nothing was removed") {
		t.Fatalf("CreateConfig error = %v, want it to report that nothing was removed", err)
	}
	if _, statErr := os.Stat(filepath.Join(metaDir, ConfigFile)); statErr != nil {
		t.Fatalf("identity was never confirmed, yet the file is gone: %v", statErr)
	}
}
