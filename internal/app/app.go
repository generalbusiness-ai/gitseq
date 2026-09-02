// Package app joins the semantic-free kernel to the workroom profile for a
// single ordinary Git repository.
package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gitseqhost "github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type ActorView struct {
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Kind        string   `json:"kind,omitempty"`
	Roles       []string `json:"roles"`
	Custody     bool     `json:"custody"`
	// Retired mirrors the durable roster: the principal signed events that
	// remain, and may no longer act. Custody is reported separately because
	// the two can disagree — a retired principal whose key file survives is a
	// custody problem this view is meant to make visible.
	Retired bool `json:"retired,omitempty"`
}

// ResidentQueueDepth bounds the submissions inside the sequencer at once,
// counting the one holding the lock. Gitseq's resident always sets it: the
// kernel treats zero as unbounded, which is the embedding opt-out and not a
// posture this application takes.
const ResidentQueueDepth = 32

type Workspace struct {
	Repo      string
	GitDir    string
	CommonDir string
	MetaDir   string
	Store     gitstore.Store
	// config is the workspace's live configuration state: its actor map and
	// frontier pointer are the memory this workspace mutates. It is
	// unexported so the compiler makes View the only way a configuration
	// leaves this package — as a copy sharing no mutable state with the
	// workspace. Tests outside this package that need to seed custody edit
	// the stored file through apphost.SaveConfig and reopen the workspace,
	// going through the same load path production does.
	config   apphost.Config
	observer observe.Observer

	// configMu guards Config's mutable state — the actor map and the
	// verified-frontier pointer — against both the readers who take copies
	// and the updates that adopt freshly stored values, so a View never
	// observes a half-applied mutation. The scalar fields (Genesis,
	// ObjectFormat, ReadOnly, SequencerKey, PayloadCeiling,
	// IdempotencyNamespace, Version) are written once before the workspace
	// is shared and never again, so reading them takes no lock.
	//
	// Lock order: configMu is the innermost lock. snapshotWithSource and
	// Verify hold snapshotMu when they call rememberVerifiedFrontier, which
	// acquires configMu, so the established order is snapshotMu before
	// configMu. Nothing holding configMu may therefore call into a path that
	// acquires snapshotMu — Act, Snapshot, AcceptSubmission, Verify — and no
	// caller may arrive holding configMu, or it would deadlock against
	// itself: updateConfig takes configMu only around its base read and its
	// adoption, releasing it across apphost.UpdateConfig, where the on-disk
	// advisory lock covers the whole load-modify-store.
	configMu sync.Mutex

	// rereadMu serialises this workspace's one cold resolution path. Every
	// hit is a memory read; only an address that misses re-reads the
	// configuration from disk, and this lock makes concurrent misses share
	// one re-read instead of each paying for their own. It is scoped to the
	// workspace rather than the package so two workrooms never wait on each
	// other's custody: each re-read runs against one MetaDir.
	rereadMu sync.Mutex

	// selected is the interpreter this repository is bound to. It is resolved
	// when the workspace is made and never again, so nothing appended
	// afterwards can change what an open workspace means.
	selected selection

	snapshotMu     sync.Mutex
	snapshotCache  *Snapshot
	snapshotSource SnapshotSource
	snapshotFolder *workroom.Folder
	// snapshotProfile gates application-derived state independently of the
	// kernel's profile-independent verified-event checkpoint.
	snapshotProfile string
	// admissionReader and admissionFolder hold the private verified world the
	// admission guards judge against, on both the signing path and the
	// sequencing path. They deliberately share nothing with the reader-side
	// snapshot: no rollback witness, no checkpoint writes, and no
	// cached-projection reuse, because admission must judge the exact position
	// an event would extend even when a reader would refuse the journey there.
	// Every use takes admissionMu, which nests inside the sequencer lock the
	// post-dedup hook runs under and is free-standing on the signing path.
	admissionMu        sync.Mutex
	admissionReader    *kernel.Reader
	admissionFolder    *workroom.Folder
	flightMu           sync.Mutex
	flight             atomic.Pointer[snapshotFlight]
	rebuildTestGate    func(kernel.Progress)
	projectionTestGate func(int)
	reader             *kernel.Reader
	submitterOnce      sync.Once
	submitter          *kernel.Submitter

	worktreesMu       sync.Mutex
	worktreesCached   []WorktreeView
	repoPathCached    string
	repoRemoteCached  string
	worktreesCachedAt time.Time
}

// snapshotFlight is one shared resident read. Its work outlives any individual
// HTTP reader; callers may stop waiting without cancelling verification for
// everybody else. Closing done publishes result and err to all waiters.
type snapshotFlight struct {
	done     chan struct{}
	progress kernel.AuditProgress
	result   SourcedSnapshot
	err      error
}

// Snapshot is an immutable borrowed view. A Workspace may return its resident
// cached maps and slices directly; callers must not mutate them.
type Snapshot struct {
	Genesis    string              `json:"genesis"`
	Head       string              `json:"head"`
	Depth      int                 `json:"depth"`
	Projection workroom.Projection `json:"projection"`
	Vocabulary workroom.Vocabulary `json:"vocabulary"`
}

// SnapshotSource says how the verified application projection reached its
// current frontier. It is deliberately separate from Snapshot so existing
// status and audit representations do not acquire local cache details.
type SnapshotSource string

const (
	SnapshotSourceSignedCheckpointTail SnapshotSource = "verified_signed_checkpoint_tail"
	SnapshotSourceColdFullAudit        SnapshotSource = "verified_cold_full_audit"
	SnapshotSourceIncrementalTail      SnapshotSource = "verified_incremental_tail"
)

// SourcedSnapshot is the local verification result plus its actual load path.
// Callers that disclose fallback behavior use this instead of guessing from
// elapsed time or resident availability.
type SourcedSnapshot struct {
	Snapshot Snapshot
	Source   SnapshotSource
}

// LocalRepo is local repository state, never part of the durable workroom
// projection. Path is the served checkout's own absolute path, so a reader can
// tell which repository a workroom is showing; `gs serve` refuses any listen
// address that is not loopback, so that reader is already on this host. The
// other checkouts stay basenames in WorktreeView: naming them is enough to
// associate work, and the wider host layout has no reader who needs it.
// Remote is the repository's own remote, and only when it is safe to render as
// a hyperlink: see linkableRemote. It is empty far more often than not, and an
// empty Remote means exactly "show the path with no link".
type LocalRepo struct {
	Path      string         `json:"path"`
	Remote    string         `json:"remote,omitempty"`
	Worktrees []WorktreeView `json:"worktrees"`
}

// Bounds on what one repository's remote configuration may be. Both are far
// above any real repository and exist so that valid-but-oversized
// configuration fails closed — no link — rather than being read whole into a
// resident that serves this on every uncached worktree read.
const (
	maxRemoteConfigBytes = 64 << 10
	maxRemoteCount       = 64
)

var errRemoteConfigTooLarge = errors.New("remote configuration exceeds the local projection limit")

// boundedBuffer collects a subprocess's output up to limit bytes and then
// fails. os/exec stops copying on that failure and reports it from Wait, so
// the caller sees an error rather than a truncated answer it might parse.
type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		return 0, errRemoteConfigTooLarge
	}
	return b.buffer.Write(p)
}

// gitStatedEnvironment is the whole of the environment a read-only Git command
// built here is given, on Unix, which is the boundary this package exercises.
// Nothing is inherited from whoever started this process: repositoryLocalGit
// hands the child a copy of this list and nothing else, so the list is an
// inventory of what these commands may see rather than a filter over what they
// were handed. On Windows the claim is false and the qualification belongs
// here rather than further down — Go's os/exec adds SYSTEMROOT to what the
// child receives — and the paragraph below says what that means.
//
// It is an inventory because the denied set it replaces was escaped four times
// in four rounds of review, and each escape arrived from a direction the
// previous list had not thought of. GIT_CONFIG_COUNT with its KEY_n/VALUE_n
// pairs writes configuration straight into the command scope, which outranks
// every file and needs no file at all. GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM
// name a configuration *file*, and a Git configuration file is not data:
// core.fsmonitor, core.pager and diff.external name programs, and Git runs
// them, so naming such a variable in an allowlist bounds nothing whatever about
// what the file it points at says. HOME reaches ~/.gitconfig with no
// GIT_CONFIG variable set at all. And with the system and global scopes pinned
// shut, HOME was still reached from inside the scope that has to stay admitted,
// because a repository-local `include.path = ~/attack.cfg` expands the tilde
// against it; while XDG_CONFIG_HOME needs no configuration file whatever,
// because the default ignore and attributes files are read on their own and can
// move the status answer without executing anything. Each escape was closed and
// the next one arrived. The pattern is not that the list was careless; it is
// that a list of forbidden names is the wrong shape for the question, because
// the channel that matters next is the one nobody here has heard of.
//
// So this answers the opposite question: what do these commands need in order
// to run? Measured, the answer is nothing. All five commands built by
// repositoryLocalGit complete with no inherited variable whatever — on darwin
// with git 2.50 and on linux with git 2.53 — so the list below is the entire
// environment and every name absent from it is absent because it was never
// copied, a future Git release's variables included.
//
// PATH is not forwarded either, and that is worth stating because an earlier
// round of this did forward it. It is not needed: exec.CommandContext resolves
// `git` through exec.LookPath against *this* process's PATH and records the
// absolute result on the command before any child environment exists, so the
// child never consults PATH to find git. Nor does Git lose the ability to
// resolve a bare program name of its own: measured with a repository-local
// `core.fsmonitor = touch <path>`, which ran with this list as the whole
// environment. Whichever search path Git used to find it — this did not measure
// that — the caller did not name it, because the child has no PATH to name it
// with.
//
// Which binary named `git` that parent-side exec.LookPath finds is a real trust
// boundary, and it is a *deployment* one that this constructor does not close
// and cannot. The choice is already made by the time this list is used.
// Forwarding PATH would not have closed it either; it would only have handed
// the caller the second resolution as well. Closing it takes an absolute path
// to a trusted git, chosen by whoever deploys this. That is named here so
// nobody reads an empty inherited set as a claim that these commands are
// running the git this package meant. What the list bounds is what git is told
// once it is running, which is less than that.
//
// The closed-environment property above is stated for Unix, which is where it
// is exercised. This package also builds for Windows, and there it is false:
// Go's os/exec appends SYSTEMROOT from this process to the child's environment
// whenever the environment it is handed carries no name folding to SYSTEMROOT,
// which this list does not. Windows is therefore outside the contract this
// comment states rather than a caveat inside it, and nothing here has been run
// on it. Note also where the append happens — os/exec builds the child's
// environment at start time and does not modify Cmd.Env — so a test reading
// Cmd.Env sees this list exactly on every platform and is not evidence about
// what a Windows child receives.
//
// Naming a value rather than leaving the variable out is the same decision the
// configuration pins already embodied, made once more: dropping HOME would
// leave what these reads look at to whatever Git does with an unset variable,
// which is a property of the Git
// on the machine rather than a contract this package holds. Measured today,
// every command built here completes with no HOME at all on darwin with git
// 2.50 and on linux with git 2.53; naming the location is how that stays true
// of the next Git and of an implementation nobody here has tried.
//
// os.DevNull is that location. It is not a directory this package creates: a
// path that cannot hold a directory entry is a stronger statement than a
// directory that happens to be empty right now, it cannot be raced or filled in
// by anyone between the two commands of a single projection, and it needs no
// creation, no cleanup and no error path in a constructor that has nowhere to
// report one. Every path Git derives from these two variables — ~/.gitconfig,
// $XDG_CONFIG_HOME/git/config, the $XDG_CONFIG_HOME/git/ignore and
// $XDG_CONFIG_HOME/git/attributes defaults, the $HOME/.config/git fallbacks for
// both, and the tilde in an `include.path` inside an admitted repository-local
// or worktree scope — resolves under it and finds nothing.
//
// The three GIT_CONFIG pins stay, and they are not made redundant by pointing
// HOME at nothing. GIT_CONFIG_NOSYSTEM and GIT_CONFIG_SYSTEM close the system
// scope, whose path is compiled into Git and owes nothing to HOME. And
// GIT_CONFIG_GLOBAL states the global scope directly: "the global scope is
// os.DevNull" is the contract, whereas "HOME points at nothing" is a fact about
// a different variable that implies the contract only for as long as Git
// derives the global scope from HOME the way it does today.
//
// SUDO_UID is neutralised here by being absent, which an inventory gives for
// free and is the reason it is worth naming. Git treats a repository
// as owned by the current user before it will parse that repository's
// configuration at all, and when Git runs as root on a platform with sudo it
// widens that test: it reads SUDO_UID and trusts repositories owned by the uid
// recorded there as well as by root. Under `sudo`, therefore, an inherited
// SUDO_UID makes another user's repository readable, and reading a repository
// means parsing its configuration file, which is the executable channel this
// whole contract is about. Git's own documentation prescribes exactly this
// remedy: remove SUDO_UID from the environment before invoking git.
//
// The cost is real and is stated rather than hidden. Two ownership allowances
// are now gone from these five commands: safe.directory, which Git honours only
// from protected configuration and so only from the scopes pinned above, and
// the sudo widening just described. A deployment that runs this under `sudo`
// against a repository owned by the invoking user will find these reads
// refused. That is a visible error and never a wrong answer — the worktree list
// fails, and citingDocuments turns a refused citation lookup into a refused
// retirement rather than a silent pass — and the remedy is to run as the owner,
// not to reopen a channel whose contents are executable.
//
// This mechanism should later be shared with internal/gitstore, which bounds
// its own Git environment separately and closes less of this: one statement of
// what Git may see, in one place, is what stops the two drifting apart. That is
// deliberately not done here. It changes a second package's contract, it is
// gated on a pending ruling, and it belongs to its own request.
var gitStatedEnvironment = []string{
	"HOME=" + os.DevNull,
	"XDG_CONFIG_HOME=" + os.DevNull,
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_SYSTEM=" + os.DevNull,
	"GIT_CONFIG_GLOBAL=" + os.DevNull,
}

// repositoryLocalGit builds every read-only Git command this package runs
// against a checkout, so that "the repository at repo" is what Git actually
// reads. There is one constructor rather than a line at each of the five call
// sites because the defect it closes is invisible at a call site: every one of
// those commands looked correct, named its repository with -C, and answered out
// of whichever repository the ambient environment pointed at. A site that forgot
// the bounding would look exactly like one that did not, so the only reliable
// place to put it is where the command is built.
//
// What the bounding leaves admitted is a scope, not a file, and stating it as
// "the repository's own configuration file" understates it by three cases that
// were measured here on git 2.50. The scope is: the repository's own
// configuration; its worktree configuration at $GIT_DIR/config.worktree, which
// Git reads once the repository sets extensions.worktreeConfig; and whatever
// `include.path` or an `includeIf` condition inside either of those names, by
// absolute path or relative one. Every part of it is executable rather than
// merely readable — `core.fsmonitor` placed in the repository's configuration,
// in its worktree configuration, behind a relative include, behind an absolute
// include, and behind an `includeIf gitdir:` condition each ran a program of its
// author's choosing during an ordinary `git status` under exactly the
// environment stated above. Admitting that scope is not an oversight: reading
// the repository Git was pointed at is what these five answers are made of, and
// a caller who can already write into that repository's .git directory is not a
// caller this bound was ever meant to hold out.
//
// What the bound does is narrower than "keeps that scope to itself", and saying
// it that way would be false against the paragraph above: an absolute include
// is that scope reaching outside itself, and it stays admitted. What is held is
// that ambient system, global and environment routing can neither redirect
// which repository Git reads nor add configuration to what it finds there.
// Everything the repository itself delegates to — its own configuration, its
// worktree configuration, and every include target reached recursively from
// either — remains trusted and executable.
//
// The environment is a copy rather than gitStatedEnvironment itself, so that a
// caller adjusting one command's Env cannot alter what the next command gets.
//
// --no-optional-locks keeps a read from writing to a repository somebody else
// may be using; it belongs to every one of these commands for the same reason
// the environment does.
func repositoryLocalGit(ctx context.Context, repo string, arguments ...string) *exec.Cmd {
	argv := append([]string{"--no-optional-locks", "-C", repo}, arguments...)
	command := exec.CommandContext(ctx, "git", argv...)
	command.Env = slices.Clone(gitStatedEnvironment)
	return command
}

// gitRemotes reads the remote URLs this repository itself configures.
//
// Two separate bounds make that word "itself" true, and neither substitutes
// for the other. --local answers "whose configuration file?": without it the
// answer is the merge of system, global, command (GIT_CONFIG_COUNT) and local
// scopes, and the outer value is emitted last, so it would win the map below.
// The sanitised environment answers "which repository?": --local bounds the
// scope Git reads and says nothing at all about which repository Git resolved
// before reading anything, so GIT_DIR or GIT_COMMON_DIR alone would make this
// disclose another repository's remote through a read that is, by its own
// lights, strictly local. The repository's own config file is the only thing
// this is entitled to disclose, and it takes both bounds to name it.
//
// --no-includes is the third word in that phrase, "config *file*", made
// explicit. Unlike the commands repositoryLocalGit builds elsewhere, this one
// is meant to read exactly one file, and Git already reads it that way: git
// config documents --includes as defaulting off when a scope or file is named
// and on when it is searching all config files, and that default was checked
// here on git 2.50 — a `[remote "origin"] url` behind an `include.path` in
// .git/config is invisible to `git config --local --get-regexp`, and visible
// again with --includes added. So the flag pins a documented default rather
// than changing what this read returns today, which is the whole reason to
// spell it: the bound should be a decision this function made and not one it
// inherited.
//
// It has a cost, and the cost is the same with the flag as without it, since
// the behaviour is unchanged. A repository that reaches its remotes through an
// include, or that keeps them in worktree configuration — also outside --local,
// checked the same way — reports no remote here and its bar shows no link.
// That is a display gap rather than a wrong answer, and widening the read to
// close it would give up the scope bound that makes the disclosed URL the
// repository's own.
//
// The --null form separates records with NUL and the key from its value with a
// newline, so a URL containing spaces or a name containing dots stays
// unambiguous. A repository with no remotes makes `git config --get-regexp`
// exit non-zero, which is not an error here: it is the ordinary answer "none".
func gitRemotes(ctx context.Context, repo string) map[string]string {
	command := repositoryLocalGit(ctx, repo, "config", "--local", "--no-includes", "--null", "--get-regexp", `^remote\..*\.url$`)
	output := &boundedBuffer{limit: maxRemoteConfigBytes}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return nil
	}
	remotes := make(map[string]string)
	for _, record := range strings.Split(output.buffer.String(), "\x00") {
		if record == "" {
			continue
		}
		key, value, ok := strings.Cut(record, "\n")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name == "" {
			continue
		}
		if _, known := remotes[name]; !known && len(remotes) >= maxRemoteCount {
			// Refuse rather than truncate: a truncated set would make which
			// remote gets linked depend on where the reading stopped.
			return nil
		}
		remotes[name] = value
	}
	return remotes
}

// linkableRemote picks the one remote a reader would follow, then admits it
// only if it is safe to link. The selection rule is deliberately dull: origin
// when the repository has one, otherwise the lexicographically first remote
// name. Selecting by name *before* filtering by scheme keeps the answer
// predictable — the bar links the repository's own remote or nothing, never
// some other remote that merely happened to carry a friendlier scheme.
func linkableRemote(remotes map[string]string) string {
	if len(remotes) == 0 {
		return ""
	}
	name := "origin"
	if _, ok := remotes[name]; !ok {
		name = ""
		for candidate := range remotes {
			if name == "" || candidate < name {
				name = candidate
			}
		}
	}
	return webRemoteURL(remotes[name])
}

// webRemoteURL is an allowlist: http and https are admitted and everything else
// is refused, including ssh, scp-style git@host:org/repo, file, and anything
// that does not parse. Nothing is matched against a list of dangerous schemes,
// so a scheme nobody thought of is refused by default rather than admitted.
//
// Userinfo declines the URL outright instead of being stripped. A remote of the
// form https://x-access-token:SECRET@host/org/repo carries a credential, and
// declining keeps it out of this response body altogether rather than trusting
// every later rendering path to drop it again.
//
// A query or fragment is declined for exactly that reason. ?access_token=SECRET
// and #access_token=SECRET carry credential material as readily as userinfo
// does, and admitting them while declining userinfo would be one rule applied
// to one syntax. What this links is a repository's own address, which needs
// neither component, so refusing both costs nothing real and needs no judgement
// about which parameter names are secret — an enumeration that would be a
// denylist, the thing the scheme rule is careful not to be.
//
// The test is made on the raw input, before parsing, because parsing is where
// the evidence is lost. url.Parse always reads a bare ? or # as the query or
// fragment delimiter — anywhere else the character has to arrive
// percent-encoded, so a raw one in the input can only be that delimiter, and
// no case is refused here that the parser would have called path or host. What
// parsing costs is the empty fragment: url.URL records an empty query as
// ForceQuery and puts the ? back, but "…/repo.git#" parses to an empty
// Fragment that leaves no trace in the serialisation at all. Testing the
// serialised form therefore admitted that one URL and emitted it with the #
// dropped, while the browser's URL keeps the trailing # and
// ui/src/lib/repolink.ts refuses it — one rule, two answers, and a link the
// page then declined to draw. The raw test is the same rule for both empty
// delimiters and one fewer thing to reason about.
func webRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "?#") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return ""
	}
	if parsed.User != nil || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

// WorktreeView is one checkout of the repository. Checkout is a display label
// rather than an absolute path.
type WorktreeView struct {
	Checkout string `json:"checkout"`
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	State    string `json:"state"` // clean | dirty | unavailable | bare | locked | prunable
	Current  bool   `json:"current,omitempty"`
	Detached bool   `json:"detached,omitempty"`
}

type Verb string

const (
	VerbState               Verb = "state"
	VerbRatify              Verb = "ratify"
	VerbSupersede           Verb = "supersede"
	VerbRetireIfUnclaimed   Verb = "retire-if-unclaimed"
	VerbReassignIfUnclaimed Verb = "reassign-if-unclaimed"
)

// Act is the one application command accepted by every local adapter. RestsOn
// contains all bases for state and only additional bases for retirement acts;
// ratify and every retirement place their target first. A guarded replacement
// places Retirement first.
type Act struct {
	Verb   Verb
	Kind   workroom.Kind
	Text   string
	Body   map[string]string
	Target string
	// Retirement names the effective guarded retirement consumed by a
	// reassign-if-unclaimed replacement.
	Retirement     string
	RestsOn        []string
	Attachments    map[string][]byte
	IdempotencyKey string

	// CitedOK lets a caller retire a record the documentation still names.
	// A migration legitimately retires first and re-anchors after, so the
	// escape has to exist — but it must be asked for, and only a surface
	// that can offer that choice to a person should ever set it.
	CitedOK bool

	// AllowDeadBasis is the explicit escape for a state resting on a basis
	// that admission already shows to be retired. Asking for it signs
	// body.dead_basis_override=true: testimony that the author saw the dead
	// bases, not a repair of them. A basis that is merely stale needs no
	// escape; admission records it on the act instead.
	AllowDeadBasis bool

	// GuardedReview marks an act whose body was built by internal/reviewguard.
	// It is process-local and never accepted as input: only its presence lets
	// the reserved review fields through the builder, and their contents are
	// still judged by admission against the workroom.
	GuardedReview bool
}

type Submission struct {
	Result kernel.Result   `json:"result"`
	Record workroom.Record `json:"record"`
}

// LocalWorktrees projects the served checkout and every linked checkout of its
// repository without writing anything to git or the workroom. Git's porcelain
// -z format keeps spaces and other path characters unambiguous; of the paths it
// reports, only the served checkout's own leaves this boundary.
func (w *Workspace) LocalWorktrees(ctx context.Context) (LocalRepo, error) {
	w.worktreesMu.Lock()
	defer w.worktreesMu.Unlock()
	if age := time.Since(w.worktreesCachedAt); !w.worktreesCachedAt.IsZero() && age >= 0 && age < 8*time.Second {
		return LocalRepo{Path: w.repoPathCached, Remote: w.repoRemoteCached, Worktrees: append([]WorktreeView(nil), w.worktreesCached...)}, nil
	}
	output, err := repositoryLocalGit(ctx, w.Repo, "worktree", "list", "--porcelain", "-z").Output()
	if err != nil {
		return LocalRepo{}, fmt.Errorf("list worktrees: %w", err)
	}
	type entry struct {
		path     string
		head     string
		branch   string
		detached bool
		bare     bool
		locked   bool
		prunable bool
	}
	var entries []entry
	var current entry
	flush := func() {
		if current.path != "" {
			entries = append(entries, current)
		}
		current = entry{}
	}
	for _, field := range strings.Split(string(output), "\x00") {
		if field == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(field, " ")
		switch key {
		case "worktree":
			current.path = value
		case "HEAD":
			current.head = value
		case "branch":
			current.branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			current.detached = true
		case "bare":
			current.bare = true
		case "locked":
			current.locked = true
		case "prunable":
			current.prunable = true
		}
	}
	flush()
	if len(entries) > 128 {
		return LocalRepo{}, fmt.Errorf("repository has %d worktrees; local projection limit is 128", len(entries))
	}

	canonicalPath := func(path string) string {
		absolute, absErr := filepath.Abs(path)
		if absErr != nil {
			return filepath.Clean(path)
		}
		if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
			absolute = resolved
		}
		return filepath.Clean(absolute)
	}
	selectedPath := w.Repo
	if top, topErr := repositoryLocalGit(ctx, w.Repo, "rev-parse", "--show-toplevel").Output(); topErr == nil {
		selectedPath = strings.TrimSpace(string(top))
	}
	selected := canonicalPath(selectedPath)
	views := make([]WorktreeView, 0, len(entries))
	inspectionCtx, cancelInspection := context.WithTimeout(ctx, 3*time.Second)
	defer cancelInspection()
	for _, item := range entries {
		view := WorktreeView{
			Checkout: filepath.Base(filepath.Clean(item.path)),
			Branch:   item.branch,
			Head:     item.head,
			State:    "unavailable",
			Current:  canonicalPath(item.path) == selected,
			Detached: item.detached,
		}
		switch {
		case item.bare:
			view.State = "bare"
		case item.prunable:
			view.State = "prunable"
		case item.locked:
			view.State = "locked"
		default:
			statusCtx, cancel := context.WithTimeout(inspectionCtx, 750*time.Millisecond)
			status, statusErr := repositoryLocalGit(statusCtx, item.path, "status", "--porcelain=v1", "--untracked-files=normal").Output()
			cancel()
			if statusErr == nil {
				view.State = "clean"
				if len(status) > 0 {
					view.State = "dirty"
				}
			}
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Current != views[j].Current {
			return views[i].Current
		}
		if views[i].Branch != views[j].Branch {
			return views[i].Branch < views[j].Branch
		}
		return views[i].Checkout < views[j].Checkout
	})
	remote := linkableRemote(gitRemotes(ctx, w.Repo))
	w.worktreesCached = append(w.worktreesCached[:0], views...)
	w.repoPathCached = selected
	w.repoRemoteCached = remote
	w.worktreesCachedAt = time.Now()
	return LocalRepo{Path: selected, Remote: remote, Worktrees: append([]WorktreeView(nil), views...)}, nil
}

func Open(ctx context.Context, repo string) (*Workspace, error) {
	return OpenObserved(ctx, repo, nil)
}

// OpenObserved opens a workspace with an exporter-neutral observer. Ordinary
// callers use Open and pay no observation cost.
func OpenObserved(ctx context.Context, repo string, observer observe.Observer) (*Workspace, error) {
	gitDir, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	metaDir := apphost.MetaDir(commonDir)
	config, err := apphost.LoadConfig(metaDir)
	if err != nil {
		return nil, err
	}
	workspace := &Workspace{Repo: repo, GitDir: gitDir, CommonDir: commonDir, MetaDir: metaDir, Store: gitstore.Store{Repo: commonDir, Observer: observer}, config: config, observer: observer}
	// Which application interprets this log is settled here, once, before the
	// workspace can fold or append anything. Reading the binding later would
	// make the answer depend on what happened after the open: a replacement
	// recorded meanwhile would silently change the meaning of a workspace
	// already in use. A repository whose log cannot be read has no answer to
	// give, so it does not open.
	selected, err := workspace.selectHost(ctx)
	if err != nil {
		return nil, err
	}
	workspace.selected = selected
	return workspace, nil
}

// SetObserver configures observation before a workspace begins serving.
func (w *Workspace) SetObserver(observer observe.Observer) {
	w.observer = observer
	w.Store.Observer = observer
}

// Init opens a repository for the application this build runs.
func Init(ctx context.Context, repo, operatorName string, ceiling uint64) (*Workspace, workroom.Record, error) {
	return initHosted(ctx, repo, operatorName, ceiling, workroomHost)
}

// initHosted binds a new repository to one application for life, and records
// that binding in its opening records — except for the application an absent
// binding already names. Recording it there would say nothing a reader does
// not already know, and would put a record in the opening of every workroom
// log to say so.
func initHosted(ctx context.Context, repo, operatorName string, ceiling uint64, running host) (*Workspace, workroom.Record, error) {
	if operatorName == "" {
		operatorName = "operator"
	}
	if ceiling == 0 {
		ceiling = 1 << 20
	}
	gitDir, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	metaDir := apphost.MetaDir(commonDir)
	if _, err := os.Stat(filepath.Join(metaDir, apphost.ConfigFile)); err == nil {
		return nil, workroom.Record{}, errors.New("workroom already initialized")
	}
	if err := os.MkdirAll(filepath.Join(metaDir, "actors"), 0o700); err != nil {
		return nil, workroom.Record{}, err
	}
	store := gitstore.Store{Repo: commonDir}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	sequencerKey := filepath.Join(metaDir, "sequencer")
	publicKey, err := gitstore.GenerateSSHKey(ctx, sequencerKey)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	private, fingerprint, actorPath, err := generateActor(filepath.Join(metaDir, "actors"), operatorName)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	genesis, err := kernel.Create(ctx, store, kernel.GenesisDescriptor{Version: 0, ObjectFormat: format, PayloadCeiling: ceiling, SequencerPublicKey: publicKey}, sequencerKey)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	workspace := &Workspace{Repo: repo, GitDir: gitDir, CommonDir: commonDir, MetaDir: metaDir, Store: store, config: apphost.Config{
		Version: 0, Genesis: genesis, ObjectFormat: format, PayloadCeiling: ceiling, IdempotencyNamespace: "workroom/v0",
		SequencerKey: sequencerKey, Actors: map[string]apphost.Actor{operatorName: {Name: operatorName, Fingerprint: fingerprint, KeyFile: actorPath}},
	}}
	workspace.selected = selection{host: running}
	request, err := workspace.BuildActRequest(ctx, private, operatorName, Act{
		Verb: VerbState, Kind: workroom.KindRoster, Text: operatorName + " begins the workroom",
		Body:           map[string]string{"actor": fingerprint, "kind": "human", "name": operatorName, "role": "operator"},
		IdempotencyKey: "bootstrap",
	})
	if err != nil {
		return nil, workroom.Record{}, err
	}
	submission, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	if running.application != apphost.DefaultApplication {
		bindingRequest, err := workspace.buildBindingRequest(ctx, private, operatorName, selfBinding(running))
		if err != nil {
			return nil, workroom.Record{}, err
		}
		if _, err := workspace.AcceptSubmission(ctx, bindingRequest); err != nil {
			return nil, workroom.Record{}, err
		}
	}
	workspace.configMu.Lock()
	err = workspace.save()
	workspace.configMu.Unlock()
	if err != nil {
		return nil, workroom.Record{}, err
	}
	return workspace, submission.Record, nil
}

func generateActor(directory, name string) (ed25519.PrivateKey, string, string, error) {
	if name == "" || strings.ContainsAny(name, "/\\\x00\r\n") {
		return nil, "", "", errors.New("invalid actor name")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", "", err
	}
	path := filepath.Join(directory, name+".key")
	if _, err := os.Stat(path); err == nil {
		return nil, "", "", errors.New("actor key already exists")
	}
	if err := os.WriteFile(path, []byte(base64.RawURLEncoding.EncodeToString(private)+"\n"), 0o600); err != nil {
		return nil, "", "", err
	}
	return private, intent.ActorFingerprint(private.Public().(ed25519.PublicKey)), path, nil
}

func readActor(path string) (ed25519.PrivateKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(content)))
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid actor private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

// save writes the whole remembered configuration. Only the bootstrap creation
// path uses it — the workspace that runs init has just written this very file
// itself, so there is no concurrent custody to lose — because writing a whole
// Config held in memory would erase whatever custody another process
// persisted since this one loaded. Every change to an existing configuration
// goes through updateConfig.
func (w *Workspace) save() error {
	return apphost.SaveConfig(w.MetaDir, w.config)
}

// View is how a configuration leaves this workspace: as a value sharing no
// mutable state with it. Handing out the config field itself would alias the
// live actor map and frontier pointer, so a holder could mutate custody state
// it was never handed, and two goroutines could race on it. Cloning at the
// boundary makes the returned value the caller's alone, and cloning under
// configMu makes the copy a consistent point-in-time observation rather than
// a read racing a concurrent mutation.
func (w *Workspace) View() apphost.Config {
	w.configMu.Lock()
	defer w.configMu.Unlock()
	return w.config.Clone()
}

// attachAbsenceGate runs on the attach path that has observed no stored
// configuration and not yet created one. It exists so a test can pin a
// concurrent creator into exactly that window; production leaves it empty.
var attachAbsenceGate = func() {}

// updateConfig persists one declared change without losing what concurrent
// processes wrote: the file is reloaded under the apphost lock, mutate
// changes exactly what this save owns on that fresh copy, and only the
// merged result's mutable custody fields — the actor map and the verified
// frontier — are adopted as the workspace's view, so stale memory refreshes
// from disk even when this save itself changes nothing while the
// written-once scalar fields stay exactly as opened. A failed update leaves
// both the file and the workspace unchanged. This workspace's
// own memory is only ever a starting point, and then solely for the metadata
// directory that holds no configuration yet — an attached view recording its
// first frontier — where someone must write a first file and nobody else's
// custody can be lost.
//
// configMu is taken here, around the base read and the adoption, and nowhere
// else may callers hold it: configMu stays innermost, the apphost lock is
// acquired only inside apphost.UpdateConfig, and neither waits on snapshotMu,
// which the durable-append paths hold. Callers therefore must not arrive
// holding configMu, or they would deadlock against themselves.
func (w *Workspace) updateConfig(mutate func(*apphost.Config) (bool, error)) error {
	w.configMu.Lock()
	base := w.config.Clone()
	w.configMu.Unlock()
	merged, err := apphost.UpdateConfig(w.MetaDir, base, mutate)
	if err != nil {
		return err
	}
	w.configMu.Lock()
	defer w.configMu.Unlock()
	// Only the custody fields move after open; the scalar fields stay
	// written-once, so they are adopted never and remain safe to read
	// without configMu.
	w.config.Actors = merged.Actors
	w.config.VerifiedFrontier = merged.VerifiedFrontier
	return nil
}

func AttachConfig(ctx context.Context, repo, genesis, objectFormat string) (*Workspace, error) {
	if err := apphost.ValidateGenesis(objectFormat, genesis); err != nil {
		return nil, fmt.Errorf("invalid attachment genesis: %w", err)
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	metaDir := apphost.MetaDir(commonDir)
	if _, err := os.Stat(filepath.Join(metaDir, apphost.ConfigFile)); errors.Is(err, os.ErrNotExist) {
		attachAbsenceGate()
		if err := os.MkdirAll(metaDir, 0o700); err != nil {
			return nil, err
		}
		created := apphost.Config{Version: 0, Genesis: genesis, ObjectFormat: objectFormat, ReadOnly: true}
		if err := apphost.CreateConfig(metaDir, created); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// os.ErrExist means a concurrent attach created the configuration
		// after the absence check. The stored one, not this call's argument,
		// is now the one to answer for, and the comparison below judges it.
	} else if err != nil {
		return nil, err
	}
	// The configuration exists, so opening it is what selects the interpreter.
	// Attaching and opening then reach the same answer by the same path, and no
	// workspace leaves this package without one. Comparing after the open — on
	// the creating path too — makes a reported success an observation of the
	// stored genesis rather than an echo of the argument: an attach whose
	// creation lost the race fails here instead of silently answering for a
	// sequence it never stored.
	workspace, err := Open(ctx, repo)
	if err != nil {
		return nil, err
	}
	if !workspace.config.ReadOnly {
		return nil, errors.New("cannot attach over a writable workroom")
	}
	if workspace.config.Genesis != genesis {
		return nil, errors.New("attached workroom genesis does not match --genesis")
	}
	if workspace.config.ObjectFormat != objectFormat {
		return nil, errors.New("attached workroom object format changed")
	}
	return workspace, nil
}

func (w *Workspace) Actor(name string) (apphost.Actor, ed25519.PrivateKey, error) {
	actor, err := w.ResolveActor(name)
	if err != nil {
		return apphost.Actor{}, nil, err
	}
	private, err := readActor(actor.KeyFile)
	return actor, private, err
}

// custodyRereadGate runs after a custody re-read has loaded the fresh record
// and before that record is reconciled into the workspace's view. Production
// leaves it empty. Tests pin the window between the load and the adoption so
// an interleaving with updateConfig is certain rather than hoped for, the
// same way attachAbsenceGate pins the attach window.
var custodyRereadGate = func() {}

// ErrUnknownActor marks an address that still answers nobody after a fresh
// re-read of the configuration custody record. Callers classify it with
// errors.Is rather than message text, and it stays apart from the I/O failure
// a re-read itself can hit, so a caller can never report a live actor as
// unknown merely because its roster had not been re-read.
var ErrUnknownActor = errors.New("addresses no known actor")

// ResolveActor addresses one actor by name against this workspace's local
// configuration custody. The in-memory view answers first and costs no
// filesystem call; only a miss re-reads config.json once, so an actor another
// process added after this workspace was opened resolves without reopening —
// the cached lifetime, not the custody record, was the defect. The two ways
// to fail stay apart on purpose: an address that still matches nobody after
// the fresh read says so, while a configuration that cannot be read reports
// its own I/O failure, so a caller can never report a live actor as unknown.
// On success the fresh record is reconciled into the workspace's view,
// keeping every later hit off the disk.
func (w *Workspace) ResolveActor(name string) (apphost.Actor, error) {
	return w.resolveCustody(name, func(c *apphost.Config) (apphost.Actor, bool) {
		actor, ok := c.Actors[name]
		return actor, ok
	})
}

func (w *Workspace) AddActor(ctx context.Context, operatorName, name, kind string) (apphost.Actor, []workroom.Record, error) {
	w.configMu.Lock()
	_, exists := w.config.Actors[name]
	w.configMu.Unlock()
	if exists {
		return apphost.Actor{}, nil, fmt.Errorf("actor %q already exists", name)
	}
	if kind == "" {
		kind = "agent"
	}
	if !workroom.IsActorKind(kind) {
		return apphost.Actor{}, nil, fmt.Errorf("actor kind must be human, agent, or service, got %q", kind)
	}
	private, fingerprint, path, err := generateActor(filepath.Join(w.MetaDir, "actors"), name)
	if err != nil {
		return apphost.Actor{}, nil, err
	}
	_ = private
	actor := apphost.Actor{Name: name, Fingerprint: fingerprint, KeyFile: path}
	stateSubmission, err := w.Act(ctx, operatorName, Act{Verb: VerbState, Kind: workroom.KindRoster, Text: name + " joins as " + kind, Body: map[string]string{"actor": fingerprint, "kind": kind, "name": name, "role": "participant"}, RestsOn: []string{w.EventID(w.config.Genesis)}, IdempotencyKey: "actor-" + name})
	if err != nil {
		return apphost.Actor{}, nil, err
	}
	state := stateSubmission.Record
	ratificationSubmission, err := w.Act(ctx, operatorName, Act{Verb: VerbRatify, Target: state.ID, IdempotencyKey: "actor-" + name + "-ratify"})
	if err != nil {
		return apphost.Actor{}, nil, err
	}
	ratification := ratificationSubmission.Record
	// The durable appends above take snapshotMu, so configMu was released
	// across them. updateConfig reloads the file under the apphost lock, so
	// custody granted here merges onto whatever other processes recorded
	// since this workspace loaded, and a failed update leaves both the file
	// and this workspace's view unchanged.
	if err := w.updateConfig(func(c *apphost.Config) (bool, error) {
		if existing, exists := c.Actors[name]; exists {
			if existing != actor {
				return false, fmt.Errorf("config already holds different custody for actor %q", name)
			}
			return false, nil
		}
		if c.Actors == nil {
			c.Actors = make(map[string]apphost.Actor)
		}
		c.Actors[name] = actor
		return true, nil
	}); err != nil {
		return apphost.Actor{}, nil, err
	}
	return actor, []workroom.Record{state, ratification}, nil
}

// GrantRole records and ratifies an authority grant independently of the
// actor's descriptive kind. The fold, not this application edge, decides
// whether the attempted ratification has authority.
func (w *Workspace) GrantRole(ctx context.Context, grantorName, actorAddress, role string) ([]workroom.Record, error) {
	if err := validateAuthorityRole(role); err != nil {
		return nil, err
	}
	actor, err := w.ResolveActorAddress(actorAddress)
	if err != nil {
		return nil, err
	}
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	actorState, exists := snapshot.Projection.Actors[actor.Fingerprint]
	if !exists || actorState.Retired || actorState.MembershipEvent == "" {
		return nil, fmt.Errorf("actor %q has no live roster membership", actor.Name)
	}
	if containsRole(actorState.Roles, role) {
		return nil, fmt.Errorf("actor %q already has role %q", actor.Name, role)
	}
	basis := actorState.MembershipEvent
	key := "role-" + actor.Name + "-" + role + "-" + snapshot.Head
	stateSubmission, err := w.Act(ctx, grantorName, Act{
		Verb: VerbState, Kind: workroom.KindRoster, Text: "grant " + role + " to " + actor.Name,
		Body:    map[string]string{"actor": actor.Fingerprint, "kind": actorState.Kind, "name": actor.Name, "role": role},
		RestsOn: []string{basis}, IdempotencyKey: key,
	})
	if err != nil {
		return nil, err
	}
	state := stateSubmission.Record
	ratificationSubmission, err := w.Act(ctx, grantorName, Act{Verb: VerbRatify, Target: state.ID, IdempotencyKey: key + "-ratify"})
	if err != nil {
		return nil, err
	}
	return []workroom.Record{state, ratificationSubmission.Record}, nil
}

// RetireActor ends a principal's membership and its local custody together.
// Retirement is a supersession of the membership statement, so the fold keeps
// the principal visible with no roles rather than forgetting that it acted;
// what stops it acting again is the fold: after retirement the principal is
// no longer a live participant, so a later state or ratification it signs is
// judged ineffective however the record reaches the log. Deleting the key is
// therefore hygiene rather than the boundary — it was the boundary before the
// fold enforced membership, and a comment still saying so would send a reader
// looking for the guard in the wrong layer. Leaving the key file behind would
// also enlarge the shared key directory with principals nobody is watching,
// so it is deleted here.
func (w *Workspace) RetireActor(ctx context.Context, retirerName, actorAddress string) ([]workroom.Record, error) {
	actor, err := w.ResolveActorAddress(actorAddress)
	if err != nil {
		return nil, err
	}
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	actorState, exists := snapshot.Projection.Actors[actor.Fingerprint]
	if !exists || actorState.MembershipEvent == "" {
		return nil, fmt.Errorf("actor %q has no roster membership", actor.Name)
	}
	if actorState.Retired {
		return nil, fmt.Errorf("actor %q is already retired", actor.Name)
	}
	if actor.Name == retirerName {
		return nil, errors.New("retire another principal; retiring the identity you are signing with would leave the workroom without its custodian")
	}
	submission, err := w.Act(ctx, retirerName, Act{
		Verb: VerbSupersede, Target: actorState.MembershipEvent, Text: "retire " + actor.Name,
		IdempotencyKey: "actor-retire-" + actor.Name + "-" + snapshot.Head,
	})
	if err != nil {
		return nil, err
	}
	// Appending is not prevailing. Whether a supersession retires anything is
	// the fold's judgement, and a participant superseding another's membership
	// is judged ineffective. The attempt stays in the log either way, so the
	// only question left here is whether custody may follow it. Deleting the
	// key on an ineffective act left a live roster member no one could sign
	// for, and reported that as a success.
	after, err := w.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	decision, judged := after.Projection.Decision(submission.Record.ID)
	if !judged || decision.Verdict != workroom.Effective {
		reason := "the fold recorded no decision for it"
		if judged {
			reason = decision.Reason
		}
		return nil, fmt.Errorf("retiring %s was ineffective (%s); its membership and its key are unchanged", actor.Name, reason)
	}
	// updateConfig reloads the file under the apphost lock, so custody ends
	// by removing exactly this entry from whatever is on disk now — a
	// concurrent View never observes the entry gone from memory while the
	// file still grants it, and a failed update leaves both unchanged. No
	// configMu section is held here, because the durable supersession and
	// the snapshots above acquire snapshotMu and apphost's lock is taken
	// only inside updateConfig.
	if err := w.updateConfig(func(c *apphost.Config) (bool, error) {
		if _, exists := c.Actors[actor.Name]; !exists {
			return false, nil
		}
		delete(c.Actors, actor.Name)
		return true, nil
	}); err != nil {
		return nil, err
	}
	if actor.KeyFile != "" {
		if err := os.Remove(actor.KeyFile); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("retired %s durably, but its key file remains: %w", actor.Name, err)
		}
	}
	return []workroom.Record{submission.Record}, nil
}

// RevokeRole supersedes every live explicit authority grant. Derived
// roles, such as operator implying ratifier, must be revoked at their source.
func (w *Workspace) RevokeRole(ctx context.Context, revokerName, actorAddress, role string) ([]workroom.Record, error) {
	if err := validateAuthorityRole(role); err != nil {
		return nil, err
	}
	actor, err := w.ResolveActorAddress(actorAddress)
	if err != nil {
		return nil, err
	}
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	actorState := snapshot.Projection.Actors[actor.Fingerprint]
	targets := append([]string(nil), actorState.RoleSources[role]...)
	if len(targets) == 0 {
		if containsRole(actorState.Roles, role) {
			return nil, fmt.Errorf("role %q for actor %q is derived; revoke its source role", role, actor.Name)
		}
		return nil, fmt.Errorf("actor %q has no active explicit role %q", actor.Name, role)
	}
	records := make([]workroom.Record, 0, len(targets))
	for _, target := range targets {
		submission, err := w.Act(ctx, revokerName, Act{
			Verb: VerbSupersede, Target: target, Text: "revoke " + role + " from " + actor.Name,
			IdempotencyKey: "role-revoke-" + target + "-" + snapshot.Head,
		})
		if err != nil {
			return nil, err
		}
		records = append(records, submission.Record)
	}
	return records, nil
}

func validateAuthorityRole(role string) error {
	if role == "" || role == "participant" || workroom.IsActorKind(role) {
		return fmt.Errorf("invalid authority role %q", role)
	}
	return nil
}

func containsRole(roles []string, role string) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func (w *Workspace) Act(ctx context.Context, actorName string, act Act) (Submission, error) {
	_, private, err := w.Actor(actorName)
	if err != nil {
		return Submission{}, err
	}
	request, err := w.BuildActRequest(ctx, private, actorName, act)
	if err != nil {
		return Submission{}, err
	}
	return w.AcceptSubmission(ctx, request)
}

func (w *Workspace) BuildActRequest(ctx context.Context, private ed25519.PrivateKey, actorName string, act Act) (kernel.Request, error) {
	var schema string
	var payload any
	guardedRetirement := false
	rests := append([]string(nil), act.RestsOn...)
	switch act.Verb {
	case VerbState:
		body, err := w.normalizeRequestShape(ctx, act.Kind, act.Body)
		if err != nil {
			return kernel.Request{}, err
		}
		// Reserved admission fields are never caller input. The guarded
		// review path stamps its own onto the body it built, and admission
		// judges those contents against the workroom like everything else.
		if !act.GuardedReview {
			if err := refuseClientReservedFields(act.Body); err != nil {
				return kernel.Request{}, err
			}
		}
		// A guarded review answers a moved world instead of refusing it: its
		// staleness goes into the signed verdict words, so the dead-basis
		// escape is part of what the path records.
		body, err = w.AdmitState(ctx, stateAdmission{
			Kind: act.Kind, Body: body, RestsOn: rests,
			AllowDeadBasis: act.AllowDeadBasis || act.GuardedReview,
		})
		if err != nil {
			return kernel.Request{}, err
		}
		lifecycle, starter := workroom.StarterLifecycle(act.Kind)
		if !starter || lifecycle == workroom.LifecycleReport {
			reporter := intent.ActorFingerprint(private.Public().(ed25519.PublicKey))
			if err := w.validateReportBasis(ctx, reporter, act.Kind, body, rests); err != nil {
				return kernel.Request{}, err
			}
		}
		schema = workroom.SchemaState
		payload = workroom.State{Kind: act.Kind, Text: act.Text, Body: body}
	case VerbRatify:
		if err := w.refuseUnratifiableTarget(ctx, act.Target); err != nil {
			return kernel.Request{}, err
		}
		schema = workroom.SchemaRatify
		payload = workroom.Ratify{Target: act.Target}
		rests = []string{act.Target}
	case VerbSupersede:
		// Every surface that can retire a record passes through here, which
		// is why the check lives here and not in the command that happens to
		// be in front of it. It was in cmd/gs alone, so a retirement filed
		// over MCP skipped it entirely and took main red on 2026-08-12.
		if err := w.RefuseCitedRetirement(ctx, act.Target, act.CitedOK); err != nil {
			return kernel.Request{}, err
		}
		schema = workroom.SchemaSupersede
		payload = workroom.Supersede{Target: act.Target, Text: act.Text}
		rests = append([]string{act.Target}, rests...)
	case VerbRetireIfUnclaimed:
		guardedRetirement = true
		schema = workroom.SchemaRetireUnclaimed
		payload = workroom.RetireIfUnclaimed{
			Target: act.Target, Text: act.Text, CitedOK: act.CitedOK,
			Expectation: workroom.UnclaimedExpectation{
				Request: act.Target, Promise: workroom.CommitmentAbsent, Completion: workroom.CommitmentAbsent,
			},
		}
		rests = append([]string{act.Target}, rests...)
	case VerbReassignIfUnclaimed:
		body, err := w.normalizeGuardedRequestShape(ctx, act.Body)
		if err != nil {
			return kernel.Request{}, err
		}
		if err := refuseClientReservedFields(act.Body); err != nil {
			return kernel.Request{}, err
		}
		rests = append([]string{act.Retirement}, rests...)
		schema = workroom.SchemaReassignRequest
		payload = workroom.ReassignIfUnclaimed{
			Text: act.Text, Body: body,
			Expectation: workroom.UnclaimedExpectation{
				Request: act.Target, Retirement: act.Retirement,
				Promise: workroom.CommitmentAbsent, Completion: workroom.CommitmentAbsent,
			},
		}
	default:
		return kernel.Request{}, fmt.Errorf("unknown act verb %q", act.Verb)
	}
	request, err := w.buildRequest(ctx, private, actorName, schema, payload, rests, act.Attachments, act.IdempotencyKey)
	if err != nil {
		return kernel.Request{}, err
	}
	if guardedRetirement {
		if err := w.RefuseCitedGuardedRetirement(ctx, act.Target, act.CitedOK, request); err != nil {
			return kernel.Request{}, err
		}
	}
	return request, nil
}

// refuseUnratifiableTarget keeps a locally built ratification out of the
// append-only log when the fold has already published that no actor can make
// it effective. The projected satisfier is the admission-time rule the fold
// itself will use, not a fresh lookup in today's vocabulary.
func (w *Workspace) refuseUnratifiableTarget(ctx context.Context, target string) error {
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read ratify target: %w", err)
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event != target {
			continue
		}
		decision, decided := snapshot.Projection.Decision(target)
		if !decided || decision.Verdict != workroom.Effective || statement.Retired {
			return nil
		}
		if statement.Satisfier != workroom.SatisfierNone && statement.Satisfier != "" {
			return nil
		}

		alternative := "use the lifecycle act defined for that statement rather than ratify"
		switch statement.Kind {
		case workroom.KindArtifact:
			alternative = "file and ratify a proposal resting on the artifact"
			for _, commitment := range snapshot.Projection.Commitments {
				if commitment.Report == target {
					alternative = "merge an independently approved exact head to close the implementation commitment"
					break
				}
			}
		case workroom.KindRequest:
			alternative = "the addressee may promise or report the request, or its author or a ratifier may supersede it"
		case workroom.KindPromise:
			alternative = "the promisor may report or publish an implementation artifact, or supersede the promise"
		case workroom.KindDissent:
			alternative = "its author or a ratifier may supersede the dissent"
		}
		return fmt.Errorf("cannot ratify target %s: kind %q has satisfier %q; %s", target, statement.Kind, statement.Satisfier, alternative)
	}
	// An unknown target still goes through the normal signed admission path.
	// Only the fold can authoritatively distinguish a moved frontier from a
	// malformed citation, and this pre-flight must not invent that decision.
	return nil
}

// normalizeGuardedRequestShape keeps a guarded replacement reproducible after
// its performer leaves local custody. Ordinary requests intentionally resolve
// only current custody. A retry of this two-act purpose must reconstruct the
// same signed fingerprint after the successful pair, though, so it may fall
// back to the durable roster entry that retirement keeps for attribution.
// Admission still requires that fingerprint to be live for every new act.
func (w *Workspace) normalizeGuardedRequestShape(ctx context.Context, body map[string]string) (map[string]string, error) {
	normalized := cloneBody(body)
	if strings.TrimSpace(normalized["conditions"]) == "" {
		return nil, fmt.Errorf("%s state requires body.conditions", workroom.KindRequest)
	}
	address := strings.TrimSpace(normalized["to"])
	if address == "" {
		return nil, fmt.Errorf("%s state requires body.to", workroom.KindRequest)
	}
	actor, err := w.ResolveActorAddress(address)
	if err == nil {
		normalized["to"] = actor.Fingerprint
		return normalized, nil
	}
	if !errors.Is(err, ErrUnknownActor) {
		return nil, fmt.Errorf("%s body.to: %w", workroom.KindRequest, err)
	}
	snapshot, snapshotErr := w.Snapshot(ctx)
	if snapshotErr != nil {
		return nil, fmt.Errorf("%s body.to: actor is absent from custody and the durable roster could not be read: %w", workroom.KindRequest, snapshotErr)
	}
	name := strings.TrimPrefix(address, "@")
	matches := make([]string, 0, 1)
	for fingerprint, historical := range snapshot.Projection.Actors {
		if fingerprint == address || historical.Name == name {
			matches = append(matches, fingerprint)
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("%s body.to: %w", workroom.KindRequest, err)
	}
	normalized["to"] = matches[0]
	return normalized, nil
}

// normalizeRequestShape mirrors the fold's request-lifecycle field and actor
// checks before the request is signed. The fold remains authoritative if the
// log moves after this check. The active vocabulary matters: a declared kind
// can participate in the request lifecycle just as the starter request kind
// does.
func (w *Workspace) normalizeRequestShape(ctx context.Context, kind workroom.Kind, body map[string]string) (map[string]string, error) {
	lifecycle, starter := workroom.StarterLifecycle(kind)
	if !starter {
		snapshot, err := w.Snapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("validate request shape: %w", err)
		}
		lifecycle = workroom.LifecycleNone
		for _, definition := range snapshot.Vocabulary.Definitions {
			if definition.Name == kind {
				lifecycle = definition.Lifecycle
				break
			}
		}
	}
	if lifecycle != workroom.LifecycleRequest {
		return body, nil
	}
	normalized := cloneBody(body)
	if strings.TrimSpace(normalized["conditions"]) == "" {
		return nil, fmt.Errorf("%s state requires body.conditions", kind)
	}
	address := strings.TrimSpace(normalized["to"])
	if address == "" {
		return nil, fmt.Errorf("%s state requires body.to", kind)
	}
	actor, err := w.ResolveActorAddress(address)
	if err != nil {
		return nil, fmt.Errorf("%s body.to: %w", kind, err)
	}
	normalized["to"] = actor.Fingerprint
	return normalized, nil
}

// validateDirectReport holds the direct shape to the fold's own terms before
// the record is signed: only the addressee may answer a request without having
// claimed it, and not while their own claim on it still stands, because one
// commitment takes one closure and a claim already made is the thing to report
// on. The fold decides this again and authoritatively; refusing here only lets
// an actor learn it before signing rather than after appending.
func (w *Workspace) validateDirectReport(request workroom.Statement, reporter string, snapshot Snapshot, statements map[string]workroom.Statement) error {
	if request.Body["to"] != reporter {
		return errors.New("only the requested performer may report directly on a request")
	}
	// Only effective, unretired promises of the reporter's block the direct
	// route: a refused record is not a commitment, and a withdrawn one is
	// history. Promise-ness is the lifecycle the fold bound to that record at
	// its own position, which the statements map carries, so a kind redefined
	// since does not change what an existing claim was.
	for _, statement := range statements {
		if statement.Actor != reporter || statement.Retired {
			continue
		}
		if statement.Lifecycle != workroom.LifecyclePromise {
			continue
		}
		for _, basis := range snapshot.Projection.Provenance[statement.Event] {
			if basis == request.Event {
				return fmt.Errorf("report rests on the request while promise %s is live; report on the promise", statement.Event)
			}
		}
	}
	return nil
}

// validateReportBasis mirrors the fold's report-lifecycle checks before the
// request is signed. The fold remains authoritative, including when the log
// moves after this snapshot, but locally constructed reports should not append
// when their lifecycle edge is already known to be ineffective or disputed.
func (w *Workspace) validateReportBasis(ctx context.Context, reporter string, kind workroom.Kind, body map[string]string, rests []string) error {
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("validate report basis: %w", err)
	}
	// The kind being written is classified by the current vocabulary, because
	// that is the definition it will be decided under. Every record already in
	// the log is classified by the lifecycle the fold bound to it, which the
	// projection now carries: a kind redefined since does not retroactively
	// change what its earlier records were, and reading them through today's
	// vocabulary is how this boundary came to admit reports the fold refuses.
	lifecycles := make(map[workroom.Kind]workroom.Lifecycle, len(snapshot.Vocabulary.Definitions))
	for _, definition := range snapshot.Vocabulary.Definitions {
		lifecycles[definition.Name] = definition.Lifecycle
	}
	if lifecycles[kind] != workroom.LifecycleReport {
		return nil
	}
	effective := make(map[string]bool, len(snapshot.Projection.Decisions))
	for _, decision := range snapshot.Projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	statements := make(map[string]workroom.Statement, len(snapshot.Projection.Statements))
	for _, statement := range snapshot.Projection.Statements {
		if effective[statement.Event] {
			statements[statement.Event] = statement
		}
	}
	var promises, requests []workroom.Statement
	for _, rest := range rests {
		statement, ok := statements[rest]
		if !ok {
			continue
		}
		switch statement.Lifecycle {
		case workroom.LifecyclePromise:
			promises = append(promises, statement)
		case workroom.LifecycleRequest:
			requests = append(requests, statement)
		}
	}
	// The same two shapes the fold admits, decided the same way: the promise
	// when there is one, the request only when there is not. A boundary still
	// enforcing the older promise-only rule would leave the widened basis
	// correct in the log and impossible to write, which is worse than never
	// having widened it.
	switch {
	case len(promises) > 1:
		return fmt.Errorf("report requires exactly one effective promise-lifecycle basis in rests_on; got %d", len(promises))
	case len(promises) == 1:
		if promises[0].Actor != reporter {
			return errors.New("report actor must be the promisor of its promise-lifecycle basis")
		}
		// A request cited beside a promise is provenance, and it has to be the
		// provenance it claims. The fold refuses any other, so accepting it
		// here would sign and append a record the fold will then rule
		// ineffective -- spending depth to record a refusal.
		var governing string
		for _, basis := range snapshot.Projection.Provenance[promises[0].Event] {
			if statement, ok := statements[basis]; ok && statement.Lifecycle == workroom.LifecycleRequest {
				if governing != "" {
					governing = ""
					break
				}
				governing = statement.Event
			}
		}
		for _, cited := range requests {
			if cited.Event != governing {
				return errors.New("report cites a request other than the one its promise answers")
			}
		}
	case len(requests) > 1:
		return fmt.Errorf("report requires exactly one effective request-lifecycle basis in rests_on; got %d", len(requests))
	case len(requests) == 1:
		if err := w.validateDirectReport(requests[0], reporter, snapshot, statements); err != nil {
			return err
		}
	default:
		return errors.New("report requires exactly one effective promise or request lifecycle basis in rests_on")
	}
	artifact := body["artifact"]
	if body["verdict"] == "approved" && artifact != "" {
		for _, rest := range rests {
			if rest == artifact {
				return nil
			}
		}
		return fmt.Errorf("approved report must rest on its named artifact %s; add it to rests_on or file the verdict with gs review", artifact)
	}
	return nil
}

// citingDocuments lists tracked documentation that names an event, so a
// retirement can be refused before it lands rather than discovered afterwards
// by the gate. Retiring a record a page cites leaves that page resting on a
// withdrawn pointer, which the documentation gate refuses: the repository goes
// red, and the act that did it is already in an append-only log. The pages are
// the thing to look at, so they are what the refusal names.
//
// git grep rather than a walk, because tracked is the question. An untracked
// working copy of a page is not what the gate reads, and a page that git does
// not know about cannot break anyone else.
//
// This one is the reason repositoryLocalGit bounds the environment at all: an
// answer of "nothing cites it" is what lets a retirement through, so a `*.md`
// reinterpreted as a literal filename, a grep re-pointed at a repository where
// the page does not exist, or a configuration setting that stops the grep
// starting, is a bypass of the guard rather than a wrong display.
//
// The two returns are separate for the same reason. An empty page list means
// the lookup ran and found nothing; an error means the lookup did not answer,
// and no caller may read the second as the first. That distinction is the
// primary defence, and it is deliberately the one that does not depend on
// anticipating anything: it holds whatever the unanswerable lookup was broken
// by. Bounding the environment is the secondary defence and narrows how easily
// a caller can reach for the breakage in the first place.
func (w *Workspace) citingDocuments(ctx context.Context, repo, event string) ([]string, error) {
	output, err := repositoryLocalGit(ctx, repo,
		"grep", "--name-only", "--fixed-strings", event, "--", "*.md").Output()
	if err != nil {
		// git grep reports two entirely different things through a non-zero
		// exit, and this guard is only entitled to act on one of them. Exit 1
		// is "no file matched": the ordinary answer on nearly every
		// retirement, and the answer that permits it. Every other exit —
		// 128 for a configuration or repository error, a signal, a failure to
		// start git at all — means the lookup did not run, so there is no
		// answer to act on.
		//
		// Returning nil for the second case is what made this a fail-open. An
		// empty page list is indistinguishable from "no page cites this", so a
		// lookup that never ran read as a clean bill of health and the
		// retirement went through. The old comment argued that a guard failing
		// closed on its own malfunction would make a broken git a broken
		// workroom. The premise is true and the conclusion still does not
		// follow, because the two costs are not comparable. Stopping costs a
		// retry: the operator fixes the repository or the environment and
		// files the same act again, and nothing durable has happened. Passing
		// costs a retirement the documentation still cites — an append-only
		// event that leaves live pages resting on a withdrawn pointer and the
		// repository red, which is exactly the breakage this guard exists to
		// prevent and which no later act can take back. A cheap reversible
		// cost against an expensive irreversible one is not a close call.
		//
		// The deliberate escape stays where it was: cited_ok is signed into
		// the act by an actor who has decided to retire anyway. A caller who
		// really must proceed past an unanswerable lookup has that, and takes
		// the responsibility with it.
		//
		// ExitCode reports -1 for a process killed by a signal and for one
		// that never started, so both land on the refusing side without a
		// case of their own. git's own diagnosis is carried through when there
		// is one, because the operator's next move depends on what broke and
		// this refusal is the only place they will see it.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 1 {
				return nil, nil
			}
			if detail := strings.TrimSpace(string(exit.Stderr)); detail != "" {
				return nil, fmt.Errorf("citation lookup for %s did not run in %s: %s", event, repo, detail)
			}
		}
		return nil, fmt.Errorf("citation lookup for %s did not run in %s: %w", event, repo, err)
	}
	var pages []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if page := strings.TrimSpace(line); page != "" {
			pages = append(pages, page)
		}
	}
	return pages, nil
}

// RefuseCitedRetirement stops a supersession whose target the documentation
// still names. Ordinary supersession calls it while building; guarded
// retirement carries its override in the signed payload and calls it from the
// post-dedup admission hook, so an exact retry is never re-judged.
func (w *Workspace) RefuseCitedRetirement(ctx context.Context, target string, allowed bool) error {
	return w.RefuseCitedRetirementInCheckout(ctx, w.Repo, target, allowed)
}

// RefuseCitedGuardedRetirement protects the caller's own checkout before a
// remote resident sees the act. Once the request is already retired, the act
// may be an exact retry; the kernel must see it before any checkout drift is
// re-judged. Genuinely new acts against that retired request still fail the
// signed workroom guard at post-dedup admission.
func (w *Workspace) RefuseCitedGuardedRetirement(ctx context.Context, target string, allowed bool, request kernel.Request) error {
	if allowed {
		return nil
	}
	replay, err := kernel.CheckReplay(ctx, w.Store, request)
	if err != nil {
		return fmt.Errorf("check guarded retirement replay: %w", err)
	}
	if replay {
		return nil
	}
	return w.RefuseCitedRetirement(ctx, target, false)
}

// RefuseCitedRetirementInCheckout evaluates the tracked tree which will
// actually receive a merge. Linked worktrees may legitimately add or remove a
// citation independently of Workspace.Repo before their candidate lands.
func (w *Workspace) RefuseCitedRetirementInCheckout(ctx context.Context, checkout, target string, allowed bool) error {
	if allowed || strings.TrimSpace(target) == "" {
		return nil
	}
	pages, err := w.citingDocuments(ctx, checkout, target)
	if err != nil {
		// A lookup that did not answer is a refusal, not a pass. The wording
		// separates it from the citation refusal below, because the two ask
		// for different things: this one asks the operator to make the
		// repository readable and file the same act again, and that one asks
		// them to repoint pages first.
		return fmt.Errorf("cannot tell whether documentation still cites %s, so the retirement is refused: %w\nrepair the repository or the environment and file it again, or retire deliberately with the cited override", target, err)
	}
	if len(pages) == 0 {
		return nil
	}
	return fmt.Errorf("retiring %s would leave %d documentation page(s) resting on a withdrawn pointer:\n  %s\nrepoint them at the successor in the same head, then retire",
		target, len(pages), strings.Join(pages, "\n  "))
}

func (w *Workspace) buildRequest(ctx context.Context, private ed25519.PrivateKey, actorName, schema string, payload any, rests []string, attachments map[string][]byte, key string) (kernel.Request, error) {
	normalized, err := w.normalizePayload(schema, payload)
	if err != nil {
		return kernel.Request{}, err
	}
	encoded, err := workroom.Encode(normalized)
	if err != nil {
		return kernel.Request{}, err
	}
	return w.signRequest(ctx, private, actorName, schema, encoded, rests, attachments, key)
}

// signRequest signs one already encoded payload. It is the only place a
// submission is signed, so the host binding is stamped with the same identity
// and idempotency rules as every application record.
func (w *Workspace) signRequest(ctx context.Context, private ed25519.PrivateKey, actorName, schema string, encoded []byte, rests []string, attachments map[string][]byte, key string) (kernel.Request, error) {
	// The signed intent needs the payload tree's identity, but admission owns
	// the durable write. Computing the identity here avoids publishing the same
	// blobs and trees twice, and leaves a request rejected during construction
	// with no objects written. The kernel reconstructs this tree, checks the
	// exact identity, and only then sequences the event.
	tree, err := gitstore.HashPayloadTree(w.config.ObjectFormat, encoded, attachments)
	if err != nil {
		return kernel.Request{}, err
	}
	if key == "" {
		key, err = randomKey()
		if err != nil {
			return kernel.Request{}, err
		}
	}
	namespace := w.config.IdempotencyNamespace
	if namespace == "" {
		// Workrooms created before the stable namespace field keep their original
		// retry identity. Changing it in place could replay an outstanding act.
		namespace = "gs/" + actorName
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + w.config.ObjectFormat + ":" + w.config.Genesis,
		Schema: schema, PayloadTree: "git:" + w.config.ObjectFormat + ":" + tree,
		RestsOn: rests, IdempotencyNS: namespace, IdempotencyKey: key,
	}, private)
	if err != nil {
		return kernel.Request{}, err
	}
	return kernel.Request{Signed: signed, Payload: encoded, Attachments: attachments}, nil
}

func (w *Workspace) normalizePayload(schema string, payload any) (any, error) {
	if schema != workroom.SchemaState {
		return payload, nil
	}
	var state workroom.State
	switch value := payload.(type) {
	case workroom.State:
		state = value
	case *workroom.State:
		state = *value
	default:
		return payload, nil
	}
	state.Body = cloneBody(state.Body)
	for field, address := range state.Body {
		if !strings.HasPrefix(address, "@") {
			continue
		}
		actor, err := w.ResolveActorAddress(address)
		if err != nil {
			return nil, fmt.Errorf("body %s: %w", field, err)
		}
		state.Body[field] = actor.Fingerprint
	}
	if state.Kind == workroom.KindRequest {
		actor, err := w.ResolveActorAddress(state.Body["to"])
		if err != nil {
			return nil, fmt.Errorf("request performer: %w", err)
		}
		state.Body["to"] = actor.Fingerprint
	}
	return state, nil
}

func cloneBody(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

// ResolveActorAddress accepts the human-facing forms used at application
// edges. Durable request payloads always carry the actor fingerprint.
// Like ResolveActor, the cached view answers first and only a miss pays one
// fresh re-read of this workspace's local custody record — never the durable
// projection — so a state request addressed to an actor another process added
// after this workspace was opened still normalises to that actor's
// fingerprint instead of reporting a live actor as unknown.
func (w *Workspace) ResolveActorAddress(address string) (apphost.Actor, error) {
	name := strings.TrimPrefix(address, "@")
	return w.resolveCustody(address, func(c *apphost.Config) (apphost.Actor, bool) {
		if actor, ok := c.Actors[name]; ok {
			return actor, true
		}
		for _, actor := range c.Actors {
			if actor.Fingerprint == address {
				return actor, true
			}
		}
		return apphost.Actor{}, false
	})
}

// resolveCustody is the one cold path both resolvers share: the cached view
// answers first, a miss re-reads config.json once under this workspace's
// rereadMu, and a hit adopts the fresh custody through three-way
// reconciliation. The matcher decides which forms of address each caller
// accepts against whichever configuration it is handed, and it runs last of
// all against exactly the reconciled state that was adopted — never against
// the bare load — so a returned answer can never name custody the live view
// does not hold. Adoption takes configMu around the same short section
// updateConfig uses, touches only the mutable custody fields, and leaves the
// open-time scalars written-once exactly as updateConfig keeps them.
func (w *Workspace) resolveCustody(address string, match func(*apphost.Config) (apphost.Actor, bool)) (apphost.Actor, error) {
	if actor, ok := w.matchView(match); ok {
		return actor, nil
	}
	w.rereadMu.Lock()
	defer w.rereadMu.Unlock()
	if actor, ok := w.matchView(match); ok {
		return actor, nil
	}
	w.configMu.Lock()
	baseline := w.config.Clone()
	w.configMu.Unlock()
	fresh, err := apphost.LoadConfig(w.MetaDir)
	if err != nil {
		return apphost.Actor{}, fmt.Errorf("re-read configuration custody to address %q: %w", address, err)
	}
	custodyRereadGate()
	w.configMu.Lock()
	defer w.configMu.Unlock()
	reconciled := w.config
	reconciled.Actors, reconciled.VerifiedFrontier, err = reconcileCustody(&baseline, &w.config, &fresh)
	if err != nil {
		return apphost.Actor{}, fmt.Errorf("reconcile configuration custody to address %q: %w", address, err)
	}
	actor, ok := match(&reconciled)
	if !ok {
		return apphost.Actor{}, fmt.Errorf("%w %q after a fresh re-read of the configuration", ErrUnknownActor, address)
	}
	w.config.Actors = reconciled.Actors
	w.config.VerifiedFrontier = reconciled.VerifiedFrontier
	return actor, nil
}

// reconcileCustody merges one freshly loaded custody record onto the live
// view, judged against the record the re-read started from: a per-name
// three-way merge, not a union. Each actor name is decided independently.
// Where live still equals the baseline, this workspace learned nothing since
// the load, so fresh stands — including its replacement or removal of that
// name; where fresh still equals the baseline, the record moved no further
// than what the view already adopted, so live's concurrent change stands;
// where live and fresh agree, the agreed entry is taken once, and an agreed
// removal preserves the name's absence rather than inserting a zero Actor.
// A name both sides moved differently since the snapshot is refused closed:
// no verified-frontier arbitration picks a winner — the reconciliation fails
// with an explicit error naming that actor, and the caller adopts nothing.
// The frontier still follows mergeVerifiedFrontier, so only a deeper fresh
// marker advances it and an equal-depth disagreement between memory and disk
// is refused there too. Only the mutable custody fields
// come back; callers hold configMu across the call and the adoption.
func reconcileCustody(baseline, live, fresh *apphost.Config) (map[string]apphost.Actor, *apphost.VerifiedFrontier, error) {
	frontier, _, err := mergeVerifiedFrontier(live.VerifiedFrontier, fresh.VerifiedFrontier)
	if err != nil {
		return nil, nil, err
	}
	actors := make(map[string]apphost.Actor, len(live.Actors)+len(fresh.Actors))
	names := make(map[string]struct{}, len(baseline.Actors)+len(live.Actors)+len(fresh.Actors))
	for name := range baseline.Actors {
		names[name] = struct{}{}
	}
	for name := range live.Actors {
		names[name] = struct{}{}
	}
	for name := range fresh.Actors {
		names[name] = struct{}{}
	}
	for name := range names {
		baseEntry, wasHeld := baseline.Actors[name]
		liveEntry, isHeld := live.Actors[name]
		freshEntry, freshHolds := fresh.Actors[name]
		switch {
		case sameActorEntry(isHeld, liveEntry, wasHeld, baseEntry):
			// Live is unchanged since the snapshot: adopt fresh wholesale,
			// replacement or removal alike.
			if freshHolds {
				actors[name] = freshEntry
			}
		case sameActorEntry(freshHolds, freshEntry, wasHeld, baseEntry):
			// Fresh is unchanged since the snapshot: keep live's concurrent
			// change, including its removal.
			if isHeld {
				actors[name] = liveEntry
			}
		case sameActorEntry(isHeld, liveEntry, freshHolds, freshEntry):
			// Both sides agree on this name: take the agreed entry once, and
			// an agreed absence stays absent rather than becoming a zero Actor.
			if isHeld {
				actors[name] = liveEntry
			}
		default:
			// One name moved differently on both sides: refuse closed. No
			// verified-frontier arbitration decides whose custody is right,
			// so nothing is adopted and the caller keeps its previous view.
			return nil, nil, fmt.Errorf("refuse divergent custody for actor %q: the live view and the freshly stored record both moved it differently since the re-read began", name)
		}
	}
	return actors, frontier, nil
}

// sameActorEntry compares two optional map entries: both absent, or both
// present and equal as values.
func sameActorEntry(holds bool, entry apphost.Actor, held bool, other apphost.Actor) bool {
	return holds == held && (!holds || entry == other)
}

func (w *Workspace) matchView(match func(*apphost.Config) (apphost.Actor, bool)) (apphost.Actor, bool) {
	w.configMu.Lock()
	defer w.configMu.Unlock()
	return match(&w.config)
}

func (w *Workspace) AcceptSubmission(ctx context.Context, request kernel.Request) (Submission, error) {
	done := observe.Begin(ctx, w.observer, observe.OperationSubmit, observe.PathSubmission)
	var resultErr error
	defer func() {
		if done != nil {
			done(resultErr)
		}
	}()
	if w.config.ReadOnly {
		resultErr = errors.New("attached workroom is read-only; configure local custody and a sequencer endpoint to submit")
		return Submission{}, resultErr
	}
	if _, refusal := w.interpreter(); refusal != nil {
		// Appending under an interpreter this build does not hold would write a
		// record nothing here can read. The refusal also asserts that the
		// repository verifies, so the kernel speaks first: Snapshot audits the
		// chain and reports the refusal only once it verifies, so whatever it
		// reports is the honest answer for this submission too.
		if _, err := w.Snapshot(ctx); err != nil {
			refusal = err
		}
		resultErr = refusal
		return Submission{}, refusal
	}
	decodedIntent, err := intent.Verify(request.Signed)
	if err != nil {
		resultErr = err
		return Submission{}, err
	}
	// The fold keeps state@0 and state@1 readable so historical decisions do
	// not change, but admission must not let a new raw submission use either
	// retired schema to evade current rules.
	if decodedIntent.Schema == workroom.SchemaStateLegacy || decodedIntent.Schema == workroom.SchemaStateV1 {
		decoded, decodeErr := workroom.Decode(decodedIntent.Schema, request.Payload)
		if decodeErr != nil {
			resultErr = decodeErr
			return Submission{}, decodeErr
		}
		if state, ok := decoded.(*workroom.State); ok {
			if state.Kind == workroom.KindFoldActivation {
				resultErr = errors.New("legacy state schema cannot admit a fold activation; record a host binding upgrade")
				return Submission{}, resultErr
			}
			snapshot, snapshotErr := w.Snapshot(ctx)
			if snapshotErr != nil {
				resultErr = snapshotErr
				return Submission{}, snapshotErr
			}
			artifactKind := false
			for _, definition := range snapshot.Vocabulary.Definitions {
				if definition.Name == state.Kind && definition.Render == workroom.RenderArtifact {
					artifactKind = true
					break
				}
			}
			if artifactKind && (state.Body["path"] == "." || strings.Contains(state.Body["path"], ",")) {
				resultErr = errors.New("legacy state schema cannot admit an unmaintainable artifact path")
				return Submission{}, resultErr
			}
		}
	}
	if decodedIntent.Schema == workroom.SchemaRatifyLegacy || decodedIntent.Schema == workroom.SchemaRatify {
		decoded, decodeErr := workroom.Decode(decodedIntent.Schema, request.Payload)
		if decodeErr != nil {
			resultErr = decodeErr
			return Submission{}, decodeErr
		}
		ratification := decoded.(*workroom.Ratify)
		snapshot, snapshotErr := w.Snapshot(ctx)
		if snapshotErr != nil {
			resultErr = snapshotErr
			return Submission{}, snapshotErr
		}
		for _, statement := range snapshot.Projection.Statements {
			if statement.Event == ratification.Target && statement.Kind == workroom.KindFoldActivation {
				resultErr = errors.New("historical fold activation can no longer be ratified; record a host binding upgrade")
				return Submission{}, resultErr
			}
		}
	}
	w.submitterOnce.Do(func() {
		checkpoint := w.checkpointOptions()
		w.submitter = kernel.NewSubmitter(w.Store, kernel.Options{
			SigningKey: w.config.SequencerKey, CheckpointEnabled: checkpoint.Enabled, CheckpointPointer: checkpoint.Pointer, PreAppend: w.allowlist,
			PostDedup:     w.admitApplication,
			MaxQueueDepth: ResidentQueueDepth,
		})
	})
	result, err := w.submitter.Submit(ctx, request)
	if err != nil {
		resultErr = err
		return Submission{}, err
	}
	if w.observer != nil {
		w.observer.Record(ctx, observe.Measurement{Operation: observe.OperationSubmit, Path: observe.PathRef, Outcome: observe.OutcomeOK, Items: int64(result.CASRetries)})
	}
	record := workroom.Record{
		ID: w.EventID(result.Commit), Timestamp: result.Timestamp, Actor: intent.ActorFingerprint(request.Signed.ActorKey),
		Schema: decodedIntent.Schema, RestsOn: append([]string(nil), decodedIntent.RestsOn...),
		Payload: append([]byte(nil), request.Payload...), Attachments: cloneAttachments(request.Attachments),
	}
	w.acceptSnapshot(result, record)
	return Submission{Result: result, Record: record}, nil
}

// acceptSnapshot applies a commit just authenticated, sequenced, and CASed by
// this workspace without making the reader audit it a second time before the
// local notice. If the projection is absent or its frontier differs (for
// example after a concurrent external append), Snapshot performs the normal
// verified catch-up instead.
func (w *Workspace) acceptSnapshot(result kernel.Result, record workroom.Record) {
	if result.Replay {
		return
	}
	w.snapshotMu.Lock()
	defer w.snapshotMu.Unlock()
	selected, err := w.interpreter()
	if err != nil || w.snapshotCache == nil || w.snapshotFolder == nil || w.snapshotProfile != selected.projectionProfile() || w.snapshotCache.Head != result.BaseHead {
		return
	}
	w.snapshotFolder.Append(record)
	snapshot := Snapshot{
		Genesis: w.snapshotCache.Genesis, Head: result.Head, Depth: w.snapshotCache.Depth + 1,
		Projection: w.snapshotFolder.Projection(), Vocabulary: w.snapshotFolder.Vocabulary(),
	}
	w.snapshotCache = &snapshot
	w.snapshotSource = SnapshotSourceIncrementalTail
}

func cloneAttachments(input map[string][]byte) map[string][]byte {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string][]byte, len(input))
	for name, content := range input {
		output[name] = append([]byte(nil), content...)
	}
	return output
}

func (w *Workspace) allowlist(_ context.Context, admission kernel.Admission) error {
	fingerprint := intent.ActorFingerprint(admission.ActorKey)
	w.configMu.Lock()
	defer w.configMu.Unlock()
	for _, actor := range w.config.Actors {
		if actor.Fingerprint == fingerprint {
			return nil
		}
	}
	return errors.New("actor is not in the static allowlist")
}

func randomKey() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func (w *Workspace) EventID(commit string) string {
	return gitseqhost.EventID(w.config.ObjectFormat, w.config.Genesis, commit)
}

// RebuildProgress reports how far a cold verified rebuild has got, and whether
// one is running at all. It takes no lock. That is not an optimisation: the
// rebuild holds snapshotMu for its whole duration, so this is the only way a
// reader can learn that the wait it is in has an end and roughly where.
//
// Running is false when no audit is in flight, which is the ordinary warm case
// — callers should stay quiet then rather than render a finished progress bar.
func (w *Workspace) RebuildProgress() (progress kernel.Progress, running bool) {
	flight := w.flight.Load()
	if flight == nil {
		return kernel.Progress{}, false
	}
	select {
	case <-flight.done:
		return kernel.Progress{}, false
	default:
	}
	progress = flight.progress.Snapshot()
	return progress, progress.Started
}

// SetRebuildTestGate installs a test-only pause in cold-audit progress. It
// must be called before the first snapshot starts. Production callers leave
// the gate nil.
func (w *Workspace) SetRebuildTestGate(gate func(kernel.Progress)) {
	w.rebuildTestGate = gate
}

// SetProjectionRebuildTestGate installs a test-only pause after the complete
// authenticated application projection is prepared but before it is
// published. Production callers leave the gate nil.
func (w *Workspace) SetProjectionRebuildTestGate(gate func(int)) {
	w.projectionTestGate = gate
}

func (w *Workspace) Snapshot(ctx context.Context) (Snapshot, error) {
	result, err := w.SnapshotWithSource(ctx)
	return result.Snapshot, err
}

// SnapshotWithSource verifies and folds the workroom exactly as Snapshot does,
// while retaining whether the local projection came from a signed checkpoint,
// a verified incremental continuation, or a cold full audit.
func (w *Workspace) SnapshotWithSource(ctx context.Context) (SourcedSnapshot, error) {
	flight := w.snapshotFlight()
	select {
	case <-flight.done:
		return flight.result, flight.err
	default:
	}
	select {
	case <-flight.done:
		return flight.result, flight.err
	case <-ctx.Done():
		return SourcedSnapshot{}, ctx.Err()
	}
}

// snapshotFlight joins or starts the one resident read in flight. The work
// deliberately has process lifetime rather than inheriting one caller's
// cancellation: one disconnected browser must not abort verification for
// every other reader or make the next request repeat the same cold audit.
func (w *Workspace) snapshotFlight() *snapshotFlight {
	w.flightMu.Lock()
	defer w.flightMu.Unlock()
	if flight := w.flight.Load(); flight != nil {
		select {
		case <-flight.done:
			// A completed flight may remain installed until its worker gets
			// flightMu for cleanup. It is no longer safe to join: the head or
			// application profile may have changed since its result was made.
		default:
			return flight
		}
	}
	flight := &snapshotFlight{done: make(chan struct{})}
	flight.progress.SetTestGate(w.rebuildTestGate)
	w.flight.Store(flight)
	go func() {
		ctx := observe.WithObserver(context.Background(), w.observer)
		flight.result, flight.err = w.snapshotWithSource(ctx, &flight.progress)
		close(flight.done)
		w.flightMu.Lock()
		if w.flight.Load() == flight {
			w.flight.Store(nil)
		}
		w.flightMu.Unlock()
	}()
	return flight
}

func (w *Workspace) newReader() *kernel.Reader {
	return kernel.NewReader(w.Store, w.checkpointOptions())
}

func (w *Workspace) snapshotWithSource(ctx context.Context, progress *kernel.AuditProgress) (SourcedSnapshot, error) {
	started := time.Now()
	w.snapshotMu.Lock()
	defer w.snapshotMu.Unlock()
	selected, refusal := w.interpreter()
	profile := selected.projectionProfile()
	head, err := w.Store.Head(ctx, kernel.Ref(w.config.Genesis))
	if err != nil {
		return SourcedSnapshot{}, err
	}
	if w.snapshotCache != nil && w.snapshotProfile == profile && w.snapshotCache.Head == head {
		if err := w.rememberVerifiedFrontier(ctx, kernel.Verification{
			Genesis: w.snapshotCache.Genesis, Head: w.snapshotCache.Head, Depth: w.snapshotCache.Depth,
		}); err != nil {
			return SourcedSnapshot{}, err
		}
		w.recordSnapshot(ctx, observe.PathCache, started, w.snapshotCache.Depth, nil)
		return SourcedSnapshot{Snapshot: *w.snapshotCache, Source: w.snapshotSource}, nil
	}
	if w.reader == nil {
		w.reader = w.newReader()
	}
	var (
		streamedFolder       *workroom.Folder
		streamedEvents       int
		streamedFoldDuration time.Duration
	)
	load := func(reader *kernel.Reader) (kernel.LoadResult, error) {
		streamedFolder = nil
		streamedEvents = 0
		streamedFoldDuration = 0
		if refusal != nil {
			return reader.LoadWithProgress(ctx, w.config.Genesis, progress)
		}
		streamedFolder = selected.newFolder(nil)
		return reader.LoadWithProgressStream(ctx, w.config.Genesis, progress, func(event kernel.Event) error {
			started := time.Time{}
			if w.observer != nil {
				started = time.Now()
			}
			streamedFolder.Append(w.record(event))
			streamedEvents++
			if !started.IsZero() {
				streamedFoldDuration += time.Since(started)
			}
			return nil
		})
	}
	loaded, err := load(w.reader)
	if err != nil {
		w.recordSnapshot(ctx, observe.PathOther, started, 0, err)
		return SourcedSnapshot{}, err
	}
	// The kernel has spoken, and only now may the host. A refusal to interpret
	// asserts that the repository is verifiable, so returning it ahead of the
	// audit would let an untrusted history pass an unverifiable chain off as a
	// missing interpreter.
	if refusal != nil {
		return SourcedSnapshot{}, refusal
	}
	if err := w.rememberVerifiedFrontier(ctx, loaded.Verification); err != nil {
		return SourcedSnapshot{}, err
	}
	if !loaded.Full && w.snapshotCache != nil && w.snapshotProfile == profile && w.snapshotCache.Head == loaded.Verification.Head {
		return SourcedSnapshot{Snapshot: *w.snapshotCache, Source: w.snapshotSource}, nil
	}
	start := 0
	if !loaded.Full && w.snapshotCache != nil && w.snapshotFolder != nil && w.snapshotProfile == profile && w.snapshotCache.Head != loaded.BaseHead {
		start = -1
		for index, event := range loaded.Events {
			if event.Commit == w.snapshotCache.Head {
				start = index + 1
				break
			}
		}
	}
	if !loaded.Full && (w.snapshotCache == nil || w.snapshotFolder == nil || w.snapshotProfile != profile || start < 0) {
		// The application projection and verified reader must advance as a
		// pair. If local application state was discarded or mismatched,
		// deliberately replace the reader and perform a cold full audit.
		w.reader = w.newReader()
		loaded, err = load(w.reader)
		if err != nil {
			return SourcedSnapshot{}, err
		}
	}
	source := SnapshotSourceIncrementalTail
	path := observe.PathIncremental
	if loaded.Full {
		source = SnapshotSourceColdFullAudit
		path = observe.PathCold
		if loaded.Checkpoint {
			source = SnapshotSourceSignedCheckpointTail
			path = observe.PathCheckpoint
		}
	}
	if loaded.Full {
		folder := streamedFolder
		foldDuration := streamedFoldDuration
		if len(loaded.Events) > 0 {
			foldStarted := time.Now()
			folder = selected.newFolder(nil)
			for index := range loaded.Events {
				folder.Append(w.record(loaded.Events[index]))
				// A checkpoint load still transfers its authenticated transport
				// records after validation. Release each one after decoding.
				loaded.Events[index] = kernel.Event{}
			}
			foldDuration = time.Since(foldStarted)
		} else if streamedEvents != loaded.Verification.Events {
			return SourcedSnapshot{}, fmt.Errorf("streamed projection events = %d, verified events = %d", streamedEvents, loaded.Verification.Events)
		}
		if w.observer != nil {
			w.observer.Record(ctx, observe.Measurement{Operation: observe.OperationFold, Path: path, Outcome: observe.OutcomeOK, Duration: foldDuration, Items: int64(loaded.Verification.Events)})
		}
		if w.projectionTestGate != nil {
			w.projectionTestGate(loaded.Verification.Events)
		}
		// The callback fold was provisional while the kernel could still
		// reject a later commit. Publish it only after the complete audit and
		// application preparation have both succeeded.
		w.snapshotFolder = folder
	} else {
		foldStarted := time.Now()
		for _, event := range loaded.Events[start:] {
			w.snapshotFolder.Append(w.record(event))
		}
		if w.observer != nil {
			w.observer.Record(ctx, observe.Measurement{Operation: observe.OperationFold, Path: path, Outcome: observe.OutcomeOK, Duration: time.Since(foldStarted), Items: int64(len(loaded.Events[start:]))})
		}
	}
	snapshot := Snapshot{
		Genesis: loaded.Verification.Genesis, Head: loaded.Verification.Head, Depth: loaded.Verification.Depth,
		Projection: w.snapshotFolder.Projection(), Vocabulary: w.snapshotFolder.Vocabulary(),
	}
	w.snapshotCache = &snapshot
	w.snapshotProfile = profile
	w.snapshotSource = source
	w.recordSnapshot(ctx, path, started, snapshot.Depth, nil)
	return SourcedSnapshot{Snapshot: snapshot, Source: source}, nil
}

func (w *Workspace) recordSnapshot(ctx context.Context, path observe.Path, started time.Time, depth int, err error) {
	if w.observer == nil {
		return
	}
	w.observer.Record(ctx, observe.Measurement{
		Operation: observe.OperationSnapshot, Path: path, Outcome: observe.Classify(ctx, err),
		Duration: time.Since(started), Items: int64(depth),
	})
}

func (w *Workspace) record(event kernel.Event) workroom.Record {
	return workroom.Record{
		ID: w.EventID(event.Commit), Timestamp: event.Timestamp, Actor: intent.ActorFingerprint(event.Signed.ActorKey),
		Schema: event.Intent.Schema, RestsOn: event.Intent.RestsOn,
		Payload: event.Payload, Attachments: event.Attachments,
	}
}

func (w *Workspace) Verify(ctx context.Context) (kernel.Verification, error) {
	w.snapshotMu.Lock()
	defer w.snapshotMu.Unlock()
	verification, err := kernel.Verify(ctx, w.Store, w.config.Genesis)
	if err != nil {
		return kernel.Verification{}, err
	}
	if err := w.rememberVerifiedFrontier(ctx, verification); err != nil {
		return kernel.Verification{}, err
	}
	return verification, nil
}

// mergeVerifiedFrontier states the conflict rule for the local rollback
// witness: the marker is monotonic local memory, so the deeper verified depth
// stands whoever recorded it, an unchanged audit writes nothing, and two
// different heads claiming one depth cannot both describe this sequence —
// that is a genuine conflict, not a race to resolve by order of arrival.
// A side carrying no marker moves nothing: an absent next keeps the live
// frontier as-is rather than moving it backwards, and an absent base adopts
// whatever is offered.
func mergeVerifiedFrontier(base, next *apphost.VerifiedFrontier) (*apphost.VerifiedFrontier, bool, error) {
	switch {
	case base == nil:
		return next, true, nil
	case next == nil:
		return base, false, nil
	case base.Head == next.Head && base.Depth == next.Depth:
		return base, false, nil
	case base.Depth > next.Depth:
		return base, false, nil
	case base.Depth < next.Depth:
		return next, true, nil
	default:
		return nil, false, fmt.Errorf("refuse conflicting verified frontier: configuration records %s at depth %d while this audit verified %s", base.Head, base.Depth, next.Head)
	}
}

func (w *Workspace) rememberVerifiedFrontier(ctx context.Context, verification kernel.Verification) error {
	// The in-memory marker is only a fast path for the common unchanged case;
	// the authoritative comparison happens against the freshly reloaded file
	// inside updateConfig, so a frontier another process advanced is honoured
	// rather than overwritten. Callers hold snapshotMu, which serializes
	// frontier writers; configMu is taken inside updateConfig, and the store
	// reads below acquire nothing further.
	w.configMu.Lock()
	previous := w.config.VerifiedFrontier
	w.configMu.Unlock()
	if previous != nil {
		if verification.Depth < previous.Depth {
			return fmt.Errorf("refuse verified frontier rollback: depth %d is shorter than previously verified depth %d", verification.Depth, previous.Depth)
		}
		if verification.Head == previous.Head {
			if verification.Depth != previous.Depth {
				return errors.New("refuse inconsistent verified frontier depth")
			}
			return nil
		}
		commits, err := w.Store.RevListAfter(ctx, previous.Head, verification.Head)
		if err != nil {
			return fmt.Errorf("compare verified frontier: %w", err)
		}
		if len(commits) == 0 {
			return fmt.Errorf("refuse non-descendant verified frontier: %s does not contain previously verified head %s", verification.Head, previous.Head)
		}
		parents, err := w.Store.CommitParents(ctx, commits[0])
		if err != nil {
			return fmt.Errorf("compare verified frontier: %w", err)
		}
		if len(parents) != 1 || parents[0] != previous.Head || verification.Depth != previous.Depth+len(commits) {
			return fmt.Errorf("refuse non-descendant verified frontier: %s does not continue previously verified head %s", verification.Head, previous.Head)
		}
	}
	next := &apphost.VerifiedFrontier{Head: verification.Head, Depth: verification.Depth}
	if err := w.updateConfig(func(c *apphost.Config) (bool, error) {
		merged, changed, err := mergeVerifiedFrontier(c.VerifiedFrontier, next)
		if err != nil {
			return false, err
		}
		c.VerifiedFrontier = merged
		return changed, nil
	}); err != nil {
		return fmt.Errorf("persist verified frontier before returning data: local rollback witness could not advance: %w", err)
	}
	return nil
}

func (w *Workspace) ActorViews(ctx context.Context) ([]ActorView, error) {
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	w.configMu.Lock()
	custody := make(map[string]apphost.Actor, len(w.config.Actors))
	for _, actor := range w.config.Actors {
		custody[actor.Fingerprint] = actor
	}
	w.configMu.Unlock()
	views := make([]ActorView, 0, len(snapshot.Projection.Actors))
	for fingerprint, state := range snapshot.Projection.Actors {
		local, held := custody[fingerprint]
		name := state.Name
		if name == "" {
			name = local.Name
		}
		views = append(views, ActorView{
			Name: name, Fingerprint: fingerprint, Kind: state.Kind,
			Roles: append([]string(nil), state.Roles...), Custody: held, Retired: state.Retired,
		})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Name == views[j].Name {
			return views[i].Fingerprint < views[j].Fingerprint
		}
		return views[i].Name < views[j].Name
	})
	return views, nil
}
