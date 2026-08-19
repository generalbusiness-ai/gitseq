package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func testRepo(t testing.TB) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repo
}

func actRecord(t *testing.T, ctx context.Context, workspace *Workspace, actor string, act Act) workroom.Record {
	t.Helper()
	submission, err := workspace.Act(ctx, actor, act)
	if err != nil {
		t.Fatal(err)
	}
	return submission.Record
}

func eventCommit(t *testing.T, format, event string) string {
	t.Helper()
	_, commit, ok := strings.Cut(event, "#git:"+format+":")
	if !ok || commit == "" {
		t.Fatalf("invalid event id %q", event)
	}
	return commit
}

func TestSubmissionAndReloadPreserveEventTimestamp(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "timed event",
		RestsOn: []string{seed.ID}, IdempotencyKey: "timed-event",
	})
	if err != nil {
		t.Fatal(err)
	}
	if submission.Result.Timestamp <= 0 || submission.Record.Timestamp != submission.Result.Timestamp {
		t.Fatalf("submission timestamps: result=%d record=%d", submission.Result.Timestamp, submission.Record.Timestamp)
	}
	assertTimestamp := func(label string, snapshot Snapshot) {
		t.Helper()
		for _, statement := range snapshot.Projection.Statements {
			if statement.Event == submission.Record.ID {
				if statement.Timestamp != submission.Result.Timestamp {
					t.Fatalf("%s timestamp = %d, want %d", label, statement.Timestamp, submission.Result.Timestamp)
				}
				return
			}
		}
		t.Fatalf("%s snapshot omitted submitted event", label)
	}
	cached, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertTimestamp("cached", cached)
	reopened, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertTimestamp("reloaded", reloaded)
}

// Run with -benchtime=1x. Setup constructs one ordinary signed chain, then
// compares a cold full Snapshot with the resident delta Snapshot at the same
// successor head. A warm implementation that regresses to a full scan makes
// the two arms converge, so the benchmark detects the failure it guards.
func BenchmarkColdVersusResidentDeltaAtRealDepth(b *testing.B) {
	if b.N != 1 {
		b.Skip("run with -benchtime=1x")
	}
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(b), "human", 1<<20)
	if err != nil {
		b.Fatal(err)
	}
	heads := map[int]string{}
	head, err := workspace.Store.Head(ctx, kernel.Ref(workspace.Config.Genesis))
	if err != nil {
		b.Fatal(err)
	}
	heads[1] = head
	for depth := 2; depth <= 351; depth++ {
		submission, err := workspace.Act(ctx, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "real depth",
			RestsOn: []string{seed.ID}, IdempotencyKey: fmt.Sprintf("real-depth-%d", depth),
		})
		if err != nil {
			b.Fatal(err)
		}
		heads[depth] = submission.Result.Head
	}
	current := heads[351]
	for _, depth := range []int{25, 100, 350} {
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.Config.Genesis), heads[depth], current); err != nil {
			b.Fatal(err)
		}
		current = heads[depth]
		warm, err := Open(ctx, workspace.Repo)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := warm.Snapshot(ctx); err != nil {
			b.Fatal(err)
		}
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.Config.Genesis), heads[depth+1], current); err != nil {
			b.Fatal(err)
		}
		current = heads[depth+1]
		cold, err := Open(ctx, workspace.Repo)
		if err != nil {
			b.Fatal(err)
		}
		var coldSnapshot, warmSnapshot Snapshot
		b.Run(fmt.Sprintf("depth-%d/cold", depth), func(bench *testing.B) {
			if bench.N != 1 {
				bench.Skip("run with -benchtime=1x")
			}
			bench.ReportAllocs()
			bench.ResetTimer()
			coldSnapshot, err = cold.Snapshot(ctx)
			if err != nil {
				bench.Fatal(err)
			}
		})
		b.Run(fmt.Sprintf("depth-%d/delta", depth), func(bench *testing.B) {
			if bench.N != 1 {
				bench.Skip("run with -benchtime=1x")
			}
			bench.ReportAllocs()
			bench.ResetTimer()
			warmSnapshot, err = warm.Snapshot(ctx)
			if err != nil {
				bench.Fatal(err)
			}
		})
		if !reflect.DeepEqual(warmSnapshot, coldSnapshot) {
			b.Fatalf("depth %d resident delta differs from cold snapshot", depth)
		}
	}
}

func TestWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request := actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindRequest, Text: "build", Body: map[string]string{"to": "agent", "conditions": "tests pass"}, RestsOn: []string{seed.ID}, IdempotencyKey: "request"})
	promise := actRecord(t, ctx, workspace, "agent", Act{Verb: VerbState, Kind: workroom.KindPromise, Text: "I promise", RestsOn: []string{request.ID}, IdempotencyKey: "promise"})
	report := actRecord(t, ctx, workspace, "agent", Act{Verb: VerbState, Kind: workroom.KindReport, Text: "done", RestsOn: []string{promise.ID}, IdempotencyKey: "report"})
	actRecord(t, ctx, workspace, "human", Act{Verb: VerbRatify, Target: report.ID, IdempotencyKey: "satisfy"})
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Depth != 7 || len(snapshot.Projection.Commitments) != 1 || snapshot.Projection.Commitments[0].Status != "satisfied" {
		t.Fatalf("unexpected snapshot: depth=%d commitments=%+v", snapshot.Depth, snapshot.Projection.Commitments)
	}
	if len(snapshot.Vocabulary.Definitions) != 12 || snapshot.Vocabulary.Binding.Status != "unbound" {
		t.Fatalf("snapshot vocabulary = %+v", snapshot.Vocabulary)
	}
	if _, err := Open(ctx, repo); err != nil {
		t.Fatal(err)
	}
}

func TestReportWithoutPromiseIsRefusedBeforeAppend(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "build",
		Body:    map[string]string{"to": "agent", "conditions": "tests pass"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "request-without-promise",
	})
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = workspace.Act(ctx, "agent", Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "done",
		RestsOn: []string{request.ID}, IdempotencyKey: "report-without-promise",
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one effective promise-lifecycle basis") {
		t.Fatalf("report without promise error = %v", err)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused report changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestReportPreflightRequiresExactlyOnePromiseFromTheReporter(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := workspace.AddActor(ctx, "human", "first", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "second", "agent"); err != nil {
		t.Fatal(err)
	}
	promises := make([]workroom.Record, 2)
	for index := range promises {
		request := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindRequest, Text: fmt.Sprintf("build %d", index),
			Body:    map[string]string{"to": first.Fingerprint, "conditions": "tests pass"},
			RestsOn: []string{seed.ID}, IdempotencyKey: fmt.Sprintf("report-preflight-request-%d", index),
		})
		promises[index] = actRecord(t, ctx, workspace, "first", Act{
			Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
			RestsOn: []string{request.ID}, IdempotencyKey: fmt.Sprintf("report-preflight-promise-%d", index),
		})
	}
	for _, test := range []struct {
		name  string
		actor string
		rests []string
		want  string
	}{
		{name: "wrong actor", actor: "second", rests: []string{promises[0].ID}, want: "report actor must be the promisor"},
		{name: "two promises", actor: "first", rests: []string{promises[0].ID, promises[1].ID}, want: "got 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = workspace.Act(ctx, test.actor, Act{
				Verb: VerbState, Kind: workroom.KindReport, Text: "done", RestsOn: test.rests,
				IdempotencyKey: "report-preflight-" + strings.ReplaceAll(test.name, " ", "-"),
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			after, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("refused report changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
}

func TestApprovedReportMustRestOnItsNamedArtifactBeforeSigning(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "review",
		Body:    map[string]string{"to": "agent", "conditions": "review the exact artifact"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "approval-basis-request",
	})
	promise := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: workroom.KindPromise, Text: "I will review it",
		RestsOn: []string{request.ID}, IdempotencyKey: "approval-basis-promise",
	})
	artifact := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindArtifact, Text: "candidate",
		Body:    map[string]string{"path": "internal/app", "commit": "candidate-head"},
		RestsOn: []string{request.ID}, IdempotencyKey: "approval-basis-artifact",
	})
	approval := Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "approved",
		Body: map[string]string{
			"verdict": "approved", "head": "candidate-head", "artifact": artifact.ID,
		},
		RestsOn: []string{promise.ID}, IdempotencyKey: "approval-without-artifact-basis",
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Act(ctx, "agent", approval); err == nil {
		t.Fatal("an approved report was signed without its named artifact basis")
	} else {
		for _, want := range []string{"approved report must rest on its named artifact", artifact.ID, "gs review"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("approval basis error %q does not name %q", err, want)
			}
		}
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused approval changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}

	approval.RestsOn = append(approval.RestsOn, artifact.ID)
	approval.IdempotencyKey = "approval-with-artifact-basis"
	record := actRecord(t, ctx, workspace, "agent", approval)
	decision, ok := workspace.mustSnapshot(t, ctx).Projection.Decision(record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("approval with its named artifact basis = %+v, found=%v", decision, ok)
	}
}

func TestReportPreflightUsesDeclaredLifecycleKinds(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	declare := func(name workroom.Kind, lifecycle workroom.Lifecycle, basis []workroom.BasisConstraint) {
		t.Helper()
		fields, err := json.Marshal([]workroom.FieldConstraint{})
		if err != nil {
			t.Fatal(err)
		}
		bases, err := json.Marshal(basis)
		if err != nil {
			t.Fatal(err)
		}
		definition := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindKindDef, Text: "define " + string(name),
			Body: map[string]string{
				"name": string(name), "fields": string(fields), "basis": string(bases),
				"satisfier": workroom.SatisfierNone, "render": string(workroom.RenderCommitment),
				"staleness": string(workroom.StalenessPropagates), "lifecycle": string(lifecycle),
				"guidance": "test lifecycle",
			},
			RestsOn: []string{seed.ID}, IdempotencyKey: "declare-" + string(name),
		})
		actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbRatify, Target: definition.ID, IdempotencyKey: "ratify-" + string(name),
		})
	}
	declare("undertaking", workroom.LifecyclePromise, []workroom.BasisConstraint{{Kinds: []workroom.Kind{workroom.KindRequest}, Min: 1, Max: 1}})
	declare("delivery", workroom.LifecycleReport, []workroom.BasisConstraint{{Kinds: []workroom.Kind{"undertaking"}, Min: 1, Max: 1}})
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "build",
		Body:    map[string]string{"to": agent.Fingerprint, "conditions": "tests pass"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "custom-lifecycle-request",
	})
	promise := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: "undertaking", Text: "I will",
		RestsOn: []string{request.ID}, IdempotencyKey: "custom-lifecycle-promise",
	})
	// Re-open before the report so no cached vocabulary can accidentally make
	// the declared lifecycle check pass only in a warm resident.
	fresh, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	before, err := fresh.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fresh.Act(ctx, "other", Act{
		Verb: VerbState, Kind: "delivery", Text: "not mine",
		RestsOn: []string{promise.ID}, IdempotencyKey: "custom-lifecycle-wrong-promisor",
	})
	if err == nil || !strings.Contains(err.Error(), "report actor must be the promisor") {
		t.Fatalf("cold custom report by wrong promisor error = %v", err)
	}
	afterRefusal, err := fresh.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterRefusal.Head != before.Head || afterRefusal.Depth != before.Depth {
		t.Fatalf("cold custom report refusal appended: before=%s/%d after=%s/%d", before.Head, before.Depth, afterRefusal.Head, afterRefusal.Depth)
	}
	report := actRecord(t, ctx, fresh, "agent", Act{
		Verb: VerbState, Kind: "delivery", Text: "done",
		RestsOn: []string{promise.ID}, IdempotencyKey: "custom-lifecycle-report",
	})
	snapshot, err := fresh.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decision, ok := snapshot.Projection.Decision(report.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("custom lifecycle report decision = %+v, found=%v", decision, ok)
	}
}

func TestOversizedCausalReferenceDoesNotPoisonSnapshot(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "refuse oversized causality",
		RestsOn: []string{strings.Repeat("r", intent.MaxStringBytes+1)}, IdempotencyKey: "oversized-causality",
	})
	if err == nil {
		t.Fatal("oversized causal reference was appended")
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused act changed snapshot: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
	verified, err := workspace.Verify(ctx)
	if err != nil || verified.Head != before.Head || verified.Depth != before.Depth {
		t.Fatalf("verify after refused act = %+v, %v", verified, err)
	}
}

func TestLinkedWorktreeSharesRepositoryWorkroom(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked checkout")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}
	fromLinked, err := Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if fromLinked.GitDir == workspace.GitDir {
		t.Fatalf("linked checkout reused main git dir %q", fromLinked.GitDir)
	}
	if fromLinked.CommonDir != workspace.CommonDir || fromLinked.MetaDir != workspace.MetaDir || fromLinked.Store.Repo != workspace.Store.Repo {
		t.Fatalf("linked checkout did not share repository state: main=%+v linked=%+v", workspace, fromLinked)
	}
	if _, err := os.Stat(filepath.Join(fromLinked.CommonDir, "gitseq", "config.json")); err != nil {
		t.Fatalf("common gitseq config: %v", err)
	}
	before, err := fromLinked.Verify(ctx)
	if err != nil || before.Depth != 1 {
		t.Fatalf("verify linked worktree: verification=%+v err=%v", before, err)
	}
	actRecord(t, ctx, fromLinked, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "written from linked checkout",
		RestsOn: []string{seed.ID}, IdempotencyKey: "linked-write",
	})
	after, err := workspace.Snapshot(ctx)
	if err != nil || after.Depth != 2 {
		t.Fatalf("main checkout did not observe linked append: snapshot=%+v err=%v", after, err)
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "written after linked checkout",
		RestsOn: []string{seed.ID}, IdempotencyKey: "main-after-linked",
	})
	final, err := fromLinked.Snapshot(ctx)
	if err != nil || final.Depth != 3 {
		t.Fatalf("resident cache did not recover from external advance: snapshot=%+v err=%v", final, err)
	}
}

func TestLocalWorktreesNamesTheServedCheckoutAndHidesTheOthers(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	mainBranchOutput, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil {
		t.Fatal(err)
	}
	mainBranch := strings.TrimSpace(string(mainBranchOutput))
	linked := filepath.Join(t.TempDir(), "linked checkout")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "task/local-view", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(linked, "unfinished.txt"), []byte("local only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	local, err := workspace.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	views := local.Worktrees
	if len(views) != 2 {
		t.Fatalf("worktrees = %#v", views)
	}
	if resolved, err := filepath.EvalSymlinks(repo); err != nil || local.Path != resolved {
		t.Fatalf("served checkout path = %q want %q (err %v)", local.Path, resolved, err)
	}
	byBranch := make(map[string]WorktreeView, len(views))
	for _, view := range views {
		byBranch[view.Branch] = view
		if strings.Contains(view.Checkout, string(filepath.Separator)) {
			t.Fatalf("checkout exposed a path: %#v", view)
		}
	}
	if main := byBranch[mainBranch]; !main.Current || main.State != "clean" || main.Head == "" {
		t.Fatalf("main checkout = %#v", main)
	}
	if linkedView := byBranch["task/local-view"]; linkedView.Current || linkedView.State != "dirty" || linkedView.Checkout != "linked checkout" || linkedView.Head == "" {
		t.Fatalf("linked checkout = %#v", linkedView)
	}
	encoded, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), repo) || strings.Contains(string(encoded), linked) {
		t.Fatalf("local view exposed checkout path: %s", encoded)
	}

	fromLinked, err := Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	fromLinkedLocal, err := fromLinked.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linkedViews := fromLinkedLocal.Worktrees
	if len(linkedViews) != 2 || linkedViews[0].Branch != "task/local-view" || !linkedViews[0].Current {
		t.Fatalf("selected linked checkout not projected first: %#v", linkedViews)
	}
	if resolved, err := filepath.EvalSymlinks(linked); err != nil || fromLinkedLocal.Path != resolved {
		t.Fatalf("linked checkout served path = %q want %q (err %v)", fromLinkedLocal.Path, resolved, err)
	}

	subdir := filepath.Join(repo, "nested", "directory")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	fromSubdir, err := Open(ctx, subdir)
	if err != nil {
		t.Fatal(err)
	}
	fromSubdirLocal, err := fromSubdir.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subdirViews := fromSubdirLocal.Worktrees
	if len(subdirViews) != 2 || !subdirViews[0].Current || subdirViews[0].Branch != mainBranch {
		t.Fatalf("repository subdirectory did not resolve current checkout: %#v", subdirViews)
	}
	if resolved, err := filepath.EvalSymlinks(repo); err != nil || fromSubdirLocal.Path != resolved {
		t.Fatalf("subdirectory served path = %q want the checkout root %q (err %v)", fromSubdirLocal.Path, resolved, err)
	}

	index := filepath.Join(repo, ".git", "index")
	old := time.Unix(946702800, 0)
	if err := os.Chtimes(index, old, old); err != nil {
		t.Fatal(err)
	}
	uncached, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uncached.LocalWorktrees(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Fatalf("read-only worktree inspection rewrote index mtime: got %s want %s", info.ModTime(), old)
	}
}

func TestLocalWorktreesDistinguishesDetachedLockedPrunableAndBare(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(t.TempDir(), "locked checkout")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "detached", locked).CombinedOutput(); err != nil {
		t.Fatalf("add legal detached branch: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repo, "worktree", "lock", locked).CombinedOutput(); err != nil {
		t.Fatalf("lock worktree: %v: %s", err, output)
	}
	detached := filepath.Join(t.TempDir(), "actual detached")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "--detach", detached, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("add detached worktree: %v: %s", err, output)
	}
	prunable := filepath.Join(t.TempDir(), "gone checkout")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "task/gone", prunable).CombinedOutput(); err != nil {
		t.Fatalf("add prunable worktree: %v: %s", err, output)
	}
	if err := os.RemoveAll(prunable); err != nil {
		t.Fatal(err)
	}
	local, err := workspace.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	views := local.Worktrees
	byCheckout := make(map[string]WorktreeView, len(views))
	for _, view := range views {
		byCheckout[view.Checkout] = view
	}
	if got := byCheckout["locked checkout"]; got.Branch != "detached" || got.Detached || got.State != "locked" {
		t.Fatalf("legal detached branch/locked state conflated: %#v", got)
	}
	if got := byCheckout["actual detached"]; got.Branch != "" || !got.Detached || got.State != "clean" {
		t.Fatalf("detached checkout = %#v", got)
	}
	if got := byCheckout["gone checkout"]; got.State != "prunable" {
		t.Fatalf("prunable checkout = %#v", got)
	}

	bare := filepath.Join(t.TempDir(), "bare.git")
	if output, err := exec.Command("git", "clone", "-q", "--bare", repo, bare).CombinedOutput(); err != nil {
		t.Fatalf("clone bare: %v: %s", err, output)
	}
	bareLocal, err := (&Workspace{Repo: bare}).LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bareViews := bareLocal.Worktrees
	if len(bareViews) != 1 || bareViews[0].State != "bare" || bareViews[0].Branch != "" {
		t.Fatalf("bare worktree = %#v", bareViews)
	}
	// A bare repository has no working tree; the served path falls back to the
	// directory asked for rather than inventing one.
	if resolved, err := filepath.EvalSymlinks(bare); err != nil || bareLocal.Path != resolved {
		t.Fatalf("bare served path = %q want %q (err %v)", bareLocal.Path, resolved, err)
	}
}

func TestBuildRequestCanonicalizesActorAddresses(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	for index, address := range []string{"agent", "@agent", agent.Fingerprint} {
		record := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindRequest, Text: "address", Body: map[string]string{"to": address, "conditions": "canonical"},
			RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "address-" + string(rune('a'+index)),
		})
		decoded, err := workroom.Decode(record.Schema, record.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if got := decoded.(*workroom.State).Body["to"]; got != agent.Fingerprint {
			t.Fatalf("address %q encoded as %q", address, got)
		}
	}
	if _, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "bad", Body: map[string]string{"to": "missing", "conditions": "never"},
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "bad-address",
	}); err == nil || !strings.Contains(err.Error(), "request body.to") {
		t.Fatalf("unknown request performer error = %v, want body.to", err)
	}
}

func TestRequestPreflightRefusesMissingFieldsBeforeSigning(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	before := workspace.mustSnapshot(t, ctx)
	for _, test := range []struct {
		name string
		body map[string]string
		want string
	}{
		{name: "conditions", body: map[string]string{"to": "agent"}, want: "request state requires body.conditions"},
		{name: "performer", body: map[string]string{"conditions": "tests pass"}, want: "request state requires body.to"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := workspace.Act(ctx, "human", Act{
				Verb: VerbState, Kind: workroom.KindRequest, Text: "malformed request", Body: test.body,
				RestsOn: []string{seed.ID}, IdempotencyKey: "request-preflight-missing-" + test.name,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			after := workspace.mustSnapshot(t, ctx)
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("refused request changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
}

func TestAcceptSubmissionRefusesLegacyCustomArtifactPath(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	definition := workroom.KindDefinition{
		Name: "release-bundle",
		Fields: []workroom.FieldConstraint{
			{Operator: workroom.FieldPresent, Name: "path"},
			{Operator: workroom.FieldPresent, Name: "commit"},
		},
		Basis: []workroom.BasisConstraint{}, Satisfier: workroom.SatisfierNone,
		Render: workroom.RenderArtifact, Staleness: workroom.StalenessPropagates,
		Lifecycle: workroom.LifecycleNone, Guidance: "Point to a release bundle.",
	}
	fields, err := json.Marshal(definition.Fields)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := json.Marshal(definition.Basis)
	if err != nil {
		t.Fatal(err)
	}
	declared := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindKindDef, Text: "Define release bundles",
		Body: map[string]string{
			"name": string(definition.Name), "fields": string(fields), "basis": string(basis),
			"satisfier": definition.Satisfier, "render": string(definition.Render),
			"staleness": string(definition.Staleness), "lifecycle": string(definition.Lifecycle),
			"guidance": definition.Guidance,
		},
		RestsOn: []string{seed.ID}, IdempotencyKey: "define-release-bundle",
	})
	if _, err := workspace.Act(ctx, "human", Act{Verb: VerbRatify, Target: declared.ID, IdempotencyKey: "ratify-release-bundle"}); err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	before := workspace.mustSnapshot(t, ctx).Depth
	raw, err := workspace.buildRequest(ctx, private, "human", workroom.SchemaStateLegacy, workroom.State{
		Kind: definition.Name, Text: "invalid legacy path",
		Body: map[string]string{"path": "release,docs", "commit": "head"},
	}, []string{seed.ID}, nil, "legacy-custom-artifact-path")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.AcceptSubmission(ctx, raw); err == nil || !strings.Contains(err.Error(), "unmaintainable artifact path") {
		t.Fatalf("legacy custom artifact submission error = %v", err)
	}
	if after := workspace.mustSnapshot(t, ctx).Depth; after != before {
		t.Fatalf("refused legacy submission changed depth from %d to %d", before, after)
	}
}

func TestIdempotencyNamespaceIsStableAndLegacySafe(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	build := func(key string) string {
		request, err := workspace.BuildActRequest(ctx, private, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: key,
			RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := intent.Verify(request.Signed)
		if err != nil {
			t.Fatal(err)
		}
		return decoded.IdempotencyNS
	}
	if got := build("stable"); got != "workroom/v0" {
		t.Fatalf("new namespace = %q", got)
	}
	workspace.Config.IdempotencyNamespace = ""
	if got := build("legacy"); got != "gs/human" {
		t.Fatalf("legacy namespace = %q", got)
	}
}

func TestAgentRatifierAuthorityLifecycle(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	proposal := func(key string) workroom.Record {
		return actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindPropose, Text: key, RestsOn: []string{seed.ID}, IdempotencyKey: key})
	}
	ratifyAsAgent := func(target, key string) workroom.Record {
		return actRecord(t, ctx, workspace, "agent", Act{Verb: VerbRatify, Target: target, IdempotencyKey: key})
	}
	verdict := func(event string) workroom.Verdict {
		snapshot, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		decision, ok := snapshot.Projection.Decision(event)
		if !ok {
			t.Fatalf("missing decision for %s", event)
		}
		return decision.Verdict
	}

	before := ratifyAsAgent(proposal("before-grant").ID, "before-grant-ratify")
	if got := verdict(before.ID); got != workroom.Ineffective {
		t.Fatalf("ordinary agent ratification = %s, want ineffective", got)
	}
	if _, err := workspace.GrantRole(ctx, "agent", "agent", "ratifier"); err != nil {
		t.Fatal(err)
	}
	selfGranted := ratifyAsAgent(proposal("after-self-grant").ID, "after-self-grant-ratify")
	if got := verdict(selfGranted.ID); got != workroom.Ineffective {
		t.Fatalf("self-granted agent ratification = %s, want ineffective", got)
	}
	grant, err := workspace.GrantRole(ctx, "human", "agent", "ratifier")
	if err != nil {
		t.Fatal(err)
	}
	grantState, err := workroom.Decode(grant[0].Schema, grant[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := grantState.(*workroom.State).Body["kind"]; got != "agent" {
		t.Fatalf("modern role grant kind = %q, want agent", got)
	}
	during := ratifyAsAgent(proposal("during-grant").ID, "during-grant-ratify")
	if got := verdict(during.ID); got != workroom.Effective {
		t.Fatalf("ratifier agent ratification = %s, want effective", got)
	}
	if _, err := workspace.RevokeRole(ctx, "human", "agent", "ratifier"); err != nil {
		t.Fatal(err)
	}
	after := ratifyAsAgent(proposal("after-revoke").ID, "after-revoke-ratify")
	if got := verdict(after.ID); got != workroom.Ineffective {
		t.Fatalf("revoked agent ratification = %s, want ineffective", got)
	}

	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorState := snapshot.Projection.Actors[agent.Fingerprint]
	if got := actorState.Kind; got != "agent" {
		t.Fatalf("projected actor kind = %q, want agent", got)
	}
	roles := actorState.Roles
	if len(roles) != 1 || roles[0] != "participant" {
		t.Fatalf("roles after revocation = %#v, want participant only", roles)
	}
	if len(actorState.RoleSources["participant"]) != 1 || actorState.MembershipEvent == "" {
		t.Fatalf("actor projection omitted membership provenance: %+v", actorState)
	}
	views, err := workspace.ActorViews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Name != "agent" || views[0].Kind != "agent" || len(views[0].Roles) != 1 || views[0].Roles[0] != "participant" || !views[0].Custody {
		t.Fatalf("actor views did not join custody to durable state: %+v", views)
	}
}

// AddActor must accept exactly the kinds the Workroom vocabulary defines. The
// expectation is derived from workroom.IsActorKind rather than restated here on
// purpose: a test that listed human, agent and service again would keep passing
// while the two lists drifted apart, which is the duplication this seam exists
// to remove. Adding a kind in one place and not the other fails this.
func TestAddActorKindsFollowWorkroomVocabulary(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Words the normalizer treats differently: kinds, an authority, the
	// membership role, the fallback kind it derives for anything unrecognised,
	// and a word it has never seen.
	for index, word := range []string{"human", "agent", "service", "operator", "participant", "unspecified", "reviewer"} {
		_, _, err := workspace.AddActor(ctx, "human", fmt.Sprintf("kind-probe-%d", index), word)
		if accepted := err == nil; accepted != workroom.IsActorKind(word) {
			t.Errorf("AddActor kind %q accepted = %v, workroom.IsActorKind = %v", word, accepted, workroom.IsActorKind(word))
		}
	}
	// An omitted kind is defaulted rather than rejected, so it is deliberately
	// outside the correspondence above; IsActorKind("") is false.
	actor, _, err := workspace.AddActor(ctx, "human", "kind-probe-default", "")
	if err != nil {
		t.Fatalf("AddActor with an omitted kind = %v", err)
	}
	if actor.Name != "kind-probe-default" {
		t.Errorf("defaulted actor = %+v", actor)
	}
}

func TestValidateAuthorityRoleUsesRosterKindClassification(t *testing.T) {
	for _, role := range []string{"", "participant", "agent", "human", "service"} {
		if err := validateAuthorityRole(role); err == nil {
			t.Errorf("validateAuthorityRole(%q) accepted a non-authority", role)
		}
	}
	for _, role := range []string{"operator", "ratifier", "custom"} {
		if err := validateAuthorityRole(role); err != nil {
			t.Errorf("validateAuthorityRole(%q) = %v", role, err)
		}
	}
}

func TestActorViewsEnumerateDurableActorsWithoutLocalCustody(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	attached := &Workspace{
		Repo: workspace.Repo, GitDir: workspace.GitDir, CommonDir: workspace.CommonDir, MetaDir: t.TempDir(), Store: workspace.Store,
		Config: apphost.Config{Version: 0, Genesis: workspace.Config.Genesis, ObjectFormat: workspace.Config.ObjectFormat, ReadOnly: true},
	}
	// An attached view reads the binding for itself, as opening it would: a
	// workspace that never selected an interpreter has none to fold with.
	if attached.selected, err = attached.selectHost(ctx); err != nil {
		t.Fatal(err)
	}
	views, err := attached.ActorViews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Name != "agent" || views[1].Name != "human" {
		t.Fatalf("attached actor views omitted durable actors: %+v", views)
	}
	for _, view := range views {
		if view.Custody {
			t.Fatalf("attached view falsely reports local custody: %+v", view)
		}
	}
}

func TestSnapshotCachesTheVerifiedHead(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached := workspace.snapshotCache
	folder := workspace.snapshotFolder
	second, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.snapshotCache != cached || first.Head != second.Head {
		t.Fatal("unchanged head did not reuse the verified snapshot")
	}
	actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "advance", RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "advance"})
	thirdResult, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	third := thirdResult.Snapshot
	if workspace.snapshotCache == cached || workspace.snapshotFolder != folder || third.Head == first.Head || third.Depth != first.Depth+1 {
		t.Fatalf("advanced head reused stale snapshot: first=%+v third=%+v", first, third)
	}
	if thirdResult.Source != SnapshotSourceIncrementalTail {
		t.Fatalf("accepted local append reported %q", thirdResult.Source)
	}

	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, external, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "external advance", RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "external-advance"})
	fourthResult, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fourth := fourthResult.Snapshot
	if workspace.snapshotFolder != folder || fourth.Depth != third.Depth+1 || fourth.Head == third.Head {
		t.Fatalf("external descendant did not extend resident folder: third=%+v fourth=%+v", third, fourth)
	}
	if fourthResult.Source != SnapshotSourceIncrementalTail {
		t.Fatalf("verified descendant tail reported %q", fourthResult.Source)
	}
	coldWorkspace, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	cold, err := coldWorkspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fourth, cold) {
		t.Fatalf("resident delta projection differs from independent cold fold:\nresident=%+v\ncold=%+v", fourth, cold)
	}

	// If application projection state is discarded independently, the reader
	// is intentionally replaced so recovery is a full audit rather than an
	// unverifiable attempt to reconstruct a missing prefix from a delta.
	workspace.snapshotCache = nil
	recovered, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.snapshotFolder == folder || recovered.Head != fourth.Head || recovered.Depth != fourth.Depth {
		t.Fatalf("discarded projection did not recover by full audit: fourth=%+v recovered=%+v", fourth, recovered)
	}
}

func TestProjectionProfileChangeRebuildsFromTheSameKernelCheckpoint(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldFolder := workspace.snapshotFolder
	oldProfile := workspace.snapshotProfile
	completed := &snapshotFlight{
		done: make(chan struct{}),
		result: SourcedSnapshot{
			Snapshot: before,
			Source:   SnapshotSourceSignedCheckpointTail,
		},
	}
	close(completed.done)
	workspace.flightMu.Lock()
	workspace.flight.Store(completed)
	workspace.flightMu.Unlock()
	workspace.selected = selection{host: host{
		application: apphost.DefaultApplication,
		foldVersion: workroom.ProfileVersion + "-projection-change",
		newFolder:   workroom.NewFolder,
	}}

	rebuilt, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Source != SnapshotSourceSignedCheckpointTail {
		t.Fatalf("projection profile change source = %q, want signed kernel checkpoint", rebuilt.Source)
	}
	if workspace.snapshotFolder == oldFolder || workspace.snapshotProfile == oldProfile {
		t.Fatalf("projection cache survived its profile change: folder=%p profile=%q", workspace.snapshotFolder, workspace.snapshotProfile)
	}
	if !reflect.DeepEqual(rebuilt.Snapshot, before) {
		t.Fatalf("profile-only rebuild changed the projection:\nbefore=%+v\nafter=%+v", before, rebuilt.Snapshot)
	}
}

func TestVerifiedFrontierPersistenceIsPassiveWhenUnchangedAndFailClosedWhenAdvancing(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}

	// Make witness storage deterministically unusable without depending on
	// chmod semantics or the user running the test. A path occupied by a file
	// cannot contain config.json.tmp.
	blockedMeta := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedMeta, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace.MetaDir = blockedMeta
	workspace.Config.ReadOnly = true

	unchanged, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatalf("unchanged snapshot rewrote its local witness: %v", err)
	}
	if unchanged.Head != trusted.Head || unchanged.Depth != trusted.Depth {
		t.Fatalf("unchanged snapshot = %+v, want %+v", unchanged, trusted)
	}
	if verified, err := workspace.Verify(ctx); err != nil {
		t.Fatalf("unchanged full audit rewrote its local witness: %v", err)
	} else if verified.Head != trusted.Head || verified.Depth != trusted.Depth {
		t.Fatalf("unchanged audit = %+v, want head %s depth %d", verified, trusted.Head, trusted.Depth)
	}

	actRecord(t, ctx, external, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "advance while witness storage is unavailable",
		RestsOn: []string{seed.ID}, IdempotencyKey: "advance-with-blocked-witness",
	})
	if _, err := workspace.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "local rollback witness could not advance") {
		t.Fatalf("advanced snapshot without durable witness error = %v", err)
	}
	if _, err := workspace.Verify(ctx); err == nil || !strings.Contains(err.Error(), "local rollback witness could not advance") {
		t.Fatalf("advanced full audit without durable witness error = %v", err)
	}
	if workspace.Config.VerifiedFrontier == nil || workspace.Config.VerifiedFrontier.Head != trusted.Head || workspace.Config.VerifiedFrontier.Depth != trusted.Depth {
		t.Fatalf("failed persistence moved trusted frontier to %+v, want %+v", workspace.Config.VerifiedFrontier, trusted)
	}

	// A failed read must not leave the reusable reader ahead of the projection.
	// Once storage is available again, recovery re-verifies and returns the
	// event that was refused above.
	workspace.MetaDir = external.MetaDir
	recovered, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Head == trusted.Head || recovered.Depth != trusted.Depth+1 {
		t.Fatalf("recovered snapshot = %+v, want one event after %+v", recovered, trusted)
	}
}

func TestAcceptSnapshotGuardsPreserveColdProjection(t *testing.T) {
	t.Run("rewind then sibling advance is refused", func(t *testing.T) {
		ctx := context.Background()
		workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
		first := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "first branch",
			RestsOn: []string{seed.ID}, IdempotencyKey: "guard-branch-first",
		})
		second := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "rewound branch",
			RestsOn: []string{seed.ID}, IdempotencyKey: "guard-branch-second",
		})
		trusted, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		firstCommit := eventCommit(t, workspace.Config.ObjectFormat, first.ID)
		secondCommit := eventCommit(t, workspace.Config.ObjectFormat, second.ID)
		if trusted.Head != secondCommit || workspace.Config.VerifiedFrontier == nil || workspace.Config.VerifiedFrontier.Head != secondCommit {
			t.Fatalf("trusted frontier = snapshot %+v config %+v, want %s", trusted, workspace.Config.VerifiedFrontier, secondCommit)
		}
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.Config.Genesis), firstCommit, secondCommit); err != nil {
			t.Fatal(err)
		}
		shorter, err := Open(ctx, workspace.Repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := shorter.Verify(ctx); err == nil || !strings.Contains(err.Error(), "shorter than previously verified") {
			t.Fatalf("restarted workspace accepted a shorter sequence: %v", err)
		}
		external, err := Open(ctx, workspace.Repo)
		if err != nil {
			t.Fatal(err)
		}
		actRecord(t, ctx, external, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "sibling branch",
			RestsOn: []string{seed.ID}, IdempotencyKey: "guard-branch-sibling",
		})
		if _, err := workspace.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "non-descendant verified frontier") {
			t.Fatalf("resident accepted sibling after verified rewind: %v", err)
		}
		restarted, err := Open(ctx, workspace.Repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := restarted.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "non-descendant verified frontier") {
			t.Fatalf("restarted workspace accepted sibling after verified rewind: %v", err)
		}
		if restarted.Config.VerifiedFrontier == nil || restarted.Config.VerifiedFrontier.Head != secondCommit {
			t.Fatalf("rejected sibling replaced trusted frontier: %+v", restarted.Config.VerifiedFrontier)
		}
	})

	t.Run("base head mismatch", func(t *testing.T) {
		ctx := context.Background()
		workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
		external, err := Open(ctx, workspace.Repo)
		if err != nil {
			t.Fatal(err)
		}
		actRecord(t, ctx, external, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "external",
			RestsOn: []string{seed.ID}, IdempotencyKey: "guard-external",
		})
		actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "local",
			RestsOn: []string{seed.ID}, IdempotencyKey: "guard-local",
		})
		got, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		coldWorkspace, err := Open(ctx, workspace.Repo)
		if err != nil {
			t.Fatal(err)
		}
		want, err := coldWorkspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("base-head guard lost an external event:\nresident=%+v\ncold=%+v", got, want)
		}
	})

	t.Run("replay", func(t *testing.T) {
		ctx := context.Background()
		workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
		act := Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "once",
			RestsOn: []string{seed.ID}, IdempotencyKey: "guard-replay",
		}
		actRecord(t, ctx, workspace, "human", act)
		actRecord(t, ctx, workspace, "human", act)
		got, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		coldWorkspace, err := Open(ctx, workspace.Repo)
		if err != nil {
			t.Fatal(err)
		}
		want, err := coldWorkspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("replay guard applied one event twice:\nresident=%+v\ncold=%+v", got, want)
		}
	})
}

func TestSnapshotCheckpointIsGitBackedReusableAndRepairable(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "checkpointed", RestsOn: []string{seed.ID}, IdempotencyKey: "checkpointed",
	})
	want, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkpointRef := kernel.CheckpointRef(workspace.Config.Genesis)
	checkpointHead, err := workspace.Store.Head(ctx, checkpointRef)
	if err != nil {
		t.Fatalf("checkpoint ref: %v", err)
	}
	pointer := filepath.Join(workspace.MetaDir, "checkpoints", workspace.Config.Genesis+".json")
	if info, err := os.Stat(pointer); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("local checkpoint pointer was not persisted at %s: info=%v err=%v", pointer, info, err)
	}

	restarted, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	restartedResult, err := restarted.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := restartedResult.Snapshot
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checkpoint projection differs from verified source:\nwant=%+v\ngot=%+v", want, got)
	}
	if restartedResult.Source != SnapshotSourceSignedCheckpointTail {
		t.Fatalf("signed checkpoint load reported %q", restartedResult.Source)
	}
	if unchanged, err := workspace.Store.Head(ctx, checkpointRef); err != nil || unchanged != checkpointHead {
		t.Fatalf("exact restart rewrote checkpoint: before=%s after=%s err=%v", checkpointHead, unchanged, err)
	}

	if err := workspace.Store.UpdateRef(ctx, checkpointRef, workspace.Config.Genesis, checkpointHead); err != nil {
		t.Fatal(err)
	}
	fromLocal, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	localResult, err := fromLocal.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if localResult.Source != SnapshotSourceSignedCheckpointTail || !reflect.DeepEqual(localResult.Snapshot, want) {
		t.Fatalf("local checkpoint did not survive ref loss: source=%q got=%+v want=%+v", localResult.Source, localResult.Snapshot, want)
	}
	if repairedRef, err := workspace.Store.Head(ctx, checkpointRef); err != nil || repairedRef != checkpointHead {
		t.Fatalf("local selector did not restore checkpoint reachability: head=%s want=%s err=%v", repairedRef, checkpointHead, err)
	}
	actRecord(t, ctx, fromLocal, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "no-server checkpoint act",
		RestsOn: []string{seed.ID}, IdempotencyKey: "no-server-checkpoint-act",
	})
	localAfterAct, err := fromLocal.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	coldAfterAct, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	coldProjection, err := coldAfterAct.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(localAfterAct, coldProjection) {
		t.Fatalf("no-server checkpoint act differs from independent restart:\nlocal=%+v\nrestart=%+v", localAfterAct, coldProjection)
	}
	want = localAfterAct
	if err := fromLocal.InvalidateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pointer, []byte(`{"schema":"gitseq-checkpoint-pointer@1","commit":"`+workspace.Config.Genesis+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	repairing, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	repairedResult, err := repairing.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repaired := repairedResult.Snapshot
	if !reflect.DeepEqual(repaired, want) {
		t.Fatalf("corrupt checkpoint fallback changed projection:\nwant=%+v\ngot=%+v", want, repaired)
	}
	if repairedResult.Source != SnapshotSourceColdFullAudit {
		t.Fatalf("corrupt checkpoint fallback reported %q", repairedResult.Source)
	}
	if repairedHead, err := workspace.Store.Head(ctx, checkpointRef); err != nil || repairedHead == workspace.Config.Genesis {
		t.Fatalf("full audit did not repair checkpoint ref: head=%s err=%v", repairedHead, err)
	}
}

func TestCheckpointOffForcesColdAuditWithoutChangingPersistentSelectors(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	pointer := workspace.checkpointPointerPath()
	pointerBefore, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatal(err)
	}
	ref := kernel.CheckpointRef(workspace.Config.Genesis)
	refBefore, err := workspace.Store.Head(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(checkpointEnvironment, "off")
	fresh, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := fresh.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != SnapshotSourceColdFullAudit {
		t.Fatalf("checkpoint-off source = %q, want cold full audit", loaded.Source)
	}
	pointerAfter, err := os.ReadFile(pointer)
	if err != nil || !reflect.DeepEqual(pointerAfter, pointerBefore) {
		t.Fatalf("checkpoint-off changed pointer: err=%v before=%q after=%q", err, pointerBefore, pointerAfter)
	}
	if refAfter, err := workspace.Store.Head(ctx, ref); err != nil || refAfter != refBefore {
		t.Fatalf("checkpoint-off changed ref: before=%s after=%s err=%v", refBefore, refAfter, err)
	}
}

func TestGenesisIsValidatedBeforeCheckpointPathSelection(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace.MetaDir, "config.json")
	config := workspace.Config
	config.Genesis = "../outside"
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, workspace.Repo); err == nil || !strings.Contains(err.Error(), "invalid genesis object id") {
		t.Fatalf("Open accepted path-shaped genesis: %v", err)
	}
	attachRepo := testRepo(t)
	if _, err := AttachConfig(ctx, attachRepo, "../outside", "sha1"); err == nil || !strings.Contains(err.Error(), "invalid genesis object id") {
		t.Fatalf("AttachConfig accepted path-shaped genesis: %v", err)
	}
	gitDir, _, err := apphost.ResolveGitDirs(ctx, attachRepo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "gitseq")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid attachment created local state: %v", err)
	}
}

// Retirement ends both halves of an identity: the durable membership and the
// local key. What it must not end is the record that the principal acted.
func TestRetireActorEndsMembershipAndCustodyWhileKeepingThePrincipalVisible(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	instance, _, err := workspace.AddActor(ctx, "human", "claude.2", "agent")
	if err != nil {
		t.Fatal(err)
	}
	spoken := actRecord(t, ctx, workspace, "claude.2", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "the instance acted",
		RestsOn: []string{seed.ID}, IdempotencyKey: "instance-assert",
	})
	if _, err := workspace.RetireActor(ctx, "human", "claude.2"); err != nil {
		t.Fatal(err)
	}
	state := workspace.mustSnapshot(t, ctx).Projection.Actors[instance.Fingerprint]
	if !state.Retired || state.Name != "claude.2" || len(state.Roles) != 0 {
		t.Fatalf("retired instance projection = %+v", state)
	}
	if statement := statementActor(t, workspace, ctx, spoken.ID); statement != instance.Fingerprint {
		t.Fatalf("the retired principal's event lost its author: %s", statement)
	}
	if _, exists := workspace.Config.Actors["claude.2"]; exists {
		t.Fatal("retired instance kept local custody")
	}
	if _, err := os.Stat(instance.KeyFile); !os.IsNotExist(err) {
		t.Fatalf("retired instance key file survives: %v", err)
	}
	if _, err := workspace.RetireActor(ctx, "human", "claude.2"); err == nil {
		t.Fatal("retiring an already retired instance succeeded")
	}
}

// A participant may not retire another principal, and the fold says so. The
// durable attempt stays visible either way; what must not happen is custody
// following an act that changed nothing, leaving a live roster member with no
// key anyone can sign for and a command that called it a success.
func TestIneffectiveRetirementLeavesMembershipAndCustodyAlone(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "claude.2", "agent"); err != nil {
		t.Fatal(err)
	}
	target, _, err := workspace.AddActor(ctx, "human", "claude.3", "agent")
	if err != nil {
		t.Fatal(err)
	}
	before := workspace.mustSnapshot(t, ctx).Depth

	if _, err := workspace.RetireActor(ctx, "claude.2", "claude.3"); err == nil || !strings.Contains(err.Error(), "ineffective") {
		t.Fatalf("a participant retiring another = %v", err)
	}

	snapshot := workspace.mustSnapshot(t, ctx)
	if snapshot.Depth != before+1 {
		t.Fatalf("depth = %d, want %d: the attempt is durable and stays visible", snapshot.Depth, before+1)
	}
	state := snapshot.Projection.Actors[target.Fingerprint]
	if state.Retired || len(state.Roles) == 0 {
		t.Fatalf("an ineffective supersession retired the target: %+v", state)
	}
	if _, exists := workspace.Config.Actors["claude.3"]; !exists {
		t.Fatal("an ineffective retirement deleted the target's custody")
	}
	if _, err := os.Stat(target.KeyFile); err != nil {
		t.Fatalf("an ineffective retirement deleted the target's key file: %v", err)
	}
	// Custody surviving is what lets the operator finish the job properly.
	if _, err := workspace.RetireActor(ctx, "human", "claude.3"); err != nil {
		t.Fatalf("the operator could not retire the target afterwards: %v", err)
	}
}

// A retired principal holds no authority and can be given none: the roster
// must not fill with names a projection cannot distinguish from live ones.
func TestRetiredActorCannotBeAddressedOrGrantedAuthority(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	instance, _, err := workspace.AddActor(ctx, "human", "claude.2", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RetireActor(ctx, "human", "claude.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.GrantRole(ctx, "human", instance.Fingerprint, "ratifier"); err == nil {
		t.Fatal("granted authority to a retired principal")
	}
	// Custody is gone, so the address no longer resolves at this edge; the fold
	// refuses a request to a retired member independently, which is what makes
	// the guarantee durable rather than local.
	if _, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "addressed to a retired instance",
		Body:    map[string]string{"to": instance.Fingerprint, "conditions": "none"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "retired-request",
	}); err == nil || !strings.Contains(err.Error(), "unknown actor address") {
		t.Fatalf("request to a retired principal = %v", err)
	}
	views, err := workspace.ActorViews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retired := 0
	for _, view := range views {
		if view.Name == "claude.2" {
			retired++
			if !view.Retired || view.Custody {
				t.Fatalf("retired actor view = %+v", view)
			}
		}
	}
	if retired != 1 {
		t.Fatalf("retired actor appears %d times in gs actors", retired)
	}
}

func (w *Workspace) mustSnapshot(t *testing.T, ctx context.Context) Snapshot {
	t.Helper()
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func statementActor(t *testing.T, workspace *Workspace, ctx context.Context, event string) string {
	t.Helper()
	for _, statement := range workspace.mustSnapshot(t, ctx).Projection.Statements {
		if statement.Event == event {
			return statement.Actor
		}
	}
	t.Fatalf("statement %s not found", event)
	return ""
}

// The first version of back-pressure shipped a kernel option that no
// deployment set, so every resident stayed unbounded while the tests passed.
// This asserts the resident's own posture, so dropping the bound fails here
// rather than silently restoring the unbounded queue.
func TestResidentSequencerIsBounded(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "force the submitter into existence",
		RestsOn: []string{seed.ID}, IdempotencyKey: "resident-bound-probe",
	}); err != nil {
		t.Fatalf("act: %v", err)
	}
	depth := workspace.submitter.QueueDepth()
	if depth == 0 {
		t.Fatal("resident sequencer is unbounded; the queue bound was not passed to the kernel")
	}
	if depth != ResidentQueueDepth {
		t.Fatalf("resident queue depth = %d, want %d", depth, ResidentQueueDepth)
	}
}

// Every surface that can retire a record reaches the log through
// BuildActRequest, so that is where a retirement the documentation still cites
// has to be refused. It used to be checked in cmd/gs alone: a supersession
// filed over MCP went straight to the resident, and on 2026-08-12 one retired a
// record eleven pages named and took main red. The CLI test for the same rule
// passed throughout, because the CLI was never the hole.
func TestBuildActRequestRefusesRetiringACitedRecord(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(repo, "docs", "reference", "thing.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("---\nrests_on:\n  - "+seed.ID+"\n---\n\nprose\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "add", "docs/reference/thing.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}

	retire := Act{Verb: VerbSupersede, Target: seed.ID, Text: "retire it"}
	if _, err := workspace.BuildActRequest(ctx, private, "human", retire); err == nil {
		t.Fatal("a retirement was built while a page still cited the target")
	} else if !strings.Contains(err.Error(), "docs/reference/thing.md") {
		t.Errorf("the refusal must name the page to repoint, got %v", err)
	}

	// The escape exists for a migration that retires first and re-anchors
	// after. It has to be asked for, so that the ordinary case cannot break
	// the repository by omission.
	retire.CitedOK = true
	if _, err := workspace.BuildActRequest(ctx, private, "human", retire); err != nil {
		t.Errorf("CitedOK must allow the retirement, got %v", err)
	}

	// A record nothing cites is untouched by any of this.
	quiet := Act{Verb: VerbSupersede, Target: seed.ID + "-unnamed", Text: "retire it"}
	if _, err := workspace.BuildActRequest(ctx, private, "human", quiet); err != nil {
		t.Errorf("an uncited target must build normally, got %v", err)
	}
}
