package apphost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestUpdateConfigNoChangeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	first := baseTestConfig()
	first.Actors = map[string]Actor{"second": {Name: "second", Fingerprint: sha1ID('b'), KeyFile: "actors/second.key"}}
	if err := SaveConfig(dir, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ConfigFile)
	before, err := os.ReadFile(path)
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
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a no-change update rewrote the file")
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
