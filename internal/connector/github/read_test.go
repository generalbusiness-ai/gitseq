package github

import "testing"

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
	clauses := ClausesFrom(sources, operator)
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
	if clauses := ClausesFrom(sources, operator); len(clauses) != 0 {
		t.Errorf("a ratifier's clause was honoured")
	}
}

// Retiring a clause is how a bad admission is withdrawn. It has to stop
// admitting at once, not merely flare what it already let in.
func TestRetiredAndStaleClausesDoNotAdmit(t *testing.T) {
	retired := clauseSource("hugh", map[string]string{"issues": "7"})
	retired.Retired = true
	stale := clauseSource("hugh", map[string]string{"issues": "8"})
	stale.Stale = true

	if clauses := ClausesFrom([]ClauseSource{retired, stale}, operator); len(clauses) != 0 {
		t.Errorf("honoured %d retired or stale clauses, want none", len(clauses))
	}
}

// Statements that are not clauses are ignored, whoever signed them. An
// operator's ordinary assert must not accidentally admit anything.
func TestOrdinaryStatementsAreNotClauses(t *testing.T) {
	ordinary := ClauseSource{Event: "e", Actor: "hugh", Body: map[string]string{"text": "a note"}}
	otherConnector := ClauseSource{Event: "e2", Actor: "hugh", Body: map[string]string{ClauseKey: "slack", "issues": "7"}}
	if clauses := ClausesFrom([]ClauseSource{ordinary, otherConnector}, operator); len(clauses) != 0 {
		t.Errorf("honoured %d non-clauses, want none", len(clauses))
	}
}

// A selection clause names issues; a criteria clause describes them. Both
// parse from the same body shape.
func TestClausesParseBothForms(t *testing.T) {
	selection := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"issues": " 12345 , 678 "})}, operator)
	if len(selection) != 1 || len(selection[0].Numbers) != 2 {
		t.Fatalf("selection parsed as %+v", selection)
	}
	if !selection[0].Selection() {
		t.Error("a clause naming issues did not read as a selection")
	}

	criteria := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"state": "open", "labels": "bug, ui"})}, operator)
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
	clauses := ClausesFrom([]ClauseSource{clauseSource("hugh", map[string]string{"issues": "7, banana, -3, 0"})}, operator)
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
	if clauses := ClausesFrom(nil, operator); len(clauses) != 0 {
		t.Errorf("got %d clauses from nothing", len(clauses))
	}
}
