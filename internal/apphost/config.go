package apphost

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ConfigFile is the repository-private gitseq configuration, relative to
// MetaDir. Every gitseq program reads the same file, so a kernel-level tool
// can open a repository whose application it does not hold.
const ConfigFile = "config.json"

// Actor is one locally held signing identity. Custody is a local operational
// fact rather than application meaning: the durable log knows keys, and a name
// here is only how this checkout finds one.
type Actor struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	KeyFile     string `json:"key_file"`
}

// VerifiedFrontier is the newest signed sequence position this local
// workspace has accepted. The marker is local memory, not a witness: its head
// becomes authoritative only when a later audit verifies a sequence that
// contains that exact commit at that exact depth.
type VerifiedFrontier struct {
	Head  string `json:"head"`
	Depth int    `json:"depth"`
}

// Config is what a checkout must remember to reopen its own log: which
// sequence, in which object format, under whose sequencer key. None of it is
// application meaning, which is why an application this build cannot
// interpret still opens far enough to be verified and reported honestly.
type Config struct {
	Version              int               `json:"version"`
	Genesis              string            `json:"genesis"`
	ObjectFormat         string            `json:"object_format"`
	PayloadCeiling       uint64            `json:"payload_ceiling"`
	IdempotencyNamespace string            `json:"idempotency_namespace,omitempty"`
	SequencerKey         string            `json:"sequencer_key,omitempty"`
	ReadOnly             bool              `json:"read_only,omitempty"`
	Actors               map[string]Actor  `json:"actors,omitempty"`
	VerifiedFrontier     *VerifiedFrontier `json:"verified_frontier,omitempty"`
}

// Validate rejects a configuration that cannot name one sequence exactly. A
// writable repository without a sequencer key could not append, and a frontier
// marker with no head would claim a verified position that names nothing.
func (c Config) Validate() error {
	if c.Version != 0 || c.Genesis == "" || c.ObjectFormat == "" || (!c.ReadOnly && c.SequencerKey == "") ||
		(c.VerifiedFrontier != nil && (c.VerifiedFrontier.Head == "" || c.VerifiedFrontier.Depth < 0)) {
		return errors.New("invalid gitseq config")
	}
	if err := ValidateGenesis(c.ObjectFormat, c.Genesis); err != nil {
		return fmt.Errorf("invalid gitseq config: %w", err)
	}
	return nil
}

// LoadConfig reads and validates the configuration in one metadata directory.
func LoadConfig(metaDir string) (Config, error) {
	content, err := os.ReadFile(filepath.Join(metaDir, ConfigFile))
	if err != nil {
		return Config{}, fmt.Errorf("read gitseq config (run `gs init` first): %w", err)
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// SaveConfig replaces the configuration atomically, so a reader never observes
// a half-written file naming no sequence at all, and concurrent writers do not
// share a temporary path.
func SaveConfig(metaDir string, config Config) error {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomically(filepath.Join(metaDir, ConfigFile), append(content, '\n'))
}

// ValidateGenesis rejects an object id that cannot name a commit in the
// declared format.
func ValidateGenesis(format, genesis string) error {
	want := 40
	if format == "sha256" {
		want = 64
	} else if format != "sha1" {
		return errors.New("unsupported object format")
	}
	if len(genesis) != want {
		return errors.New("invalid genesis object id")
	}
	if _, err := hex.DecodeString(genesis); err != nil {
		return errors.New("invalid genesis object id")
	}
	return nil
}

// ResolveGitDirs keeps the selected checkout distinct from repository-wide
// state. Linked worktrees have their own GitDir, while objects, refs, gitseq
// configuration, and actor custody belong to CommonDir.
func ResolveGitDirs(ctx context.Context, repo string) (gitDir, commonDir string, err error) {
	if repo == "" {
		repo = "."
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--path-format=absolute", "--absolute-git-dir", "--git-common-dir")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("resolve git dirs: %w: %s", err, strings.TrimSpace(string(output)))
	}
	paths := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(paths) != 2 {
		return "", "", fmt.Errorf("resolve git dirs: expected worktree and common paths, got %q", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(paths[0]), strings.TrimSpace(paths[1]), nil
}

// MetaDir is where a repository keeps its gitseq state, given the common
// directory that ResolveGitDirs reported.
func MetaDir(commonDir string) string { return filepath.Join(commonDir, "gitseq") }
