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

// LoadConfig reads and validates the configuration in one metadata directory,
// holding the shared side of the configuration lock across the read. An
// update replaces the file by renaming over it, and rename atomicity toward a
// reader that already opened the old file is not promised on every platform
// this code runs on; exclusion is what makes a read complete-or-refused
// everywhere. A reader that cannot take the shared lock refuses rather than
// read uncoordinated — except where the lock file cannot exist at all,
// because the metadata directory does not, which is the same "no
// configuration here" the read itself would report.
func LoadConfig(metaDir string) (Config, error) {
	release, err := lockMetaFile(metaDir, configLockFile, lockFileShared)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read gitseq config (run `gs init` first): %w", err)
		}
		return Config{}, fmt.Errorf("lock gitseq config for reading: %w", err)
	}
	defer release()
	return readStoredConfig(metaDir)
}

// readStoredConfig reads and validates the stored file without coordinating.
// Only a caller already holding the configuration lock — either side — may
// use it, because flock is per open description: a locked caller that went
// back through LoadConfig would open a second description and wait on itself.
// Everyone else goes through LoadConfig.
func readStoredConfig(metaDir string) (Config, error) {
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
// was lost. It therefore takes no lock and is nobody's entry point. Production
// stores a change only as UpdateConfig's step inside the exclusive lock, and
// writes the first record only through CreateConfig, exclusively; SaveConfig
// is otherwise for tests that seed stored state before any concurrency
// begins.
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
// last-writer-wins over the file.
//
// opened is the configuration the caller opened. Every immutable field of it
// — everything except the actor map and the frontier pointer — is compared
// against the stored file before mutate runs, and any divergence refuses the
// whole update without changing a stored byte: a workspace holding a
// different identity is not stale, it opened a different configuration, and
// no change to one may be applied to the other.
//
// An update never creates the record. The stored file's only first write is
// initialization's exclusive CreateConfig, so a missing file here means the
// record was never created or has been removed, and either way the honest
// answer is refusal: creating it from the caller's in-memory snapshot would
// resurrect whatever custody that snapshot still remembers.
//
// mutate reports whether it changed anything: an update that changes nothing
// rewrites no file and returns the stored configuration as it was read — a
// copy taken before mutate ran, so anything mutate wrote into its argument
// before deciding nothing changed is discarded rather than adopted into the
// caller's memory. Either way the returned configuration is the stored one, so
// a caller may refresh its own memory with custody other processes recorded
// meanwhile.
func UpdateConfig(metaDir string, opened Config, mutate func(*Config) (bool, error)) (Config, error) {
	return withConfigLock(metaDir, func() (Config, error) {
		current, err := readStoredConfig(metaDir)
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, errors.New("refuse to update gitseq config: no configuration is stored here, and an update never creates one — only initialization does, exclusively")
		} else if err != nil {
			return Config{}, err
		}
		if immutableFieldsDiverge(current, opened) {
			return Config{}, errors.New("refuse to update gitseq config: an immutable field of the stored file differs from the configuration this workspace opened")
		}
		stored := current.Clone()
		changed, err := mutate(&current)
		if err != nil {
			return Config{}, err
		}
		if !changed {
			return stored, nil
		}
		if err := current.Validate(); err != nil {
			return Config{}, err
		}
		if err := SaveConfig(metaDir, current); err != nil {
			return Config{}, err
		}
		return current, nil
	})
}

// immutableFieldsDiverge compares every field of Config except the two the
// update exists to change — the actor map and the frontier pointer. The
// comparisons below follow Config's field order, so checking this list against
// the struct is one read: a field missing here is either one of those two or
// an omission to fix.
func immutableFieldsDiverge(stored, opened Config) bool {
	return stored.Version != opened.Version ||
		stored.Genesis != opened.Genesis ||
		stored.ObjectFormat != opened.ObjectFormat ||
		stored.PayloadCeiling != opened.PayloadCeiling ||
		stored.IdempotencyNamespace != opened.IdempotencyNamespace ||
		stored.SequencerKey != opened.SequencerKey ||
		stored.ReadOnly != opened.ReadOnly
}

// configLockFile guards the configuration file it sits beside. It is never
// renamed, so a holder's advisory lock lives on one stable file for a whole
// load-modify-store, and the kernel drops it if the process dies: a crash
// cannot leave a stale lock behind. The lock lives on this sidecar rather
// than on the configuration itself because the configuration is replaced by
// rename, and a lock on the old file would guard an object the next writer no
// longer opens. The sidecar is created on first use and never removed —
// removing it would let a later locker lock a fresh file while an earlier one
// still holds the removed one.
const configLockFile = ".config.lock"

// withConfigLock runs fn while holding the exclusive side of the
// configuration lock in metaDir, so concurrent updaters serialise instead of
// racing a lost update through the gap between reading ConfigFile and
// renaming over it.
func withConfigLock[T any](metaDir string, fn func() (T, error)) (T, error) {
	return WithMetaLock(metaDir, configLockFile, fn)
}

// WithMetaLock runs fn while holding an exclusive advisory lock on the named
// lock file inside metaDir. It is the one advisory-lock primitive in this
// repository, and it names no application vocabulary: a caller says which
// file it is serialising on, and the host layer says how a lock is taken and
// released. A second helper elsewhere would be a second answer to the
// crash-safety question this one already answers.
//
// The lock file is created if it does not exist and is never renamed, so the
// kernel drops the lock when the process dies. Each distinct name is a
// distinct lock: config updates take configLockFile and nothing else, so a
// caller holding another name may still update its configuration inside fn.
// A caller must never nest two acquisitions of the same name in one process
// — the lock is per open description, so the inner acquisition would block on
// the outer one forever.
func WithMetaLock[T any](metaDir, lockFile string, fn func() (T, error)) (T, error) {
	var zero T
	if err := validateLockFile(lockFile); err != nil {
		return zero, err
	}
	release, err := lockMetaFile(metaDir, lockFile, lockFileExclusively)
	if err != nil {
		return zero, err
	}
	// A failed unlock cannot un-happen a stored change, and reporting it as
	// the operation's failure would tell the caller a stored change did not
	// happen. The lock dies with its file handle or the process instead.
	defer release()
	return fn()
}

// lockMetaFile opens the named sidecar in metaDir and takes one side of its
// advisory lock, returning the release that unlocks and closes the handle.
// Writers pass lockFileExclusively and hold it for a whole transaction,
// readers pass lockFileShared and hold it across one read, so shared readers
// exclude exclusive writers and rename atomicity toward an open reader is
// never relied on. On a platform with no lock implementation both sides fail
// closed, and no coordinated path proceeds.
func lockMetaFile(metaDir, lockFile string, lock func(*os.File) error) (release func(), err error) {
	file, err := os.OpenFile(filepath.Join(metaDir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if witness := lockAttemptWitness; witness != nil {
		if err := witness(file); err != nil {
			file.Close()
			return nil, err
		}
	}
	if err := lock(file); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		unlockFile(file)
		file.Close()
	}, nil
}

// lockAttemptWitness is nil in production, where lockMetaFile goes straight
// from opening the sidecar to the blocking acquisition. The cross-process
// tests in this package set it — in their child process only — to a probe
// that runs between those two steps, on the very handle the blocking call
// will use, so the child can prove its acquisition was refused while another
// process held the lock. An error from the witness abandons the acquisition
// before any lock is taken, exactly as a failed lock call does.
var lockAttemptWitness func(*os.File) error

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
