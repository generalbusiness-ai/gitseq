package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// queryFixture is a workroom with one commitment loop in it, opened through the
// commands a person actually types rather than through the builders directly.
// The point of these tests is the surface: that the CLI hands the shared
// builder what the caller asked for and prints what came back.
type queryFixture struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	workspace *app.Workspace
	seed      string
	request   string
	artifact  string
}

func newQueryFixture(t *testing.T) queryFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "worker", "agent"); err != nil {
		t.Fatal(err)
	}
	fixture := queryFixture{t: t, ctx: ctx, repo: repo, workspace: workspace, seed: seed.ID}

	request, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "Add the changelog",
		Body: map[string]string{"to": "@worker", "conditions": "it exists"}, RestsOn: []string{fixture.seed},
		IdempotencyKey: "query-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.request = request.Record.ID
	if _, err := workspace.Act(ctx, "worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "I will add it",
		RestsOn: []string{fixture.request}, IdempotencyKey: "query-promise",
	}); err != nil {
		t.Fatal(err)
	}
	head := testGit(t, repo, "rev-parse", "HEAD")
	artifact, err := workspace.Act(ctx, "worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the changelog stands here",
		Body: map[string]string{"path": "CHANGELOG.md", "commit": head}, RestsOn: []string{fixture.request},
		IdempotencyKey: "query-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.artifact = artifact.Record.ID
	// A second commitment, so the operator's page has something to continue to.
	// A cursor test on a one-row page proves nothing about paging.
	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "Also write the release note",
		Body: map[string]string{"to": "@operator", "conditions": "it exists"}, RestsOn: []string{fixture.seed},
		IdempotencyKey: "query-second-request",
	}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// run types one command and returns both streams and its refusal, because which
// stream carried which half of the answer is part of what is under test.
func (f queryFixture) run(command func(context.Context, []string) error, arguments ...string) (string, string, error) {
	f.t.Helper()
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWriter, errWriter
	commandErr := command(f.ctx, append([]string{"--repo", f.repo}, arguments...))
	os.Stdout, os.Stderr = stdout, stderr
	outWriter.Close()
	errWriter.Close()
	printed, err := io.ReadAll(outReader)
	if err != nil {
		f.t.Fatal(err)
	}
	warned, err := io.ReadAll(errReader)
	if err != nil {
		f.t.Fatal(err)
	}
	outReader.Close()
	errReader.Close()
	return string(printed), string(warned), commandErr
}

// The CLI JSON is the page the MCP tools and the resident routes return, not a
// second shape rendered beside them. A caller who learned one has learned both,
// which is only true while both come out of the same builder.
func TestWorkJSONIsTheSharedWorkPage(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(workCommand, "--as", "worker", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page statusview.WorkPage
	decoder := json.NewDecoder(strings.NewReader(printed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&page); err != nil {
		t.Fatalf("the CLI printed something that is not a work page: %v\n%s", err, printed)
	}
	snapshot, err := fixture.workspace.Snapshot(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := statusview.BuildWorkPage(snapshot, statusview.WorkQuery{Actor: fixture.workspace.Config.Actors["worker"].Fingerprint}, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.MatchingTotal != direct.MatchingTotal || page.Returned != direct.Returned || len(page.Items) != len(direct.Items) {
		t.Fatalf("the CLI and the shared builder disagree: cli %+v builder %+v", page, direct)
	}
	if page.Returned == 0 {
		t.Fatal("the fixture has an open commitment and the page returned nothing")
	}
}

// Every WorkQuery selector the MCP tool exposes has to arrive at the builder.
// A flag that parses and is then dropped is worse than an absent flag: the
// answer looks selective and is not.
func TestWorkSelectorsReachTheBuilder(t *testing.T) {
	fixture := newQueryFixture(t)
	worker := fixture.workspace.Config.Actors["worker"].Fingerprint

	narrowed, _, err := fixture.run(workCommand, "--as", "worker", "--json", "--lane", "not_actionable")
	if err != nil {
		t.Fatal(err)
	}
	var page statusview.WorkPage
	if err := json.Unmarshal([]byte(narrowed), &page); err != nil {
		t.Fatal(err)
	}
	if page.MatchingTotal != 0 {
		t.Fatalf("--lane not_actionable returned %d rows for an actor whose only commitment is promised", page.MatchingTotal)
	}
	if page.Actor.Fingerprint != worker {
		t.Fatalf("--as selected %s, want %s", page.Actor.Fingerprint, worker)
	}

	excluded, _, err := fixture.run(workCommand, "--as", "worker", "--json", "--status", "satisfied")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(excluded), &page); err != nil {
		t.Fatal(err)
	}
	if page.MatchingTotal != 0 {
		t.Fatalf("--status satisfied returned %d rows before anything was satisfied", page.MatchingTotal)
	}

	if _, _, err := fixture.run(workCommand, "--as", "worker", "--stale", "sometimes"); err == nil {
		t.Fatal("an unknown staleness policy was guessed at instead of refused")
	}
	if _, _, err := fixture.run(workCommand, "--as", "worker", "--lane", "somewhere"); err == nil {
		t.Fatal("an unknown lane was guessed at instead of refused")
	}
	if _, _, err := fixture.run(workCommand, "--as", "nobody"); err == nil {
		t.Fatal("an actor this checkout has never provisioned was accepted")
	}
}

// A cursor continues the page it was minted for and refuses anything else, so a
// caller paging through cannot silently splice two selections together.
func TestWorkPagesThroughItsOwnCursor(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(workCommand, "--as", "operator", "--json", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	var first statusview.WorkPage
	if err := json.Unmarshal([]byte(printed), &first); err != nil {
		t.Fatal(err)
	}
	if first.Returned != 1 || first.MatchingTotal != 2 || first.Remaining != 1 || first.NextCursor == "" {
		t.Fatalf("the first page did not disclose its own bounds: %+v", first)
	}
	printed, _, err = fixture.run(workCommand, "--as", "operator", "--json", "--limit", "1", "--cursor", first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var second statusview.WorkPage
	if err := json.Unmarshal([]byte(printed), &second); err != nil {
		t.Fatal(err)
	}
	if second.Before != 1 || second.Returned != 1 || second.Items[0].Request == first.Items[0].Request {
		t.Fatalf("the continuation did not advance: first %+v second %+v", first.Items[0].Request, second)
	}
	if _, _, err := fixture.run(workCommand, "--as", "operator", "--json", "--limit", "1", "--cursor", "not-a-cursor"); err == nil {
		t.Fatal("a malformed cursor was accepted")
	}
	// The cursor is bound to the filters it was minted under, so continuing it
	// into a differently filtered query is a refusal rather than a splice.
	if _, _, err := fixture.run(workCommand, "--as", "operator", "--json", "--limit", "1", "--stale", "only", "--cursor", first.NextCursor); err == nil {
		t.Fatal("a cursor crossed a change of filters")
	}
}

func TestArtifactSelectorsReachTheBuilder(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(artifactsCommand, "--path", "CHANGELOG.md", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page statusview.ArtifactPage
	decoder := json.NewDecoder(strings.NewReader(printed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&page); err != nil {
		t.Fatalf("the CLI printed something that is not an artifact page: %v\n%s", err, printed)
	}
	if page.MatchingTotal != 1 || page.Artifacts[0].Event != fixture.artifact {
		t.Fatalf("--path did not select the artifact recorded at that exact string: %+v", page)
	}

	// Adversarial: a path nobody recorded. Bounded, honest, and not a refusal.
	empty, _, err := fixture.run(artifactsCommand, "--path", "CHANGELOG.md/nested", "--json")
	if err != nil {
		t.Fatalf("an unknown path was refused instead of answered: %v", err)
	}
	if err := json.Unmarshal([]byte(empty), &page); err != nil {
		t.Fatal(err)
	}
	if page.MatchingTotal != 0 || page.NextCursor != "" {
		t.Fatalf("an unknown path did not answer empty: %+v", page)
	}

	if _, _, err := fixture.run(artifactsCommand, "--path", "CHANGELOG.md", "--state", "withdrawn"); err == nil {
		t.Fatal("an unknown artifact state was guessed at instead of refused")
	}
	if _, _, err := fixture.run(artifactsCommand, "--json"); err == nil {
		t.Fatal("a query naming neither a path nor an anchor was admitted")
	}
}

// The human view is not a debug dump. It has to carry the counts a reader is
// asking for, or they will reach for --json and jq again.
func TestHumanArtifactViewCarriesItsCounts(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(artifactsCommand, "--path", "CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 matching", "1 returned", "0 remaining", "CHANGELOG.md", "current"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("the human artifact view does not say %q:\n%s", want, printed)
		}
	}
}

func TestSupersessionPlanProducesCompleteBatchInput(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(supersessionPlanCommand,
		"--path", "CHANGELOG.md", "--text", "retire the old pointer", "--idempotency-prefix", "retire-", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var plan []batchAct
	decoder := json.NewDecoder(strings.NewReader(printed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		t.Fatalf("plan is not gs batch input: %v\n%s", err, printed)
	}
	if len(plan) != 1 || plan[0].Verb != app.VerbSupersede || plan[0].Target != fixture.artifact || plan[0].Text != "retire the old pointer" || plan[0].IdempotencyKey != "retire-"+fixture.artifact {
		t.Fatalf("unexpected supersession plan: %+v", plan)
	}
}

func TestSupersessionPlanRefusesAPlannedSubset(t *testing.T) {
	page := statusview.ArtifactPage{MatchingTotal: 2, Returned: 1, Remaining: 1,
		Artifacts: []statusview.ArtifactRow{{Event: "first", Path: "."}}}
	if plan, err := buildSupersessionPlan(page, "retire", "key-"); err == nil || plan != nil {
		t.Fatalf("partial plan was emitted: plan=%+v err=%v", plan, err)
	}
}

// These are the five concrete jq programs from the retirement runbook, not a
// guessed query language. Each assertion compares the new bounded capability
// to the exact population the old program selected.
func TestFiveRunbookProgramsHaveCommandParity(t *testing.T) {
	effective := func(event string) workroom.Decision {
		return workroom.Decision{Event: event, Verdict: workroom.Effective}
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 8, Projection: workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "dot-live", Path: "."}, {Event: "dot-old", Path: ".", Retired: true},
			{Event: "doc", Path: "docs/why.md"}, {Event: "guide", Path: "docs/guide.md"},
			{Event: "other", Path: "ui"},
		},
		Statements:  []workroom.Statement{{Event: "review-request", Kind: workroom.KindRequest, Body: map[string]string{"artifact": "doc"}}},
		Commitments: []workroom.Commitment{{Request: "review-request", Status: "open"}},
		Decisions: []workroom.Decision{
			effective("dot-live"), effective("dot-old"), effective("doc"), effective("guide"),
			effective("other"), effective("review-request"),
		},
		Provenance: map[string][]string{"doc": {"dot-live"}, "guide": {"doc"}, "other": {"elsewhere"}},
	}}

	liveDots, err := statusview.BuildArtifactSelectionPage(snapshot, statusview.ArtifactSelection{Paths: []string{"."}, Limit: 50}, false)
	if err != nil || liveDots.MatchingTotal != 1 || liveDots.Artifacts[0].Event != "dot-live" {
		t.Fatalf("program 1 exact live path: page=%+v err=%v", liveDots, err)
	}
	plan, err := buildSupersessionPlan(liveDots, "retire dot", "retire-dot-")
	if err != nil || len(plan) != 1 || plan[0].Target != "dot-live" {
		t.Fatalf("program 2 batch plan: plan=%+v err=%v", plan, err)
	}
	gate, err := statusview.BuildReviewGate(snapshot, 50, false)
	if err != nil || gate.Awaiting != 1 || gate.AwaitingRequests[0] != "review-request" {
		t.Fatalf("program 3 review quiet gate: gate=%+v err=%v", gate, err)
	}
	anchored, err := statusview.BuildArtifactSelectionPage(snapshot, statusview.ArtifactSelection{Reaches: ".", Limit: 50}, false)
	if err != nil || anchored.MatchingTotal != 2 || anchored.Artifacts[0].Event != "guide" || anchored.Artifacts[1].Event != "doc" {
		t.Fatalf("program 4 artifact provenance gate: page=%+v err=%v", anchored, err)
	}
	wave, err := statusview.BuildStalenessWave(snapshot, ".", false)
	if err != nil || wave.Records != 6 || wave.Reached != 4 || wave.LiveArtifacts != 3 || wave.Reaching != 2 {
		t.Fatalf("program 5 whole-log wave: wave=%+v err=%v", wave, err)
	}
}

// Adversarial: an event that resolves to nothing. The refusal names the fact
// and prints no page, rather than rendering an empty inspection that reads like
// a real answer.
func TestInspectRefusesAnEventTheLogDoesNotHold(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(inspectCommand, strings.Replace(fixture.request, "#git:sha1:", "#git:sha1:0", 1))
	if err == nil {
		t.Fatalf("an unknown event was answered instead of refused:\n%s", printed)
	}
	if strings.TrimSpace(printed) != "" {
		t.Fatalf("a refused inspection printed a page anyway:\n%s", printed)
	}
	if !strings.Contains(err.Error(), "not in the durable projection") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

func TestInspectJSONIsTheSharedInspection(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(inspectCommand, "--json", fixture.artifact)
	if err != nil {
		t.Fatal(err)
	}
	var inspection statusview.ItemInspection
	decoder := json.NewDecoder(strings.NewReader(printed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inspection); err != nil {
		t.Fatalf("the CLI printed something that is not an inspection: %v\n%s", err, printed)
	}
	if inspection.Event != fixture.artifact || inspection.Statement == nil {
		t.Fatalf("the inspection does not describe the event asked for: %+v", inspection)
	}
	if !contains(inspection.ProvenanceBases, fixture.request) {
		t.Fatalf("the inspection lost the basis the artifact rests on: %v", inspection.ProvenanceBases)
	}
}

func TestReviewsRefusesWhenTheQueueIsNotQuiet(t *testing.T) {
	fixture := newQueryFixture(t)
	if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review the changelog head",
		Body:    map[string]string{"to": "@worker", "conditions": "approve or request changes", "artifact": fixture.artifact},
		RestsOn: []string{fixture.artifact}, IdempotencyKey: "query-review-request",
	}); err != nil {
		t.Fatal(err)
	}
	printed, _, err := fixture.run(reviewsCommand, "--branch", "main")
	if err == nil {
		t.Fatalf("a queue with an unanswered review request called itself quiet:\n%s", printed)
	}
	if !strings.Contains(err.Error(), "await a first verdict") {
		t.Fatalf("the refusal does not say what is outstanding: %v", err)
	}
	// The report is still printed. A gate that refuses without saying what is
	// outstanding sends its operator back to jq.
	if !strings.Contains(printed, "1 awaiting a first verdict") {
		t.Fatalf("the gate refused without reporting:\n%s", printed)
	}
	if !strings.Contains(printed, "Approved heads: 0 in main") {
		t.Fatalf("the gate did not report the branch question at all:\n%s", printed)
	}
}

func TestReviewsIsQuietWithNothingOutstanding(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(reviewsCommand, "--branch", "main", "--json")
	if err != nil {
		t.Fatalf("a queue with no review request at all refused: %v\n%s", err, printed)
	}
	var gate struct {
		statusview.ReviewGate
		Branch string `json:"branch"`
		Quiet  bool   `json:"quiet"`
	}
	if err := json.Unmarshal([]byte(printed), &gate); err != nil {
		t.Fatal(err)
	}
	if !gate.Quiet || gate.Named != 0 || gate.Branch != "main" {
		t.Fatalf("an empty queue did not report itself quiet: %+v", gate)
	}
}

// Git answers three ways about one commit, not two. A non-zero status means
// either "not an ancestor" or "the check never ran", and reading the second as
// the first states a fact nobody measured.
func TestApprovedHeadsAreSplitThreeWaysNotTwo(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "first")
	landed := testGit(t, repo, "rev-parse", "HEAD")
	testGit(t, repo, "checkout", "-qb", "side")
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "unmerged")
	unmerged := testGit(t, repo, "rev-parse", "HEAD")
	absent := strings.Repeat("0", len(landed))

	landing, err := classifyHeads(ctx, repo, "main", []string{landed, unmerged, absent}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(landing.in) != 1 || landing.in[0] != landed {
		t.Fatalf("the landed head was not reported as in the branch: %+v", landing)
	}
	if len(landing.out) != 1 || landing.out[0] != unmerged {
		t.Fatalf("the unmerged head was not reported as out of the branch: %+v", landing)
	}
	if len(landing.unknown) != 1 || landing.unknown[0] != absent {
		t.Fatalf("a commit this clone does not hold was reported as a measured answer: %+v", landing)
	}
	if landing.refusal(statusview.ReviewGate{}, "main") == nil {
		t.Fatal("a head out of the branch did not stop the gate")
	}

	// Exercise the whole projection-to-Git path with more distinct actionable
	// approvals than one display page can hold.
	unmergedHeads := []string{unmerged}
	for index := 1; index < 6; index++ {
		testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", fmt.Sprintf("unmerged-%02d", index))
		unmergedHeads = append(unmergedHeads, testGit(t, repo, "rev-parse", "HEAD"))
	}
	absentHeads := []string{absent}
	for index := 1; index < 6; index++ {
		absentHeads = append(absentHeads, fmt.Sprintf("%040x", index))
	}
	testGit(t, repo, "checkout", "-q", "main")
	projection := workroom.Projection{}
	for index := 0; index < 64; index++ {
		testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", fmt.Sprintf("landed-%02d", index))
		head := testGit(t, repo, "rev-parse", "HEAD")
		projection.Reviews = append(projection.Reviews, workroom.Review{
			Report: fmt.Sprintf("report-landed-%02d", index), Verdict: "approved", Ratified: true, Head: head,
		})
	}
	for index, head := range unmergedHeads {
		projection.Reviews = append(projection.Reviews, workroom.Review{
			Report: fmt.Sprintf("report-unmerged-%02d", index), Verdict: "approved", Ratified: true, Head: head,
		})
	}
	for index, head := range absentHeads {
		projection.Reviews = append(projection.Reviews, workroom.Review{
			Report: fmt.Sprintf("report-absent-%02d", index), Verdict: "approved", Ratified: true, Head: head,
		})
	}
	gate, err := statusview.BuildReviewGate(app.Snapshot{Genesis: "g", Head: "h", Projection: projection}, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	heads := statusview.ActionableApprovedHeads(projection)
	if len(heads) != 76 {
		t.Fatalf("actionable head projection returned %d distinct heads, want 76", len(heads))
	}
	bounded, err := classifyHeads(ctx, repo, "main", heads, 5)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Approved != 76 || len(gate.ApprovedHeads) != 5 || gate.ApprovedHeadsOmitted != 71 || !gate.Quiet() {
		t.Fatalf("bounded projection gate lost its honest totals: %+v", gate)
	}
	if bounded.inTotal != 64 || len(bounded.in) != 5 || bounded.outTotal != 6 || len(bounded.out) != 5 || bounded.unknownTotal != 6 || len(bounded.unknown) != 5 || bounded.outOmitted != 1 || bounded.unknownOmitted != 1 {
		t.Fatalf("bounded full classification lost heads: %+v", bounded)
	}
	if rendered := renderReviewGate(gate, "main", "test", bounded); !strings.Contains(rendered, "64 in main, 6 not in main, 6 unknown") || !strings.Contains(rendered, "1 additional out-of-branch heads omitted") || !strings.Contains(rendered, "1 additional unknown heads omitted") {
		t.Fatalf("bounded rendered totals or omissions hid classified heads:\n%s", rendered)
	}
	if bounded.refusal(statusview.ReviewGate{}, "main") == nil {
		t.Fatal("unknown and unlanded heads did not refuse after a >50-head classification")
	}
	landedProjection := workroom.Projection{Reviews: projection.Reviews[:64]}
	landedGate, err := statusview.BuildReviewGate(app.Snapshot{Genesis: "g", Head: "h", Projection: landedProjection}, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	allLanded, err := classifyHeads(ctx, repo, "main", statusview.ActionableApprovedHeads(landedProjection), 5)
	if err != nil {
		t.Fatal(err)
	}
	if landedGate.ApprovedHeadsOmitted != 59 || !landedGate.Quiet() || allLanded.refusal(landedGate, "main") != nil {
		t.Fatalf("all 64 classified landed heads could not clear the gate: %+v", allLanded)
	}
	if rendered := renderReviewGate(landedGate, "main", "test", allLanded); !strings.Contains(rendered, "64 in main, 0 not in main, 0 unknown") {
		t.Fatalf("landed omitted heads did not render quiet totals:\n%s", rendered)
	}
}

// Asking about a branch Git cannot resolve would report every head as out of
// it. That is a confident wrong answer, and a refusal is the only honest one.
func TestAnUnresolvableBranchIsRefusedRatherThanAnswered(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "first")
	head := testGit(t, repo, "rev-parse", "HEAD")
	if _, err := classifyHeads(ctx, repo, "no-such-branch", []string{head}, 50); err == nil {
		t.Fatal("ancestry was reported against a branch that does not resolve")
	}
}

// A fallback answer must never be presented as a resident one, and an ordinary
// local read must not be presented as a fallback from a resident nobody asked.
// Three states, three words.
func TestTheFrontierLineNamesWhereTheAnswerCameFrom(t *testing.T) {
	for _, test := range []struct {
		asked, answered bool
		want            string
	}{
		{false, false, "verified local"},
		{true, true, "resident"},
		{true, false, "verified local fallback"},
	} {
		if got := querySource(test.asked, test.answered); got != test.want {
			t.Fatalf("asked=%v answered=%v named %q, want %q", test.asked, test.answered, got, test.want)
		}
	}
	rendered := renderArtifactPage(statusview.ArtifactPage{}, querySource(true, false))
	if !strings.Contains(rendered, "(verified local fallback)") {
		t.Fatalf("the rendered header does not carry the source:\n%s", rendered)
	}
}

func TestTheHumanViewOfALocalAnswerSaysSo(t *testing.T) {
	fixture := newQueryFixture(t)
	printed, _, err := fixture.run(artifactsCommand, "--path", "CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed, "(verified local)") {
		t.Fatalf("a local read did not name itself:\n%s", printed)
	}
}

// The obvious implementation runs one `merge-base --is-ancestor` per approved
// head, so an actor filing approvals makes this command arbitrarily expensive
// without anything in the repository changing. Capping the head list would
// bound the work and break the gate: a head omitted from the list decides
// nothing while the caller reads "quiet", and a gate that can declare quiet
// with work outstanding is the one failure an irreversible step cannot have.
// So the list stays complete and the process count stops tracking it.
//
// Seconds are the wrong measurement — a loaded machine can make any duration
// look like any other. This counts the subprocesses, which is the claim.
func TestAncestryCostsTheSameWhateverTheNumberOfHeads(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	// Repository-local, so it binds this fixture and leaks into no other test.
	// Sixty-four commits then a cleanup that must find .git quiet: any
	// maintenance Git decides to start on its own outlives the test body and
	// writes into .git while TempDir is removing it, which is what CI reported
	// as "unlinkat .../repo/.git: directory not empty". The fixture asks for
	// exactly two Git processes, so it should also be the thing that says no
	// third one may appear.
	testGit(t, repo, "config", "maintenance.auto", "false")
	testGit(t, repo, "config", "gc.auto", "0")
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "first")
	landed := []string{testGit(t, repo, "rev-parse", "HEAD")}
	for index := 1; index < 64; index++ {
		testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", fmt.Sprintf("landed-%02d", index))
		landed = append(landed, testGit(t, repo, "rev-parse", "HEAD"))
	}

	shim := t.TempDir()
	tally := filepath.Join(shim, "calls")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH to shim")
	}
	script := "#!/bin/sh\nprintf x >> " + tally + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	count := func(heads []string) int {
		if err := os.WriteFile(tally, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := classifyHeads(ctx, repo, "main", heads, 50); err != nil {
			t.Fatal(err)
		}
		calls, err := os.ReadFile(tally)
		if err != nil {
			t.Fatal(err)
		}
		return len(calls)
	}

	few, many := count(landed[:2]), count(landed)
	if few != 2 || many != 2 {
		t.Fatalf("ancestry ran %d git processes for 2 heads and %d for 64, want exactly 2 for each", few, many)
	}
}

// The four documented lifecycle states have to survive into the output. The
// filter distinguished succeeded from retired and the row did not carry the
// difference, so --state succeeded returned the right artifacts and reported
// every one of them as merely retired.
func TestASucceededArtifactDoesNotReportAsMerelyRetired(t *testing.T) {
	succeeded := statusview.ArtifactRow{Retired: true, Succeeded: true}
	retired := statusview.ArtifactRow{Retired: true}
	if artifactRowState(succeeded) == artifactRowState(retired) {
		t.Fatalf("a succeeded artifact renders as %q, the same as a retired one: the successor is where the behaviour went, and a reader cannot tell", artifactRowState(succeeded))
	}
	if got := artifactRowState(succeeded); got != "succeeded" {
		t.Fatalf("succeeded artifact rendered as %q", got)
	}
	// A remote live-only row carries only Retired=false. If an older local row
	// carries Retired=true without Succeeded, it still means withdrawal.
	if got := artifactRowState(statusview.ArtifactRow{Retired: true}); got != "retired" {
		t.Fatalf("a row without a lifecycle rendered as %q, not retired", got)
	}
}
