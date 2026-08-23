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

// Clone returns a configuration sharing no mutable state with the receiver.
// Config is a value type, but two of its fields are references — the actor map
// and the frontier pointer — so a plain struct copy still aliases them, and a
// holder of the "copy" could mutate live custody state. Every other field is a
// scalar, and Actor holds only strings, so copying these two is a complete
// deep copy.
func (c Config) Clone() Config {
	if c.Actors != nil {
		actors := make(map[string]Actor, len(c.Actors))
		for name, actor := range c.Actors {
			actors[name] = actor
		}
		c.Actors = actors
	}
	if c.VerifiedFrontier != nil {
		frontier := *c.VerifiedFrontier
		c.VerifiedFrontier = &frontier
	}
	return c
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

// CreateConfig stores a configuration only where none exists yet. Creation is
// exclusive rather than replacing: when two creators race, exactly one wins
// and the other sees os.ErrExist, so a stored genesis is never silently
// overwritten by a concurrent creation. A reader can observe the file between
// exclusive creation and the completed write, but the partial content never
// validates, so such a reader fails rather than acting on a configuration
// nobody stored.
//
// A failure after the exclusive create removes the file this call created —
// and only that file. O_EXCL proved the path empty at creation, not at
// cleanup, so before removing anything the cleanup confirms the file at the
// destination is still the one this call created; a file someone else stored
// there in the meantime is left in place. A pre-existing file is never
// touched: it makes the open fail before anything is written or removed.
func CreateConfig(metaDir string, config Config) error {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(metaDir, ConfigFile)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	// The created file's identity is taken while the handle is certainly
	// open — after a failed close the handle can no longer answer — so the
	// cleanup can know which file this call created before removing anything.
	created, identityErr := file.Stat()
	_, writeErr := createConfigWrite(file, append(content, '\n'))
	closeErr := createConfigClose(file)
	if writeErr == nil && closeErr == nil {
		return nil
	}
	return abandonPartialConfig(file, path, created, identityErr, writeErr, closeErr)
}

// abandonPartialConfig cleans up after a failure between CreateConfig's
// exclusive creation and its completed write, so the path is free for a retry
// rather than permanently occupied by a partial configuration no reader will
// ever validate and no later creation can replace. The write failure — or the
// close failure when the write succeeded — is what the caller must see;
// everything else the cleanup learns is reported alongside it, never in place
// of it.
//
// The handle is released before the unlink, because an open handle blocks the
// removal on Windows: a failed close may not have released it, so it is closed
// again, and os.ErrClosed from that second attempt means the first close did
// release the handle, which is not a new failure. The file is then removed
// only if it is still the very file this call created — another process may
// have removed the partial file and stored its own configuration in the
// window since creation, and removing by pathname would delete that stranger's
// file and report it as this call's partial write. When identity cannot be
// confirmed, nothing is removed.
func abandonPartialConfig(file *os.File, path string, created os.FileInfo, identityErr, writeErr, closeErr error) error {
	err := writeErr
	if err == nil {
		err = closeErr
	} else if closeErr != nil {
		err = errors.Join(err, fmt.Errorf("closing the partial configuration also failed: %w", closeErr))
	}
	if closeErr != nil {
		if retryErr := file.Close(); retryErr != nil && !errors.Is(retryErr, os.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("the file handle could not be closed: %w", retryErr))
		}
	}
	if identityErr != nil {
		return errors.Join(err, fmt.Errorf("cannot confirm the destination still holds this call's file, so nothing was removed: %w", identityErr))
	}
	current, statErr := os.Stat(path)
	if statErr != nil {
		return errors.Join(err, fmt.Errorf("cannot confirm the destination still holds this call's file, so nothing was removed: %w", statErr))
	}
	if !os.SameFile(created, current) {
		return errors.Join(err, errors.New("the destination now holds a file this call did not create, which was left in place"))
	}
	if removeErr := os.Remove(path); removeErr != nil {
		return errors.Join(err, fmt.Errorf("the partial configuration could not be removed and still occupies its path: %w", removeErr))
	}
	return err
}

// CreateConfig's write and close failures are storage conditions a test
// cannot arrange on a healthy filesystem, so these two indirections exist for
// tests in this package to force each failure branch. Production never
// replaces them.
var (
	createConfigWrite = func(file *os.File, content []byte) (int, error) { return file.Write(content) }
	createConfigClose = func(file *os.File) error { return file.Close() }
)

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
