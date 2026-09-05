package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Request authoring at the command line. Every case here drives the real
// stateCommand or reassignIfUnclaimedCommand against a real repository,
// because what is being proved is that the surface a person types into states
// the request's result, resolves its head from the repository, and refuses
// before anything is appended when it cannot.

type authoringFixture struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	workspace *app.Workspace
	seed      string
	ref       string
}

func newAuthoringFixture(t *testing.T) authoringFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	return authoringFixture{t: t, ctx: ctx, repo: repo, workspace: workspace, seed: seed.ID, ref: "refs/heads/main"}
}

// file runs gs state --kind request with the body the case states. It answers
// with the event the request now occupies, found by its own text rather than by
// reading what the command printed, so nothing here depends on capturing a
// shared file descriptor other tests also write to.
func (f authoringFixture) file(key, text string, body map[string]string, bases ...string) (string, error) {
	f.t.Helper()
	if len(bases) == 0 {
		bases = []string{f.seed}
	}
	arguments := []string{"--repo", f.repo, "--as", "operator", "--kind", "request",
		"--text", text, "--idempotency-key", key}
	for _, basis := range bases {
		arguments = append(arguments, "--rests-on", basis)
	}
	for field, value := range body {
		arguments = append(arguments, "--body", field+"="+value)
	}
	if err := stateCommand(f.ctx, arguments); err != nil {
		return "", err
	}
	return f.eventOf(text), nil
}

// eventOf finds the one statement carrying this exact text.
func (f authoringFixture) eventOf(text string) string {
	f.t.Helper()
	found := ""
	for _, statement := range f.statements() {
		if statement.Text == text {
			if found != "" {
				f.t.Fatalf("two statements say %q", text)
			}
			found = statement.Event
		}
	}
	if found == "" {
		f.t.Fatalf("no statement says %q", text)
	}
	return found
}

func (f authoringFixture) statements() []workroom.Statement {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot.Projection.Statements
}

func (f authoringFixture) head() string {
	f.t.Helper()
	return strings.TrimSpace(testGit(f.t, f.repo, "rev-parse", f.ref))
}

func (f authoringFixture) frontier() string {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot.Head
}

// schemaOf reads the schema the event was actually signed under, from the
// verified log rather than from anything a command printed.
func (f authoringFixture) schemaOf(event string) string {
	f.t.Helper()
	loaded, err := kernel.NewReader(f.workspace.Store).Load(f.ctx, f.workspace.View().Genesis)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, stored := range loaded.Events {
		if f.workspace.EventID(stored.Commit) == event {
			return stored.Intent.Schema
		}
	}
	f.t.Fatalf("no stored event for %s", event)
	return ""
}

func (f authoringFixture) commitment(event string) workroom.Commitment {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, row := range snapshot.Projection.Commitments {
		if row.Request == event {
			return row
		}
	}
	f.t.Fatalf("no commitment row for %s", event)
	return workroom.Commitment{}
}

func (f authoringFixture) body(event string) map[string]string {
	f.t.Helper()
	for _, statement := range f.statements() {
		if statement.Event == event {
			return statement.Body
		}
	}
	f.t.Fatalf("no statement for %s", event)
	return nil
}

func (f authoringFixture) genesisID() string {
	f.t.Helper()
	view := f.workspace.View()
	return "git:" + view.ObjectFormat + ":" + view.Genesis
}

func TestCLIRequestStatesItsResult(t *testing.T) {
	fixture := newAuthoringFixture(t)

	t.Run("by value resolves the target head from the ref", func(t *testing.T) {
		event, err := fixture.file("by-value", "land it", map[string]string{
			"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main"})
		if err != nil {
			t.Fatalf("filing a landing request: %v", err)
		}
		if schema := fixture.schemaOf(event); schema != workroom.SchemaStateV3 {
			t.Fatalf("stored schema = %q, want %q", schema, workroom.SchemaStateV3)
		}
		body := fixture.body(event)
		if body["target_head"] != fixture.head() {
			t.Fatalf("target_head = %q, want the ref's head %q", body["target_head"], fixture.head())
		}
		if body["target_repo"] != fixture.genesisID() {
			t.Fatalf("target_repo = %q, want %q", body["target_repo"], fixture.genesisID())
		}
		row := fixture.commitment(event)
		if row.TargetRef != "refs/heads/main" || row.Legacy || row.TargetRepo != body["target_repo"] {
			t.Fatalf("folded result = %+v", row)
		}
	})

	t.Run("no_git_artifact owes no landing", func(t *testing.T) {
		event, err := fixture.file("no-artifact", "review it", map[string]string{
			"to": "@agent", "conditions": "it is reviewed", "no_git_artifact": "true"})
		if err != nil {
			t.Fatalf("filing a no-artifact request: %v", err)
		}
		if schema := fixture.schemaOf(event); schema != workroom.SchemaStateV3 {
			t.Fatalf("stored schema = %q", schema)
		}
		if row := fixture.commitment(event); row.TargetRef != "" || row.Legacy {
			t.Fatalf("folded result = %+v, want no landing", row)
		}
	})

	t.Run("inherit takes the parent's destination", func(t *testing.T) {
		parent, err := fixture.file("inherit-parent", "parent lands", map[string]string{
			"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main"})
		if err != nil {
			t.Fatal(err)
		}
		child, err := fixture.file("inherit-child", "child inherits", map[string]string{
			"to": "@agent", "conditions": "it lands too", "target": "inherit"}, parent)
		if err != nil {
			t.Fatalf("filing an inheriting request: %v", err)
		}
		row := fixture.commitment(child)
		if row.TargetRef != "refs/heads/main" || row.Legacy {
			t.Fatalf("inherited result = %+v", row)
		}
		if fixture.body(child)["target_head"] != "" {
			t.Fatalf("an inheriting request stated a triple: %+v", fixture.body(child))
		}
	})

	t.Run("held names its owner", func(t *testing.T) {
		event, err := fixture.file("held", "land it under a hold", map[string]string{
			"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main",
			"landing": "held", "hold_owner": "@agent"})
		if err != nil {
			t.Fatalf("filing a held request: %v", err)
		}
		agent, err := fixture.workspace.ResolveActor("agent")
		if err != nil {
			t.Fatal(err)
		}
		if row := fixture.commitment(event); row.HoldOwner != agent.Fingerprint {
			t.Fatalf("hold owner = %q, want %q", row.HoldOwner, agent.Fingerprint)
		}
	})
}

func TestCLIRequestRefusesBeforeAnyAppend(t *testing.T) {
	fixture := newAuthoringFixture(t)
	for _, testCase := range []struct {
		name string
		body map[string]string
		want string
	}{
		{"no choice", map[string]string{"to": "@agent", "conditions": "do it"},
			"request states no result"},
		{"mixed choice", map[string]string{"to": "@agent", "conditions": "do it",
			"target_ref": "refs/heads/main", "no_git_artifact": "true"},
			"request states more than one result"},
		{"mixed inherit", map[string]string{"to": "@agent", "conditions": "do it",
			"target": "inherit", "no_git_artifact": "true"},
			"request states more than one result"},
		{"foreign target_repo", map[string]string{"to": "@agent", "conditions": "do it",
			"target_repo": "git:sha1:" + strings.Repeat("f", 40), "target_ref": "refs/heads/main"},
			"target_repo is resolved at filing and cannot be supplied"},
		{"own target_repo is still not caller input", map[string]string{"to": "@agent",
			"conditions": "do it", "target_repo": fixture.genesisID(), "target_ref": "refs/heads/main"},
			"target_repo is resolved at filing and cannot be supplied"},
		{"caller-supplied target_head", map[string]string{"to": "@agent", "conditions": "do it",
			"target_ref": "refs/heads/main", "target_head": strings.Repeat("a", 40)},
			"target_head is resolved at filing and cannot be supplied"},
		{"non-branch ref", map[string]string{"to": "@agent", "conditions": "do it",
			"target_ref": "refs/tags/v1"},
			"target_ref must name a branch under refs/heads/"},
		{"missing ref", map[string]string{"to": "@agent", "conditions": "do it",
			"target_ref": "refs/heads/nowhere"},
			"does not resolve in"},
		{"target is not a word the fold takes", map[string]string{"to": "@agent", "conditions": "do it",
			"target": "main"},
			`target must be "inherit" when stated`},
		{"hold without a landing", map[string]string{"to": "@agent", "conditions": "do it",
			"no_git_artifact": "true", "landing": "held"},
			"landing hold applies only to a request that owes a landing"},
		{"hold owner without a hold", map[string]string{"to": "@agent", "conditions": "do it",
			"target_ref": "refs/heads/main", "hold_owner": "@agent"},
			"hold_owner applies only to a held landing request"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			before := fixture.frontier()
			event, err := fixture.file("refuse-"+testCase.name, "refused: "+testCase.name, testCase.body)
			if err == nil {
				t.Fatalf("%s was filed as %s", testCase.name, event)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("refusal %q does not name %q", err, testCase.want)
			}
			if after := fixture.frontier(); after != before {
				t.Fatalf("the frontier moved from %s to %s on a refused request", before, after)
			}
		})
	}
}

// The measurement is taken per filing. A ref that moves changes what the next
// request records and changes nothing about one already accepted, which is the
// difference between a fresh act and a replay.
func TestCLIRequestMeasuresTheRefAtEachFiling(t *testing.T) {
	fixture := newAuthoringFixture(t)
	body := map[string]string{"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main"}
	first, err := fixture.file("movement-first", "land the first", body)
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.head()
	if got := fixture.body(first)["target_head"]; got != before {
		t.Fatalf("first target_head = %q, want %q", got, before)
	}

	testGit(t, fixture.repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "the ref moves")
	moved := fixture.head()
	if moved == before {
		t.Fatal("the ref did not move")
	}

	frontier := fixture.frontier()
	replay, err := fixture.file("movement-first", "land the first", body)
	if err != nil {
		t.Fatalf("exact retry after the ref moved: %v", err)
	}
	if replay != first {
		t.Fatalf("exact retry returned %s, want the original %s", replay, first)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("an exact retry appended: frontier %s to %s", frontier, after)
	}
	if got := fixture.body(first)["target_head"]; got != before {
		t.Fatalf("a replay re-measured the ref: %q", got)
	}

	fresh, err := fixture.file("movement-second", "land the second", body)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixture.body(fresh)["target_head"]; got != moved {
		t.Fatalf("a fresh filing recorded %q, want the current head %q", got, moved)
	}
}

// A request already in the log under state@2 keeps its bytes and its reading,
// and an exact retry of it replays that act rather than re-signing it under the
// new schema.
func TestCLIRequestReplaysAnExistingLegacyAct(t *testing.T) {
	fixture := newAuthoringFixture(t)
	agent, err := fixture.workspace.ResolveActor("agent")
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := fixture.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := workroom.Encode(workroom.State{Kind: workroom.KindRequest, Text: "legacy request",
		Body: map[string]string{"to": agent.Fingerprint, "conditions": "the old way"}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fixture.workspace.Store.WritePayloadTree(fixture.ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := fixture.workspace.View()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        []string{fixture.seed},
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: "legacy-request",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	request := kernel.Request{Signed: signed, Payload: payload}
	first, err := kernel.Submit(fixture.ctx, fixture.workspace.Store, request,
		kernel.Options{SigningKey: view.SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	event := fixture.workspace.EventID(first.Head)
	if schema := fixture.schemaOf(event); schema != workroom.SchemaState {
		t.Fatalf("the legacy act was stored as %q", schema)
	}
	if body := fixture.body(event); body["target_ref"] != "" || len(body) != 2 {
		t.Fatalf("the legacy body was rewritten: %+v", body)
	}

	frontier := fixture.frontier()
	second, err := kernel.Submit(fixture.ctx, fixture.workspace.Store, request,
		kernel.Options{SigningKey: view.SequencerKey})
	if err != nil {
		t.Fatalf("exact retry of the legacy act: %v", err)
	}
	if fixture.workspace.EventID(second.Head) != event {
		t.Fatalf("the legacy retry returned %s, want %s", fixture.workspace.EventID(second.Head), event)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("the legacy retry appended: %s to %s", frontier, after)
	}
	if schema := fixture.schemaOf(event); schema != workroom.SchemaState {
		t.Fatalf("the replayed act reads as %q; a retry re-signed history", schema)
	}
}

// A guarded replacement is a new request and states its own result. It is filed
// under reassign-if-unclaimed@1, which is what makes the fold read that
// statement rather than treat it as body text.
func TestCLIReassignmentStatesTheReplacementResult(t *testing.T) {
	t.Run("a replacement stating no result never reaches the log", func(t *testing.T) {
		fixture := newAuthoringFixture(t)
		if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "second", "agent"); err != nil {
			t.Fatal(err)
		}
		old, err := fixture.file("reassign-old", "the old request", map[string]string{
			"to": "@agent", "conditions": "do it", "no_git_artifact": "true"})
		if err != nil {
			t.Fatal(err)
		}
		err = reassignIfUnclaimedCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--to", "@second",
			"--text", "ask again", "--conditions", "do it",
			"--idempotency-key", "reassign-no-choice", old,
		})
		if err == nil || !strings.Contains(err.Error(), "request states no result") {
			t.Fatalf("a replacement stating no result = %v", err)
		}
		for _, row := range fixture.statements() {
			if row.Kind == workroom.KindRequest && row.Text == "ask again" {
				t.Fatalf("a replacement with no stated result reached the log: %+v", row)
			}
		}
	})

	t.Run("a stated result is read by the fold", func(t *testing.T) {
		fixture := newAuthoringFixture(t)
		if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "second", "agent"); err != nil {
			t.Fatal(err)
		}
		old, err := fixture.file("reassign-old", "the old request", map[string]string{
			"to": "@agent", "conditions": "do it", "no_git_artifact": "true"})
		if err != nil {
			t.Fatal(err)
		}
		if err := reassignIfUnclaimedCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--to", "@second",
			"--text", "ask again", "--conditions", "do it",
			"--body", "target_ref=refs/heads/main",
			"--idempotency-key", "reassign-landing", old,
		}); err != nil {
			t.Fatalf("reassigning with a stated result: %v", err)
		}
		replacement := fixture.eventOf("ask again")
		if schema := fixture.schemaOf(replacement); schema != workroom.SchemaReassignRequestV1 {
			t.Fatalf("replacement schema = %q, want %q", schema, workroom.SchemaReassignRequestV1)
		}
		body := fixture.body(replacement)
		if body["target_head"] != fixture.head() || body["target_repo"] != fixture.genesisID() {
			t.Fatalf("replacement triple = %+v", body)
		}
		if row := fixture.commitment(replacement); row.TargetRef != "refs/heads/main" || row.Legacy {
			t.Fatalf("the fold read the replacement as %+v", row)
		}
	})
}

// An accepted act is recovered from the log before any ref is read, so a
// branch that is gone by the time the caller retries cannot stand between them
// and the act they already have. A fresh filing against that same absent ref
// still has nothing to measure and is still refused.
func TestCLIAcceptedRequestIsRecoveredBeforeAnyRefIsRead(t *testing.T) {
	fixture := newAuthoringFixture(t)
	testGit(t, fixture.repo, "branch", "side")
	body := map[string]string{"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/side"}
	first, err := fixture.file("cli-vanishing", "land it on the side", body)
	if err != nil {
		t.Fatalf("filing against refs/heads/side: %v", err)
	}
	measured := fixture.body(first)["target_head"]
	if measured == "" {
		t.Fatalf("the accepted request measured nothing: %+v", fixture.body(first))
	}

	testGit(t, fixture.repo, "branch", "-D", "side")
	frontier := fixture.frontier()
	replay, err := fixture.file("cli-vanishing", "land it on the side", body)
	if err != nil {
		t.Fatalf("identical retry after the branch was deleted: %v", err)
	}
	if replay != first {
		t.Fatalf("the retry returned %s, want the original %s", replay, first)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("the retry appended: frontier %s to %s", frontier, after)
	}
	if got := fixture.body(first)["target_head"]; got != measured {
		t.Fatalf("the replayed request now measures %q, want %q", got, measured)
	}

	event, err := fixture.file("cli-vanishing-fresh", "land it on the side again", body)
	if err == nil {
		t.Fatalf("a fresh filing against an absent ref was accepted as %s", event)
	}
	if !strings.Contains(err.Error(), "does not resolve in") {
		t.Fatalf("refusal %q does not name the unresolvable ref", err)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused fresh filing appended: frontier %s to %s", frontier, after)
	}
}

// The destination is the caller's own intent, never a measurement recovered
// from an earlier act. A reused key naming a different branch is a different
// request, and the answer is the kernel's refusal, not the old request.
func TestCLIReusedKeyWithADifferentDestinationIsRefused(t *testing.T) {
	fixture := newAuthoringFixture(t)
	first, err := fixture.file("cli-retarget", "land it", map[string]string{
		"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main"})
	if err != nil {
		t.Fatal(err)
	}
	// The other branch holds exactly what main holds, so the only thing that
	// differs between the two filings is the destination the caller chose.
	testGit(t, fixture.repo, "branch", "other")

	frontier := fixture.frontier()
	event, err := fixture.file("cli-retarget", "land it", map[string]string{
		"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/other"})
	if err == nil {
		t.Fatalf("a reused key naming refs/heads/other was accepted as %s", event)
	}
	if !strings.Contains(err.Error(), "idempotency key reused with different intent") {
		t.Fatalf("refusal %q is not the reused-key refusal", err)
	}
	if strings.Contains(err.Error(), first) {
		t.Fatalf("the refusal names the old request %s: %v", first, err)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused retarget appended: frontier %s to %s", frontier, after)
	}
	if got := fixture.body(first)["target_ref"]; got != "refs/heads/main" {
		t.Fatalf("the accepted request now names %q", got)
	}
}

// The guarded replacement is authored on the same path and answers the same
// way: an exact retry replays after the branch is gone, and a reused key that
// retargets the replacement is refused.
func TestCLIReassignmentRecoversAnAcceptedReplacement(t *testing.T) {
	fixture := newAuthoringFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "second", "agent"); err != nil {
		t.Fatal(err)
	}
	old, err := fixture.file("reassign-old", "the old request", map[string]string{
		"to": "@agent", "conditions": "do it", "no_git_artifact": "true"})
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "branch", "side")
	reassign := func(key, ref string) error {
		return reassignIfUnclaimedCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--to", "@second",
			"--text", "ask again", "--conditions", "do it",
			"--body", "target_ref=" + ref, "--idempotency-key", key, old,
		})
	}
	if err := reassign("cli-reassign-recover", "refs/heads/side"); err != nil {
		t.Fatalf("reassigning onto refs/heads/side: %v", err)
	}
	replacement := fixture.eventOf("ask again")

	testGit(t, fixture.repo, "branch", "-D", "side")
	frontier := fixture.frontier()
	if err := reassign("cli-reassign-recover", "refs/heads/side"); err != nil {
		t.Fatalf("identical reassignment retry after the branch was deleted: %v", err)
	}
	if again := fixture.eventOf("ask again"); again != replacement {
		t.Fatalf("the retry returned %s, want the original %s", again, replacement)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("the reassignment retry appended: frontier %s to %s", frontier, after)
	}

	err = reassign("cli-reassign-recover", "refs/heads/main")
	if err == nil {
		t.Fatal("a reused reassignment key naming a different branch was accepted")
	}
	if !strings.Contains(err.Error(), "idempotency key reused with different intent") {
		t.Fatalf("refusal %q is not the reused-key refusal", err)
	}
	if strings.Contains(err.Error(), replacement) {
		t.Fatalf("the refusal names the accepted replacement %s: %v", replacement, err)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused retarget appended: frontier %s to %s", frontier, after)
	}
}
