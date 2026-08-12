package github

import (
	"strings"
	"testing"
)

var operator = map[string]Author{
	"hugh":   {Fingerprint: "hugh", Roles: []string{"operator", "participant", "ratifier"}},
	"claude": {Fingerprint: "claude", Roles: []string{"participant"}},
	"codex":  {Fingerprint: "codex", Roles: []string{"participant", "ratifier"}},
}

const testCharter = "git:sha1:g#git:sha1:charter"

// clauseSource builds a clause already anchored to the charter the tests act
// under, so each test below varies one thing rather than two.
func clauseSource(actor string, body map[string]string) ClauseSource {
	body[ClauseKey] = ClauseConnector
	return ClauseSource{
		Event: "git:sha1:g#git:sha1:" + actor, Actor: actor, Body: body,
		Bases: []string{testCharter}, Effective: true,
	}
}

// under runs admission for the charter these fixtures are anchored to.
func under(sources []ClauseSource, authors map[string]Author) Reading {
	return ClausesFrom(sources, authors, testCharter)
}

// The charter fixes who may state a clause, but the fold does not enforce
// charters. So a statement that looks like a clause is not one until the
// connector has checked who signed it — otherwise any participant could widen
// the connector's own admission.
func TestOnlyAnOperatorMayStateAClause(t *testing.T) {
	sources := []ClauseSource{
		clauseSource("hugh", map[string]string{"issues": "7"}),
		clauseSource("claude", map[string]string{"issues": "8"}),
		clauseSource("codex", map[string]string{"issues": "9"}),
	}
	clauses := under(sources, operator).Clauses
	if len(clauses) != 1 {
		t.Fatalf("honoured %d clauses, want 1 — only the operator's", len(clauses))
	}
	if clauses[0].Numbers[0] != 7 {
		t.Errorf("honoured issue %d, want the operator's 7", clauses[0].Numbers[0])
	}
}

// Ratifier is deliberately not enough. A clause decides what foreign content
// enters the log, and the charter names operator specifically.
func TestRatifierAloneCannotStateAClause(t *testing.T) {
	sources := []ClauseSource{clauseSource("codex", map[string]string{"state": "open"})}
	if clauses := under(sources, operator).Clauses; len(clauses) != 0 {
		t.Errorf("a ratifier's clause was honoured")
	}
}

// Retirement and staleness both refuse, and each says which it was.
//
// Retiring a clause withdraws an admission and must stop it at once rather than
// merely flare what it already let in. Staleness means a basis underneath the
// clause moved, and the thing that moved may be exactly the scope somebody
// withdrew — so admitting anyway would let foreign material in on authority
// that no longer stands. They are separate reasons because they call for
// different repairs, and the reader is told which applies.
func TestRetirementAndStalenessBothRefuseWithDistinctReasons(t *testing.T) {
	retired := clauseSource("hugh", map[string]string{"issues": "7"})
	retired.Retired = true
	retired.Event += "-retired"
	stale := clauseSource("hugh", map[string]string{"issues": "8"})
	stale.Stale = true
	stale.Event += "-stale"

	reading := under([]ClauseSource{retired, stale}, operator)
	if len(reading.Clauses) != 0 {
		t.Fatalf("admitted %d clauses, want none: neither a withdrawn admission nor one whose authority has moved", len(reading.Clauses))
	}
	if len(reading.Refusals) != 2 {
		t.Fatalf("refusals = %+v, want both named", reading.Refusals)
	}
	reasons := map[string]string{}
	for _, refusal := range reading.Refusals {
		reasons[refusal.Event] = refusal.Reason
	}
	if !strings.Contains(reasons[retired.Event], "retired") {
		t.Errorf("the retired clause was not refused as retired: %q", reasons[retired.Event])
	}
	if !strings.Contains(reasons[stale.Event], "stale") {
		t.Errorf("the stale clause was not refused as stale: %q", reasons[stale.Event])
	}
}

// A stale clause fails closed, and says enough that an operator can repair it.
//
// The basis that moved beneath a clause may be the very scope somebody
// withdrew, so admitting it would let foreign material in on authority that no
// longer stands. The repair is a fresh clause on current governance, not a
// looser door.
func TestAStaleClauseIsRefusedAndSaysHowToRepairIt(t *testing.T) {
	clause := clauseSource("hugh", map[string]string{"issues": "1"})
	clause.Stale = true

	reading := under([]ClauseSource{clause}, operator)
	if len(reading.Clauses) != 0 {
		t.Fatalf("a clause whose authority has moved was admitted: %+v", reading)
	}
	if len(reading.Refusals) != 1 {
		t.Fatalf("refusals = %+v, want the stale clause named", reading.Refusals)
	}
	// Refusing is not enough on its own: being told there is no clause, with no
	// reason, is what sent an agent to file a false request against the
	// operator. The refusal has to point at the repair.
	if !strings.Contains(reading.Refusals[0].Reason, "fresh clause") {
		t.Errorf("the refusal does not say how to fix it: %q", reading.Refusals[0].Reason)
	}
}

// A refusal must say which statement it refused and why. Silence here is what
// made "the operator stated nothing" and "I threw away what the operator
// stated" the same message.
func TestRefusalsNameTheClauseAndTheReason(t *testing.T) {
	unauthorized := clauseSource("mallory", map[string]string{"issues": "7"})
	unbounded := clauseSource("hugh", map[string]string{"issues": "banana"})

	reading := under([]ClauseSource{unauthorized, unbounded}, operator)
	if len(reading.Clauses) != 0 {
		t.Fatalf("admitted %d clauses, want none", len(reading.Clauses))
	}
	if len(reading.Refusals) != 2 {
		t.Fatalf("refusals = %+v, want both named", reading.Refusals)
	}
	for _, refusal := range reading.Refusals {
		if refusal.Event == "" || refusal.Reason == "" {
			t.Errorf("refusal is silent about what or why: %+v", refusal)
		}
	}
}

// Statements that are not clauses are ignored, whoever signed them. An
// operator's ordinary assert must not accidentally admit anything.
func TestOrdinaryStatementsAreNotClauses(t *testing.T) {
	ordinary := ClauseSource{Event: "e", Actor: "hugh", Body: map[string]string{"text": "a note"}}
	otherConnector := ClauseSource{Event: "e2", Actor: "hugh", Body: map[string]string{ClauseKey: "slack", "issues": "7"}}
	if clauses := under([]ClauseSource{ordinary, otherConnector}, operator).Clauses; len(clauses) != 0 {
		t.Errorf("honoured %d non-clauses, want none", len(clauses))
	}
}

// A selection clause names issues; a criteria clause describes them. Both
// parse from the same body shape.
func TestClausesParseBothForms(t *testing.T) {
	selection := under([]ClauseSource{clauseSource("hugh", map[string]string{"issues": " 12345 , 678 "})}, operator).Clauses
	if len(selection) != 1 || len(selection[0].Numbers) != 2 {
		t.Fatalf("selection parsed as %+v", selection)
	}
	if !selection[0].Selection() {
		t.Error("a clause naming issues did not read as a selection")
	}

	criteria := under([]ClauseSource{clauseSource("hugh", map[string]string{"state": "open", "labels": "bug, ui"})}, operator).Clauses
	if len(criteria) != 1 {
		t.Fatal("criteria clause did not parse")
	}
	if criteria[0].Selection() {
		t.Error("a criteria clause read as a selection")
	}
	if criteria[0].State != "open" || len(criteria[0].Labels) != 2 {
		t.Errorf("criteria parsed as %+v", criteria[0])
	}
}

// Junk in the issues field must not become issue zero or a negative number,
// which would either match nothing confusingly or be sent to GitHub as a bad
// request.
func TestMalformedIssueNumbersAreDropped(t *testing.T) {
	clauses := under([]ClauseSource{clauseSource("hugh", map[string]string{"issues": "7, banana, -3, 0"})}, operator).Clauses
	if len(clauses) != 1 {
		t.Fatal("clause did not parse")
	}
	if len(clauses[0].Numbers) != 1 || clauses[0].Numbers[0] != 7 {
		t.Errorf("parsed numbers %v, want just [7]", clauses[0].Numbers)
	}
}

// With no clause at all the connector admits nothing, which is the property the
// whole doorstep rests on.
func TestNoClausesMeansNoAdmission(t *testing.T) {
	if clauses := under(nil, operator).Clauses; len(clauses) != 0 {
		t.Errorf("got %d clauses from nothing", len(clauses))
	}
}

// The sharpest edge in the connector: a body marked for this connector that
// names nothing must produce no clause. It used to parse into an empty criteria
// clause, whose query is state=all and whose Admits accepts everything, so a
// half-written body or a typo in an issue number listed the whole tracker and
// admitted all of it — the unbounded read arriving through the doorstep itself.
//
// Each shape is asserted separately rather than in a table, because they fail
// for different reasons and a table would let one quietly stop firing.
func TestAnUnboundedBodyProducesNoClause(t *testing.T) {
	operators := map[string]Author{"op": {Fingerprint: "op", Roles: []string{"operator"}}}
	clausesFor := func(body map[string]string) []Clause {
		return under([]ClauseSource{{Event: "e", Actor: "op", Body: body, Bases: []string{testCharter}, Effective: true}}, operators).Clauses
	}

	for name, body := range map[string]map[string]string{
		"nothing but the connector marker": {"connector": "github"},
		"an empty issues field":            {"connector": "github", "issues": ""},
		"issues that are not numbers":      {"connector": "github", "issues": "abc,#12,"},
		"issues that are not positive":     {"connector": "github", "issues": "0,-3"},
		"empty state and empty labels":     {"connector": "github", "state": "", "labels": ""},
		"a state github would not know":    {"connector": "github", "state": "everything"},
	} {
		if got := clausesFor(body); len(got) != 0 {
			t.Errorf("%s produced clause %+v with query %q, want no clause",
				name, got[0], got[0].Query().Encode())
		}
	}

	// The bounded shapes must still be admitted, or the fix would close the
	// doorstep entirely rather than bounding it.
	for name, body := range map[string]map[string]string{
		"a named issue":              {"connector": "github", "issues": "1"},
		"a label":                    {"connector": "github", "labels": "bug"},
		"a state":                    {"connector": "github", "state": "open"},
		"one good and one bad issue": {"connector": "github", "issues": "abc,7"},
	} {
		if got := clausesFor(body); len(got) != 1 {
			t.Errorf("%s produced %d clauses, want 1", name, len(got))
		}
	}

	// A malformed issues field is a broken selection, not a fall-through to
	// criteria: the state beside it must not rescue it into admitting the world.
	if got := clausesFor(map[string]string{"connector": "github", "issues": "abc", "state": "open"}); len(got) != 0 {
		t.Errorf("a broken selection fell through to criteria: %+v", got[0])
	}
}

// A clause is a scope granted under a particular charter, and admission must
// check that anchor rather than only who signed it.
//
// Operator standing says this actor may state clauses somewhere. It does not
// say this clause belongs to the doorstep this run is acting under. Without the
// basis check, a clause written under one charter — or under a charter since
// withdrawn — is honoured beneath any other charter the run happens to name,
// which is a way to widen admission without stating anything new.
func TestAClauseMustRestOnTheCharterBeingActedUnder(t *testing.T) {
	const otherCharter = "git:sha1:g#git:sha1:other-charter"

	mine := clauseSource("hugh", map[string]string{"issues": "7"})
	elsewhere := ClauseSource{
		Event: "git:sha1:g#git:sha1:elsewhere", Actor: "hugh",
		Body:  map[string]string{ClauseKey: ClauseConnector, "issues": "8"},
		Bases: []string{otherCharter}, Effective: true,
	}

	reading := under([]ClauseSource{mine, elsewhere}, operator)
	if len(reading.Clauses) != 1 {
		t.Fatalf("admitted %d clauses, want only the one anchored to this charter: %+v", len(reading.Clauses), reading)
	}
	if reading.Clauses[0].Numbers[0] != 7 {
		t.Errorf("admitted the clause belonging to another charter: %+v", reading.Clauses[0])
	}
	if len(reading.Refusals) != 1 || reading.Refusals[0].Event != elsewhere.Event {
		t.Fatalf("refusals = %+v, want the foreign clause named", reading.Refusals)
	}
	if !strings.Contains(reading.Refusals[0].Reason, "charter") {
		t.Errorf("the refusal does not say why: %q", reading.Refusals[0].Reason)
	}
}

// An intermediate basis is not a grant, and this expectation used to say the
// opposite.
//
// A rests_on edge records that an act bears on another; nothing in it delegates
// a charter's admission scope. Following arbitrary ancestry invents a meaning
// the signer never expressed, and in a workroom almost everything is
// transitively connected to almost everything else, so the walk would have
// admitted clauses granted under no charter at all. The charter must be cited
// outright.
func TestAnIntermediateBasisIsNotAGrant(t *testing.T) {
	const intermediate = "git:sha1:g#git:sha1:intermediate"
	indirect := ClauseSource{
		Event: "git:sha1:g#git:sha1:indirect", Actor: "hugh",
		Body:  map[string]string{ClauseKey: ClauseConnector, "issues": "9"},
		Bases: []string{intermediate}, Effective: true,
	}
	reading := ClausesFrom([]ClauseSource{indirect}, operator, testCharter)
	if len(reading.Clauses) != 0 {
		t.Fatalf("a clause that never cites this charter was admitted: %+v", reading)
	}
	if len(reading.Refusals) != 1 || !strings.Contains(reading.Refusals[0].Reason, "charter") {
		t.Fatalf("refusals = %+v, want the unanchored clause named", reading.Refusals)
	}
}

// The fold keeps refused acts in Statements, so presence is not force. An
// operator-signed clause the workroom rejected must not open the door.
func TestAnIneffectiveClauseIsRefused(t *testing.T) {
	refused := clauseSource("hugh", map[string]string{"issues": "7"})
	refused.Effective = false

	reading := ClausesFrom([]ClauseSource{refused}, operator, testCharter)
	if len(reading.Clauses) != 0 {
		t.Fatalf("a clause the fold gave no force was admitted: %+v", reading)
	}
	if len(reading.Refusals) != 1 || !strings.Contains(reading.Refusals[0].Reason, "force") {
		t.Fatalf("refusals = %+v, want the ineffective clause named", reading.Refusals)
	}
}

// An unnamed charter anchors nothing, so nothing is admitted. Failing closed
// here matters because the alternative is that forgetting the flag silently
// admits every clause in the log.
func TestNoCharterAdmitsNothing(t *testing.T) {
	reading := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"issues": "7"})}, operator, "")
	if len(reading.Clauses) != 0 {
		t.Fatalf("clauses were admitted with no charter named: %+v", reading)
	}
}
