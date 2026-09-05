package service

import (
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Request authoring over the resident's own act endpoint. The browser and every
// other HTTP client file requests here, so the result choice, the filing-time
// resolution and the refusals are proved against the running handler rather
// than against the boundary underneath it.

func (f authorizationFixture) fileRequest(key, text string, body map[string]string, bases ...string) (string, string) {
	f.t.Helper()
	return f.post(actRequest{Session: f.credential, Act: "state", Kind: "request", Text: text,
		Body: body, RestsOn: bases, IdempotencyKey: key})
}

func (f authorizationFixture) schemaOf(event string) string {
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

func (f authorizationFixture) requestBody(event string) map[string]string {
	f.t.Helper()
	for _, statement := range f.snapshot().Projection.Statements {
		if statement.Event == event {
			return statement.Body
		}
	}
	f.t.Fatalf("no statement for %s", event)
	return nil
}

func (f authorizationFixture) row(event string) workroom.Commitment {
	f.t.Helper()
	for _, commitment := range f.snapshot().Projection.Commitments {
		if commitment.Request == event {
			return commitment
		}
	}
	f.t.Fatalf("no commitment row for %s", event)
	return workroom.Commitment{}
}

func (f authorizationFixture) genesisID() string {
	view := f.workspace.View()
	return "git:" + view.ObjectFormat + ":" + view.Genesis
}

func TestResidentRequestStatesItsResult(t *testing.T) {
	fixture := newAuthorizationFixture(t)
	head := fixture.git("rev-parse", fixture.ref)

	landing, refusal := fixture.fileRequest("http-by-value", "land it", map[string]string{
		"to": "reviewer", "conditions": "it lands", "target_ref": fixture.ref})
	if refusal != "" {
		t.Fatalf("filing a landing request: %s", refusal)
	}
	if schema := fixture.schemaOf(landing); schema != workroom.SchemaStateV3 {
		t.Fatalf("stored schema = %q, want %q", schema, workroom.SchemaStateV3)
	}
	body := fixture.requestBody(landing)
	if body["target_head"] != head || body["target_repo"] != fixture.genesisID() {
		t.Fatalf("resolved triple = %+v, ref head %s", body, head)
	}
	if row := fixture.row(landing); row.TargetRef != fixture.ref || row.Legacy {
		t.Fatalf("folded result = %+v", row)
	}

	child, refusal := fixture.fileRequest("http-inherit", "child inherits", map[string]string{
		"to": "reviewer", "conditions": "it lands too", "target": "inherit"}, landing)
	if refusal != "" {
		t.Fatalf("filing an inheriting request: %s", refusal)
	}
	if row := fixture.row(child); row.TargetRef != fixture.ref {
		t.Fatalf("inherited result = %+v", row)
	}

	review, refusal := fixture.fileRequest("http-no-artifact", "review it", map[string]string{
		"to": "reviewer", "conditions": "it is reviewed", "no_git_artifact": "true"})
	if refusal != "" {
		t.Fatalf("filing a no-artifact request: %s", refusal)
	}
	if row := fixture.row(review); row.TargetRef != "" || row.Legacy {
		t.Fatalf("a no-artifact request owes %+v", row)
	}

	held, refusal := fixture.fileRequest("http-held", "land it under a hold", map[string]string{
		"to": "reviewer", "conditions": "it lands", "target_ref": fixture.ref, "landing": "held"})
	if refusal != "" {
		t.Fatalf("filing a held request: %s", refusal)
	}
	reviewer, err := fixture.workspace.ResolveActor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if row := fixture.row(held); row.HoldOwner != reviewer.Fingerprint {
		t.Fatalf("hold owner = %q, want the requester %q", row.HoldOwner, reviewer.Fingerprint)
	}
}

func TestResidentRequestRefusesBeforeAnyAppend(t *testing.T) {
	fixture := newAuthorizationFixture(t)
	for _, testCase := range []struct {
		name string
		body map[string]string
		want string
	}{
		{"no choice", map[string]string{"to": "reviewer", "conditions": "do it"},
			"request states no result"},
		{"mixed choice", map[string]string{"to": "reviewer", "conditions": "do it",
			"target_ref": fixture.ref, "no_git_artifact": "true"},
			"request states more than one result"},
		{"foreign target_repo", map[string]string{"to": "reviewer", "conditions": "do it",
			"target_repo": "git:sha1:" + strings.Repeat("f", 40), "target_ref": fixture.ref},
			"target_repo is resolved at filing and cannot be supplied"},
		{"caller-supplied target_head", map[string]string{"to": "reviewer", "conditions": "do it",
			"target_ref": fixture.ref, "target_head": strings.Repeat("a", 40)},
			"target_head is resolved at filing and cannot be supplied"},
		{"non-branch ref", map[string]string{"to": "reviewer", "conditions": "do it",
			"target_ref": "refs/tags/v1"},
			"target_ref must name a branch under refs/heads/"},
		{"missing ref", map[string]string{"to": "reviewer", "conditions": "do it",
			"target_ref": "refs/heads/nowhere"},
			"does not resolve in"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			before := fixture.snapshot().Head
			event, refusal := fixture.fileRequest("http-refuse-"+testCase.name, "refused: "+testCase.name, testCase.body)
			if refusal == "" {
				t.Fatalf("%s was filed as %s", testCase.name, event)
			}
			if !strings.Contains(refusal, testCase.want) {
				t.Fatalf("refusal %q does not name %q", refusal, testCase.want)
			}
			if after := fixture.snapshot().Head; after != before {
				t.Fatalf("the frontier moved from %s to %s on a refused request", before, after)
			}
		})
	}
}

func TestResidentRequestMeasuresTheRefAtEachFiling(t *testing.T) {
	fixture := newAuthorizationFixture(t)
	body := map[string]string{"to": "reviewer", "conditions": "it lands", "target_ref": fixture.ref}
	first, refusal := fixture.fileRequest("http-movement", "land the first", body)
	if refusal != "" {
		t.Fatal(refusal)
	}
	before := fixture.git("rev-parse", fixture.ref)

	fixture.git("commit", "--allow-empty", "-qm", "the ref moves")
	moved := fixture.git("rev-parse", fixture.ref)
	if moved == before {
		t.Fatal("the ref did not move")
	}

	frontier := fixture.snapshot().Head
	replay, refusal := fixture.fileRequest("http-movement", "land the first", body)
	if refusal != "" {
		t.Fatalf("exact retry after the ref moved: %s", refusal)
	}
	if replay != first {
		t.Fatalf("exact retry returned %s, want the original %s", replay, first)
	}
	if after := fixture.snapshot().Head; after != frontier {
		t.Fatalf("an exact retry appended: %s to %s", frontier, after)
	}
	if got := fixture.requestBody(first)["target_head"]; got != before {
		t.Fatalf("a replay re-measured the ref: %q", got)
	}

	fresh, refusal := fixture.fileRequest("http-movement-second", "land the second", body)
	if refusal != "" {
		t.Fatal(refusal)
	}
	if got := fixture.requestBody(fresh)["target_head"]; got != moved {
		t.Fatalf("a fresh filing recorded %q, want the current head %q", got, moved)
	}
}
