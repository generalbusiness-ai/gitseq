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
// a half-written file naming no sequence at all. Atomic replacement prevents
// torn reads and nothing else: a writer saving a Config it loaded earlier
// writes back its whole stale view and silently erases whatever other
// processes persisted in between, which is exactly how recorded actor custody
// was lost. SaveConfig is for creating the first configuration in a metadata
// directory; every change to an existing one belongs in UpdateConfig, which
// reloads and merges under a lock instead of trusting process memory.
func SaveConfig(metaDir string, config Config) error {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomically(filepath.Join(metaDir, ConfigFile), append(content, '\n'))
}

// CreateConfig stores a configuration only where none exists yet. Creation is
// exclusive rather than replacing: the content is written and closed at a
// staging file with a unique private name in the same directory, and only the
// completed file is then hard-linked to the destination. Linking fails with
// os.ErrExist whenever the destination holds any entry — of two concurrent
// creators exactly one wins and the other is refused — so a stored genesis is
// never silently overwritten, and a concurrent reader never observes partial
// content at the destination: while the system stays up, the destination is
// either absent or complete. That guarantee covers concurrent readers only,
// not a crash: nothing here syncs the staging file or its directory before
// the link, so a power loss can persist the directory entry ahead of the
// data. A filesystem
// that cannot hard-link refuses the creation and the refusal is reported;
// there is no fallback, per the custody contract in
// docs/reference/architecture.md.
//
// No failure path ever removes the destination. An earlier revision created
// at the destination directly and removed it on failure behind an
// os.SameFile guard, but device and inode numbers are not a durable
// identity: Linux reuses a freed inode eagerly, so after the partial file
// was unlinked, a replacement stored by another process could answer as the
// file this call created, and the cleanup deleted it (GitHub run
// 32670745698). Here the only file any path removes is the staging file, at
// a private name no other process holds, so there is no identity question to
// answer: whatever happens at the destination, it is not cleanup's to
// remove.
func CreateConfig(metaDir string, config Config) error {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(metaDir, "."+ConfigFile+".create-*")
	if err != nil {
		return err
	}
	staging := file.Name()
	err = writeAndCloseStagedConfig(file, append(content, '\n'))
	if err == nil {
		err = os.Link(staging, filepath.Join(metaDir, ConfigFile))
	}
	// Every path — success, write or close failure, link refusal — removes
	// exactly the staging file, so the removal happens once, here. Its
	// private name is not the destination, so a file that could not be
	// removed does not occupy the destination and a retry can still succeed;
	// the caller learns which of the two states the leftover sits beside.
	if removeErr := createConfigRemove(staging); removeErr != nil {
		if err == nil {
			err = fmt.Errorf("the configuration was stored, but its staging file could not be removed and remains beside it: %w", removeErr)
		} else {
			err = errors.Join(err, fmt.Errorf("the abandoned staging file could not be removed and remains at its private name: %w", removeErr))
		}
	}
	return err
}

// writeAndCloseStagedConfig writes the content to the staging file and closes
// the handle, reporting every failure it saw. The write failure — or the
// close failure when the write succeeded — is what the caller must see;
// everything else is reported alongside it, never in place of it.
//
// The handle must be released before the caller removes the staging file,
// because an open handle blocks the removal on Windows: a failed close may
// not have released it, so it is closed again, and os.ErrClosed from that
// second attempt means the first close did release the handle — as POSIX
// close(2) does even when it reports an error — which is not a new failure.
func writeAndCloseStagedConfig(file *os.File, content []byte) error {
	_, writeErr := createConfigWrite(file, content)
	closeErr := createConfigClose(file)
	if closeErr == nil {
		return writeErr
	}
	err := closeErr
	if writeErr != nil {
		err = errors.Join(writeErr, fmt.Errorf("closing the partial configuration also failed: %w", closeErr))
	}
	if retryErr := createConfigRetryClose(file); retryErr != nil && !errors.Is(retryErr, os.ErrClosed) {
		err = errors.Join(err, fmt.Errorf("the file handle could not be closed: %w", retryErr))
	}
	return err
}

// CreateConfig's write, close, retry-close, and removal failures are storage
// conditions a test cannot arrange on a healthy filesystem, so these
// indirections exist for tests in this package to force each failure branch.
// Production never replaces them.
var (
	createConfigWrite      = func(file *os.File, content []byte) (int, error) { return file.Write(content) }
	createConfigClose      = func(file *os.File) error { return file.Close() }
	createConfigRetryClose = func(file *os.File) error { return file.Close() }
	createConfigRemove     = func(path string) error { return os.Remove(path) }
)

// UpdateConfig applies mutate to the configuration currently on disk and
// stores the result atomically, holding an exclusive lock across the whole
// read-modify-write. A process may hold a Config it loaded at open, long
// before other processes changed anything; reloading inside the lock means
// only what mutate changes can differ from what anyone else wrote, and
// everything mutate leaves alone carries forward unchanged. Merge rules are
// therefore the caller's declared intent, field by field, rather than
// last-writer-wins over the file. mutate reports whether it changed anything:
// an update that changes nothing rewrites no file, and the reloaded
// configuration is returned either way so a caller may refresh its own memory
// with custody other processes recorded meanwhile. When no configuration file
// exists yet, base is what mutate starts from instead — some workspace must
// write the first one — and because that choice is made under the same lock,
// a second creator serialises behind the first and merges onto its file
// rather than overwriting it. SaveConfig remains for creating a first
// configuration outside this contract.
func UpdateConfig(metaDir string, base Config, mutate func(*Config) (bool, error)) (Config, error) {
	return withConfigLock(metaDir, func() (Config, error) {
		current, err := LoadConfig(metaDir)
		if errors.Is(err, os.ErrNotExist) {
			current = base
		} else if err != nil {
			return Config{}, err
		}
		changed, err := mutate(&current)
		if err != nil {
			return Config{}, err
		}
		if changed {
			if err := current.Validate(); err != nil {
				return Config{}, err
			}
			if err := SaveConfig(metaDir, current); err != nil {
				return Config{}, err
			}
		}
		return current, nil
	})
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

// validateLockFile keeps a lock name a name. WithMetaLock joins it to
// metaDir, so a caller passing a separator or a parent reference would take
// its lock somewhere other than the directory it named — and two callers
// spelling the same target differently would then serialise on nothing.
// Refusing the shape is cheaper than reasoning about which spellings collide.
func validateLockFile(lockFile string) error {
	if lockFile == "" || lockFile == "." || lockFile == ".." ||
		strings.ContainsAny(lockFile, `/\`) || strings.ContainsRune(lockFile, 0) {
		return fmt.Errorf("lock file %q must be a bare file name", lockFile)
	}
	return nil
}
