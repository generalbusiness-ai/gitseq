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
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
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

func TestBuildActRequestHashesPayloadTreeUntilAdmissionWritesIt(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	unique := "payload tree stays unwritten before admission: " + t.TempDir()
	act := Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: unique,
		RestsOn: []string{seed.ID}, IdempotencyKey: "hash-before-admission",
		Attachments: map[string][]byte{"proof.txt": []byte(unique)},
	}

	const builders = 16
	type buildResult struct {
		request kernel.Request
		err     error
	}
	results := make(chan buildResult, builders)
	var group sync.WaitGroup
	for range builders {
		group.Add(1)
		go func() {
			defer group.Done()
			request, buildErr := workspace.BuildActRequest(ctx, private, "human", act)
			results <- buildResult{request: request, err: buildErr}
		}()
	}
	group.Wait()
	close(results)

	var request kernel.Request
	var tree string
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		decoded, err := intent.Verify(result.request.Signed)
		if err != nil {
			t.Fatal(err)
		}
		format, builtTree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
		if err != nil {
			t.Fatal(err)
		}
		if format != workspace.config.ObjectFormat {
			t.Fatalf("payload tree format = %q, want %q", format, workspace.config.ObjectFormat)
		}
		if tree == "" {
			tree = builtTree
			request = result.request
		} else if builtTree != tree {
			t.Fatalf("concurrent payload tree = %s, want %s", builtTree, tree)
		}
	}
	if _, err := workspace.Store.ReadFile(ctx, tree, "event"); err == nil {
		t.Fatalf("BuildActRequest wrote payload tree %s before admission", tree)
	}

	submission, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	event, err := workspace.Store.ReadFile(ctx, tree, "event")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, request.Payload) {
		t.Fatalf("written event payload differs from signed request")
	}
	attachment, err := workspace.Store.ReadFile(ctx, tree, "attachments/proof.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(attachment) != unique {
		t.Fatalf("written attachment = %q, want %q", attachment, unique)
	}
	snapshot := workspace.mustSnapshot(t, ctx)
	if snapshot.Head != submission.Result.Head {
		t.Fatalf("snapshot head = %s, want admitted head %s", snapshot.Head, submission.Result.Head)
	}

	invalid := act
	invalid.IdempotencyKey = "invalid-attachment"
	invalid.Attachments = map[string][]byte{".hidden": []byte("refused")}
	if _, err := workspace.BuildActRequest(ctx, private, "human", invalid); err == nil || !strings.Contains(err.Error(), "invalid attachment name") {
		t.Fatalf("invalid attachment error = %v", err)
	}
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
	head, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
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
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.config.Genesis), heads[depth], current); err != nil {
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
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.config.Genesis), heads[depth+1], current); err != nil {
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

// What this holds is refusal *before* append: a report the boundary rejects
// must leave the workroom exactly as it found it, with nothing signed and no
// depth spent. It used to make that point with a report that had no promise,
// which is now a legitimate shape -- the addressee may answer directly. The
// point survives with a reporter who was never asked, which no shape admits.
func TestReportFromAStrangerIsRefusedBeforeAppend(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "stranger", "agent"); err != nil {
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
	_, err = workspace.Act(ctx, "stranger", Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "done",
		RestsOn: []string{request.ID}, IdempotencyKey: "report-without-promise",
	})
	if err == nil || !strings.Contains(err.Error(), "only the requested performer may report directly on a request") {
		t.Fatalf("stranger's report error = %v", err)
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
		// Admission refuses the verdict shape outright now, naming the guarded
		// route, which subsumes the older artifact-basis check for ordinary
		// state writes.
		for _, want := range []string{"review verdict", "gs review", "MCP review tool"} {
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

	// The verdict shape is now guarded at the write boundary, but a record an
	// older writer already put in the log stays visible exactly as recorded.
	// File one below this process's application boundary to prove the fold
	// still reads it as an effective review once its named artifact basis is
	// present.
	approval.RestsOn = append(approval.RestsOn, artifact.ID)
	approval.IdempotencyKey = "approval-with-artifact-basis"
	_, private, err := workspace.Actor("agent")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := workroom.Encode(workroom.State{Kind: approval.Kind, Text: approval.Text, Body: map[string]string{
		"verdict": approval.Body["verdict"], "head": approval.Body["head"], "artifact": approval.Body["artifact"],
	}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := workspace.Store.WritePayloadTree(ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := workspace.View()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        approval.RestsOn,
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: approval.IdempotencyKey,
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	result, err := kernel.Submit(ctx, workspace.Store, kernel.Request{Signed: signed, Payload: payload}, kernel.Options{SigningKey: view.SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	record := workroom.Record{
		ID: workspace.EventID(result.Commit), Timestamp: result.Timestamp,
		Actor: intent.ActorFingerprint(signed.ActorKey), Schema: workroom.SchemaState,
		RestsOn: approval.RestsOn, Payload: payload,
	}
	decision, ok := workspace.mustSnapshot(t, ctx).Projection.Decision(record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("historical approval with its named artifact basis = %+v, found=%v", decision, ok)
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
			RestsOn: []string{workspace.EventID(workspace.config.Genesis)}, IdempotencyKey: "address-" + string(rune('a'+index)),
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
		RestsOn: []string{workspace.EventID(workspace.config.Genesis)}, IdempotencyKey: "bad-address",
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
			RestsOn: []string{workspace.EventID(workspace.config.Genesis)}, IdempotencyKey: key,
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
	workspace.config.IdempotencyNamespace = ""
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
		config: apphost.Config{Version: 0, Genesis: workspace.config.Genesis, ObjectFormat: workspace.config.ObjectFormat, ReadOnly: true},
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
	actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "advance", RestsOn: []string{workspace.EventID(workspace.config.Genesis)}, IdempotencyKey: "advance"})
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
	actRecord(t, ctx, external, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "external advance", RestsOn: []string{workspace.EventID(workspace.config.Genesis)}, IdempotencyKey: "external-advance"})
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

func TestColdStreamPublishesOnlyTheCompleteProjection(t *testing.T) {
	t.Setenv(checkpointEnvironment, "off")
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 12 {
		if _, err := workspace.Act(ctx, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "streamed record " + strconv.Itoa(index),
			IdempotencyKey: "streamed-record-" + strconv.Itoa(index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	want, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cold, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	prepared := make(chan struct{})
	release := make(chan struct{})
	var preparedOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	cold.SetProjectionRebuildTestGate(func(total int) {
		if total != want.Depth {
			t.Errorf("prepared events = %d, want depth %d", total, want.Depth)
		}
		preparedOnce.Do(func() { close(prepared) })
		<-release
	})
	type result struct {
		loaded SourcedSnapshot
		err    error
	}
	done := make(chan result, 1)
	go func() {
		loaded, err := cold.SnapshotWithSource(ctx)
		done <- result{loaded: loaded, err: err}
	}()

	select {
	case <-prepared:
	case <-time.After(20 * time.Second):
		t.Fatal("cold stream did not prepare its projection")
	}
	if cold.snapshotCache != nil || cold.snapshotFolder != nil {
		t.Fatalf("provisional projection escaped before publication: snapshot=%v folder=%p", cold.snapshotCache, cold.snapshotFolder)
	}
	unblock()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.loaded.Source != SnapshotSourceColdFullAudit {
			t.Fatalf("snapshot source = %q, want cold full audit", result.loaded.Source)
		}
		if !reflect.DeepEqual(result.loaded.Snapshot, want) {
			t.Fatalf("published streamed projection differs from ordinary fold\nstreamed: %#v\nordinary: %#v", result.loaded.Snapshot, want)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("cold stream did not publish after release")
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
	workspace.config.ReadOnly = true

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
	if workspace.config.VerifiedFrontier == nil || workspace.config.VerifiedFrontier.Head != trusted.Head || workspace.config.VerifiedFrontier.Depth != trusted.Depth {
		t.Fatalf("failed persistence moved trusted frontier to %+v, want %+v", workspace.config.VerifiedFrontier, trusted)
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
		firstCommit := eventCommit(t, workspace.config.ObjectFormat, first.ID)
		secondCommit := eventCommit(t, workspace.config.ObjectFormat, second.ID)
		if trusted.Head != secondCommit || workspace.config.VerifiedFrontier == nil || workspace.config.VerifiedFrontier.Head != secondCommit {
			t.Fatalf("trusted frontier = snapshot %+v config %+v, want %s", trusted, workspace.config.VerifiedFrontier, secondCommit)
		}
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.config.Genesis), firstCommit, secondCommit); err != nil {
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
		if restarted.config.VerifiedFrontier == nil || restarted.config.VerifiedFrontier.Head != secondCommit {
			t.Fatalf("rejected sibling replaced trusted frontier: %+v", restarted.config.VerifiedFrontier)
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
	checkpointRef := kernel.CheckpointRef(workspace.config.Genesis)
	checkpointHead, err := workspace.Store.Head(ctx, checkpointRef)
	if err != nil {
		t.Fatalf("checkpoint ref: %v", err)
	}
	pointer := filepath.Join(workspace.MetaDir, "checkpoints", workspace.config.Genesis+".json")
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

	if err := workspace.Store.UpdateRef(ctx, checkpointRef, workspace.config.Genesis, checkpointHead); err != nil {
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
	if err := os.WriteFile(pointer, []byte(`{"schema":"gitseq-checkpoint-pointer@1","commit":"`+workspace.config.Genesis+`"}`), 0o600); err != nil {
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
	if repairedHead, err := workspace.Store.Head(ctx, checkpointRef); err != nil || repairedHead == workspace.config.Genesis {
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
	ref := kernel.CheckpointRef(workspace.config.Genesis)
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
	config := workspace.config
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
	if _, exists := workspace.config.Actors["claude.2"]; exists {
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
	if _, exists := workspace.config.Actors["claude.3"]; !exists {
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
	}); err == nil || !strings.Contains(err.Error(), "addresses no known actor") {
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

func TestBuildActRequestRefusesRatifyingAnArtifactAndNamesTheClosingAct(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "Implement it",
		Body:    map[string]string{"to": agent.Fingerprint, "conditions": "approved head merges"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "unratifiable-artifact-request",
	})
	promise := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
		RestsOn: []string{request.ID}, IdempotencyKey: "unratifiable-artifact-promise",
	})
	artifact := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: workroom.KindArtifact, Text: "Exact implementation head",
		Body:    map[string]string{"path": "internal/workroom", "commit": "abc123"},
		RestsOn: []string{promise.ID}, IdempotencyKey: "unratifiable-artifact",
	})
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	before := workspace.mustSnapshot(t, ctx)
	_, err = workspace.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbRatify, Target: artifact.ID, IdempotencyKey: "refused-artifact-ratification",
	})
	if err == nil {
		t.Fatal("ratifying an artifact was built even though its satisfier is none")
	}
	for _, want := range []string{`kind "artifact"`, `satisfier "none"`, "merge an independently approved exact head"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ratify refusal %q does not name %q", err, want)
		}
	}
	after := workspace.mustSnapshot(t, ctx)
	if after.Depth != before.Depth || after.Head != before.Head {
		t.Fatalf("refused ratification changed the log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

// The write boundary has to admit what the fold admits. A report may rest on
// the request it answers, and this is where that record is built and signed:
// if the validator still demands a promise, the rule is widened in the log and
// unusable by any actor, which is worse than not widening it.
//
// This builds and submits the record rather than asking the validator its
// opinion of one, because the question is whether a direct report can be
// written at all.
func TestADirectReportCanActuallyBeBuilt(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "Do the thing",
		Body:    map[string]string{"to": agent.Fingerprint, "conditions": "it is done"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "direct-request",
	})

	// The addressee reports directly, having made no promise. The fold admits
	// this, so the builder must too.
	if _, err := workspace.Act(ctx, "agent", Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "done",
		RestsOn: []string{request.ID}, IdempotencyKey: "direct-report",
	}); err != nil {
		t.Fatalf("a direct report could not be written: %v", err)
	}

	// And widening the shape must not widen who may use it: somebody who was
	// not asked is still refused here, as at the fold.
	if _, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "not mine to report",
		RestsOn: []string{request.ID}, IdempotencyKey: "stranger-report",
	}); err == nil {
		t.Fatal("a stranger's direct report was accepted at the write boundary")
	}
}

// The three cases codex named in verdict d7d8d952. All of them are the same
// question: does the pre-signing boundary ask what the fold asks, or a second
// narrower question of its own? A boundary that answers differently either
// refuses work the fold would accept, or signs and appends a record the fold
// will rule ineffective — spending depth to record a refusal.
func TestTheWriteBoundaryAsksWhatTheFoldAsks(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
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

	newRequest := func(key string) workroom.Record {
		t.Helper()
		return actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindRequest, Text: "build " + key,
			Body:    map[string]string{"to": agent.Fingerprint, "conditions": "tests pass"},
			RestsOn: []string{seed.ID}, IdempotencyKey: "request-" + key,
		})
	}

	t.Run("an ineffective promise does not block direct completion", func(t *testing.T) {
		one, two := newRequest("ineffective-a"), newRequest("ineffective-b")
		// Resting on two requests makes this promise ineffective, while its
		// provenance still names the request below. A refused record is not a
		// commitment, so it must not stand in the way of one.
		refused := actRecord(t, ctx, workspace, "agent", Act{
			Verb: VerbState, Kind: workroom.KindPromise, Text: "I will, ambiguously",
			RestsOn: []string{one.ID, two.ID}, IdempotencyKey: "ambiguous-promise",
		})
		snapshot, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, decision := range snapshot.Projection.Decisions {
			if decision.Event == refused.ID && decision.Verdict == workroom.Effective {
				t.Fatalf("the fixture promise was effective; this case proves nothing")
			}
		}
		if _, err := workspace.Act(ctx, "agent", Act{
			Verb: VerbState, Kind: workroom.KindReport, Text: "done anyway",
			RestsOn: []string{one.ID}, IdempotencyKey: "direct-past-ineffective",
		}); err != nil {
			t.Fatalf("an ineffective promise blocked a direct report: %v", err)
		}
	})

	t.Run("a declared active promise kind does block it", func(t *testing.T) {
		request := newRequest("declared")
		actRecord(t, ctx, workspace, "agent", Act{
			Verb: VerbState, Kind: "undertaking", Text: "I will",
			RestsOn: []string{request.ID}, IdempotencyKey: "declared-promise",
		})
		_, err := workspace.Act(ctx, "agent", Act{
			Verb: VerbState, Kind: workroom.KindReport, Text: "done",
			RestsOn: []string{request.ID}, IdempotencyKey: "direct-past-declared",
		})
		if err == nil || !strings.Contains(err.Error(), "report on the promise") {
			t.Fatalf("a declared promise-lifecycle claim did not redirect the report: %v", err)
		}
	})

	t.Run("a promised report citing an unrelated request is refused before append", func(t *testing.T) {
		mine, unrelated := newRequest("promised"), newRequest("unrelated")
		promise := actRecord(t, ctx, workspace, "agent", Act{
			Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
			RestsOn: []string{mine.ID}, IdempotencyKey: "promise-for-unrelated-case",
		})
		before, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = workspace.Act(ctx, "agent", Act{
			Verb: VerbState, Kind: workroom.KindReport, Text: "done",
			RestsOn: []string{promise.ID, unrelated.ID}, IdempotencyKey: "false-provenance",
		})
		if err == nil || !strings.Contains(err.Error(), "cites a request other than the one its promise answers") {
			t.Fatalf("false provenance was accepted: %v", err)
		}
		after, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Head != before.Head || after.Depth != before.Depth {
			t.Fatalf("refused report changed the workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
		}
	})
}

// Codex's fourth mismatch, and the subtlest: the boundary classified every
// historical statement with the vocabulary as it stands now, while the fold
// classifies each record with the definition that was in force where it sits.
// Redefine a promise-lifecycle kind and the two answers diverge, so the
// boundary signs and appends a record the fold then rules ineffective —
// spending depth to record a refusal, from a mismatch that was locally known.
func TestVocabularyRedefinitionDoesNotLetARefusedReportBeAppended(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	define := func(lifecycle workroom.Lifecycle, key string) workroom.Record {
		t.Helper()
		fields, err := json.Marshal([]workroom.FieldConstraint{})
		if err != nil {
			t.Fatal(err)
		}
		bases, err := json.Marshal([]workroom.BasisConstraint{{Kinds: []workroom.Kind{workroom.KindRequest}, Min: 1, Max: 1}})
		if err != nil {
			t.Fatal(err)
		}
		definition := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindKindDef, Text: "define undertaking " + key,
			Body: map[string]string{
				"name": "undertaking", "fields": string(fields), "basis": string(bases),
				"satisfier": workroom.SatisfierNone, "render": string(workroom.RenderCommitment),
				"staleness": string(workroom.StalenessPropagates), "lifecycle": string(lifecycle),
				"guidance": "test lifecycle",
			},
			RestsOn: []string{seed.ID}, IdempotencyKey: "define-" + key,
		})
		actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbRatify, Target: definition.ID, IdempotencyKey: "ratify-" + key,
		})
		return definition
	}

	asPromise := define(workroom.LifecyclePromise, "as-promise")
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "build",
		Body:    map[string]string{"to": agent.Fingerprint, "conditions": "tests pass"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "redefinition-request",
	})
	// A live claim, recorded while undertaking was a promise.
	actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: "undertaking", Text: "I will",
		RestsOn: []string{request.ID}, IdempotencyKey: "claim-under-old-vocabulary",
	})
	// Now the kind is redefined to carry no lifecycle at all. The claim keeps
	// the meaning it had where it stands; only later records see the new one.
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbSupersede, Target: asPromise.ID, Text: "redefining", IdempotencyKey: "retire-as-promise",
	})
	define(workroom.LifecycleNone, "as-none")

	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = workspace.Act(ctx, "agent", Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "done",
		RestsOn: []string{request.ID}, IdempotencyKey: "report-past-redefined-claim",
	})
	// The reason matters as much as the refusal. Asserting only that some error
	// came back would let this pass through any unrelated fail-closed path and
	// still read as proof that the claim was seen.
	if err == nil {
		t.Fatal("the boundary accepted a direct report the fold refuses, because it read the current vocabulary rather than the claim's own")
	}
	if !strings.Contains(err.Error(), "report on the promise") {
		t.Fatalf("refused, but not because the redefined claim was still seen: %v", err)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused report changed the workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

// The other half of the profile contract.
// TestProjectionProfileChangeRebuildsFromTheSameKernelCheckpoint proves a cache
// is dropped when the profile string changes and that a rebuild with the same
// rules gives the same answer. This one proves the case the profile exists for:
// rules that actually changed, where serving the cache would answer with the
// old world.
//
// The transition under test is whichever one the build is making -- @13 to @14
// as this stands, for the satisfier the fold captured on each statement.
// Re-anchor both constants and the observable when the profile moves again;
// that edit is the point, because it makes whoever bumps the profile look at
// the cache. Anchoring the seed on a predecessor the build no longer makes
// leaves the test passing while it proves nothing about the transition it
// names.
//
// That is not a hypothetical. This block said "@12 to @13" while the constants
// below already read @13 and @14, because the profile moved and only the prose
// was left behind -- the exact drift the paragraph above warns about, in the
// paragraph that warns about it. The constants were right and the test was
// sound; a reader trusting the comment would have believed otherwise.
//
// The observable is the satisfier on the ratified approval. @13 projected no
// such field, so an @13 cache carries the empty string where @14 carries
// "originating-requester", and the difference is not cosmetic: mayRatify reads
// exactly this value and refuses when it is absent, so a served @13 cache
// answers every reader "no authority" and reintroduces through the cache alone
// the silent denial the field was added to close.
//
// The cache is keyed on the whole stored identity, the application and the fold
// version together, so this seeds and asserts that exact composite. A bare fold
// version would never match whatever the build holds, and the cache would be
// dropped for the wrong reason: the witness would pass with the bump reverted.
// The seeded cache stands at the current head, so the profile is the only thing
// that can cause a replay. Revert ProfileVersion to @13 and the first branch of
// snapshotWithSource returns the @13 answer verbatim.
//
// The @11-to-@12 receipt checkpoint this test used to witness is not kept here.
// It says nothing about the transition the build is making, and the rule
// itself is covered directly by internal/workroom/receipt_checkpoint_test.go:
// eleven dedicated tests, of which
// TestReceiptCheckpointRestoresASuccessorThatWasStaleAtBirth is the case this
// fixture reproduced. Keeping the scaffolding would restate a settled rule
// inside a test about something else.
func TestAnOlderProfileCacheIsRebuiltUnderTheNewRules(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	implementation := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: workroom.KindArtifact, Text: "the reviewed candidate",
		Body:    map[string]string{"path": "spike", "commit": "head1"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "profile-rebuild-candidate",
	})
	reviewRequest := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "review the exact head",
		Body:    map[string]string{"to": "reviewer", "conditions": "exact head"},
		RestsOn: []string{implementation.ID}, IdempotencyKey: "profile-rebuild-review-request",
	})
	promise := actRecord(t, ctx, workspace, "reviewer", Act{
		Verb: VerbState, Kind: workroom.KindPromise, Text: "will review",
		RestsOn: []string{reviewRequest.ID}, IdempotencyKey: "profile-rebuild-review-promise",
	})
	// This cache-migration fixture represents a verdict written by the older
	// application profile. Put that historical record below today's admission
	// boundary; the test is about refolding the same durable history, not about
	// whether a generic state write may file a new verdict now.
	_, reviewerPrivate, err := workspace.Actor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	approvalRestsOn := []string{promise.ID, implementation.ID}
	approvalPayload, err := workroom.Encode(workroom.State{
		Kind: workroom.KindReport,
		Text: "approved",
		Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": implementation.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	approvalTree, err := workspace.Store.WritePayloadTree(ctx, approvalPayload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := workspace.View()
	signedApproval, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + approvalTree,
		RestsOn:        approvalRestsOn,
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: "profile-rebuild-approval",
	}, reviewerPrivate)
	if err != nil {
		t.Fatal(err)
	}
	approvalResult, err := kernel.Submit(ctx, workspace.Store, kernel.Request{
		Signed: signedApproval, Payload: approvalPayload,
	}, kernel.Options{SigningKey: view.SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	approval := workroom.Record{
		ID: workspace.EventID(approvalResult.Commit), Timestamp: approvalResult.Timestamp,
		Actor: intent.ActorFingerprint(signedApproval.ActorKey), Schema: workroom.SchemaState,
		RestsOn: approvalRestsOn, Payload: approvalPayload,
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbRatify, Target: approval.ID, IdempotencyKey: "profile-rebuild-ratify",
	})

	current, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	index := statementIndex(t, current.Projection, approval.ID)
	admitted := current.Projection.Statements[index].Satisfier
	// Not merely non-empty. The value is the one thing a served cache cannot
	// produce, so naming it here means a projection that publishes some other
	// satisfier for a report fails this test rather than passing it.
	if admitted != workroom.SatisfierOriginatingRequester {
		t.Fatalf("the approval's admitted satisfier is %q, want %q: the seeded cache below would not differ from a replay", admitted, workroom.SatisfierOriginatingRequester)
	}

	// What an @13 cache holds: the same projection at the same head, with no
	// satisfier on any statement, because @13 did not project the field.
	// Nothing else is touched, so the satisfier is the only thing that can tell
	// a replay from a served cache.
	unsatisfied := current
	unsatisfied.Projection.Statements = append([]workroom.Statement(nil), current.Projection.Statements...)
	for position := range unsatisfied.Projection.Statements {
		unsatisfied.Projection.Statements[position].Satisfier = ""
	}
	oldProfile := apphost.DefaultApplication + "\x00" + "workroom-fold@13"
	wantProfile := apphost.DefaultApplication + "\x00" + "workroom-fold@16"
	workspace.snapshotMu.Lock()
	workspace.snapshotCache = &unsatisfied
	workspace.snapshotSource = SnapshotSourceSignedCheckpointTail
	workspace.snapshotProfile = oldProfile
	workspace.snapshotMu.Unlock()

	rebuilt, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	replayed := rebuilt.Snapshot.Projection.Statements[statementIndex(t, rebuilt.Snapshot.Projection, approval.ID)].Satisfier
	if replayed != admitted {
		t.Fatalf("the rebuilt projection has satisfier %q for the approval, want %q: the %q cache was served instead of replayed", replayed, admitted, oldProfile)
	}
	if workspace.snapshotProfile != wantProfile {
		t.Fatalf("cache profile = %q, want %q: the older profile was not replaced", workspace.snapshotProfile, wantProfile)
	}
}

func TestAwaitingMergeStatusRebuildsAnOlderProfileCache(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "Implement it",
		Body:    map[string]string{"to": agent.Fingerprint, "conditions": "approved head merges"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "profile-awaiting-merge-request",
	})
	promise := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
		RestsOn: []string{request.ID}, IdempotencyKey: "profile-awaiting-merge-promise",
	})
	artifact := actRecord(t, ctx, workspace, "agent", Act{
		Verb: VerbState, Kind: workroom.KindArtifact, Text: "Exact implementation head",
		Body:    map[string]string{"path": "internal/workroom", "commit": "abc123"},
		RestsOn: []string{promise.ID}, IdempotencyKey: "profile-awaiting-merge-artifact",
	})

	current := workspace.mustSnapshot(t, ctx)
	old := current
	old.Projection.Commitments = append([]workroom.Commitment(nil), current.Projection.Commitments...)
	position := -1
	for index := range old.Projection.Commitments {
		if old.Projection.Commitments[index].Report == artifact.ID {
			position = index
			break
		}
	}
	if position < 0 {
		t.Fatal("artifact completion is absent from the current projection")
	}
	if got := old.Projection.Commitments[position]; got.Status != "awaiting-merge" || got.WaitingOn != agent.Fingerprint {
		t.Fatalf("current artifact completion = %+v", got)
	}
	// @15 projected the artifact as awaiting merge with no waiting party, so
	// a served cache would keep the approved head out of every actor's queue.
	old.Projection.Commitments[position].WaitingOn = ""
	oldProfile := apphost.DefaultApplication + "\x00workroom-fold@15"
	wantProfile := apphost.DefaultApplication + "\x00workroom-fold@16"
	workspace.snapshotMu.Lock()
	workspace.snapshotCache = &old
	workspace.snapshotSource = SnapshotSourceSignedCheckpointTail
	workspace.snapshotProfile = oldProfile
	workspace.snapshotMu.Unlock()

	rebuilt, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, commitment := range rebuilt.Snapshot.Projection.Commitments {
		if commitment.Report != artifact.ID {
			continue
		}
		if commitment.Status != "awaiting-merge" || commitment.WaitingOn != agent.Fingerprint {
			t.Fatalf("rebuilt artifact completion = %+v; the %q cache was served instead of replayed", commitment, oldProfile)
		}
		if workspace.snapshotProfile != wantProfile {
			t.Fatalf("cache profile = %q, want %q", workspace.snapshotProfile, wantProfile)
		}
		return
	}
	t.Fatal("rebuilt projection omitted the artifact completion")
}

// The guarded schemas have a semantic witness of their own. An @12 fold cannot decode
// either guarded schema, so its cache sees ineffective opaque records. The
// current fold lowers them to an ordinary supersession and request only after
// enforcing their signed commitment tuple. Serving the old cache would thus
// erase a reassignment that the durable history actually contains.
func TestReassignSchemasRebuildAnOlderProfileCache(t *testing.T) {
	ctx := context.Background()
	fixture := newReassignFixture(t)
	retirement := actRecord(t, ctx, fixture.workspace, "human", fixture.retireAct())
	replacement := actRecord(t, ctx, fixture.workspace, "human", fixture.replacementAct(retirement.ID))
	current := fixture.workspace.mustSnapshot(t, ctx)
	for _, event := range []string{retirement.ID, replacement.ID} {
		decision, ok := current.Projection.Decision(event)
		if !ok || decision.Verdict != workroom.Effective {
			t.Fatalf("current guarded decision %s = %+v, found=%v", event, decision, ok)
		}
	}

	old := current
	old.Projection.Decisions = append([]workroom.Decision(nil), current.Projection.Decisions...)
	for index := range old.Projection.Decisions {
		decision := &old.Projection.Decisions[index]
		if decision.Event == retirement.ID || decision.Event == replacement.ID {
			decision.Verdict = workroom.Ineffective
			decision.Reason = "unsupported workroom schema under workroom-fold@12"
		}
	}
	old.Projection.Acts = slices.DeleteFunc(append([]workroom.Act(nil), current.Projection.Acts...), func(act workroom.Act) bool {
		return act.Event == retirement.ID
	})
	old.Projection.Statements = slices.DeleteFunc(append([]workroom.Statement(nil), current.Projection.Statements...), func(statement workroom.Statement) bool {
		return statement.Event == replacement.ID
	})
	fixture.workspace.snapshotMu.Lock()
	fixture.workspace.snapshotCache = &old
	fixture.workspace.snapshotSource = SnapshotSourceSignedCheckpointTail
	fixture.workspace.snapshotProfile = apphost.DefaultApplication + "\x00workroom-fold@12"
	fixture.workspace.snapshotMu.Unlock()

	rebuilt, err := fixture.workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{retirement.ID, replacement.ID} {
		decision, ok := rebuilt.Snapshot.Projection.Decision(event)
		if !ok || decision.Verdict != workroom.Effective {
			t.Fatalf("rebuilt guarded decision %s = %+v, found=%v", event, decision, ok)
		}
	}
	want := apphost.DefaultApplication + "\x00workroom-fold@16"
	if fixture.workspace.snapshotProfile != want {
		t.Fatalf("cache profile = %q, want %q", fixture.workspace.snapshotProfile, want)
	}
}

func statementIndex(t *testing.T, projection workroom.Projection, event string) int {
	t.Helper()
	for index, statement := range projection.Statements {
		if statement.Event == event {
			return index
		}
	}
	t.Fatalf("no statement projected for %s", event)
	return -1
}
