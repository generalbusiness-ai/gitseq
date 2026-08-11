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
		Body: map[string]string{
			"connector": "github", "owner": "generalbusiness-ai",
			"repo": "gitseq", "actor": "github-connector",
		},
	}
}

// live is charterIsLive with the arguments this process would actually have
// been given, so each test varies one thing. It asks about observation, which
// is what the connector does unless --propose says otherwise; the write path is
// exercised separately because it is the one that cannot be undone.
func live(projection workroom.Projection, event string) error {
	return charterIsLive(projection, event, "generalbusiness-ai", "gitseq", "github-connector", OperationObserve)
}

// propose is the same doorstep asked about the operation that publishes.
func propose(projection workroom.Projection, event string) error {
	return charterIsLive(projection, event, "generalbusiness-ai", "gitseq", "github-connector", OperationPropose)
}

// The fold does not know what a charter is and will not enforce one, so this
// check is the connector holding itself to its own contract. Each refusal
// matters for a different reason, so each is asserted separately rather than
// through a table that would let one silently stop firing.
func TestTheConnectorRefusesToActWithoutALiveCharter(t *testing.T) {
	const event = "git:sha1:g#git:sha1:charter"

	if err := live(projection(), event); err == nil {
		t.Error("a charter absent from the workroom was accepted")
	} else if !strings.Contains(err.Error(), "not in this workroom") {
		t.Errorf("absent charter reported as %q", err)
	}

	if err := live(projection(charter(event, false, false, false)), event); err == nil {
		t.Error("an unratified charter was accepted — ratification is what makes it policy")
	}

	if err := live(projection(charter(event, true, true, false)), event); err == nil {
		t.Error("a retired charter was accepted")
	}

	// Stale matters as much as retired here. A charter whose basis died is no
	// longer standing on what it claimed to stand on, and acting under it would
	// be the connector deciding that the flare did not apply to itself.
	if err := live(projection(charter(event, true, false, true)), event); err == nil {
		t.Error("a stale charter was accepted")
	}

	if err := live(projection(charter(event, true, false, false)), event); err != nil {
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

// Liveness is not enough. A ratified statement that does not say what it
// charters bounds nothing, and accepting one would let this command observe any
// repository at all while claiming a doorstep it does not have.
func TestTheCharterMustAuthorizeThisRepositoryAndActor(t *testing.T) {
	const event = "git:sha1:g#git:sha1:charter"

	// A charter with no body is the case that matters most, because it is what
	// an ordinary ratified proposal looks like. It authorizes nothing in
	// particular, so it is refused rather than read generously.
	empty := charter(event, true, false, false)
	empty.Body = nil
	if err := live(projection(empty), event); err == nil {
		t.Error("a charter naming no repository or actor was accepted")
	} else if !strings.Contains(err.Error(), "authorizes nothing in particular") {
		t.Errorf("empty charter reported as %q", err)
	}

	// Each mismatch is separate because they mean different things: the wrong
	// repository is a scope error, the wrong actor an identity one.
	for field, value := range map[string]string{
		"connector": "slack", "owner": "someone-else",
		"repo": "another-repo", "actor": "some-other-connector",
	} {
		wrong := charter(event, true, false, false)
		wrong.Body[field] = value
		if err := live(projection(wrong), event); err == nil {
			t.Errorf("a charter whose %s is %q was accepted for a different one", field, value)
		}
	}

	if err := live(projection(charter(event, true, false, false)), event); err != nil {
		t.Errorf("a charter authorizing exactly this repository and actor was refused: %v", err)
	}
}

// chartered builds a live charter that declares exactly these operations.
// Passing none produces the shape every charter ratified so far has: four
// scope fields and no statement about reading versus writing.
func chartered(event string, operations ...string) workroom.Statement {
	statement := charter(event, true, false, false)
	if len(operations) > 0 {
		statement.Body["operations"] = strings.Join(operations, " ")
	}
	return statement
}

// The finding this closes: the doorstep could not tell reading from writing, so
// any live inbound charter authorized opening a pull request on a public
// repository under the project's name. Scope alone is not permission — the four
// fields say *where* the connector may act, and nothing said *what* it may do.
func TestAnInboundCharterDoesNotAuthorizeWritingToTheForge(t *testing.T) {
	const event = "git:sha1:g#git:sha1:charter"

	// The shape of every charter ratified so far. It must keep working for
	// observation, or fixing this would silently disarm the inbound half.
	silent := projection(chartered(event))
	if err := live(silent, event); err != nil {
		t.Errorf("a charter with no declared operations stopped authorizing observation: %v", err)
	}
	// And must not authorize the forge write.
	err := propose(silent, event)
	if err == nil {
		t.Fatal("an inbound charter authorized opening a pull request")
	}
	if !strings.Contains(err.Error(), "authorizes observe only") {
		t.Errorf("the refusal does not say why: %q", err)
	}

	// Declaring observation explicitly is not a licence to write either.
	if err := propose(projection(chartered(event, OperationObserve)), event); err == nil {
		t.Error("a charter declaring observation alone authorized proposing")
	}

	// A charter that says so authorizes it, or the check would be unusable.
	if err := propose(projection(chartered(event, OperationObserve, OperationPropose)), event); err != nil {
		t.Errorf("a charter declaring propose refused it: %v", err)
	}
	// Declaring propose alone does not smuggle in observation.
	if err := live(projection(chartered(event, OperationPropose)), event); err == nil {
		t.Error("a charter declaring propose alone authorized observation")
	}

	// Scope is still checked alongside the operation, so a charter authorizing
	// the write for a different repository does not authorize it here.
	elsewhere := chartered(event, OperationPropose)
	elsewhere.Body["repo"] = "somewhere-else"
	if err := propose(projection(elsewhere), event); err == nil {
		t.Error("a charter for another repository authorized proposing here")
	}
}

// A pull request cannot be superseded. So the durable facts it names are
// checked before it is opened, not merely required to be present: three
// well-formed strings that have nothing to do with each other would render a
// rendering pointing at a record that does not say what it claims, and the
// reader on GitHub has no way to tell.
func TestAProposalMustNameFactsThatHoldTogether(t *testing.T) {
	const request = "git:sha1:g#git:sha1:request"
	const artifact = "git:sha1:g#git:sha1:artifact"
	const commit = "6ca1266b21306cb96726d345eac9021a91488fe7"

	coherent := workroom.Projection{
		Statements: []workroom.Statement{
			{Event: request, Kind: workroom.KindRequest},
			{Event: artifact, Kind: workroom.KindArtifact},
		},
		Artifacts:  []workroom.Artifact{{Event: artifact, Commit: commit}},
		Provenance: map[string][]string{artifact: {request}},
	}
	if err := proposalIsCoherent(coherent, request, artifact, commit); err != nil {
		t.Fatalf("a coherent proposal was refused: %v", err)
	}

	// Each of these is well-formed and supplied. Only the relationship differs.
	t.Run("unknown request", func(t *testing.T) {
		if err := proposalIsCoherent(coherent, "git:sha1:g#git:sha1:nothing", artifact, commit); err == nil {
			t.Error("a request naming nothing was rendered")
		}
	})
	t.Run("retired artifact", func(t *testing.T) {
		retired := coherent
		retired.Statements = []workroom.Statement{
			{Event: request, Kind: workroom.KindRequest},
			{Event: artifact, Kind: workroom.KindArtifact, Retired: true},
		}
		if err := proposalIsCoherent(retired, request, artifact, commit); err == nil {
			t.Error("a retired artifact was rendered as current")
		}
	})
	t.Run("stale artifact", func(t *testing.T) {
		stale := coherent
		stale.Statements = []workroom.Statement{
			{Event: request, Kind: workroom.KindRequest},
			{Event: artifact, Kind: workroom.KindArtifact, Stale: true},
		}
		if err := proposalIsCoherent(stale, request, artifact, commit); err == nil {
			t.Error("a stale artifact was rendered without saying its basis moved")
		}
	})
	t.Run("artifact names another commit", func(t *testing.T) {
		if err := proposalIsCoherent(coherent, request, artifact, "0000000000000000000000000000000000000000"); err == nil {
			t.Error("the rendering claimed a commit the artifact does not name")
		}
	})
	t.Run("artifact belongs to other work", func(t *testing.T) {
		elsewhere := coherent
		elsewhere.Provenance = map[string][]string{artifact: {"git:sha1:g#git:sha1:other"}}
		if err := proposalIsCoherent(elsewhere, request, artifact, commit); err == nil {
			t.Error("a real request was paired with a real artifact from different work")
		}
	})
}
