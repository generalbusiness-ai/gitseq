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

func clauseSource(actor string, body map[string]string) ClauseSource {
	body[ClauseKey] = ClauseConnector
	return ClauseSource{Event: "git:sha1:g#git:sha1:" + actor, Actor: actor, Body: body}
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
	clauses := ClausesFrom(sources, operator).Clauses
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
	if clauses := ClausesFrom(sources, operator).Clauses; len(clauses) != 0 {
		t.Errorf("a ratifier's clause was honoured")
	}
}

// Retirement and staleness are different facts and the connector must not
// treat them alike.
//
// Retiring a clause is how a bad admission is withdrawn, and it has to stop
// admitting at once rather than merely flare what it already let in. Staleness
// says a basis underneath the clause moved, which is not a withdrawal. Refusing
// on staleness made the sanctioned way to replace a charter self-defeating: the
// replacement must cite what it replaces, what it replaces is then retired, and
// the successor is stale by construction — so governing correctly turned the
// connector off with no way back.
func TestRetirementRefusesButStalenessAdmitsWithNotice(t *testing.T) {
	retired := clauseSource("hugh", map[string]string{"issues": "7"})
	retired.Retired = true
	stale := clauseSource("hugh", map[string]string{"issues": "8"})
	stale.Stale = true

	reading := ClausesFrom([]ClauseSource{retired, stale}, operator)
	if len(reading.Clauses) != 1 {
		t.Fatalf("admitted %d clauses, want exactly the stale one", len(reading.Clauses))
	}
	if reading.Clauses[0].Numbers[0] != 8 {
		t.Errorf("admitted the wrong clause: %+v", reading.Clauses[0])
	}
	if !reading.Clauses[0].Stale {
		t.Error("the admitted clause does not carry its staleness, so a reader cannot see the ground moved")
	}
	if len(reading.Refusals) != 1 || reading.Refusals[0].Event != retired.Event {
		t.Fatalf("refusals = %+v, want exactly the retired clause named", reading.Refusals)
	}
	if !strings.Contains(reading.Refusals[0].Reason, "retired") {
		t.Errorf("refusal does not say why: %q", reading.Refusals[0].Reason)
	}
}

// The live workroom's own shape, which is what sent me to file a false request
// against the operator. The scoped charter was replaced correctly, so it and
// every clause resting on it are stale and none is retired. The connector
// reported "no live admission clause" while looking straight at ratified,
// correctly scoped governance.
func TestTheWorkroomsOwnStaleCharterShapeAdmits(t *testing.T) {
	clause := clauseSource("hugh", map[string]string{"issues": "1"})
	clause.Stale = true // rests on a charter that replaced a retired predecessor

	reading := ClausesFrom([]ClauseSource{clause}, operator)
	if len(reading.Clauses) != 1 {
		t.Fatalf("a live ratified clause was refused for being stale: %+v", reading)
	}
	if !reading.Clauses[0].Stale {
		t.Error("staleness was dropped instead of reported")
	}
}

// A refusal must say which statement it refused and why. Silence here is what
// made "the operator stated nothing" and "I threw away what the operator
// stated" the same message.
func TestRefusalsNameTheClauseAndTheReason(t *testing.T) {
	unauthorized := clauseSource("mallory", map[string]string{"issues": "7"})
	unbounded := clauseSource("hugh", map[string]string{"issues": "banana"})

	reading := ClausesFrom([]ClauseSource{unauthorized, unbounded}, operator)
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
	if clauses := ClausesFrom([]ClauseSource{ordinary, otherConnector}, operator).Clauses; len(clauses) != 0 {
		t.Errorf("honoured %d non-clauses, want none", len(clauses))
	}
}

// A selection clause names issues; a criteria clause describes them. Both
// parse from the same body shape.
func TestClausesParseBothForms(t *testing.T) {
	selection := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"issues": " 12345 , 678 "})}, operator).Clauses
	if len(selection) != 1 || len(selection[0].Numbers) != 2 {
		t.Fatalf("selection parsed as %+v", selection)
	}
	if !selection[0].Selection() {
		t.Error("a clause naming issues did not read as a selection")
	}

	criteria := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"state": "open", "labels": "bug, ui"})}, operator).Clauses
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
	clauses := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"issues": "7, banana, -3, 0"})}, operator).Clauses
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
	if clauses := ClausesFrom(nil, operator).Clauses; len(clauses) != 0 {
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
		return ClausesFrom([]ClauseSource{{Event: "e", Actor: "op", Body: body}}, operators).Clauses
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
