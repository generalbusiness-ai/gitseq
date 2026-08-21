package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/connector/github"
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

	// Staleness refuses, and briefly did not. The argument for admitting it was
	// that correct replacement makes a successor stale by construction; that is
	// false — a successor rests on stable current governance and the separate
	// supersession links it to the predecessor. Nothing forced the widening, and
	// it would have converted a flare into continuing authority for an
	// irreversible public write, on a basis that may be the very scope an
	// operator withdrew.
	if err := live(projection(charter(event, true, false, true)), event); err == nil {
		t.Error("a charter whose authority has moved was accepted")
	} else if !strings.Contains(err.Error(), "successor") {
		t.Errorf("the refusal does not say how to repair it: %v", err)
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

	effective := []workroom.Decision{
		{Event: request, Verdict: workroom.Effective, Reason: "statement recorded"},
		{Event: artifact, Verdict: workroom.Effective, Reason: "statement recorded"},
	}
	coherent := workroom.Projection{
		Decisions: effective,
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
	// Presence in Statements is not force. The fold keeps ineffective acts
	// there on purpose, so a well-formed request the workroom never stood
	// behind is neither retired nor stale and would otherwise pass.
	t.Run("ineffective request", func(t *testing.T) {
		never := coherent
		never.Decisions = []workroom.Decision{
			{Event: request, Verdict: workroom.Ineffective, Reason: "dangling promise has no request"},
			{Event: artifact, Verdict: workroom.Effective, Reason: "statement recorded"},
		}
		if err := proposalIsCoherent(never, request, artifact, commit); err == nil {
			t.Error("a pull request cited a governing request the fold gave no force")
		}
	})
	t.Run("ineffective artifact", func(t *testing.T) {
		never := coherent
		never.Decisions = []workroom.Decision{
			{Event: request, Verdict: workroom.Effective, Reason: "statement recorded"},
			{Event: artifact, Verdict: workroom.Ineffective, Reason: "statement recorded"},
		}
		if err := proposalIsCoherent(never, request, artifact, commit); err == nil {
			t.Error("a pull request cited an artifact the fold gave no force")
		}
	})
	t.Run("disputed request", func(t *testing.T) {
		disputed := coherent
		disputed.Decisions = []workroom.Decision{
			{Event: request, Verdict: workroom.Disputed, Reason: "competing settlements"},
			{Event: artifact, Verdict: workroom.Effective, Reason: "statement recorded"},
		}
		if err := proposalIsCoherent(disputed, request, artifact, commit); err == nil {
			t.Error("a disputed request was rendered as governing")
		}
	})
	t.Run("no decision at all", func(t *testing.T) {
		silent := coherent
		silent.Decisions = []workroom.Decision{{Event: artifact, Verdict: workroom.Effective}}
		if err := proposalIsCoherent(silent, request, artifact, commit); err == nil {
			t.Error("a request with nothing saying it took effect was rendered")
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

// The composed path, from a projection through clauseSources into admission.
//
// The unit fixtures in internal/connector manufacture ClauseSource values
// directly, so they cannot prove that this command builds them correctly from a
// real projection — and every defect codex found at this boundary lived in that
// gap rather than in the admission logic. This exercises what the command
// actually assembles: the fold's ruling, the signed bases, and the charter the
// run was told to act under.
func TestTheCommandAdmitsOnlyEffectiveClausesCitingTheSelectedCharter(t *testing.T) {
	const chosen = "git:sha1:g#git:sha1:chosen-charter"
	const other = "git:sha1:g#git:sha1:other-charter"

	clause := func(event, basis string) workroom.Statement {
		return workroom.Statement{
			Event: event, Kind: workroom.KindAssert, Actor: "hugh",
			Body: map[string]string{"connector": "github", "issues": "7"},
		}
	}
	good := clause("git:sha1:g#git:sha1:good", chosen)
	refused := clause("git:sha1:g#git:sha1:refused", chosen)
	foreign := clause("git:sha1:g#git:sha1:foreign", other)

	view := workroom.Projection{
		Statements: []workroom.Statement{good, refused, foreign},
		Decisions: []workroom.Decision{
			{Event: good.Event, Verdict: workroom.Effective},
			{Event: refused.Event, Verdict: workroom.Ineffective, Reason: "request state requires body.conditions"},
			{Event: foreign.Event, Verdict: workroom.Effective},
		},
		Provenance: map[string][]string{
			good.Event:    {chosen},
			refused.Event: {chosen},
			foreign.Event: {other},
		},
		Actors: map[string]workroom.ActorState{
			"hugh": {Name: "hugh", Roles: []string{"operator"}},
		},
	}

	reading := github.ClausesFrom(clauseSources(view), authors(view), chosen)
	if len(reading.Clauses) != 1 {
		t.Fatalf("admitted %d clauses, want only the effective one citing this charter: %+v", len(reading.Clauses), reading)
	}
	if reading.Clauses[0].Event != good.Event {
		t.Errorf("admitted %q, want %q", reading.Clauses[0].Event, good.Event)
	}

	reasons := map[string]string{}
	for _, refusal := range reading.Refusals {
		reasons[refusal.Event] = refusal.Reason
	}
	if !strings.Contains(reasons[refused.Event], "force") {
		t.Errorf("an ineffective clause was not refused for want of force: %q", reasons[refused.Event])
	}
	if !strings.Contains(reasons[foreign.Event], "charter") {
		t.Errorf("a clause under another charter was not refused for that: %q", reasons[foreign.Event])
	}

	// A run naming no charter reaches GitHub for nothing at all.
	if empty := github.ClausesFrom(clauseSources(view), authors(view), ""); len(empty.Clauses) != 0 {
		t.Fatalf("clauses were admitted with no charter selected: %+v", empty)
	}
}

func TestObservedStatementsPreserveAuthorshipForTheCorrespondenceFold(t *testing.T) {
	const connector = "connector-fingerprint"
	forged := github.ObserveIssue(github.Issue{
		Owner: "generalbusiness-ai", Repo: "gitseq", Number: 7,
		Title: "an issue", Author: "someone", URL: "https://example.invalid/7",
	})
	genuine := github.ObserveIssue(github.Issue{
		Owner: "generalbusiness-ai", Repo: "gitseq", Number: 8,
		Title: "another issue", Author: "someone", URL: "https://example.invalid/8",
	})
	projection := workroom.Projection{Statements: []workroom.Statement{
		{
			Event: "git:sha1:g#git:sha1:forged", Actor: "ordinary-participant",
			Body: map[string]string{"source": "github", "external_id": forged.ExternalID},
		},
		{
			Event: "git:sha1:g#git:sha1:genuine", Actor: connector,
			Body: map[string]string{"source": "github", "external_id": genuine.ExternalID},
		},
	}}

	seen := github.Fold(observedStatements(projection), connector)
	fresh := github.Unobserved([]github.Observation{forged, genuine}, seen)
	if len(fresh) != 1 || fresh[0].ExternalID != forged.ExternalID {
		t.Fatalf("command adapter lost authorship at the fold boundary: %+v", fresh)
	}
}

func TestDryRunProposalDoesNotRequireLocalConnectorCustody(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	state := func(act app.Act) workroom.Record {
		t.Helper()
		submission, err := workspace.Act(ctx, "human", act)
		if err != nil {
			t.Fatal(err)
		}
		return submission.Record
	}
	charter := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindPropose, Text: "connector charter",
		Body: map[string]string{
			"connector": "github", "owner": "generalbusiness-ai", "repo": "gitseq",
			"actor": "github-connector", "operations": "propose",
		},
		RestsOn: []string{seed.ID}, IdempotencyKey: "charter",
	})
	state(app.Act{Verb: app.VerbRatify, Target: charter.ID, IdempotencyKey: "ratify-charter"})
	request := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "fix issue",
		Body: map[string]string{
			"to": workspace.Config.Actors["human"].Fingerprint, "conditions": "exact head",
		},
		RestsOn: []string{seed.ID}, IdempotencyKey: "request",
	})
	const commit = "1111111111111111111111111111111111111111"
	artifact := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "candidate",
		Body:    map[string]string{"path": "internal/connector", "commit": commit},
		RestsOn: []string{request.ID}, IdempotencyKey: "artifact",
	})

	err = run(ctx, []string{
		"--repo", repo, "--as", "github-connector", "--charter", charter.ID,
		"--owner", "generalbusiness-ai", "--repo-name", "gitseq",
		"--propose", "7", "--branch", "request/fix", "--base", "main",
		"--commit", commit, "--request", request.ID, "--artifact", artifact.ID,
		"--title", "Fix issue", "--dry-run",
	})
	if err != nil {
		t.Fatalf("dry-run proposal required local connector custody: %v", err)
	}
}

func TestObservationIdentityRefusesConfiguredFingerprintMismatch(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "connector", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	configured := workspace.Config.Actors["connector"]
	configured.Fingerprint = strings.Repeat("0", len(configured.Fingerprint))
	workspace.Config.Actors["connector"] = configured

	if _, err := loadObservationIdentity(workspace, "connector"); err == nil || !strings.Contains(err.Error(), "does not match configured fingerprint") {
		t.Fatalf("mismatched connector identity error = %v", err)
	}
}

func TestObservationIdentityCanonicalizesActorAddresses(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "connector", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := workspace.Config.Actors["connector"]
	for _, address := range []string{"connector", "@connector", want.Fingerprint} {
		identity, err := loadObservationIdentity(workspace, address)
		if err != nil {
			t.Fatalf("load %q: %v", address, err)
		}
		if identity.Name != "connector" || identity.Fingerprint != want.Fingerprint {
			t.Fatalf("load %q = %+v, want canonical connector %s", address, identity, want.Fingerprint)
		}
	}
}
