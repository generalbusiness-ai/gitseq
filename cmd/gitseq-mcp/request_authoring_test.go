package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Request authoring through the MCP adapter. Every case drives the real tool
// call, because an agent's requests reach the log through this surface and
// nowhere else.

type mcpAuthoring struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	workspace *app.Workspace
	adapter   *mcpServer
	seed      string
}

func newMCPAuthoring(t *testing.T) mcpAuthoring {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	mcpAuthoringGit(t, "", "init", "-q", "-b", "main", repo)
	mcpAuthoringGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "seed")
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	return mcpAuthoring{t: t, ctx: ctx, repo: repo, workspace: workspace,
		adapter: newServer("human", repo), seed: seed.ID}
}

func mcpAuthoringGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	full := arguments
	if repo != "" {
		full = append([]string{"-C", repo}, arguments...)
	}
	output, err := exec.Command("git", full...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
}

func (m mcpAuthoring) file(key, text string, body map[string]string, bases ...string) (string, error) {
	m.t.Helper()
	if len(bases) == 0 {
		bases = []string{m.seed}
	}
	restsOn := make([]any, 0, len(bases))
	for _, basis := range bases {
		restsOn = append(restsOn, basis)
	}
	fields := make(map[string]any, len(body))
	for field, value := range body {
		fields[field] = value
	}
	value, _, err := m.adapter.call(m.ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "request", "text": text, "body": fields, "rests_on": restsOn, "idempotency_key": key,
	}})
	if err != nil {
		return "", err
	}
	record, ok := submissionRecord(value)
	if !ok {
		m.t.Fatalf("state returned no durable record: %#v", value)
	}
	return record.ID, nil
}

func (m mcpAuthoring) head() string {
	m.t.Helper()
	return mcpAuthoringGit(m.t, m.repo, "rev-parse", "refs/heads/main")
}

func (m mcpAuthoring) frontier() string {
	m.t.Helper()
	snapshot, err := m.workspace.Snapshot(m.ctx)
	if err != nil {
		m.t.Fatal(err)
	}
	return snapshot.Head
}

func (m mcpAuthoring) schemaOf(event string) string {
	m.t.Helper()
	loaded, err := kernel.NewReader(m.workspace.Store).Load(m.ctx, m.workspace.View().Genesis)
	if err != nil {
		m.t.Fatal(err)
	}
	for _, stored := range loaded.Events {
		if m.workspace.EventID(stored.Commit) == event {
			return stored.Intent.Schema
		}
	}
	m.t.Fatalf("no stored event for %s", event)
	return ""
}

func (m mcpAuthoring) body(event string) map[string]string {
	m.t.Helper()
	snapshot, err := m.workspace.Snapshot(m.ctx)
	if err != nil {
		m.t.Fatal(err)
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == event {
			return statement.Body
		}
	}
	m.t.Fatalf("no statement for %s", event)
	return nil
}

func (m mcpAuthoring) commitment(event string) workroom.Commitment {
	m.t.Helper()
	snapshot, err := m.workspace.Snapshot(m.ctx)
	if err != nil {
		m.t.Fatal(err)
	}
	for _, row := range snapshot.Projection.Commitments {
		if row.Request == event {
			return row
		}
	}
	m.t.Fatalf("no commitment row for %s", event)
	return workroom.Commitment{}
}

func (m mcpAuthoring) genesisID() string {
	view := m.workspace.View()
	return "git:" + view.ObjectFormat + ":" + view.Genesis
}

func TestMCPRequestStatesItsResult(t *testing.T) {
	parallelTest(t)
	fixture := newMCPAuthoring(t)

	landing, err := fixture.file("mcp-by-value", "land it", map[string]string{
		"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main"})
	if err != nil {
		t.Fatalf("filing a landing request: %v", err)
	}
	if schema := fixture.schemaOf(landing); schema != workroom.SchemaStateV3 {
		t.Fatalf("stored schema = %q, want %q", schema, workroom.SchemaStateV3)
	}
	body := fixture.body(landing)
	if body["target_head"] != fixture.head() || body["target_repo"] != fixture.genesisID() {
		t.Fatalf("resolved triple = %+v, ref head %s", body, fixture.head())
	}
	if row := fixture.commitment(landing); row.TargetRef != "refs/heads/main" || row.Legacy {
		t.Fatalf("folded result = %+v", row)
	}

	child, err := fixture.file("mcp-inherit", "child inherits", map[string]string{
		"to": "@agent", "conditions": "it lands too", "target": "inherit"}, landing)
	if err != nil {
		t.Fatalf("filing an inheriting request: %v", err)
	}
	if row := fixture.commitment(child); row.TargetRef != "refs/heads/main" {
		t.Fatalf("inherited result = %+v", row)
	}

	review, err := fixture.file("mcp-no-artifact", "review it", map[string]string{
		"to": "@agent", "conditions": "it is reviewed", "no_git_artifact": "true"})
	if err != nil {
		t.Fatalf("filing a no-artifact request: %v", err)
	}
	if row := fixture.commitment(review); row.TargetRef != "" || row.Legacy {
		t.Fatalf("a no-artifact request owes %+v", row)
	}

	held, err := fixture.file("mcp-held", "land it under a hold", map[string]string{
		"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main",
		"landing": "held", "hold_owner": "@agent"})
	if err != nil {
		t.Fatalf("filing a held request: %v", err)
	}
	agent, err := fixture.workspace.ResolveActor("agent")
	if err != nil {
		t.Fatal(err)
	}
	if row := fixture.commitment(held); row.HoldOwner != agent.Fingerprint {
		t.Fatalf("hold owner = %q, want %q", row.HoldOwner, agent.Fingerprint)
	}
}

func TestMCPRequestRefusesBeforeAnyAppend(t *testing.T) {
	parallelTest(t)
	fixture := newMCPAuthoring(t)
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
		{"foreign target_repo", map[string]string{"to": "@agent", "conditions": "do it",
			"target_repo": "git:sha1:" + strings.Repeat("f", 40), "target_ref": "refs/heads/main"},
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
	} {
		t.Run(testCase.name, func(t *testing.T) {
			before := fixture.frontier()
			event, err := fixture.file("mcp-refuse-"+testCase.name, "refused: "+testCase.name, testCase.body)
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

func TestMCPRequestMeasuresTheRefAtEachFiling(t *testing.T) {
	parallelTest(t)
	fixture := newMCPAuthoring(t)
	body := map[string]string{"to": "@agent", "conditions": "it lands", "target_ref": "refs/heads/main"}
	first, err := fixture.file("mcp-movement", "land the first", body)
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.head()

	mcpAuthoringGit(t, fixture.repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "the ref moves")
	moved := fixture.head()
	if moved == before {
		t.Fatal("the ref did not move")
	}

	frontier := fixture.frontier()
	replay, err := fixture.file("mcp-movement", "land the first", body)
	if err != nil {
		t.Fatalf("exact retry after the ref moved: %v", err)
	}
	if replay != first {
		t.Fatalf("exact retry returned %s, want the original %s", replay, first)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("an exact retry appended: %s to %s", frontier, after)
	}
	if got := fixture.body(first)["target_head"]; got != before {
		t.Fatalf("a replay re-measured the ref: %q", got)
	}

	fresh, err := fixture.file("mcp-movement-second", "land the second", body)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixture.body(fresh)["target_head"]; got != moved {
		t.Fatalf("a fresh filing recorded %q, want the current head %q", got, moved)
	}
}

// The guarded replacement reaches the same authoring path through its own tool,
// and states its result in the body argument this adapter passes through.
func TestMCPReassignmentStatesTheReplacementResult(t *testing.T) {
	parallelTest(t)
	fixture := newMCPAuthoring(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "human", "second", "agent"); err != nil {
		t.Fatal(err)
	}
	old, err := fixture.file("mcp-reassign-old", "the old request", map[string]string{
		"to": "@agent", "conditions": "do it", "no_git_artifact": "true"})
	if err != nil {
		t.Fatal(err)
	}

	// The guarded pair is retirement then replacement, so a refused replacement
	// leaves its own fixture's old request retired. The two cases therefore get
	// one fixture each rather than sharing a request one of them consumed.
	refused := newMCPAuthoring(t)
	if _, _, err := refused.workspace.AddActor(refused.ctx, "human", "second", "agent"); err != nil {
		t.Fatal(err)
	}
	refusedOld, err := refused.file("mcp-reassign-old", "the old request", map[string]string{
		"to": "@agent", "conditions": "do it", "no_git_artifact": "true"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = refused.adapter.call(refused.ctx, toolCall{Name: "reassign_if_unclaimed", Arguments: map[string]any{
		"old_request": refusedOld, "to": "@second", "text": "ask again", "conditions": "do it",
		"idempotency_key": "mcp-reassign-no-choice",
	}})
	if err == nil || !strings.Contains(err.Error(), "request states no result") {
		t.Fatalf("a replacement stating no result = %v", err)
	}
	for _, statement := range mcpStatements(t, refused) {
		if statement.Kind == workroom.KindRequest && statement.Text == "ask again" {
			t.Fatalf("a replacement with no stated result reached the log: %+v", statement)
		}
	}

	value, _, err := fixture.adapter.call(fixture.ctx, toolCall{Name: "reassign_if_unclaimed", Arguments: map[string]any{
		"old_request": old, "to": "@second", "text": "ask again", "conditions": "do it",
		"body":            map[string]any{"target_ref": "refs/heads/main"},
		"idempotency_key": "mcp-reassign-landing",
	}})
	if err != nil {
		t.Fatalf("reassigning with a stated result: %v", err)
	}
	pair, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("reassignment result = %#v", value)
	}
	replacement, ok := submissionRecord(pair["request"])
	if !ok {
		t.Fatalf("replacement = %#v", pair["request"])
	}
	if schema := fixture.schemaOf(replacement.ID); schema != workroom.SchemaReassignRequestV1 {
		t.Fatalf("replacement schema = %q, want %q", schema, workroom.SchemaReassignRequestV1)
	}
	if body := fixture.body(replacement.ID); body["target_head"] != fixture.head() ||
		body["target_repo"] != fixture.genesisID() {
		t.Fatalf("replacement triple = %+v", body)
	}
	if row := fixture.commitment(replacement.ID); row.TargetRef != "refs/heads/main" || row.Legacy {
		t.Fatalf("the fold read the replacement as %+v", row)
	}
}

func mcpStatements(t *testing.T, fixture mcpAuthoring) []workroom.Statement {
	t.Helper()
	snapshot, err := fixture.workspace.Snapshot(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Projection.Statements
}
