package apphost

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	errInjectedRemove     = errors.New("injected removal failure")
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

// metaDirEntries lists what the metadata directory holds, in name order, so a
// test can prove a creation left nothing behind beyond what it claims.
func metaDirEntries(t *testing.T, metaDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(metaDir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// A CreateConfig that fails mid-write must not poison its destination: the
// partial content it wrote never validates, so leaving it behind would make
// every later creation fail with os.ErrExist for a configuration nobody
// stored. The failure branch must close the handle it opened, remove the file
// it wrote, leave nothing else behind, and hand back the original failure, so
// a retry succeeds.
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
	if names := metaDirEntries(t, metaDir); len(names) != 0 {
		t.Fatalf("failed creation left files behind: %v", names)
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
	if names := metaDirEntries(t, metaDir); len(names) != 0 {
		t.Fatalf("failed creation left files behind: %v", names)
	}
	if retryErr := CreateConfig(metaDir, testConfig()); retryErr != nil {
		t.Fatalf("retry after failed creation: %v", retryErr)
	}
	if _, loadErr := LoadConfig(metaDir); loadErr != nil {
		t.Fatalf("retried creation stored no valid configuration: %v", loadErr)
	}
}

// When the cleanup itself fails, the caller must still see the original
// failure — the reason nothing was stored — and must also see the removal
// failure, because a file this call created is still on disk. The file that
// remains is this call's own staging file at its private name, never the
// destination, so the destination stays free and a retry still succeeds.
// Written the easy way — asserting only the original failure — this test
// would pass even if the removal's error were silently discarded, which is
// exactly the defect it exists to catch.
func TestCreateConfigSurfacesCleanupFailureAlongsideOriginal(t *testing.T) {
	metaDir := t.TempDir()
	previousWrite := createConfigWrite
	previousRemove := createConfigRemove
	createConfigWrite = func(*os.File, []byte) (int, error) { return 0, errInjectedWrite }
	createConfigRemove = func(string) error { return errInjectedRemove }
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	createConfigRemove = previousRemove
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want it to preserve the original injected write failure", err)
	}
	if !errors.Is(err, errInjectedRemove) {
		t.Fatalf("CreateConfig error = %v, want the removal failure carried alongside the original", err)
	}
	if err == nil || !strings.Contains(err.Error(), "could not be removed") {
		t.Fatalf("CreateConfig error = %v, want it to report the file that could not be removed", err)
	}
	if _, statErr := os.Stat(filepath.Join(metaDir, ConfigFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the failure happened before the destination existed, yet the destination holds a file: stat = %v", statErr)
	}
	names := metaDirEntries(t, metaDir)
	if len(names) != 1 || !strings.HasPrefix(names[0], "."+ConfigFile+".create-") {
		t.Fatalf("metadata directory holds %v, want only this call's staging file at its private name", names)
	}
	if retryErr := CreateConfig(metaDir, testConfig()); retryErr != nil {
		t.Fatalf("a staging file that could not be removed must not poison the destination for retry: %v", retryErr)
	}
	if _, loadErr := LoadConfig(metaDir); loadErr != nil {
		t.Fatalf("retried creation stored no valid configuration: %v", loadErr)
	}
}

// A pre-existing file makes the creation fail before the destination is
// changed, and the failure handling must never remove or alter it: it is
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
	if names := metaDirEntries(t, metaDir); !slices.Equal(names, []string{ConfigFile}) {
		t.Fatalf("refused creation left files behind: %v", names)
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

// POSIX close(2) releases the descriptor even when it reports a failure such
// as EIO, so the fallback close then finds the handle already closed and
// os.File.Close reports os.ErrClosed. That is confirmation the handle was
// released, not a new failure: the caller must see the genuine close failure
// alone, with no fabricated "could not be closed" alongside it. The injected
// close here closes for real before reporting failure, exactly the EIO shape,
// and the fallback close stays at its production default so its os.ErrClosed
// is the real one.
func TestCreateConfigTreatsAnAlreadyClosedFallbackAsReleased(t *testing.T) {
	metaDir := t.TempDir()
	previousClose := createConfigClose
	createConfigClose = func(file *os.File) error {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return errInjectedClose
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigClose = previousClose
	if !errors.Is(err, errInjectedClose) {
		t.Fatalf("CreateConfig error = %v, want the original injected close failure", err)
	}
	if errors.Is(err, os.ErrClosed) {
		t.Fatalf("CreateConfig error = %v, want no fabricated failure from the fallback close finding the handle already released", err)
	}
	if names := metaDirEntries(t, metaDir); len(names) != 0 {
		t.Fatalf("failed creation left files behind: %v", names)
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

// GitHub run 32670745698: a cleanup that removes by destination pathname can
// delete a file another process stored there. An identity check over device
// and inode numbers cannot repair that, because an inode number is not a
// durable identity — Linux reuses a freed inode eagerly, so a replacement
// stored where a file was just unlinked can answer os.SameFile as the file
// this call created. This test stages that scenario: during the failure,
// another process's configuration lands at the destination — which under the
// current code held nothing, since this call's partial content sits only at
// its private staging name, so the removal before the store is a no-op kept
// from the pre-fix window it re-enacts. Whatever identity the replacement
// carries, cleanup must leave it in place, because the destination is not
// cleanup's to remove.
//
// This test pins the pre-fix defect only where the kernel reuses a freed
// inode number eagerly (Linux/ext4): on macOS/APFS the replacement gets a
// fresh inode, the pre-fix os.SameFile guard refuses the removal, and the
// pre-fix code passes here. TestCreateConfigCleanupRemovesOnlyWhatItCreated
// is the platform-independent guard for that defect. This test still earns
// its place on every platform, as the only one that catches a cleanup
// removing the destination directly, bypassing the createConfigRemove seam.
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
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
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
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("the replacement was removed by cleanup: %v", readErr)
	}
	if string(content) != string(replacement) {
		t.Fatalf("the replacement changed: got %q, want %q", content, replacement)
	}
	if names := metaDirEntries(t, metaDir); !slices.Equal(names, []string{ConfigFile}) {
		t.Fatalf("cleanup left more than the replacement behind: %v", names)
	}
}

// The pathname is the only name a cleanup shares with other processes, so
// removing by the destination pathname is what made run 32670745698 possible:
// after the partial file was unlinked and replaced, the removal deleted a
// file this call never created. The only file cleanup may remove is the one
// this call created, at a private name no other process holds. This test
// records every path the cleanup removes and refuses any removal that names
// the shared destination — deterministically, on every platform, without
// needing the kernel to reuse an inode number.
func TestCreateConfigCleanupRemovesOnlyWhatItCreated(t *testing.T) {
	metaDir := t.TempDir()
	path := filepath.Join(metaDir, ConfigFile)
	var removed []string
	previousWrite := createConfigWrite
	previousRemove := createConfigRemove
	createConfigWrite = func(*os.File, []byte) (int, error) { return 0, errInjectedWrite }
	createConfigRemove = func(p string) error {
		removed = append(removed, p)
		return os.Remove(p)
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigWrite = previousWrite
	createConfigRemove = previousRemove
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("CreateConfig error = %v, want the original injected write failure preserved", err)
	}
	for _, p := range removed {
		if p == path {
			t.Fatalf("cleanup removed the shared destination path %q; it may remove only the file this call created", p)
		}
	}
	if len(removed) == 0 {
		t.Fatal("cleanup removed nothing, so the file this call created was left behind")
	}
	if names := metaDirEntries(t, metaDir); len(names) != 0 {
		t.Fatalf("failed creation left files behind: %v", names)
	}
}

// Exclusivity must hold over the whole creation, not only at its first step:
// when another creator claims the destination between this call's start and
// its completion, exactly one configuration may win. The winner is the one at
// the destination; this call must be refused with os.ErrExist — not report a
// success for content the destination does not hold — and must leave the
// winner's file untouched and no staging file behind.
func TestCreateConfigRefusesADestinationClaimedDuringItsWindow(t *testing.T) {
	metaDir := t.TempDir()
	path := filepath.Join(metaDir, ConfigFile)
	winner := []byte("the concurrent creator's configuration\n")
	previousClose := createConfigClose
	createConfigClose = func(file *os.File) error {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, winner, 0o600); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigClose = previousClose
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("CreateConfig against a destination claimed during its window = %v, want os.ErrExist", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("the winner's file is no longer readable: %v", readErr)
	}
	if string(content) != string(winner) {
		t.Fatalf("the winner's file changed: got %q, want %q", content, winner)
	}
	if names := metaDirEntries(t, metaDir); !slices.Equal(names, []string{ConfigFile}) {
		t.Fatalf("refused creation left files behind: %v", names)
	}
}

// A symbolic link planted at the destination during the window is an entry
// this call did not create: the creation must be refused, and neither the
// link nor the file it points at may be removed or altered.
func TestCreateConfigLeavesASymlinkAtItsDestinationAlone(t *testing.T) {
	metaDir := t.TempDir()
	probe := filepath.Join(metaDir, "probe")
	if err := os.Symlink("probe-target", probe); err != nil {
		t.Skipf("symbolic links are unavailable here: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(metaDir, ConfigFile)
	decoy := filepath.Join(metaDir, "decoy")
	decoyContent := []byte("the decoy's content\n")
	if err := os.WriteFile(decoy, decoyContent, 0o600); err != nil {
		t.Fatal(err)
	}
	previousClose := createConfigClose
	createConfigClose = func(file *os.File) error {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.Symlink(decoy, path); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	err := CreateConfig(metaDir, testConfig())
	createConfigClose = previousClose
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("CreateConfig with a link planted at its destination = %v, want os.ErrExist", err)
	}
	info, lstatErr := os.Lstat(path)
	if lstatErr != nil {
		t.Fatalf("the link at the destination is gone: %v", lstatErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the destination entry is no longer a link: mode = %v", info.Mode())
	}
	content, readErr := os.ReadFile(decoy)
	if readErr != nil {
		t.Fatalf("the linked file was removed: %v", readErr)
	}
	if string(content) != string(decoyContent) {
		t.Fatalf("the linked file changed: got %q, want %q", content, decoyContent)
	}
	if names := metaDirEntries(t, metaDir); !slices.Equal(names, []string{ConfigFile, "decoy"}) {
		t.Fatalf("refused creation left files behind: %v", names)
	}
}

// A successful creation must leave exactly the configuration — a staging file
// left beside it would sit in the metadata directory forever, unexplained —
// and what it stored must be the configuration it was given, not merely
// something that validates.
func TestCreateConfigSuccessLeavesOnlyTheConfiguration(t *testing.T) {
	metaDir := t.TempDir()
	if err := CreateConfig(metaDir, testConfig()); err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	// The directory is inspected before anything reads the configuration: a
	// read takes the shared side of the configuration lock, which creates the
	// lock sidecar, and this assertion is about what creation leaves.
	if names := metaDirEntries(t, metaDir); !slices.Equal(names, []string{ConfigFile}) {
		t.Fatalf("successful creation left files behind: %v", names)
	}
	loaded, err := LoadConfig(metaDir)
	if err != nil {
		t.Fatalf("created configuration does not load: %v", err)
	}
	if !reflect.DeepEqual(loaded, testConfig()) {
		t.Fatalf("stored configuration = %+v, want the configuration CreateConfig was given: %+v", loaded, testConfig())
	}
}

// When the configuration is stored but the staging file cannot be removed,
// both facts matter: the caller must learn about the file left on disk, and
// must be able to tell this outcome from a failed creation — the stored
// configuration is valid and stays. Written the easy way — asserting only
// success — this test would pass even if the removal's error were silently
// discarded, which is exactly the defect it exists to catch.
func TestCreateConfigReportsAStagingFileItCouldNotRemoveAfterStoring(t *testing.T) {
	metaDir := t.TempDir()
	previousRemove := createConfigRemove
	createConfigRemove = func(string) error { return errInjectedRemove }
	err := CreateConfig(metaDir, testConfig())
	createConfigRemove = previousRemove
	if !errors.Is(err, errInjectedRemove) {
		t.Fatalf("CreateConfig error = %v, want the staging file's removal failure reported", err)
	}
	if err == nil || !strings.Contains(err.Error(), "was stored") {
		t.Fatalf("CreateConfig error = %v, want it to say the configuration was stored", err)
	}
	// Counted before the load below, which creates the lock sidecar the
	// shared side of the configuration lock lives on.
	names := metaDirEntries(t, metaDir)
	if len(names) != 2 || !slices.Contains(names, ConfigFile) {
		t.Fatalf("metadata directory holds %v, want the configuration and the one staging file the error reports", names)
	}
	if _, loadErr := LoadConfig(metaDir); loadErr != nil {
		t.Fatalf("the error reports the configuration stored, but loading it failed: %v", loadErr)
	}
}
