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

	merged, err := UpdateConfig(dir, Config{}, func(c *Config) (bool, error) {
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
			_, errs[i] = UpdateConfig(dir, Config{}, func(c *Config) (bool, error) {
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
	if _, err := UpdateConfig(dir, Config{}, func(c *Config) (bool, error) {
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
	if _, err := UpdateConfig(dir, Config{}, func(c *Config) (bool, error) {
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

func TestUpdateConfigCreatesFromBaseWhenNoFileExists(t *testing.T) {
	dir := t.TempDir()
	merged, err := UpdateConfig(dir, baseTestConfig(), func(c *Config) (bool, error) {
		c.VerifiedFrontier = &VerifiedFrontier{Head: sha1ID('c'), Depth: 3}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Genesis != sha1ID('a') || merged.VerifiedFrontier == nil {
		t.Fatalf("first update did not build on the base it was given: %+v", merged)
	}
	reread, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Genesis != sha1ID('a') || reread.VerifiedFrontier.Depth != 3 {
		t.Fatalf("created configuration is not what the update declared: %+v", reread)
	}
}
