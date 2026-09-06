package reviewguard

import (
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// bindingWorld is one assigned implementation lane at head: an operator's
// request to the implementer, the implementer's promise, and artifacts at the
// exact head. Commitments are supplied by the caller, because the fold's
// Report binding is the fact under test.
func bindingWorld(t *testing.T, commitments []workroom.Commitment, provenance map[string][]string, statements ...workroom.Statement) workroom.Projection {
	t.Helper()
	projection := lane(provenance, statements...)
	projection.Commitments = commitments
	for _, statement := range statements {
		if statement.Kind == workroom.KindArtifact {
			projection.Artifacts = append(projection.Artifacts, workroom.Artifact{Event: statement.Event, Path: statement.Body["path"], Commit: statement.Body["commit"]})
		}
	}
	return projection
}

func assignedStatements(extra ...workroom.Statement) []workroom.Statement {
	return append([]workroom.Statement{
		{Event: "assign", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest, Body: map[string]string{"to": "implementer", "conditions": "land it", "target_ref": "refs/heads/main"}},
		{Event: "work", Actor: "implementer", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise},
		{Event: "primary", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "cmd/gs/main.go", "commit": head}},
		{Event: "companion", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "docs/reference/gs/merge.md", "commit": head}},
	}, extra...)
}

func assignedProvenance() map[string][]string {
	return map[string][]string{"assign": nil, "work": {"assign"}, "primary": {"work"}, "companion": {"work"}}
}

func assignedCommitment(report string) workroom.Commitment {
	return workroom.Commitment{Request: "assign", Requester: "operator", AddressedTo: "implementer", Performer: "implementer", Promise: "work", Report: report, Status: "reported", TargetRepo: "repo", TargetRef: "refs/heads/main"}
}

func mustRefuse(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func TestResolveBindsAnAssignedPrimaryByExactReportEquality(t *testing.T) {
	projection := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary")}, assignedProvenance(), assignedStatements()...)
	binding, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"primary", "companion"}})
	if err != nil {
		t.Fatal(err)
	}
	if binding.Kind != BindingAssigned || len(binding.Implementations) != 1 || binding.Implementations[0].Request != "assign" || binding.Implementations[0].Report != "primary" || binding.Implementations[0].TargetRef != "refs/heads/main" {
		t.Fatalf("binding = %+v", binding)
	}
	if got := binding.BodyFields(); got[BodyBinding] != BindingAssigned || got[BodyImplementations] != `["assign"]` {
		t.Fatalf("body fields = %v", got)
	}
}

// The three recorded historical pairs share one shape: the supplied primary
// reports nothing, and the corrected primary is the request's actual report.
// The resolver refuses before signing and names the required artifact; the
// wrong primary stays a companion if the reviewer examined it.
func TestResolveRefusesTheHistoricalWrongPrimaryAndNamesTheRequiredReport(t *testing.T) {
	for _, pair := range []struct{ supplied, required, path string }{
		{"primary", "companion", "docs/reference/gs/merge.md"},
		{"companion", "primary", "cmd/gs/main.go"},
	} {
		projection := bindingWorld(t, []workroom.Commitment{assignedCommitment(pair.required)}, assignedProvenance(), assignedStatements()...)
		_, err := Resolve(projection, Scope{Candidate: head, Examined: []string{pair.supplied, pair.required}})
		mustRefuse(t, err, "reported by "+quoted(pair.required)+" ("+pair.path+")")
		mustRefuse(t, err, "keep "+quoted(pair.supplied)+" as a companion")
		binding, err := Resolve(projection, Scope{Candidate: head, Examined: []string{pair.required, pair.supplied}})
		if err != nil || binding.Primary != pair.required || len(binding.Implementations) != 1 {
			t.Fatalf("corrected primary: %+v %v", binding, err)
		}
	}
}

func TestResolveNeverInfersSelfInitiationFromAnEmptyLookup(t *testing.T) {
	projection := bindingWorld(t, nil, assignedProvenance(), assignedStatements()...)
	_, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}})
	mustRefuse(t, err, "reporting link is broken")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}, Decision: "assign"})
	mustRefuse(t, err, "does not become self-initiated by selecting a mode")
}

func TestResolveEvidenceOnlyNeedsANoArtifactRequestEdge(t *testing.T) {
	statements := []workroom.Statement{
		{Event: "ask", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest, Body: map[string]string{"to": "implementer", "conditions": "show evidence", "no_git_artifact": "true"}},
		{Event: "work", Actor: "implementer", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise},
		{Event: "evidence", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "notes/evidence.md", "commit": head}},
	}
	provenance := map[string][]string{"ask": nil, "work": {"ask"}, "evidence": {"work"}}
	commitments := []workroom.Commitment{{Request: "ask", Requester: "operator", AddressedTo: "implementer", Performer: "implementer", Promise: "work", Status: "promised"}}
	projection := bindingWorld(t, commitments, provenance, statements...)
	_, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"evidence"}})
	mustRefuse(t, err, "owes no Git artifact; it cannot be a delivery primary")
	binding, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"evidence"}, EvidenceOnly: true})
	if err != nil || binding.Kind != BindingEvidenceOnly || binding.Evidence != "ask" || len(binding.Implementations) != 0 {
		t.Fatalf("evidence binding = %+v %v", binding, err)
	}
	// An assigned delivery cannot be relabelled evidence, and a self-initiated
	// claim cannot ride on an evidence edge.
	assigned := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary")}, assignedProvenance(), assignedStatements()...)
	_, err = Resolve(assigned, Scope{Candidate: head, Examined: []string{"primary"}, EvidenceOnly: true})
	mustRefuse(t, err, "cannot be reviewed as evidence-only")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"evidence"}, Decision: "ask"})
	mustRefuse(t, err, "does not become self-initiated")
}

func TestResolveSelfInitiatedNeedsARatifiedDecisionThePrimaryRestsOn(t *testing.T) {
	statements := []workroom.Statement{
		{Event: "decision", Actor: "hugh", Kind: workroom.KindPropose, Ratified: true, RatifiedBy: "adopt"},
		{Event: "draft", Actor: "hugh", Kind: workroom.KindPropose},
		{Event: "own", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "internal/x.go", "commit": head}},
		{Event: "other", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "internal/y.go", "commit": head}},
	}
	provenance := map[string][]string{"decision": nil, "draft": nil, "own": {"decision"}, "other": {"draft"}}
	projection := bindingWorld(t, nil, provenance, statements...)
	binding, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"own"}, Decision: "decision"})
	if err != nil || binding.Kind != BindingSelfInitiated || binding.Decision != "decision" {
		t.Fatalf("self-initiated binding = %+v %v", binding, err)
	}
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"other"}, Decision: "draft"})
	mustRefuse(t, err, "not ratified")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"own"}, Decision: "draft"})
	mustRefuse(t, err, "do not rest directly on each other")
	// The adoption may instead rest on the artifact it adopts, as a decision
	// record's proposal does.
	adopted := bindingWorld(t, nil, map[string][]string{"record": nil, "adopt": {"record"}},
		workroom.Statement{Event: "record", Actor: "dana", Kind: workroom.KindArtifact, Body: map[string]string{"path": "docs/decisions/0001.md", "commit": head}},
		workroom.Statement{Event: "adopt", Actor: "dana", Kind: workroom.KindPropose, Ratified: true, RatifiedBy: "ok"})
	if binding, err := Resolve(adopted, Scope{Candidate: head, Examined: []string{"record"}, Decision: "adopt"}); err != nil || binding.Kind != BindingSelfInitiated {
		t.Fatalf("adoption resting on the record: %+v %v", binding, err)
	}
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"own"}})
	mustRefuse(t, err, "rests on no request or promise of its author")
	// A direct assignment edge cannot be overridden by adding the witness.
	assigned := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary")}, assignedProvenance(), append(assignedStatements(), statements[0])...)
	assigned.Provenance["primary"] = []string{"work", "decision"}
	_, err = Resolve(assigned, Scope{Candidate: head, Examined: []string{"primary"}, Decision: "decision"})
	mustRefuse(t, err, "assigned work cannot be reviewed as self-initiated")
}

func TestResolveCombinedImplementationsKeepEveryReportAndCompanion(t *testing.T) {
	statements := append(assignedStatements(),
		workroom.Statement{Event: "assign2", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest, Body: map[string]string{"to": "implementer", "conditions": "land it too", "target_ref": "refs/heads/main"}},
		workroom.Statement{Event: "work2", Actor: "implementer", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise},
		workroom.Statement{Event: "second", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "internal/second.go", "commit": head}},
		workroom.Statement{Event: "extra", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "internal/extra.go", "commit": head}},
	)
	provenance := assignedProvenance()
	provenance["assign2"], provenance["work2"], provenance["second"], provenance["extra"] = nil, []string{"assign2"}, []string{"work2"}, []string{"work2"}
	second := assignedCommitment("second")
	second.Request, second.Promise = "assign2", "work2"
	projection := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary"), second}, provenance, statements...)
	binding, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"primary", "companion", "second", "extra"}})
	if err != nil || len(binding.Implementations) != 2 || binding.Implementations[1].Request != "assign2" || len(binding.Examined) != 4 {
		t.Fatalf("combined binding = %+v %v", binding, err)
	}
	// Selectors: exact, complete, and in primary order.
	binding, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary", "second"}, Implementations: []string{"assign", "work2"}})
	if err != nil || len(binding.Implementations) != 2 {
		t.Fatalf("selected binding = %+v %v", binding, err)
	}
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary", "second"}, Implementations: []string{"assign2", "assign"}})
	mustRefuse(t, err, "differs from the first selected implementation's report")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}, Implementations: []string{"assign", "assign2"}})
	mustRefuse(t, err, "not in the examined set")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}, Implementations: []string{"assign", "assign"}})
	mustRefuse(t, err, "selected twice")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}, Implementations: []string{"nobody"}})
	mustRefuse(t, err, "matches 0 commitment lifecycles")
	// Incompatible intended targets refuse before a delivery review is signed.
	projection.Commitments[1].TargetRef = "refs/heads/release"
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary", "second"}})
	mustRefuse(t, err, "different targets")
}

func TestResolveRefusesAReportSignedByAnotherActorAndAmbiguousLifecycles(t *testing.T) {
	projection := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary")}, assignedProvenance(), assignedStatements()...)
	projection.Commitments[0].Performer = "stranger"
	_, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}})
	mustRefuse(t, err, "not by the performer")
	twice := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary"), assignedCommitment("primary")}, assignedProvenance(), assignedStatements()...)
	twice.Commitments[1].Request = "assign-again"
	_, err = Resolve(twice, Scope{Candidate: head, Examined: []string{"primary"}})
	mustRefuse(t, err, "select one with --implementation")
	_, err = Resolve(projection, Scope{Candidate: head, Examined: []string{"primary"}, Decision: "x", EvidenceOnly: true})
	mustRefuse(t, err, "choose one of")
	_, err = Resolve(projection, Scope{Candidate: "2222222222222222222222222222222222222222", Examined: []string{"primary"}})
	mustRefuse(t, err, "not at the reviewed head")
}

func TestScopeFromVerdictRoundTripsAndReclassifiesLegacyApprovals(t *testing.T) {
	projection := bindingWorld(t, []workroom.Commitment{assignedCommitment("primary")}, assignedProvenance(), assignedStatements()...)
	binding, err := Resolve(projection, Scope{Candidate: head, Examined: []string{"primary", "companion"}, Implementations: []string{"assign"}})
	if err != nil {
		t.Fatal(err)
	}
	verdict := workroom.Statement{Event: "verdict", Kind: workroom.KindReport, Body: map[string]string{"verdict": "approved", "head": head, "artifact": "primary"}}
	for field, value := range binding.BodyFields() {
		verdict.Body[field] = value
	}
	projection.Statements = append(projection.Statements, verdict, workroom.Statement{Event: "review-promise", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise}, workroom.Statement{Event: "review-request", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest})
	projection.Provenance["verdict"] = []string{"review-promise", "review-request", "companion", "primary"}
	for _, event := range []string{"verdict", "review-promise", "review-request"} {
		projection.Decisions = append(projection.Decisions, workroom.Decision{Event: event, Verdict: workroom.Effective})
	}
	scope, err := ScopeFromVerdict(projection, verdict)
	if err != nil || scope.Examined[0] != "primary" || len(scope.Examined) != 2 || scope.Implementations[0] != "assign" {
		t.Fatalf("scope = %+v %v", scope, err)
	}
	again, err := Resolve(projection, scope)
	if err != nil || !SameBinding(binding, again) {
		t.Fatalf("re-resolution differs: %+v %v", again, err)
	}
	// A legacy verdict carries no selectors; its actual primary is judged.
	legacy := verdict
	legacy.Body = map[string]string{"verdict": "approved", "head": head, "artifact": "companion"}
	scope, err = ScopeFromVerdict(projection, legacy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(projection, scope)
	mustRefuse(t, err, "reported by \"primary\"")
}
