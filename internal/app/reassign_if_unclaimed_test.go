package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type reassignFixture struct {
	workspace *Workspace
	request   workroom.Record
}

func newReassignFixture(t *testing.T) reassignFixture {
	t.Helper()
	return reassignFixtureOn(t, false)
}

// staleReassignFixture is the same room with one act of ground under the
// request, then withdrawn. The request stands unclaimed and still names its
// addressee; only something underneath it moved.
func staleReassignFixture(t *testing.T) reassignFixture {
	t.Helper()
	return reassignFixtureOn(t, true)
}

func reassignFixtureOn(t *testing.T, stale bool) reassignFixture {
	t.Helper()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	basis := seed.ID
	if stale {
		basis = actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "ground under the request",
			RestsOn: []string{seed.ID}, IdempotencyKey: "request-ground",
		}).ID
	}
	request := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "do it",
		Body:    map[string]string{"to": "@agent", "conditions": "finish"},
		RestsOn: []string{basis}, IdempotencyKey: "original-request",
	})
	if stale {
		actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbSupersede, Target: basis, Text: "the ground moved",
			IdempotencyKey: "withdraw-ground",
		})
		if !statementRow(t, workspace.mustSnapshot(t, ctx), request.ID).Stale {
			t.Fatal("the fixture request did not go stale")
		}
	}
	return reassignFixture{workspace: workspace, request: request}
}

func (f reassignFixture) retireAct() Act {
	return Act{
		Verb: VerbRetireIfUnclaimed, Target: f.request.ID,
		Text: "retire before reassignment", IdempotencyKey: "guarded-retirement",
	}
}

func (f reassignFixture) replacementAct(retirement string) Act {
	return Act{
		Verb: VerbReassignIfUnclaimed, Target: f.request.ID, Retirement: retirement,
		Text: "ask the other agent", Body: map[string]string{"to": "@other", "conditions": "finish"},
		IdempotencyKey: "guarded-replacement",
	}
}

func TestApplicationReassignIfUnclaimedAllowsUnrelatedTraffic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newReassignFixture(t)
	retirement, err := fixture.workspace.Act(ctx, "human", fixture.retireAct())
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, fixture.workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "unrelated traffic",
		RestsOn: []string{retirement.Record.ID}, IdempotencyKey: "unrelated-between",
	})
	replacement, err := fixture.workspace.Act(ctx, "human", fixture.replacementAct(retirement.Record.ID))
	if err != nil {
		t.Fatal(err)
	}
	decision, ok := fixture.workspace.mustSnapshot(t, ctx).Projection.Decision(replacement.Record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("replacement decision = %+v, found=%v", decision, ok)
	}
}

func TestApplicationReassignIfUnclaimedRefusesBothRaceWindowsBeforeAppend(t *testing.T) {
	t.Parallel()
	t.Run("promise before retirement", func(t *testing.T) {
		ctx := context.Background()
		fixture := newReassignFixture(t)
		actRecord(t, ctx, fixture.workspace, "agent", Act{
			Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
			RestsOn: []string{fixture.request.ID}, IdempotencyKey: "promise-before",
		})
		before := fixture.workspace.mustSnapshot(t, ctx)
		_, err := fixture.workspace.Act(ctx, "human", fixture.retireAct())
		if err == nil || !strings.Contains(err.Error(), "admitted promise") {
			t.Fatalf("retirement error = %v", err)
		}
		after := fixture.workspace.mustSnapshot(t, ctx)
		if after.Depth != before.Depth {
			t.Fatalf("refusal appended: depth %d -> %d", before.Depth, after.Depth)
		}
	})

	t.Run("promise between guarded acts", func(t *testing.T) {
		ctx := context.Background()
		fixture := newReassignFixture(t)
		retirement, err := fixture.workspace.Act(ctx, "human", fixture.retireAct())
		if err != nil {
			t.Fatal(err)
		}
		// AllowDeadBasis reproduces traffic admitted by an older resident: the
		// promise remains an effective fold fact even though its request moved.
		actRecord(t, ctx, fixture.workspace, "agent", Act{
			Verb: VerbState, Kind: workroom.KindPromise, Text: "late claim",
			RestsOn: []string{fixture.request.ID}, AllowDeadBasis: true,
			IdempotencyKey: "promise-between",
		})
		retirementReplay, err := fixture.workspace.Act(ctx, "human", fixture.retireAct())
		if err != nil || !retirementReplay.Result.Replay || retirementReplay.Record.ID != retirement.Record.ID {
			t.Fatalf("whole-helper retirement retry = %+v, %v", retirementReplay, err)
		}
		before := fixture.workspace.mustSnapshot(t, ctx)
		_, err = fixture.workspace.Act(ctx, "human", fixture.replacementAct(retirement.Record.ID))
		if err == nil || !strings.Contains(err.Error(), "admitted promise") {
			t.Fatalf("replacement error = %v", err)
		}
		after := fixture.workspace.mustSnapshot(t, ctx)
		if after.Depth != before.Depth {
			t.Fatalf("refusal appended: depth %d -> %d", before.Depth, after.Depth)
		}
	})
}

func TestApplicationReassignIfUnclaimedExactRetriesReplayWithoutRejudging(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newReassignFixture(t)
	retirementAct := fixture.retireAct()
	firstRetirement, err := fixture.workspace.Act(ctx, "human", retirementAct)
	if err != nil {
		t.Fatal(err)
	}
	replacementAct := fixture.replacementAct(firstRetirement.Record.ID)
	firstReplacement, err := fixture.workspace.Act(ctx, "human", replacementAct)
	if err != nil {
		t.Fatal(err)
	}
	// Move both current-world facts that request construction used. Retirement
	// removes the addressee from local custody, while the new tracked page would
	// refuse a genuinely new retirement. Neither may stop the kernel from seeing
	// the byte-identical submissions as replays.
	if _, err := fixture.workspace.RetireActor(ctx, "human", "@other"); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(fixture.workspace.Repo, "docs", "reference", "late-citation.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("---\nrests_on:\n  - "+fixture.request.ID+"\n---\n\nlate citation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", fixture.workspace.Repo, "add", "docs/reference/late-citation.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	retirementReplay, err := fixture.workspace.Act(ctx, "human", retirementAct)
	if err != nil {
		t.Fatalf("retirement retry: %v", err)
	}
	replacementReplay, err := fixture.workspace.Act(ctx, "human", replacementAct)
	if err != nil {
		t.Fatalf("replacement retry: %v", err)
	}
	if !retirementReplay.Result.Replay || retirementReplay.Record.ID != firstRetirement.Record.ID {
		t.Fatalf("retirement replay = %+v", retirementReplay)
	}
	if !replacementReplay.Result.Replay || replacementReplay.Record.ID != firstReplacement.Record.ID {
		t.Fatalf("replacement replay = %+v", replacementReplay)
	}
}

func TestGuardedRequestHistoricalFallbackDoesNotMaskCustodyFailure(t *testing.T) {
	t.Parallel()
	fixture := newReassignFixture(t)
	blocked := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(blocked, []byte("not a metadata directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.workspace.MetaDir = blocked
	_, err := fixture.workspace.normalizeGuardedRequestShape(context.Background(), map[string]string{
		"to": "@missing", "conditions": "finish",
	})
	if err == nil || !strings.Contains(err.Error(), "re-read configuration custody") {
		t.Fatalf("guarded normalization error = %v, want custody failure", err)
	}
}

func TestGuardedRetirementPostDedupCitationObservationAndOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newReassignFixture(t)
	page := filepath.Join(fixture.workspace.Repo, "docs", "reference", "cited-request.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("---\nrests_on:\n  - "+fixture.request.ID+"\n---\n\ncited request\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", fixture.workspace.Repo, "add", "docs/reference/cited-request.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	_, private, err := fixture.workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	payload := workroom.RetireIfUnclaimed{
		Target: fixture.request.ID, Text: "retire before reassignment",
		Expectation: workroom.UnclaimedExpectation{
			Request: fixture.request.ID, Promise: workroom.CommitmentAbsent, Completion: workroom.CommitmentAbsent,
		},
	}
	build := func(key string) kernel.Request {
		t.Helper()
		request, err := fixture.workspace.buildRequest(ctx, private, "human", workroom.SchemaRetireUnclaimed,
			payload, []string{fixture.request.ID}, nil, key)
		if err != nil {
			t.Fatal(err)
		}
		return request
	}
	before := fixture.workspace.mustSnapshot(t, ctx).Depth
	if _, err := fixture.workspace.AcceptSubmission(ctx, build("cited-false")); err == nil || !strings.Contains(err.Error(), "cited-request.md") {
		t.Fatalf("post-dedup citation refusal = %v", err)
	}
	if after := fixture.workspace.mustSnapshot(t, ctx).Depth; after != before {
		t.Fatalf("citation refusal appended: depth %d -> %d", before, after)
	}
	payload.CitedOK = true
	accepted, err := fixture.workspace.AcceptSubmission(ctx, build("cited-override"))
	if err != nil {
		t.Fatalf("signed cited_ok override: %v", err)
	}
	decision, ok := fixture.workspace.mustSnapshot(t, ctx).Projection.Decision(accepted.Record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("override decision = %+v, found=%v", decision, ok)
	}
}

// The whole write boundary, not just the fold: a stale unclaimed request is
// reassigned, and the same request refuses once someone has promised it.
func TestApplicationReassignIfUnclaimedActsOnAStaleUnclaimedRequest(t *testing.T) {
	t.Run("stale and unclaimed reassigns", func(t *testing.T) {
		ctx := context.Background()
		fixture := staleReassignFixture(t)
		retirement, err := fixture.workspace.Act(ctx, "human", fixture.retireAct())
		if err != nil {
			t.Fatalf("guarded retirement of a stale unclaimed request: %v", err)
		}
		replacement, err := fixture.workspace.Act(ctx, "human", fixture.replacementAct(retirement.Record.ID))
		if err != nil {
			t.Fatalf("guarded replacement of a stale unclaimed request: %v", err)
		}
		projection := fixture.workspace.mustSnapshot(t, ctx).Projection
		for _, event := range []string{retirement.Record.ID, replacement.Record.ID} {
			decision, ok := projection.Decision(event)
			if !ok || decision.Verdict != workroom.Effective {
				t.Fatalf("decision %s = %+v, found=%v", event, decision, ok)
			}
		}
	})

	t.Run("stale and promised still refuses", func(t *testing.T) {
		ctx := context.Background()
		fixture := staleReassignFixture(t)
		actRecord(t, ctx, fixture.workspace, "agent", Act{
			Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
			RestsOn: []string{fixture.request.ID}, IdempotencyKey: "promise-on-stale",
		})
		before := fixture.workspace.mustSnapshot(t, ctx)
		_, err := fixture.workspace.Act(ctx, "human", fixture.retireAct())
		if err == nil || !strings.Contains(err.Error(), "admitted promise") {
			t.Fatalf("retirement of a promised stale request = %v", err)
		}
		if after := fixture.workspace.mustSnapshot(t, ctx); after.Depth != before.Depth {
			t.Fatalf("refusal appended: depth %d -> %d", before.Depth, after.Depth)
		}
	})
}
