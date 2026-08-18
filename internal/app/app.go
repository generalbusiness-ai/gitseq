// Package app joins the semantic-free kernel to the workroom profile for a
// single ordinary Git repository.
package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type Actor struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	KeyFile     string `json:"key_file"`
}

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

// VerifiedFrontier is the newest signed sequence position this local
// workspace has accepted. The marker is local memory, not a witness: its head
// becomes authoritative only when a later audit verifies a sequence that
// contains that exact commit at that exact depth.
type VerifiedFrontier struct {
	Head  string `json:"head"`
	Depth int    `json:"depth"`
}

// ResidentQueueDepth bounds the submissions inside the sequencer at once,
// counting the one holding the lock. Gitseq's resident always sets it: the
// kernel treats zero as unbounded, which is the embedding opt-out and not a
// posture this application takes.
const ResidentQueueDepth = 32

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

type Workspace struct {
	Repo      string
	GitDir    string
	CommonDir string
	MetaDir   string
	Store     gitstore.Store
	Config    Config
	observer  observe.Observer

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
	snapshotProfile    string
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
type LocalRepo struct {
	Path      string         `json:"path"`
	Worktrees []WorktreeView `json:"worktrees"`
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
	VerbState     Verb = "state"
	VerbRatify    Verb = "ratify"
	VerbSupersede Verb = "supersede"
)

// Act is the one application command accepted by every local adapter. RestsOn
// contains all bases for state and only additional bases for supersede; ratify
// and supersede always place their target first.
type Act struct {
	Verb           Verb
	Kind           workroom.Kind
	Text           string
	Body           map[string]string
	Target         string
	RestsOn        []string
	Attachments    map[string][]byte
	IdempotencyKey string

	// CitedOK lets a caller retire a record the documentation still names.
	// A migration legitimately retires first and re-anchors after, so the
	// escape has to exist — but it must be asked for, and only a surface
	// that can offer that choice to a person should ever set it.
	CitedOK bool
}

type Submission struct {
	Result kernel.Result   `json:"result"`
	Record workroom.Record `json:"record"`
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

// LocalWorktrees projects the served checkout and every linked checkout of its
// repository without writing anything to git or the workroom. Git's porcelain
// -z format keeps spaces and other path characters unambiguous; of the paths it
// reports, only the served checkout's own leaves this boundary.
func (w *Workspace) LocalWorktrees(ctx context.Context) (LocalRepo, error) {
	w.worktreesMu.Lock()
	defer w.worktreesMu.Unlock()
	if age := time.Since(w.worktreesCachedAt); !w.worktreesCachedAt.IsZero() && age >= 0 && age < 8*time.Second {
		return LocalRepo{Path: w.repoPathCached, Worktrees: append([]WorktreeView(nil), w.worktreesCached...)}, nil
	}
	output, err := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", w.Repo, "worktree", "list", "--porcelain", "-z").Output()
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
	if top, topErr := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", w.Repo, "rev-parse", "--show-toplevel").Output(); topErr == nil {
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
			status, statusErr := exec.CommandContext(statusCtx, "git", "--no-optional-locks", "-C", item.path, "status", "--porcelain=v1", "--untracked-files=normal").Output()
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
	w.worktreesCached = append(w.worktreesCached[:0], views...)
	w.repoPathCached = selected
	w.worktreesCachedAt = time.Now()
	return LocalRepo{Path: selected, Worktrees: append([]WorktreeView(nil), views...)}, nil
}

func Open(ctx context.Context, repo string) (*Workspace, error) {
	return OpenObserved(ctx, repo, nil)
}

// OpenObserved opens a workspace with an exporter-neutral observer. Ordinary
// callers use Open and pay no observation cost.
func OpenObserved(ctx context.Context, repo string, observer observe.Observer) (*Workspace, error) {
	gitDir, commonDir, err := ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	metaDir := filepath.Join(commonDir, "gitseq")
	content, err := os.ReadFile(filepath.Join(metaDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read gitseq config (run `gs init` first): %w", err)
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, err
	}
	if config.Version != 0 || config.Genesis == "" || config.ObjectFormat == "" || (!config.ReadOnly && config.SequencerKey == "") ||
		(config.VerifiedFrontier != nil && (config.VerifiedFrontier.Head == "" || config.VerifiedFrontier.Depth < 0)) {
		return nil, errors.New("invalid gitseq config")
	}
	if err := validateGenesis(config.ObjectFormat, config.Genesis); err != nil {
		return nil, fmt.Errorf("invalid gitseq config: %w", err)
	}
	workspace := &Workspace{Repo: repo, GitDir: gitDir, CommonDir: commonDir, MetaDir: metaDir, Store: gitstore.Store{Repo: commonDir, Observer: observer}, Config: config, observer: observer}
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
	gitDir, commonDir, err := ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	metaDir := filepath.Join(commonDir, "gitseq")
	if _, err := os.Stat(filepath.Join(metaDir, "config.json")); err == nil {
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
	workspace := &Workspace{Repo: repo, GitDir: gitDir, CommonDir: commonDir, MetaDir: metaDir, Store: store, Config: Config{
		Version: 0, Genesis: genesis, ObjectFormat: format, PayloadCeiling: ceiling, IdempotencyNamespace: "workroom/v0",
		SequencerKey: sequencerKey, Actors: map[string]Actor{operatorName: {Name: operatorName, Fingerprint: fingerprint, KeyFile: actorPath}},
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
	if running.application != defaultApplication {
		bindingRequest, err := workspace.buildBindingRequest(ctx, private, operatorName, selfBinding(running))
		if err != nil {
			return nil, workroom.Record{}, err
		}
		if _, err := workspace.AcceptSubmission(ctx, bindingRequest); err != nil {
			return nil, workroom.Record{}, err
		}
	}
	if err := workspace.save(); err != nil {
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

func (w *Workspace) save() error {
	content, err := json.MarshalIndent(w.Config, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(w.MetaDir, "config.json.tmp")
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(w.MetaDir, "config.json"))
}

func AttachConfig(ctx context.Context, repo, genesis, objectFormat string) (*Workspace, error) {
	if err := validateGenesis(objectFormat, genesis); err != nil {
		return nil, fmt.Errorf("invalid attachment genesis: %w", err)
	}
	gitDir, commonDir, err := ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	metaDir := filepath.Join(commonDir, "gitseq")
	configPath := filepath.Join(metaDir, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		workspace, err := Open(ctx, repo)
		if err != nil {
			return nil, err
		}
		if !workspace.Config.ReadOnly {
			return nil, errors.New("cannot attach over a writable workroom")
		}
		if workspace.Config.Genesis != genesis {
			return nil, errors.New("attached workroom genesis does not match --genesis")
		}
		if workspace.Config.ObjectFormat != objectFormat {
			return nil, errors.New("attached workroom object format changed")
		}
		return workspace, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		return nil, err
	}
	workspace := &Workspace{Repo: repo, GitDir: gitDir, CommonDir: commonDir, MetaDir: metaDir, Store: gitstore.Store{Repo: commonDir}, Config: Config{Version: 0, Genesis: genesis, ObjectFormat: objectFormat, ReadOnly: true}}
	if err := workspace.save(); err != nil {
		return nil, err
	}
	// The configuration is written, so opening it is what selects the
	// interpreter. Attaching and opening then reach the same answer by the same
	// path, and no workspace leaves this package without one.
	return Open(ctx, repo)
}

func (w *Workspace) Actor(name string) (Actor, ed25519.PrivateKey, error) {
	actor, ok := w.Config.Actors[name]
	if !ok {
		return Actor{}, nil, fmt.Errorf("unknown actor %q", name)
	}
	private, err := readActor(actor.KeyFile)
	return actor, private, err
}

func (w *Workspace) AddActor(ctx context.Context, operatorName, name, kind string) (Actor, []workroom.Record, error) {
	if _, exists := w.Config.Actors[name]; exists {
		return Actor{}, nil, fmt.Errorf("actor %q already exists", name)
	}
	if kind == "" {
		kind = "agent"
	}
	if !workroom.IsActorKind(kind) {
		return Actor{}, nil, fmt.Errorf("actor kind must be human, agent, or service, got %q", kind)
	}
	private, fingerprint, path, err := generateActor(filepath.Join(w.MetaDir, "actors"), name)
	if err != nil {
		return Actor{}, nil, err
	}
	_ = private
	actor := Actor{Name: name, Fingerprint: fingerprint, KeyFile: path}
	stateSubmission, err := w.Act(ctx, operatorName, Act{Verb: VerbState, Kind: workroom.KindRoster, Text: name + " joins as " + kind, Body: map[string]string{"actor": fingerprint, "kind": kind, "name": name, "role": "participant"}, RestsOn: []string{w.EventID(w.Config.Genesis)}, IdempotencyKey: "actor-" + name})
	if err != nil {
		return Actor{}, nil, err
	}
	state := stateSubmission.Record
	ratificationSubmission, err := w.Act(ctx, operatorName, Act{Verb: VerbRatify, Target: state.ID, IdempotencyKey: "actor-" + name + "-ratify"})
	if err != nil {
		return Actor{}, nil, err
	}
	ratification := ratificationSubmission.Record
	w.Config.Actors[name] = actor
	if err := w.save(); err != nil {
		return Actor{}, nil, err
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
// what stops it acting again is the removal of its key from local custody,
// because the durable log admits any allowlisted signer. Leaving the key file
// behind after retiring the name would enlarge the shared key directory with
// principals nobody is watching, so it is deleted here.
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
	delete(w.Config.Actors, actor.Name)
	if err := w.save(); err != nil {
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
	rests := append([]string(nil), act.RestsOn...)
	switch act.Verb {
	case VerbState:
		lifecycle, starter := workroom.StarterLifecycle(act.Kind)
		if !starter || lifecycle == workroom.LifecycleReport {
			reporter := intent.ActorFingerprint(private.Public().(ed25519.PublicKey))
			if err := w.validateReportBasis(ctx, reporter, act.Kind, act.Body, rests); err != nil {
				return kernel.Request{}, err
			}
		}
		schema = workroom.SchemaState
		payload = workroom.State{Kind: act.Kind, Text: act.Text, Body: act.Body}
	case VerbRatify:
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
	default:
		return kernel.Request{}, fmt.Errorf("unknown act verb %q", act.Verb)
	}
	return w.buildRequest(ctx, private, actorName, schema, payload, rests, act.Attachments, act.IdempotencyKey)
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
	var promises []workroom.Statement
	for _, rest := range rests {
		statement, ok := statements[rest]
		if ok && lifecycles[statement.Kind] == workroom.LifecyclePromise {
			promises = append(promises, statement)
		}
	}
	if len(promises) != 1 {
		return fmt.Errorf("report requires exactly one effective promise-lifecycle basis in rests_on; got %d", len(promises))
	}
	if promises[0].Actor != reporter {
		return errors.New("report actor must be the promisor of its promise-lifecycle basis")
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
func (w *Workspace) citingDocuments(ctx context.Context, repo, event string) []string {
	output, err := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", repo,
		"grep", "--name-only", "--fixed-strings", event, "--", "*.md").Output()
	if err != nil {
		// git grep exits non-zero when it matches nothing, which is the
		// ordinary case and not a failure. Anything else is worth noticing but
		// never worth blocking a retirement over: a guard that fails closed on
		// its own malfunction would make a broken git a broken workroom.
		return nil
	}
	var pages []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if page := strings.TrimSpace(line); page != "" {
			pages = append(pages, page)
		}
	}
	return pages
}

// RefuseCitedRetirement stops a supersession whose target the documentation
// still names, whatever surface asked for it. BuildActRequest calls it so no
// surface can skip it; `gs batch` also calls it ahead of its first append, so
// a batch that cannot land cleanly lands nothing rather than stopping halfway.
func (w *Workspace) RefuseCitedRetirement(ctx context.Context, target string, allowed bool) error {
	return w.RefuseCitedRetirementInCheckout(ctx, w.Repo, target, allowed)
}

// RefuseCitedRetirementInCheckout evaluates the tracked tree which will
// actually receive a merge. Linked worktrees may legitimately add or remove a
// citation independently of Workspace.Repo before their candidate lands.
func (w *Workspace) RefuseCitedRetirementInCheckout(ctx context.Context, checkout, target string, allowed bool) error {
	if allowed || strings.TrimSpace(target) == "" {
		return nil
	}
	pages := w.citingDocuments(ctx, checkout, target)
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
	tree, err := w.Store.WritePayloadTree(ctx, encoded, attachments)
	if err != nil {
		return kernel.Request{}, err
	}
	if key == "" {
		key, err = randomKey()
		if err != nil {
			return kernel.Request{}, err
		}
	}
	namespace := w.Config.IdempotencyNamespace
	if namespace == "" {
		// Workrooms created before the stable namespace field keep their original
		// retry identity. Changing it in place could replay an outstanding act.
		namespace = "gs/" + actorName
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + w.Config.ObjectFormat + ":" + w.Config.Genesis,
		Schema: schema, PayloadTree: "git:" + w.Config.ObjectFormat + ":" + tree,
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
func (w *Workspace) ResolveActorAddress(address string) (Actor, error) {
	name := strings.TrimPrefix(address, "@")
	if actor, ok := w.Config.Actors[name]; ok {
		return actor, nil
	}
	for _, actor := range w.Config.Actors {
		if actor.Fingerprint == address {
			return actor, nil
		}
	}
	return Actor{}, fmt.Errorf("unknown actor address %q", address)
}

func (w *Workspace) AcceptSubmission(ctx context.Context, request kernel.Request) (Submission, error) {
	done := observe.Begin(ctx, w.observer, observe.OperationSubmit, observe.PathSubmission)
	var resultErr error
	defer func() {
		if done != nil {
			done(resultErr)
		}
	}()
	if w.Config.ReadOnly {
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
			SigningKey: w.Config.SequencerKey, CheckpointEnabled: checkpoint.Enabled, CheckpointPointer: checkpoint.Pointer, PreAppend: w.allowlist,
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
	for _, actor := range w.Config.Actors {
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
	return "git:" + w.Config.ObjectFormat + ":" + w.Config.Genesis + "#git:" + w.Config.ObjectFormat + ":" + commit
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

// SetProjectionRebuildTestGate installs a test-only pause after authenticated
// events are loaded but before their application projection is folded and
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
		return flight
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
	head, err := w.Store.Head(ctx, kernel.Ref(w.Config.Genesis))
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
	loaded, err := w.reader.LoadWithProgress(ctx, w.Config.Genesis, progress)
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
		loaded, err = w.reader.LoadWithProgress(ctx, w.Config.Genesis, progress)
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
	foldStarted := time.Now()
	if loaded.Full {
		records := make([]workroom.Record, 0, len(loaded.Events))
		for _, event := range loaded.Events {
			records = append(records, w.record(event))
		}
		if w.projectionTestGate != nil {
			w.projectionTestGate(len(records))
		}
		w.snapshotFolder = selected.newFolder(records)
		if w.observer != nil {
			w.observer.Record(ctx, observe.Measurement{Operation: observe.OperationFold, Path: path, Outcome: observe.OutcomeOK, Duration: time.Since(foldStarted), Items: int64(len(records))})
		}
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
	verification, err := kernel.Verify(ctx, w.Store, w.Config.Genesis)
	if err != nil {
		return kernel.Verification{}, err
	}
	if err := w.rememberVerifiedFrontier(ctx, verification); err != nil {
		return kernel.Verification{}, err
	}
	return verification, nil
}

func (w *Workspace) rememberVerifiedFrontier(ctx context.Context, verification kernel.Verification) error {
	previous := w.Config.VerifiedFrontier
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
	w.Config.VerifiedFrontier = &VerifiedFrontier{Head: verification.Head, Depth: verification.Depth}
	if err := w.save(); err != nil {
		w.Config.VerifiedFrontier = previous
		return fmt.Errorf("persist verified frontier before returning data: local rollback witness could not advance: %w", err)
	}
	return nil
}

func (w *Workspace) ActorViews(ctx context.Context) ([]ActorView, error) {
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	custody := make(map[string]Actor, len(w.Config.Actors))
	for _, actor := range w.Config.Actors {
		custody[actor.Fingerprint] = actor
	}
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
