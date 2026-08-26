package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Publication records a fact about an ordinary Git remote, and it is not an
// artifact. Merge succession already owns source paths: `gs merge` lands one
// artifact at every changed path and retires the predecessors it covers. A
// second live artifact minted per push at a source path would be an
// accounting row the merger did not create and often cannot lawfully retire,
// so the merge either strands or drags another actor's pointer with it.
//
// So publication mints no artifacts at all. What it records is an
// app-validated `assert`: the named remote accepted this exact head, and this
// watched path changed within it. `assert` is fold-governed only in the sense
// that it is a defined kind with no required fields and no basis constraint —
// no governed kind requires a path, a head, or a remote — so this command
// validates those fields itself, at this boundary, before anything is signed.
// Asserts never enter the artifact map, so merge succession and its left-live
// accounting are untouched by every publication this command ever makes.
const (
	publicationConfigLimit   = 64 << 10
	publicationPathLimit     = 4096
	publicationRefLimit      = 1024
	publicationBasisLimit    = 1024
	publicationBatchLimit    = 256
	publicationGlobLimit     = 64
	publicationBatchesLimit  = 16
	publicationOutboxLimit   = 4 << 20
	publicationTreeLimit     = 16 << 20
	publicationRemoteLimit   = 8 << 10
	publicationAttemptLimit  = 3
	publicationOutboxV1      = 1
	publicationStateV1       = 1
	publicationLockFile      = ".publication.lock"
	publicationReasonLimit   = 512
	publicationHandoverNamed = 3
)

// The body field names are deliberately not `path` and `commit`. Those two
// are the artifact schema's fields, and other readers in this repository key
// on them — merge protection indexing reads `body["commit"]` off any
// statement it reaches. A publication fact that borrowed them would look like
// an artifact to code that never asked what kind it was. The `publication_`
// prefix says what layer wrote it, the same way `merge_` does on a receipt.
const (
	publicationBodyPath     = "publication_path"
	publicationBodyHead     = "publication_head"
	publicationBodyRemote   = "publication_remote"
	publicationBodyRef      = "publication_ref"
	publicationBodyArtifact = "publication_artifact"
)

// The literal idempotency namespaces. They are constants because a retry has
// to compute the same string a crashed run computed, and because a test can
// then pin the exact bytes rather than agreeing with whatever the code says
// today.
const (
	publicationAssertKeyPrefix   = "publication:"
	publicationRetireKeyPrefix   = "publication-retire:"
	publicationWithdrawKeyPrefix = "publication-withdraw:"
)

type publicationEntry struct {
	Path string `json:"path"`
	// Basis is the artifact standing at this exact path and this exact
	// accepted head, when one was found at queue time. Empty means none was,
	// and the fact rests on the governing publication basis instead.
	Basis string `json:"basis,omitempty"`
	// Prior is the same-path publication fact this one succeeds, and it is
	// always this actor's own: a batch whose predecessor belongs to another
	// actor is refused whole, before it is queued. The successor rests on it
	// and retires it, in two separately durable phases.
	Prior string `json:"prior,omitempty"`
	// Withdraw marks an entry that only bare-retires Prior: the path left the
	// repository's watch globs, so its final fact is withdrawn with no
	// successor and no new assert is made.
	Withdraw bool   `json:"withdraw,omitempty"`
	State    string `json:"state"` // pending, done, or abandoned
	// Event is phase one: the successor assert the sequencer accepted. Empty
	// on a withdrawal, which has no successor.
	Event string `json:"event,omitempty"`
	// Retire is phase two: the supersession of Prior. Recording it is not
	// completing it — the entry stays pending until this exact event is
	// verified effective.
	Retire   string `json:"retire,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
	Error    string `json:"error,omitempty"`
}

type publicationBatch struct {
	Remote string `json:"remote"`
	Ref    string `json:"ref"`
	Before string `json:"before,omitempty"`
	Head   string `json:"head"`
	// Basis is the governing publication basis this batch was queued under. It
	// is stored rather than re-read from the flag so a resumed batch signs the
	// citation the operator authorised, not whatever a later run was given.
	Basis   string             `json:"basis"`
	Entries []publicationEntry `json:"entries"`
}

type publicationOutbox struct {
	Version int                `json:"version"`
	Actor   string             `json:"actor"`
	Batches []publicationBatch `json:"batches,omitempty"`
}

// publicationState belongs to the repository, not to an actor. The remote
// branch has one previously reconciled head even when a different actor makes
// the next push; keeping this pointer in an actor outbox republishes every
// watched file whenever custody changes hands.
type publicationState struct {
	Version  int               `json:"version"`
	Observed map[string]string `json:"observed,omitempty"`
}

type publicationOutcome struct {
	Path  string `json:"path"`
	Event string `json:"event,omitempty"`
	// Retire names the supersession of the same-path predecessor, when this
	// run made one.
	Retire string `json:"retire,omitempty"`
	// Outcome is landed, replayed, withdrawn, pending, or abandoned. It says
	// landed or replayed only when every durable phase the entry owed — the
	// successor assert and, where there is a predecessor, its retirement — is
	// verified effective.
	Outcome string `json:"outcome"`
	// Basis names the artifact this fact rested on, and is empty when no
	// artifact stood at the path and head and the governing basis was used.
	Basis string `json:"basis,omitempty"`
	Error string `json:"error,omitempty"`
}

type publicationReport struct {
	Remote    string               `json:"remote,omitempty"`
	Ref       string               `json:"ref,omitempty"`
	Before    string               `json:"before,omitempty"`
	Head      string               `json:"head,omitempty"`
	Basis     string               `json:"basis,omitempty"`
	Ignored   string               `json:"ignored,omitempty"`
	Published []publicationOutcome `json:"published,omitempty"`
	Refused   []string             `json:"refused,omitempty"`
	Warnings  []string             `json:"warnings,omitempty"`
}

type publicationError struct{ report publicationReport }

func (e *publicationError) Error() string {
	if len(e.report.Refused) != 0 {
		return fmt.Sprintf("publication refused %d path or input item(s)", len(e.report.Refused))
	}
	for _, outcome := range e.report.Published {
		if outcome.Outcome == "abandoned" {
			return "publication abandoned an act the fold refused; see the report"
		}
	}
	return "publication outbox still contains pending acts"
}

// publicationAfterQueue is a test-only crash seam. Production leaves it nil;
// tests stop precisely after the durable queue write to prove a later process
// can recover without relying on any in-memory state.
var publicationAfterQueue func() error

// publicationAfterSubmit is a test-only crash seam taken between a successful
// submission and the durable write that records its event. It is the window a
// real process death can fall in, and the only thing that saves the next run
// is the idempotency key.
var publicationAfterSubmit func() error

// publicationAfterLoad is a test-only seam used to hold the transaction after
// it has loaded shared state. A second caller must not reach it until the first
// releases the operating-system lock.
var publicationAfterLoad func()

// runPublication records publication facts only after it has proved the named
// remote branch already stands at the head being published. The local outbox
// is written first; a process death can therefore cause a replay, never an
// invisible omission.
func runPublication(ctx context.Context, workspace *app.Workspace, private ed25519.PrivateKey, actorName, fingerprint, serverURL, remote, ref, basis string) (publicationReport, error) {
	report := publicationReport{Remote: remote, Ref: ref, Basis: basis}
	if remote == "" || !utf8.ValidString(remote) || hasControl(remote) {
		return report, errors.New("publication remote must be a configured remote name with no control bytes")
	}
	if err := validatePublicationBasis(basis); err != nil {
		return report, err
	}
	if ref == "" {
		resolved, err := git(ctx, workspace.Repo, "symbolic-ref", "--quiet", "HEAD")
		if err != nil {
			return report, errors.New("publish requires --ref from a detached checkout")
		}
		ref = strings.TrimSpace(resolved)
		report.Ref = ref
	}
	if err := validatePublicationRef(ref); err != nil {
		return report, err
	}
	if strings.HasPrefix(ref, "refs/tags/") {
		report.Ignored = "tag ref"
		return report, nil
	}
	if !strings.HasPrefix(ref, "refs/heads/") {
		return report, fmt.Errorf("publish ref must be under refs/heads, got %q", ref)
	}

	// One operating-system lock spans the whole transaction: the shared
	// remote/ref frontier, every actor outbox beside it, and the durable acts
	// derived from them. It is the host layer's own advisory-lock primitive,
	// on its own name, so it serialises publishers across linked worktrees
	// without colliding with a configuration update taken inside it.
	_, err := apphost.WithMetaLock(workspace.MetaDir, publicationLockFile, func() (struct{}, error) {
		return struct{}{}, publishLocked(ctx, workspace, private, actorName, fingerprint, serverURL, remote, ref, basis, &report)
	})
	if err != nil {
		return report, err
	}
	if len(report.Refused) != 0 || publicationUnfinished(report) {
		return report, &publicationError{report: report}
	}
	return report, nil
}

func publicationUnfinished(report publicationReport) bool {
	for _, outcome := range report.Published {
		if outcome.Outcome == "pending" || outcome.Outcome == "abandoned" {
			return true
		}
	}
	return false
}

func publishLocked(ctx context.Context, workspace *app.Workspace, private ed25519.PrivateKey, actorName, fingerprint, serverURL, remote, ref, basis string, report *publicationReport) error {
	outboxPath := publicationOutboxPath(workspace.MetaDir, fingerprint)
	statePath := publicationStatePath(workspace.MetaDir)
	outbox, err := loadPublicationOutbox(outboxPath, fingerprint)
	if err != nil {
		return err
	}
	state, err := loadPublicationState(statePath)
	if err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	if err := checkForeignPublicationBatches(filepath.Dir(outboxPath), outboxPath, remote, ref, snapshot.Projection, report); err != nil {
		return err
	}
	if publicationAfterLoad != nil {
		publicationAfterLoad()
	}
	if err := reconcilePublicationOutbox(ctx, workspace, private, actorName, serverURL, outboxPath, &outbox, statePath, &state, report); err != nil {
		return err
	}
	if err := validateConfiguredRemote(ctx, workspace.Repo, remote); err != nil {
		return err
	}
	head, err := remoteBranchHead(ctx, workspace.Repo, remote, ref)
	if err != nil {
		return err
	}
	report.Head = head
	if _, err := git(ctx, workspace.Repo, "cat-file", "-e", head+"^{commit}"); err != nil {
		return fmt.Errorf("remote head %s is not present locally; fetch it before publishing: %w", head, err)
	}
	configBytes, err := gitOutputLimited(ctx, workspace.Repo, publicationConfigLimit, "show", head+":.gitseq")
	if err != nil {
		return fmt.Errorf("read tracked .gitseq at published head %s: %w", head, err)
	}
	patterns, err := parsePublicationConfig(configBytes)
	if err != nil {
		return err
	}
	key := publicationFrontierKey(remote, ref)
	before := state.Observed[key]
	report.Before = before
	changes, err := changedPublicationPaths(ctx, workspace.Repo, before, head)
	if err != nil {
		return err
	}
	paths, refused := selectWatchedPaths(changes, patterns)
	report.Refused = append(report.Refused, refused...)
	if err := validatePublicationBatchSize(len(paths)); err != nil {
		report.Refused = append(report.Refused, err.Error())
		return nil
	}

	// The projection has to be re-read here: reconciliation above may have
	// landed acts, and both the suppression check and the successor chain read
	// facts those acts created.
	snapshot, err = workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	facts := livePublicationFacts(snapshot.Projection, remote)
	paths = unpublishedPaths(facts, head, paths)
	withdrawn := withdrawnPublicationPaths(facts, patterns, fingerprint)
	if err := validatePublicationBatchSize(len(paths) + len(withdrawn)); err != nil {
		report.Refused = append(report.Refused, err.Error())
		return nil
	}
	// Before anything is queued, appended, or written to the shared frontier.
	// A batch that would hand one path's wire to a second author is refused
	// whole, so nothing partial survives the refusal.
	if err := refusePublicationHandover(facts, paths, fingerprint); err != nil {
		return err
	}
	if len(paths) == 0 && len(withdrawn) == 0 {
		state.Observed[key] = head
		return savePublicationState(statePath, state)
	}
	batch := publicationBatch{Remote: remote, Ref: ref, Before: before, Head: head, Basis: basis}
	for _, path := range paths {
		entry := publicationEntry{Path: path, State: "pending"}
		entry.Basis = artifactAtPathAndHead(snapshot.Projection, path, head)
		if prior, found := facts[path]; found {
			entry.Prior = prior.Event
		}
		batch.Entries = append(batch.Entries, entry)
	}
	for _, path := range withdrawn {
		batch.Entries = append(batch.Entries, publicationEntry{
			Path: path, State: "pending", Withdraw: true, Prior: facts[path].Event,
		})
	}
	outbox.Batches = append(outbox.Batches, batch)
	if err := validatePublicationOutbox(outbox); err != nil {
		return fmt.Errorf("derived publication batch is not queueable: %w", err)
	}
	if err := savePublicationOutbox(outboxPath, outbox); err != nil {
		return fmt.Errorf("queue publication before writing: %w", err)
	}
	if publicationAfterQueue != nil {
		if err := publicationAfterQueue(); err != nil {
			return err
		}
	}
	return reconcilePublicationOutbox(ctx, workspace, private, actorName, serverURL, outboxPath, &outbox, statePath, &state, report)
}

func publicationFrontierKey(remote, ref string) string { return remote + "\x00" + ref }

// refusePublicationHandover is the contract in one place: one live publication
// wire per remote and path, succeeded by one author from end to end.
//
// The earlier shape recorded a cross-author succession as a debt — the
// successor rested on the foreign predecessor, the supersession was skipped
// because the fold admits one only from the target's author or a ratifier, and
// the report named a retirement somebody else owed. That manufactured standing
// retirement obligations nobody in the run could lawfully close, which is the
// same accounting the command already refuses to create by minting no
// artifacts. A foreign predecessor is therefore not a debt to record while
// proceeding. It is a reason to refuse the whole derived batch before a byte
// is queued, an act appended, or the shared frontier moved.
//
// Holding `ratifier` is deliberately no exception. A ratifier may lawfully
// retire another actor's fact, but doing it inside publication would end one
// author's wire and begin another's in a single unreviewed step, and leave the
// two chains related by nothing a reader could follow. Terminating an orphan
// stays a separate, deliberate act — see `gs publish`.
func refusePublicationHandover(facts map[string]workroom.Statement, paths []string, fingerprint string) error {
	var named []string
	foreign := 0
	for _, path := range paths {
		fact, found := facts[path]
		if !found || fact.Actor == fingerprint {
			continue
		}
		foreign++
		if len(named) < publicationHandoverNamed {
			named = append(named, fmt.Sprintf("%s (live fact %s by actor %s)",
				truncateForError(path), truncateForError(fact.Event), truncateForError(fact.Actor)))
		}
	}
	if foreign == 0 {
		return nil
	}
	listed := strings.Join(named, "; ")
	if foreign > len(named) {
		listed += fmt.Sprintf("; and %d more", foreign-len(named))
	}
	return fmt.Errorf("publication refuses this whole batch: %d watched path(s) already carry a live publication fact authored by another actor: %s. "+
		"There is one live publication wire per remote and path, succeeded by one author end to end. "+
		"The same publisher must continue the chain, or a ratifier must terminally retire an orphan before a new actor begins a fresh chain",
		foreign, listed)
}

func validatePublicationBatchSize(count int) error {
	if count > publicationBatchLimit {
		return fmt.Errorf("%d watched paths exceed the batch ceiling of %d", count, publicationBatchLimit)
	}
	return nil
}

// validatePublicationBasis checks the governing citation this command is asked
// to stand a fact on. It is a flag rather than a line in the tracked `.gitseq`
// on purpose: the configuration comes out of the head a remote accepted, so a
// pushed commit would otherwise choose the durable citation of an act signed
// by whoever runs this command next. Which event governs publication here is
// the operator's answer, given in the operator's own process.
func validatePublicationBasis(basis string) error {
	if basis == "" {
		return errors.New("publish requires --basis naming the event that governs publication in this repository")
	}
	if !utf8.ValidString(basis) || hasControl(basis) || len(basis) > publicationBasisLimit || strings.HasPrefix(basis, "-") {
		return fmt.Errorf("invalid publication basis %q", truncateForError(basis))
	}
	return nil
}

func publicationOutboxDir(metaDir string) string { return filepath.Join(metaDir, "publication-outbox") }

func publicationOutboxPath(metaDir, fingerprint string) string {
	digest := sha256.Sum256([]byte(fingerprint))
	return filepath.Join(publicationOutboxDir(metaDir), hex.EncodeToString(digest[:])+".json")
}

func publicationStatePath(metaDir string) string {
	return filepath.Join(publicationOutboxDir(metaDir), "state.json")
}

// readBoundedFile applies the bound before reading rather than after, the way
// internal/app reads a resident advertisement: one byte past the limit is read
// so content exactly at the limit still parses and anything larger is refused
// without ever being held whole. Reading first and measuring afterwards makes
// the caller's memory the attacker's choice.
func readBoundedFile(path string, limit int64, what string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readBounded(file, limit, what)
}

func readBounded(reader io.Reader, limit int64, what string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", what, err)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d bytes it may be", what, limit)
	}
	return content, nil
}

func loadPublicationOutbox(path, fingerprint string) (publicationOutbox, error) {
	outbox := publicationOutbox{Version: publicationOutboxV1, Actor: fingerprint}
	content, err := readBoundedFile(path, publicationOutboxLimit, "publication outbox")
	if errors.Is(err, os.ErrNotExist) {
		return outbox, nil
	}
	if err != nil {
		return outbox, err
	}
	if err := decodePublicationJSON(content, &outbox); err != nil {
		return outbox, fmt.Errorf("decode publication outbox: %w", err)
	}
	if outbox.Version != publicationOutboxV1 || outbox.Actor != fingerprint {
		return outbox, errors.New("invalid publication outbox")
	}
	if err := validatePublicationOutbox(outbox); err != nil {
		return outbox, err
	}
	return outbox, nil
}

func decodePublicationJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing content after the record")
	}
	return nil
}

func validatePublicationOutbox(outbox publicationOutbox) error {
	if len(outbox.Batches) > publicationBatchesLimit {
		return errors.New("publication outbox contains too many batches")
	}
	for _, batch := range outbox.Batches {
		if batch.Remote == "" || !utf8.ValidString(batch.Remote) || hasControl(batch.Remote) ||
			validatePublicationRef(batch.Ref) != nil || !strings.HasPrefix(batch.Ref, "refs/heads/") ||
			validatePublicationBasis(batch.Basis) != nil ||
			!validHexOID(batch.Head) || (batch.Before != "" && !validHexOID(batch.Before)) ||
			len(batch.Entries) == 0 || len(batch.Entries) > publicationBatchLimit {
			return errors.New("publication outbox contains an invalid batch")
		}
		seen := make(map[string]bool, len(batch.Entries))
		for _, entry := range batch.Entries {
			if validatePublicationPath(entry.Path) != nil || seen[entry.Path] {
				return errors.New("publication outbox contains an invalid or duplicate path")
			}
			seen[entry.Path] = true
			if entry.Attempts < 0 || entry.Attempts > publicationAttemptLimit {
				return errors.New("publication outbox contains an out-of-range attempt count")
			}
			if entry.Withdraw && entry.Prior == "" {
				return errors.New("publication outbox contains a withdrawal with nothing to withdraw")
			}
			switch entry.State {
			case "pending":
			case "done", "abandoned":
				if entry.Event == "" && entry.Retire == "" {
					return errors.New("publication outbox contains a completed entry without an event")
				}
			default:
				return errors.New("publication outbox contains an invalid entry state")
			}
		}
	}
	return nil
}

func loadPublicationState(path string) (publicationState, error) {
	state := publicationState{Version: publicationStateV1, Observed: make(map[string]string)}
	content, err := readBoundedFile(path, publicationOutboxLimit, "publication state")
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := decodePublicationJSON(content, &state); err != nil {
		return state, fmt.Errorf("decode publication state: %w", err)
	}
	if state.Version != publicationStateV1 {
		return state, errors.New("invalid publication state")
	}
	if state.Observed == nil {
		state.Observed = make(map[string]string)
	}
	for key, head := range state.Observed {
		remote, ref, found := strings.Cut(key, "\x00")
		if !found || remote == "" || !utf8.ValidString(remote) || hasControl(remote) || validatePublicationRef(ref) != nil ||
			!strings.HasPrefix(ref, "refs/heads/") || !validHexOID(head) {
			return state, errors.New("publication state contains an invalid observed head")
		}
	}
	return state, nil
}

// checkForeignPublicationBatches stops one actor advancing the shared
// remote/ref frontier past another actor's unsent work — and stops there.
//
// Two things it used to do were unbounded wedges rather than safety. It
// refused on any foreign batch whatever branch it belonged to, so one actor's
// queue on one branch blocked publication of every other branch in the
// repository. And it refused on a batch belonging to an actor the workroom has
// since retired, who can no longer sign anything: nobody could ever drain that
// queue, so the refusal had no exit at all. The bound now is the frontier the
// two runs actually share, and the fold's own liveness fact: a live actor's
// overlapping queue refuses, a retired actor's queue is reported as orphaned
// and stepped over. Clearing an orphaned file, and retiring the facts a
// departed actor left behind, belongs to a ratifier — see `gs publish`.
//
// The caller holds the publication lock, so this scan and the later state
// update are one cross-process transaction.
func checkForeignPublicationBatches(directory, currentPath, remote, ref string, projection workroom.Projection, report *publicationReport) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	key := publicationFrontierKey(remote, ref)
	for _, item := range entries {
		path := filepath.Join(directory, item.Name())
		if item.IsDir() || filepath.Clean(path) == filepath.Clean(currentPath) || item.Name() == "state.json" || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		content, err := readBoundedFile(path, publicationOutboxLimit, "another actor's publication outbox")
		if err != nil {
			return err
		}
		var other publicationOutbox
		if err := decodePublicationJSON(content, &other); err != nil || other.Version != publicationOutboxV1 || other.Actor == "" {
			return errors.New("another actor's publication outbox is invalid")
		}
		if err := validatePublicationOutbox(other); err != nil {
			return fmt.Errorf("another actor's publication outbox is invalid: %w", err)
		}
		overlapping := 0
		for _, batch := range other.Batches {
			if publicationFrontierKey(batch.Remote, batch.Ref) == key {
				overlapping++
			}
		}
		if overlapping == 0 {
			continue
		}
		if state, known := projection.Actors[other.Actor]; !known || state.Retired {
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"actor %s left %d unresolved publication batch(es) on %s and is no longer a live participant; a ratifier must clear %s",
				truncateForError(other.Actor), overlapping, ref, item.Name()))
			continue
		}
		return fmt.Errorf("actor %s has unresolved publication work on %s", truncateForError(other.Actor), ref)
	}
	return nil
}

func savePublicationOutbox(path string, outbox publicationOutbox) error {
	return savePublicationJSON(path, outbox)
}

func savePublicationState(path string, state publicationState) error {
	return savePublicationJSON(path, state)
}

func savePublicationJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".outbox-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// reconcilePublicationOutbox drains queued entries and never returns a
// transport or visibility failure as success. Every state change is fsynced
// before the next act is attempted, so a death mid-drain resumes exactly here.
func reconcilePublicationOutbox(ctx context.Context, workspace *app.Workspace, private ed25519.PrivateKey, actorName, serverURL, path string, outbox *publicationOutbox, statePath string, state *publicationState, report *publicationReport) error {
	// An empty queue writes nothing. A run that refuses before it derives a
	// batch must leave no trace of an outbox at all, so "nothing was queued"
	// stays a fact a reader — and a test — can check by looking.
	if len(outbox.Batches) == 0 {
		return nil
	}
	for batchIndex := range outbox.Batches {
		batch := &outbox.Batches[batchIndex]
		for entryIndex := range batch.Entries {
			entry := &batch.Entries[entryIndex]
			switch entry.State {
			case "done":
				continue
			case "abandoned":
				report.Published = append(report.Published, publicationOutcome{
					Path: entry.Path, Event: entry.Event, Retire: entry.Retire,
					Outcome: "abandoned", Error: entry.Error})
				continue
			}
			if err := reconcilePublicationEntry(ctx, workspace, private, actorName, serverURL, path, outbox, batch, entry, report); err != nil {
				return err
			}
		}
	}
	kept := outbox.Batches[:0]
	for _, batch := range outbox.Batches {
		settled := true
		for _, entry := range batch.Entries {
			settled = settled && entry.State != "pending"
		}
		if settled {
			// The frontier advances on a settled batch even when an entry was
			// abandoned. Holding it back would re-derive the same refused act on
			// every later run, which is the wedge this design exists to remove;
			// the abandonment is reported instead, and the command exits
			// non-zero, so nothing is presented as published that was not.
			state.Observed[publicationFrontierKey(batch.Remote, batch.Ref)] = batch.Head
			continue
		}
		kept = append(kept, batch)
	}
	outbox.Batches = kept
	if err := savePublicationState(statePath, *state); err != nil {
		return err
	}
	return savePublicationOutbox(path, *outbox)
}

// publicationVerdict is what the sequencer that accepted an act says about
// that exact event, and nothing weaker. Absence is pending, never a verdict.
type publicationVerdict int

const (
	publicationUnseen publicationVerdict = iota
	publicationEffective
	publicationIneffective
)

// verifyPublicationEvent asks the authority about one exact event. It is the
// single place both durable phases get their answer, so the envelope and
// decision identity checks inside publicationDecision cover the retirement as
// well as the successor: an entry may never be closed on a verdict made about
// a record nobody here named.
func verifyPublicationEvent(ctx context.Context, workspace *app.Workspace, serverURL, event string) (publicationVerdict, string) {
	if event == "" {
		return publicationUnseen, "nothing has been submitted for this phase yet"
	}
	decision, judged, err := publicationDecision(ctx, workspace, serverURL, event)
	if err != nil || !judged {
		reason := "the submitted event is not yet visible in a verified projection"
		if err != nil {
			reason += ": " + err.Error()
		}
		return publicationUnseen, reason
	}
	if decision.Verdict != workroom.Effective {
		return publicationIneffective, truncateForError(decision.Reason)
	}
	return publicationEffective, ""
}

// reconcilePublicationEntry drives one entry through the two durable phases a
// publication owes: the successor assert, and the retirement of the same-path
// predecessor it succeeds. Both are separately durable and separately verified
// against the exact event the sequencer accepted, and the entry stays pending
// until every phase it owes is effective — so a batch whose retirement has not
// landed neither settles nor advances the shared frontier.
//
// A recovered entry whose successor is already visible resumes at phase two.
// Visibility is not completion: the predecessor is still live, and stopping
// there is exactly how a crash used to leave two live facts at one path.
//
// The predecessor is always this actor's own. A batch carrying a foreign one
// is refused whole before it is queued, so no cross-author supersession is
// ever signed and none is ever owed.
func reconcilePublicationEntry(ctx context.Context, workspace *app.Workspace, private ed25519.PrivateKey, actorName, serverURL, path string, outbox *publicationOutbox, batch *publicationBatch, entry *publicationEntry, report *publicationReport) error {
	// Judging what is already recorded signs nothing, so it costs no attempt.
	// An entry that landed before its response was observed must be able to
	// settle even at the ceiling.
	assertVerdict, assertReason := publicationEffective, ""
	if !entry.Withdraw {
		if assertVerdict, assertReason = verifyPublicationEvent(ctx, workspace, serverURL, entry.Event); assertVerdict == publicationIneffective {
			return abandonPublicationEntry(assertReason, path, outbox, entry, report)
		}
	}
	retireVerdict, retireReason := publicationEffective, ""
	if entry.Prior != "" {
		if retireVerdict, retireReason = verifyPublicationEvent(ctx, workspace, serverURL, entry.Retire); retireVerdict == publicationIneffective {
			return abandonPublicationEntry(retireReason, path, outbox, entry, report)
		}
	}
	if assertVerdict == publicationEffective && retireVerdict == publicationEffective {
		return settlePublicationEntry("replayed", path, outbox, entry, report)
	}

	// Something still owes a signature. One reconcile pass costs one attempt
	// however many phases it drives, so the ceiling bounds the entry rather
	// than each half of it.
	if entry.Attempts >= publicationAttemptLimit {
		return abandonPublicationEntry(fmt.Sprintf("gave up after %d attempts: %s", entry.Attempts, entry.Error), path, outbox, entry, report)
	}
	entry.Attempts++
	if err := savePublicationOutbox(path, *outbox); err != nil {
		return err
	}

	outcome := "replayed"
	if assertVerdict != publicationEffective {
		outcome = "landed"
		submission, err := submitSigned(ctx, workspace, serverURL, actorName, private, publicationAssertAct(batch, entry))
		if err != nil {
			return recordPublicationPending(path, outbox, entry, report, err.Error())
		}
		entry.Event = submission.Record.ID
		if publicationAfterSubmit != nil {
			if seam := publicationAfterSubmit(); seam != nil {
				return seam
			}
		}
		if err := savePublicationOutbox(path, *outbox); err != nil {
			return err
		}
		if submission.Result.Replay {
			outcome = "replayed"
		}
		switch assertVerdict, assertReason = verifyPublicationEvent(ctx, workspace, serverURL, entry.Event); assertVerdict {
		case publicationIneffective:
			return abandonPublicationEntry(assertReason, path, outbox, entry, report)
		case publicationUnseen:
			return recordPublicationPending(path, outbox, entry, report, assertReason)
		}
	}

	if retireVerdict != publicationEffective {
		submission, err := submitSigned(ctx, workspace, serverURL, actorName, private, publicationRetireAct(entry))
		if err != nil {
			return recordPublicationPending(path, outbox, entry, report, err.Error())
		}
		entry.Retire = submission.Record.ID
		if err := savePublicationOutbox(path, *outbox); err != nil {
			return err
		}
		switch retireVerdict, retireReason = verifyPublicationEvent(ctx, workspace, serverURL, entry.Retire); retireVerdict {
		case publicationIneffective:
			return abandonPublicationEntry(retireReason, path, outbox, entry, report)
		case publicationUnseen:
			return recordPublicationPending(path, outbox, entry, report, retireReason)
		}
	}
	return settlePublicationEntry(outcome, path, outbox, entry, report)
}

// settlePublicationEntry closes an entry every phase of which is verified
// effective. Nothing else reaches here, so a landed or replayed outcome in the
// report means the whole succession stands: the successor is effective and its
// predecessor is retired.
func settlePublicationEntry(outcome, path string, outbox *publicationOutbox, entry *publicationEntry, report *publicationReport) error {
	entry.State = "done"
	entry.Error = ""
	result := publicationOutcome{Path: entry.Path, Event: entry.Event, Retire: entry.Retire, Outcome: outcome, Basis: entry.Basis}
	if entry.Withdraw {
		result.Outcome = "withdrawn"
	}
	report.Published = append(report.Published, result)
	return savePublicationOutbox(path, *outbox)
}

// abandonPublicationEntry ends an entry that can never complete: an act the
// fold ruled ineffective, whose idempotency key is spent so no replay could
// reach a different verdict, or one retried past the attempt ceiling. Either
// half of the succession can end here, and neither is ever presented as
// published — a successor whose retirement never became effective is reported
// abandoned, so partial succession is never shown as a publication.
func abandonPublicationEntry(reason, path string, outbox *publicationOutbox, entry *publicationEntry, report *publicationReport) error {
	entry.State = "abandoned"
	entry.Error = truncateForError(reason)
	report.Published = append(report.Published, publicationOutcome{
		Path: entry.Path, Event: entry.Event, Retire: entry.Retire, Outcome: "abandoned", Error: entry.Error})
	return savePublicationOutbox(path, *outbox)
}

func recordPublicationPending(path string, outbox *publicationOutbox, entry *publicationEntry, report *publicationReport, reason string) error {
	entry.Error = truncateForError(reason)
	if err := savePublicationOutbox(path, *outbox); err != nil {
		return fmt.Errorf("%s; preserve outbox: %w", entry.Error, err)
	}
	report.Published = append(report.Published, publicationOutcome{
		Path: entry.Path, Event: entry.Event, Outcome: "pending", Error: entry.Error})
	return errors.New(entry.Error)
}

// publicationRetireAct is phase two. A successor's retirement rests on the
// successor and names it, so exactly one fact per remote and path stands live
// and a reader can follow the wire in both directions. A withdrawal names
// nothing beyond the fact it retires, because the path left the watch globs
// and nothing takes its place.
//
// The target is always this actor's own fact: publication refuses a batch
// whose predecessor belongs to another actor, so this never signs a
// supersession the fold would refuse.
func publicationRetireAct(entry *publicationEntry) app.Act {
	if entry.Withdraw {
		return app.Act{
			Verb: app.VerbSupersede, Target: entry.Prior,
			Text:           "The repository stopped watching " + entry.Path + ", so its final publication fact is withdrawn.",
			IdempotencyKey: publicationWithdrawKey(entry.Prior),
		}
	}
	return app.Act{
		Verb: app.VerbSupersede, Target: entry.Prior,
		Text:           "Publication succeeded the previous fact at " + entry.Path + ".",
		RestsOn:        []string{entry.Event},
		IdempotencyKey: publicationRetireKey(entry.Prior),
	}
}

func publicationAssertAct(batch *publicationBatch, entry *publicationEntry) app.Act {
	body := map[string]string{
		publicationBodyPath:   entry.Path,
		publicationBodyHead:   batch.Head,
		publicationBodyRemote: batch.Remote,
		publicationBodyRef:    batch.Ref,
	}
	text := batch.Remote + " accepted " + batch.Head + " and " + entry.Path + " changed within it."
	// Condition two, in the order it is written: an artifact standing at this
	// exact path and this exact head is preferred, unsettled candidate
	// included, because it is the strongest thing in the log saying what this
	// head contains at this path. Only when there is none does the fact stand
	// on the governing publication basis, and it then says so, so a reader is
	// never left to infer from an absent field that nobody looked.
	rests := []string{batch.Basis}
	if entry.Basis != "" {
		body[publicationBodyArtifact] = entry.Basis
		rests = []string{entry.Basis}
	} else {
		text += " No artifact stood at this path and head, so this fact rests on the governing publication basis."
	}
	if entry.Prior != "" {
		rests = append(rests, entry.Prior)
	}
	return app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert,
		Text: text, Body: body, RestsOn: rests,
		IdempotencyKey: publicationAssertKey(batch.Remote, batch.Ref, entry.Path, batch.Head),
	}
}

// publicationAssertKey is the exact replay identity of one publication fact:
// this remote, this ref, this path, this accepted head. Running the command
// twice over an unchanged remote therefore cannot mint a second fact even if
// the frontier and the projection were both lost, and a crash between
// submission and the durable write of the event id replays instead of
// duplicating.
func publicationAssertKey(remote, ref, path, head string) string {
	return publicationAssertKeyPrefix + publicationDigest(remote, ref, path, head)
}

func publicationRetireKey(prior string) string {
	return publicationRetireKeyPrefix + publicationDigest(prior)
}

func publicationWithdrawKey(prior string) string {
	return publicationWithdrawKeyPrefix + publicationDigest(prior)
}

func publicationDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// publicationDecision asks the sequencer that accepted the submission, and
// only that one. A read may fall back to the verified local fold and say so;
// a durable write may not, because a local answer about an act a resident
// admitted is an answer about a different frontier, and taking it would let
// the outbox record as settled an act this process cannot see the verdict of.
// Absence from the authority is pending, never a fold verdict.
func publicationDecision(ctx context.Context, workspace *app.Workspace, serverURL, event string) (workroom.Decision, bool, error) {
	if serverURL == "" {
		return localPublicationDecision(ctx, workspace, event)
	}
	var inspection statusview.ItemInspection
	if err := residentclient.New(10*time.Second).PostJSON(ctx, serverURL, "/v0/inspect",
		statusview.InspectRequest{Event: event}, residentclient.SubmissionResponseLimit, &inspection); err != nil {
		return workroom.Decision{}, false, err
	}
	// The answer has to be about the question. A resident that returns some
	// other event's verdict — misrouted, cached, or hostile — would otherwise
	// settle this entry on a decision made about a record nobody here named.
	if inspection.Event != event {
		return workroom.Decision{}, false, fmt.Errorf("resident answered for event %q, not the queried %q",
			truncateForError(inspection.Event), truncateForError(event))
	}
	if inspection.Decision == nil {
		return workroom.Decision{}, false, nil
	}
	if inspection.Decision.Event != event {
		return workroom.Decision{}, false, fmt.Errorf("resident returned a decision about %q, not the queried %q",
			truncateForError(inspection.Decision.Event), truncateForError(event))
	}
	return *inspection.Decision, true, nil
}

func localPublicationDecision(ctx context.Context, workspace *app.Workspace, event string) (workroom.Decision, bool, error) {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return workroom.Decision{}, false, err
	}
	decision, judged := snapshot.Projection.Decision(event)
	return decision, judged, nil
}

// truncateForError bounds text that came from a remote, a file, or a fold
// reason before it reaches an error string or a report field. None of those
// sources is this program's, and an unbounded one turns a diagnostic into a
// denial of service against whoever reads the output.
func truncateForError(value string) string {
	runes := []rune(value)
	if len(runes) <= publicationReasonLimit {
		return value
	}
	return string(runes[:publicationReasonLimit]) + "…"
}

// livePublicationFacts indexes the current publication fact per watched path
// for one remote: not retired, ruled effective, and carrying this command's
// own body fields. The newest by log position wins. It answers two questions
// with one index — which predecessor a successor rests on and retires, and
// which actor authored it, which is what the handover refusal reads.
func livePublicationFacts(projection workroom.Projection, remote string) map[string]workroom.Statement {
	facts := make(map[string]workroom.Statement)
	for _, statement := range projection.Statements {
		if statement.Kind != workroom.KindAssert || statement.Retired {
			continue
		}
		if statement.Body[publicationBodyRemote] != remote {
			continue
		}
		path := statement.Body[publicationBodyPath]
		if path == "" || statement.Body[publicationBodyHead] == "" {
			continue
		}
		if decision, judged := projection.Decision(statement.Event); !judged || decision.Verdict != workroom.Effective {
			continue
		}
		if current, found := facts[path]; found && current.Sequence >= statement.Sequence {
			continue
		}
		facts[path] = statement
	}
	return facts
}

// unpublishedPaths drops the paths whose current fact already names this exact
// accepted head. It is the suppression that makes an interrupted run safe to
// repeat: the diff can be non-empty and every path in it already published,
// which is what a lost frontier write leaves behind.
func unpublishedPaths(facts map[string]workroom.Statement, head string, paths []string) []string {
	remaining := paths[:0]
	for _, path := range paths {
		if fact, found := facts[path]; found && fact.Body[publicationBodyHead] == head {
			continue
		}
		remaining = append(remaining, path)
	}
	return remaining
}

// withdrawnPublicationPaths names the paths this actor must bare-retire: it
// holds the live fact, and the repository's watch globs at the accepted head
// no longer cover the path. Another actor's orphan is deliberately not
// included — the fold would refuse the act — and `gs publish` says who clears
// those instead.
func withdrawnPublicationPaths(facts map[string]workroom.Statement, patterns []*regexp.Regexp, fingerprint string) []string {
	var paths []string
	for path, fact := range facts {
		if fact.Actor != fingerprint || matchesAnyPattern(patterns, path) {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func matchesAnyPattern(patterns []*regexp.Regexp, path string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}

// artifactAtPathAndHead answers condition two's preferred basis: a live
// artifact standing at this exact path and this exact accepted head. Live
// includes an unsettled candidate — one whose merge has not landed — because
// what is being cited is the pointer, not its settlement. Retired is excluded:
// nothing may rest on a withdrawn pointer. Ties break on the event identifier
// so two runs of this command over one log choose the same one.
func artifactAtPathAndHead(projection workroom.Projection, path, head string) string {
	chosen := ""
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path != path || artifact.Commit != head {
			continue
		}
		if chosen == "" || artifact.Event < chosen {
			chosen = artifact.Event
		}
	}
	return chosen
}

func validatePublicationRef(ref string) error {
	if ref == "" || !utf8.ValidString(ref) || len(ref) > publicationRefLimit || hasControl(ref) || strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid publication ref %q", truncateForError(ref))
	}
	return nil
}

func remoteBranchHead(ctx context.Context, repo, remote, ref string) (string, error) {
	output, err := gitOutputLimited(ctx, repo, publicationRemoteLimit, "ls-remote", "--refs", "--exit-code", "--", remote, ref)
	if err != nil {
		return "", fmt.Errorf("read published remote head: %w", err)
	}
	fields := strings.Fields(string(output))
	// `git ls-remote <remote> <pattern>` matches whole path components from
	// the right, so asking for `refs/heads/main` also answers for a branch
	// named `foo/refs/heads/main`. Without the exact equality below, a remote
	// that carries the near-miss and not the branch would hand back another
	// branch's head and this command would publish it as the accepted head of
	// the ref the operator named.
	if len(fields) != 2 || fields[1] != ref || !validHexOID(fields[0]) {
		return "", fmt.Errorf("remote returned an invalid exact head for %q", truncateForError(ref))
	}
	return fields[0], nil
}

func validHexOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func parsePublicationConfig(content []byte) ([]*regexp.Regexp, error) {
	if len(content) > publicationConfigLimit {
		return nil, fmt.Errorf("tracked .gitseq exceeds %d bytes", publicationConfigLimit)
	}
	if !utf8.Valid(content) {
		return nil, errors.New("tracked .gitseq is not valid UTF-8")
	}
	var patterns []*regexp.Regexp
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 1024), publicationConfigLimit)
	for line := 1; scanner.Scan(); line++ {
		value := strings.TrimSpace(scanner.Text())
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		directive, pattern, found := strings.Cut(value, " ")
		pattern = strings.TrimSpace(pattern)
		if !found || directive != "watch" || pattern == "" {
			return nil, fmt.Errorf("tracked .gitseq line %d must be `watch <glob>`", line)
		}
		if !utf8.ValidString(pattern) || len(pattern) > publicationPathLimit || hasControl(pattern) ||
			strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") || hasTraversalComponent(pattern) {
			return nil, fmt.Errorf("tracked .gitseq line %d has an invalid watch glob", line)
		}
		compiled, err := compilePublicationGlob(pattern)
		if err != nil {
			return nil, fmt.Errorf("tracked .gitseq line %d: %w", line, err)
		}
		patterns = append(patterns, compiled)
		if len(patterns) > publicationGlobLimit {
			return nil, fmt.Errorf("tracked .gitseq has more than %d watch globs", publicationGlobLimit)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		return nil, errors.New("tracked .gitseq has no watch globs")
	}
	return patterns, nil
}

func compilePublicationGlob(pattern string) (*regexp.Regexp, error) {
	var expression strings.Builder
	expression.WriteString("^")
	for index := 0; index < len(pattern); {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				expression.WriteString("(?s:.*)")
				index += 2
			} else {
				expression.WriteString("[^/]*")
				index++
			}
		case '?':
			expression.WriteString("[^/]")
			index++
		case '[':
			return nil, errors.New("watch globs support *, **, and ?, not character classes")
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[index : index+1]))
			index++
		}
	}
	expression.WriteString("$")
	return regexp.Compile(expression.String())
}

type publicationChange struct {
	Path string
	Kind byte // A or M; deletions are absent and a rename contributes its new path
}

func changedPublicationPaths(ctx context.Context, repo, before, head string) ([]publicationChange, error) {
	if before == "" {
		output, err := gitOutputLimited(ctx, repo, publicationTreeLimit, "ls-tree", "-rz", "--name-only", head)
		if err != nil {
			return nil, err
		}
		var changes []publicationChange
		for _, item := range splitNUL(output) {
			changes = append(changes, publicationChange{Path: item, Kind: 'A'})
		}
		return changes, nil
	}
	if !validHexOID(before) {
		return nil, errors.New("publication state has an invalid previous head")
	}
	output, err := gitOutputLimited(ctx, repo, publicationTreeLimit, "diff", "--name-status", "-z", "--find-renames", before, head, "--")
	if err != nil {
		return nil, fmt.Errorf("list paths changed between published heads: %w", err)
	}
	fields := splitNUL(output)
	var changes []publicationChange
	for index := 0; index < len(fields); {
		status := fields[index]
		index++
		if status == "" || index >= len(fields) {
			return nil, errors.New("git returned malformed changed-path data")
		}
		kind := status[0]
		path := fields[index]
		index++
		switch kind {
		case 'R', 'C':
			if index >= len(fields) {
				return nil, errors.New("git returned a rename without its new path")
			}
			path = fields[index]
			index++
			changes = append(changes, publicationChange{Path: path, Kind: 'A'})
		case 'D':
			// Retirement is merge-sealed; a deletion publishes nothing. A path
			// that leaves the watch globs is withdrawn separately.
		case 'A', 'M', 'T':
			changes = append(changes, publicationChange{Path: path, Kind: kind})
		default:
			return nil, fmt.Errorf("git returned unsupported path status %q", truncateForError(status))
		}
	}
	return changes, nil
}

func splitNUL(content []byte) []string {
	parts := bytes.Split(content, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			result = append(result, string(part))
		}
	}
	return result
}

func selectWatchedPaths(changes []publicationChange, patterns []*regexp.Regexp) ([]string, []string) {
	selected := make(map[string]bool)
	var refused []string
	for _, change := range changes {
		if !matchesAnyPattern(patterns, change.Path) {
			continue
		}
		if err := validatePublicationPath(change.Path); err != nil {
			refused = append(refused, err.Error())
			continue
		}
		selected[change.Path] = true
	}
	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	sort.Strings(refused)
	return paths, refused
}

// validatePublicationPath is the whole hostile-path answer, in one place, so
// the queue check and the selection check cannot drift apart.
//
// The `..` decision is the part worth stating. Path matching in this
// repository is pure string comparison with no normalisation — the fold's
// pathCovers and this command's neighbours in succession.go all compare
// prefixes byte for byte — so `notes/../x.md` neither covers nor is covered by
// `x.md`. Admitting one would create a shadow chain: two live facts about one
// file that no succession rule can ever relate, and a bare retirement of
// either leaves the other standing as if it were current. Git's own diff and
// ls-tree output never contains a `.` or `..` component, so refusing costs a
// real repository nothing and closes the door on a crafted head. The same rule
// refuses an empty component, which is the other spelling of the same trick.
func validatePublicationPath(path string) error {
	if path == "" {
		return errors.New("path is empty")
	}
	if !utf8.ValidString(path) {
		return fmt.Errorf("path is not valid UTF-8: %q", truncateForError(path))
	}
	if len(path) > publicationPathLimit {
		return fmt.Errorf("path exceeds %d bytes: %q", publicationPathLimit, truncateForError(path))
	}
	if hasControl(path) {
		return fmt.Errorf("path contains a control byte: %q", truncateForError(path))
	}
	if strings.Contains(path, ",") {
		return fmt.Errorf("path contains a comma no artifact successor at this path could ever carry: %q", truncateForError(path))
	}
	if hasTraversalComponent(path) {
		return fmt.Errorf("path has a %q, %q, or empty component, which string-prefix path matching cannot reconcile with its normalised spelling: %q", ".", "..", truncateForError(path))
	}
	return nil
}

func hasTraversalComponent(path string) bool {
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return true
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return true
		}
	}
	return false
}

func hasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func gitOutputLimited(ctx context.Context, repo string, limit int64, arguments ...string) ([]byte, error) {
	content, truncated, err := gitOutputPrefix(ctx, repo, limit, arguments...)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("git output exceeds %d bytes", limit)
	}
	return content, nil
}

func gitOutputPrefix(ctx context.Context, repo string, limit int64, arguments ...string) ([]byte, bool, error) {
	args := append([]string{"--no-replace-objects", "-C", repo}, arguments...)
	command := exec.CommandContext(ctx, "git", args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, false, err
	}
	content, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	truncated := int64(len(content)) > limit
	if truncated {
		content = content[:limit]
		_ = stdout.Close()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return nil, truncated, readErr
	}
	if waitErr != nil && !truncated {
		return nil, false, fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), waitErr, truncateForError(strings.TrimSpace(stderr.String())))
	}
	return content, truncated, nil
}
