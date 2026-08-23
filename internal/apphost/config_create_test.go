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
	errInjectedWrite = errors.New("injected write failure")
	errInjectedClose = errors.New("injected close failure")
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
// reports it as this call's partial write. The injected write failure stages
// that race deterministically: it replaces the created file before the
// cleanup runs. Written the easy way — with nothing racing — this test would
// pass even if the cleanup removed whatever stands at the path, which is
// exactly the defect it exists to catch.
func TestCreateConfigLeavesAReplacementAtItsDestinationAlone(t *testing.T) {
	metaDir := t.TempDir()
	path := filepath.Join(metaDir, ConfigFile)
	replacement := []byte("another process's configuration\n")
	previousWrite := createConfigWrite
	createConfigWrite = func(*os.File, []byte) (int, error) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, replacement, 0o600); err != nil {
			t.Fatal(err)
		}
		return 0, errInjectedWrite
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
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
