package main

import (
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func projection(statements ...workroom.Statement) workroom.Projection {
	return workroom.Projection{Statements: statements}
}

func charter(event string, ratified, retired, stale bool) workroom.Statement {
	return workroom.Statement{
		Event: event, Kind: workroom.KindPropose, Actor: "hugh",
		Ratified: ratified, Retired: retired, Stale: stale,
	}
}

// The fold does not know what a charter is and will not enforce one, so this
// check is the connector holding itself to its own contract. Each refusal
// matters for a different reason, so each is asserted separately rather than
// through a table that would let one silently stop firing.
func TestTheConnectorRefusesToActWithoutALiveCharter(t *testing.T) {
	const event = "git:sha1:g#git:sha1:charter"

	if err := charterIsLive(projection(), event); err == nil {
		t.Error("a charter absent from the workroom was accepted")
	} else if !strings.Contains(err.Error(), "not in this workroom") {
		t.Errorf("absent charter reported as %q", err)
	}

	if err := charterIsLive(projection(charter(event, false, false, false)), event); err == nil {
		t.Error("an unratified charter was accepted — ratification is what makes it policy")
	}

	if err := charterIsLive(projection(charter(event, true, true, false)), event); err == nil {
		t.Error("a retired charter was accepted")
	}

	// Stale matters as much as retired here. A charter whose basis died is no
	// longer standing on what it claimed to stand on, and acting under it would
	// be the connector deciding that the flare did not apply to itself.
	if err := charterIsLive(projection(charter(event, true, false, true)), event); err == nil {
		t.Error("a stale charter was accepted")
	}

	if err := charterIsLive(projection(charter(event, true, false, false)), event); err != nil {
		t.Errorf("a live ratified charter was refused: %v", err)
	}
}

// Clause sources come from the projection unchanged. If this dropped the actor
// or the retired flag, the authority and liveness checks downstream would be
// deciding on data that had already lost what they test.
func TestClauseSourcesCarryAuthorAndLiveness(t *testing.T) {
	sources := clauseSources(projection(workroom.Statement{
		Event: "e", Actor: "hugh", Retired: true, Stale: true,
		Body: map[string]string{"connector": "github", "issues": "7"},
	}))
	if len(sources) != 1 {
		t.Fatalf("got %d sources", len(sources))
	}
	if sources[0].Actor != "hugh" {
		t.Errorf("actor lost: %q", sources[0].Actor)
	}
	if !sources[0].Retired || !sources[0].Stale {
		t.Error("liveness flags lost on the way to the clause reader")
	}
	if sources[0].Body["issues"] != "7" {
		t.Error("body lost")
	}
}

// Roles reach the clause reader, which is what stops a participant's
// well-formed body from widening the connector's own admission.
func TestAuthorsCarryRoles(t *testing.T) {
	people := authors(workroom.Projection{Actors: map[string]workroom.ActorState{
		"fp-hugh":   {Name: "hugh", Roles: []string{"operator", "ratifier"}},
		"fp-claude": {Name: "claude", Roles: []string{"participant"}},
	}})
	if len(people["fp-hugh"].Roles) != 2 {
		t.Errorf("operator roles lost: %+v", people["fp-hugh"])
	}
	if len(people["fp-claude"].Roles) != 1 {
		t.Errorf("participant roles lost: %+v", people["fp-claude"])
	}
}
