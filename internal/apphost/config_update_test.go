package apphost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func sha1ID(rune_ byte) string { return strings.Repeat(string(rune_), 40) }

func baseTestConfig() Config {
	return Config{
		Version: 0, Genesis: sha1ID('a'), ObjectFormat: "sha1", PayloadCeiling: 1024,
		IdempotencyNamespace: "test/v0", SequencerKey: filepath.Join("gitseq", "sequencer"),
	}
}

// The defect behind request #8762: a process that loaded its Config before
// another process recorded custody must not erase that custody when it saves
// an unrelated field.
func TestUpdateConfigPreservesConcurrentCustody(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, baseTestConfig()); err != nil {
		t.Fatal(err)
	}

	concurrent := baseTestConfig()
	concurrent.Actors = map[string]Actor{"second": {Name: "second", Fingerprint: sha1ID('b'), KeyFile: "actors/second.key"}}
	if err := SaveConfig(dir, concurrent); err != nil {
		t.Fatal(err)
	}

	merged, err := UpdateConfig(dir, baseTestConfig(), func(c *Config) (bool, error) {
		if c.Actors["second"].Fingerprint != sha1ID('b') {
			t.Fatalf("update did not reload the custody another writer had persisted: %+v", c.Actors)
		}
		c.VerifiedFrontier = &VerifiedFrontier{Head: sha1ID('c'), Depth: 3}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Actors["second"].Fingerprint != sha1ID('b') {
		t.Fatalf("concurrent custody lost in merged view: %+v", merged.Actors)
	}
	if merged.VerifiedFrontier == nil || merged.VerifiedFrontier.Depth != 3 {
		t.Fatalf("declared frontier change missing from merged view: %+v", merged.VerifiedFrontier)
	}
	reread, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Actors["second"].Name != "second" || reread.VerifiedFrontier.Depth != 3 {
		t.Fatalf("stored configuration lost the merge: %+v", reread)
	}
}

// The advisory lock is the entire mechanism of UpdateConfig, so its test must
// fail when the lock is removed: thirty-two concurrent updaters released at
// once each record a distinct actor, and under a real lock the
// read-modify-store windows serialise so every grant survives, while without
// it overlapping windows overwrite one another with stale views and grants
// disappear.
func TestUpdateConfigSerialisesConcurrentGrants(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, baseTestConfig()); err != nil {
		t.Fatal(err)
	}

	const grants = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, grants)
	for i := range grants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("grant-%02d", i)
			_, errs[i] = UpdateConfig(dir, baseTestConfig(), func(c *Config) (bool, error) {
				if c.Actors == nil {
					c.Actors = map[string]Actor{}
				}
				c.Actors[name] = Actor{Name: name, Fingerprint: sha1ID('f'), KeyFile: "actors/" + name + ".key"}
				return true, nil
			})
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("grant %02d failed: %v", i, err)
		}
	}
	stored, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range grants {
		name := fmt.Sprintf("grant-%02d", i)
		actor, ok := stored.Actors[name]
		if !ok || actor.Fingerprint != sha1ID('f') {
			t.Fatalf("concurrent grant %02d was lost: the update lock is not serialising read-modify-write windows (%d actors survived)", i, len(stored.Actors))
		}
	}
}

func TestUpdateConfigNoChangeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	first := baseTestConfig()
	first.Actors = map[string]Actor{"second": {Name: "second", Fingerprint: sha1ID('b'), KeyFile: "actors/second.key"}}
	if err := SaveConfig(dir, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ConfigFile)
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	if _, err := UpdateConfig(dir, baseTestConfig(), func(c *Config) (bool, error) {
		called = true
		if c.Actors["second"].Name != "second" {
			t.Fatalf("reload lost custody before mutate ran: %+v", c.Actors)
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("mutate was never called")
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatal("a no-change update rewrote the file: its modification time moved")
	}
	if beforeIno, afterIno := configInode(beforeInfo), configInode(afterInfo); beforeIno != 0 && afterIno != beforeIno {
		t.Fatal("a no-change update rewrote the file: the path names a different inode")
	}
}

func TestUpdateConfigMutateErrorLeavesFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, baseTestConfig()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("genuine conflict")
	if _, err := UpdateConfig(dir, baseTestConfig(), func(c *Config) (bool, error) {
		c.SequencerKey = "elsewhere"
		return true, want
	}); !errors.Is(err, want) {
		t.Fatalf("mutate error = %v, want %v", err, want)
	}
	after, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a failed update still reached the file")
	}
}

// An update never creates the record: where nothing is stored, it refuses
// without running the mutation and without writing a file from the caller's
// in-memory snapshot, which could resurrect custody a deletion just removed.
func TestUpdateConfigFailsClosedWhereNothingIsStored(t *testing.T) {
	dir := t.TempDir()
	remembered := baseTestConfig()
	remembered.Actors = map[string]Actor{"ghost": {Name: "ghost", Fingerprint: sha1ID('e'), KeyFile: "actors/ghost.key"}}
	_, err := UpdateConfig(dir, remembered, func(*Config) (bool, error) {
		t.Error("the mutation ran with no stored record to run against")
		return false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "never creates") {
		t.Fatalf("missing-record error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused update left a config file behind: %v", err)
	}
}

// Every immutable field is identity: a divergence in any one of them refuses
// the update before the mutation runs and without changing a stored byte.
func TestUpdateConfigRefusesEveryImmutableFieldDivergence(t *testing.T) {
	divergences := map[string]func(*Config){
		"version":               func(c *Config) { c.Version = 1 },
		"genesis":               func(c *Config) { c.Genesis = sha1ID('d') },
		"object format":         func(c *Config) { c.ObjectFormat = "sha256" },
		"payload ceiling":       func(c *Config) { c.PayloadCeiling = 42 },
		"idempotency namespace": func(c *Config) { c.IdempotencyNamespace = "other/v0" },
		"sequencer key":         func(c *Config) { c.SequencerKey = "elsewhere" },
		"read only":             func(c *Config) { c.ReadOnly = true },
	}
	for name, diverge := range divergences {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			stored := baseTestConfig()
			if err := SaveConfig(dir, stored); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, ConfigFile))
			if err != nil {
				t.Fatal(err)
			}
			opened := stored
			diverge(&opened)
			// A sha256 genesis is 64 hex characters, so the object-format
			// divergence must carry one or the opened value would be
			// invalid for reasons other than the divergence under test.
			if opened.ObjectFormat == "sha256" {
				opened.Genesis = strings.Repeat("a", 64)
			}
			_, err = UpdateConfig(dir, opened, func(*Config) (bool, error) {
				t.Error("the mutation ran against a stored file whose identity its caller never opened")
				return false, nil
			})
			if err == nil || !strings.Contains(err.Error(), "immutable field") {
				t.Fatalf("divergence error = %v", err)
			}
			after, err := os.ReadFile(filepath.Join(dir, ConfigFile))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("a refused update changed the stored file")
			}
		})
	}
}

// A mutation may write into its argument and then report that nothing
// changed. Those writes were never stored, so they must not be returned
// either: memory adopting an unstored value is the split between memory and
// disk this transaction exists to prevent.
func TestUpdateConfigUnchangedReturnsTheStoredStateNotTheMutationsWrites(t *testing.T) {
	dir := t.TempDir()
	stored := baseTestConfig()
	stored.Actors = map[string]Actor{"held": {Name: "held", Fingerprint: sha1ID('b'), KeyFile: "actors/held.key"}}
	if err := SaveConfig(dir, stored); err != nil {
		t.Fatal(err)
	}
	result, err := UpdateConfig(dir, baseTestConfig(), func(c *Config) (bool, error) {
		c.Actors["leaked"] = Actor{Name: "leaked", Fingerprint: sha1ID('c'), KeyFile: "actors/leaked.key"}
		c.VerifiedFrontier = &VerifiedFrontier{Head: sha1ID('c'), Depth: 9}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, held := result.Actors["held"]; !held {
		t.Fatal("an unchanged update did not return the stored state")
	}
	if _, leaked := result.Actors["leaked"]; leaked {
		t.Fatalf("an unchanged update returned the mutation's unstored writes: %+v", result.Actors)
	}
	if result.VerifiedFrontier != nil {
		t.Fatalf("an unchanged update returned the mutation's unstored frontier: %+v", result.VerifiedFrontier)
	}
}
