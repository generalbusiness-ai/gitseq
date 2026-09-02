package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	testApplication = "gitseq-host-test"
	testFoldVersion = "gitseq-host-test-fold@0"
)

// testHost is an application this build does not hold. Nothing registers it:
// the point of most of these tests is what a reader does with a binding it
// cannot honour, and a host that is never held states that directly.
func testHost() host {
	return host{application: testApplication, foldVersion: testFoldVersion, newFolder: workroom.NewFolder}
}

// recordBinding appends one binding record signed by the named actor, which is
// how a repository is bound after its opening records.
func recordBinding(t *testing.T, ctx context.Context, w *Workspace, actorName string, recorded apphost.Binding) {
	t.Helper()
	_, private, err := w.Actor(actorName)
	if err != nil {
		t.Fatal(err)
	}
	request, err := w.buildBindingRequest(ctx, private, actorName, recorded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AcceptSubmission(ctx, request); err != nil {
		t.Fatal(err)
	}
}

// selectedBy reopens the repository, so the answer comes from the log rather
// than from a selection an earlier workspace already cached.
func selectedBy(t *testing.T, ctx context.Context, repo string) (host, error) {
	t.Helper()
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	return reopened.interpreter()
}

func TestWorkroomRepositoryRecordsNoBindingAndSelectsWorkroom(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Absence is the workroom declaration, permanently. Recording one would put
	// a record in the opening of every workroom log to repeat what the reader
	// already knows.
	if snapshot.Depth != 1 {
		t.Fatalf("depth after init = %d, want 1: the default application records no binding", snapshot.Depth)
	}
	recorded, err := apphost.BindingInForce(ctx, workspace.Store, workspace.config.Genesis, kernel.Ref(workspace.config.Genesis))
	if err != nil || recorded != nil {
		t.Fatalf("binding = %+v err=%v, want none", recorded, err)
	}
	selected, err := selectedBy(t, ctx, repo)
	if err != nil || selected.application != apphost.DefaultApplication {
		t.Fatalf("selected %+v err=%v, want the workroom host", selected, err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.interpreter(); err != nil {
		t.Fatal(err)
	}
	if reopened.selected.host.projectionProfile() != apphost.DefaultApplication+"\x00"+workroom.ProfileVersion {
		t.Fatalf("projection profile = %q, want workroom application and fold", reopened.selected.host.projectionProfile())
	}
}

func TestInitRecordsTheBindingOfAnApplicationAbsenceDoesNotName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := initHosted(ctx, repo, "human", 1<<20, testHost())
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := apphost.BindingInForce(ctx, workspace.Store, workspace.config.Genesis, kernel.Ref(workspace.config.Genesis))
	if err != nil || recorded == nil {
		t.Fatalf("binding = %+v err=%v, want the recorded apphost.Binding", recorded, err)
	}
	if recorded.Application != testApplication || recorded.FoldVersion != testFoldVersion {
		t.Fatalf("binding = %+v, want application %q at fold %q", recorded, testApplication, testFoldVersion)
	}
	if recorded.SourceCommit != "" && !strings.HasPrefix(recorded.SourceCommit, "git:") {
		t.Fatalf("binding source commit %q is not format-qualified", recorded.SourceCommit)
	}
	// The binding sits in the opening records, immediately after the roster
	// statement the workroom bootstrap grant needs first.
	verification, err := workspace.Verify(ctx)
	if err != nil || verification.Depth != 2 {
		t.Fatalf("verify = %+v err=%v, want the binding recorded at init", verification, err)
	}
}

func TestBoundRepositoryIsVerifiableWithoutItsInterpreter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	// A build that does not hold the application: the log is unchanged, only
	// the reader is different.
	if _, _, err := initHosted(ctx, repo, "human", 1<<20, testHost()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatalf("open refused a repository it can still verify: %v", err)
	}
	verification, err := reopened.Verify(ctx)
	if err != nil || verification.Depth != 2 {
		t.Fatalf("verify = %+v err=%v, want the kernel facts to stand", verification, err)
	}
	if _, err := reopened.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("snapshot error = %v, want a refusal to interpret", err)
	}
	if _, err := reopened.Act(ctx, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "under the wrong interpreter", IdempotencyKey: "wrong"}); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("act error = %v, want appends refused while the interpreter is unavailable", err)
	}
}

func TestBoundRepositoryRefusesAnotherFoldVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// The same application, a different meaning. A fold-version change
	// invalidates every reader by construction.
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: apphost.DefaultApplication, FoldVersion: workroom.ProfileVersion + "1"})
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("snapshot error = %v, want a refusal naming the fold mismatch", err)
	}
}

func TestLaterBindingByTheInitializingKeyReplacesTheOneInForce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "ordinary work first",
		RestsOn: []string{seed.ID}, IdempotencyKey: "first",
	})
	// An upgrade is a replacement: a binding recorded long after the opening,
	// signed by the key that initialized the repository, takes force.
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion})
	recorded, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	inForce, err := apphost.BindingInForce(ctx, recorded.Store, recorded.config.Genesis, kernel.Ref(recorded.config.Genesis))
	if err != nil || inForce == nil || inForce.Application != testApplication {
		t.Fatalf("binding = %+v err=%v, want the later replacement in force", inForce, err)
	}
	if _, err := selectedBy(t, ctx, repo); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("selection error = %v, want the replacement honoured", err)
	}
}

// The selection is fixed when a workspace opens. Reading the binding at first
// use instead would make the answer depend on what happened after the open: the
// same workspace would mean one thing before a replacement landed and another
// after, without reopening.
func TestSelectionIsFixedAtOpenAgainstALaterReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	// A replacement recorded through another workspace over the same
	// repository, after the first one opened and before it first folds.
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion})
	if selected, err := opened.interpreter(); err != nil || selected.application != apphost.DefaultApplication {
		t.Fatalf("selected %+v err=%v, want the binding this workspace opened under", selected, err)
	}
	if _, err := opened.Snapshot(ctx); err != nil {
		t.Fatalf("snapshot = %v, want the workspace to keep reading under the binding it opened with", err)
	}
	if _, err := opened.Act(ctx, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "still workroom here", IdempotencyKey: "after-replacement"}); err != nil {
		t.Fatalf("act = %v, want appends to continue under the binding at open", err)
	}
	// And the replacement is in force for whoever opens next, so the test
	// cannot pass by ignoring the record altogether.
	if _, err := selectedBy(t, ctx, repo); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("selection error = %v, want the replacement honoured by the next open", err)
	}
}

// Selecting at open means a workspace nobody opened has no interpreter. It has
// to say so: handing it the default would be the guess the open-time order
// exists to prevent, and folding with the interpreter it never selected would
// dereference nothing at all.
func TestAWorkspaceThatNeverOpenedHasNoInterpreter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	unopened := &Workspace{
		Repo: workspace.Repo, GitDir: workspace.GitDir, CommonDir: workspace.CommonDir,
		MetaDir: workspace.MetaDir, Store: workspace.Store, config: workspace.config,
	}
	if _, err := unopened.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "no interpreter") {
		t.Fatalf("snapshot = %v, want a refusal naming the interpreter it never selected", err)
	}
}

// Kernel verification outranks an interpreter refusal. A refusal says the
// repository verifies but cannot be interpreted, so a chain that does not
// verify must never be reported as one: the binding read runs before the audit,
// and an attacker who can write the ref could otherwise dress an unverifiable
// history up as a missing interpreter.
func TestAnUnverifiableChainOutranksTheInterpreterRefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	// A binding the initializing key really signed, so the pre-audit read
	// honours it and the repository looks bound to an application this build
	// does not hold.
	request, err := workspace.buildBindingRequest(ctx, private, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion})
	if err != nil {
		t.Fatal(err)
	}
	declared, err := intent.Verify(request.Signed)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := workspace.Store.WritePayloadTree(ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	// The commit carrying it is signed by a key that is not this repository's
	// sequencer, so the chain does not verify.
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	head, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	forged, err := workspace.Store.SignedCommit(ctx, tree, head, intent.Envelope(request.Signed, declared.RestsOn), wrongKey, gitstore.CommitIdentity{
		AuthorName: "hostile", AuthorEmail: "hostile@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.config.Genesis), forged, head); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.interpreter(); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("selection error = %v, want the pre-audit read to have honoured the forged binding", err)
	}
	if _, err := reopened.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "sequencer signature") {
		t.Fatalf("snapshot error = %v, want the sequencer signature failure rather than a refusal to interpret", err)
	}
	// The same order holds for an append: nothing may claim this repository
	// verifies while its chain does not.
	act, err := reopened.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "onto an unverifiable chain", IdempotencyKey: "unverifiable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.AcceptSubmission(ctx, act); err == nil || !strings.Contains(err.Error(), "sequencer signature") {
		t.Fatalf("submission error = %v, want the sequencer signature failure to win there too", err)
	}
}

func TestTheLastAuthorizedBindingWinsInEitherDirection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Bound away from workroom, then back. Reading the first binding, or the
	// first two records, would leave this repository uninterpretable.
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion})
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: apphost.DefaultApplication, FoldVersion: workroom.ProfileVersion})
	selected, err := selectedBy(t, ctx, repo)
	if err != nil || selected.application != apphost.DefaultApplication {
		t.Fatalf("selected %+v err=%v, want the last binding to win", selected, err)
	}
	// And away again, so the test cannot pass by preferring workroom rather
	// than by ordering.
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion + "-next"})
	if _, err := selectedBy(t, ctx, repo); err == nil || !strings.Contains(err.Error(), testApplication) {
		t.Fatalf("selection error = %v, want the last binding to win again", err)
	}
}

func TestARollbackToAnAlreadyRecordedBindingTakesForce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Rolling back to an earlier build records the binding that build already
	// recorded, byte for byte. It must take force again rather than be
	// swallowed as a repeat of the act it is undoing.
	rolledBack := apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion}
	recordBinding(t, ctx, workspace, "human", rolledBack)
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion + "-next"})
	recordBinding(t, ctx, workspace, "human", rolledBack)
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	inForce, err := apphost.BindingInForce(ctx, reopened.Store, reopened.config.Genesis, kernel.Ref(reopened.config.Genesis))
	if err != nil || inForce == nil || inForce.FoldVersion != testFoldVersion {
		t.Fatalf("binding = %+v err=%v, want the rolled-back binding in force", inForce, err)
	}
}

func TestBindingFromAnotherKeyHasNoForce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// A key with custody and admission, but not the one that initialized the
	// repository. Anyone able to append could otherwise make a repository
	// unreadable by naming an application nobody holds.
	_, fingerprint, keyFile, err := generateActor(filepath.Join(workspace.MetaDir, "actors"), "intruder")
	if err != nil {
		t.Fatal(err)
	}
	workspace.config.Actors["intruder"] = apphost.Actor{Name: "intruder", Fingerprint: fingerprint, KeyFile: keyFile}
	recordBinding(t, ctx, workspace, "intruder", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion})
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	inForce, err := apphost.BindingInForce(ctx, reopened.Store, reopened.config.Genesis, kernel.Ref(reopened.config.Genesis))
	if err != nil || inForce != nil {
		t.Fatalf("binding = %+v err=%v: only the initializing key binds a repository", inForce, err)
	}
	if _, err := reopened.Snapshot(ctx); err != nil {
		t.Fatalf("workroom repository stopped reading after an unauthorized binding: %v", err)
	}
}

// The binding in force is a question about one history, and the revision is
// what says which one. A caller that has already verified a frontier reads the
// binding out of that frontier: asking the ref a second time would leave a gap
// between the two questions, and a replacement recorded in between would decide
// what a workspace means on the strength of a frontier nobody verified.
func TestBindingInForceAnswersForTheRevisionNamed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: testApplication, FoldVersion: testFoldVersion})

	atFrontier, err := apphost.BindingInForce(ctx, workspace.Store, workspace.config.Genesis, frontier)
	if err != nil {
		t.Fatal(err)
	}
	if atFrontier != nil {
		t.Fatalf("binding at the earlier frontier = %+v, want a replacement recorded after it left out of the answer", atFrontier)
	}
	atRef, err := apphost.BindingInForce(ctx, workspace.Store, workspace.config.Genesis, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	if atRef == nil || atRef.Application != testApplication {
		t.Fatalf("binding at the ref = %+v, want the replacement in force", atRef)
	}
	// A read with no revision has no history to answer for, and guessing one
	// is what this parameter exists to stop.
	if _, err := apphost.BindingInForce(ctx, workspace.Store, workspace.config.Genesis, ""); err == nil {
		t.Fatal("a binding read naming no revision was allowed to answer")
	}
}

func TestAMalformedBindingLeavesTheOneInForceStanding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recordBinding(t, ctx, workspace, "human", apphost.Binding{Application: apphost.DefaultApplication, FoldVersion: workroom.ProfileVersion})
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	// A binding-shaped record from the authorized key whose payload is not a
	// binding. It has no force, and it must not cost the repository its
	// interpreter: a malformed record is a record nobody can act on, not an
	// off switch.
	malformed, err := workspace.signRequest(ctx, private, "human", apphost.BindingSchema, []byte(`{"application":"only-half-a-binding"}`), nil, nil, "malformed-binding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.AcceptSubmission(ctx, malformed); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	inForce, err := apphost.BindingInForce(ctx, reopened.Store, reopened.config.Genesis, kernel.Ref(reopened.config.Genesis))
	if err != nil || inForce == nil || inForce.Application != apphost.DefaultApplication {
		t.Fatalf("binding = %+v err=%v, want the last well-formed binding still in force", inForce, err)
	}
	if _, err := reopened.Snapshot(ctx); err != nil {
		t.Fatalf("a malformed binding-shaped record made the repository unreadable: %v", err)
	}
}

func TestABindingShapedRecordGetsNoForceFromTheWorkroomFold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.buildBindingRequest(ctx, private, "human", apphost.Binding{Application: apphost.DefaultApplication, FoldVersion: workroom.ProfileVersion})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The host reads the binding; the application never gives it meaning.
	for _, decision := range snapshot.Projection.Decisions {
		if decision.Event == submission.Record.ID && decision.Verdict == workroom.Effective {
			t.Fatalf("workroom fold gave a binding record force: %+v", decision)
		}
	}
}

func TestLegacyStateCannotRecordANewFoldActivation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.buildRequest(ctx, private, "human", workroom.SchemaStateLegacy, workroom.State{
		Kind: workroom.KindFoldActivation, Text: "activate through the retired application meta-kind",
		Body: map[string]string{
			"fold": "internal/workroom@abc123", "entry": "gitseq/internal/workroom",
			"interface": "workroom-fold@1", "toolchain": "go1.25.0", "prefix": "genesis",
		},
	}, nil, nil, "legacy-fold-activation")
	if err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.AcceptSubmission(ctx, request); err == nil || !strings.Contains(err.Error(), "host binding upgrade") {
		t.Fatalf("legacy fold activation error = %v", err)
	}
	after, err := workspace.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("refused activation advanced verified history: before=%+v after=%+v", before, after)
	}

	// A room may already contain an unratified state@0 activation. The bridge
	// keeps that record readable, but ratifying it now would be a new
	// activation rather than historical replay.
	appended, err := kernel.Submit(ctx, workspace.Store, request, kernel.Options{SigningKey: workspace.config.SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err = reopened.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	ratify, err := reopened.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbRatify, Target: reopened.EventID(appended.Commit), IdempotencyKey: "late-legacy-ratification",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.AcceptSubmission(ctx, ratify); err == nil || !strings.Contains(err.Error(), "can no longer be ratified") {
		t.Fatalf("late legacy ratification error = %v", err)
	}
}

func TestRetryingOneBindingSubmissionAppendsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.buildBindingRequest(ctx, private, "human", apphost.Binding{Application: apphost.DefaultApplication, FoldVersion: workroom.ProfileVersion})
	if err != nil {
		t.Fatal(err)
	}
	first, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Record.ID != first.Record.ID {
		t.Fatalf("retrying one binding submission appended %s then %s, want one act", first.Record.ID, retried.Record.ID)
	}
}

func TestBindingPayloadsAreCanonicalAndComplete(t *testing.T) {
	t.Parallel()
	for name, payload := range map[string]string{
		"no application":               `{"fold_version":"workroom-fold@3"}`,
		"no fold version":              `{"application":"workroom"}`,
		"bare source commit":           `{"application":"workroom","source_commit":"deadbeef","fold_version":"workroom-fold@3"}`,
		"unknown field":                `{"application":"workroom","fold_version":"workroom-fold@3","interpreter":"anything"}`,
		"not canonical":                `{"application":"workroom", "fold_version":"workroom-fold@3"}`,
		"reordered fields":             `{"fold_version":"workroom-fold@3","application":"workroom"}`,
		"trailing json":                `{"application":"workroom","fold_version":"workroom-fold@3"}{}`,
		"empty":                        ``,
		"empty source commit":          `{"application":"workroom","source_commit":"","fold_version":"workroom-fold@3"}`,
		"unqualified url form":         `{"application":"workroom","source_commit":"sha1:deadbeef","fold_version":"workroom-fold@3"}`,
		"replacement without genesis":  `{"application":"workroom","fold_version":"workroom-fold@3","previous_fold_version":"workroom-fold@2"}`,
		"replacement without outgoing": `{"application":"workroom","fold_version":"workroom-fold@3","genesis":"git:sha1:0123456789012345678901234567890123456789"}`,
	} {
		if _, err := apphost.DecodeBinding([]byte(payload)); err == nil {
			t.Fatalf("%s: decoded %q, want a refusal", name, payload)
		}
	}
	accepted := `{"application":"workroom","source_commit":"git:sha1:0123456789012345678901234567890123456789","source_url":"https://example.invalid/workroom","fold_version":"workroom-fold@3"}`
	decoded, err := apphost.DecodeBinding([]byte(accepted))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Application != "workroom" || decoded.FoldVersion != "workroom-fold@3" || decoded.SourceURL != "https://example.invalid/workroom" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
