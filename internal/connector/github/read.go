package github

import (
	"strconv"
	"strings"
)

// ClauseKey is the body field that marks a durable act as an admission clause
// for this connector. A clause is an ordinary statement; nothing in the fold
// knows what a clause is, so the connector recognizes its own by structure.
const ClauseKey = "connector"

// ClauseConnector is the value ClauseKey carries for the GitHub connector.
const ClauseConnector = "github"

// Author is what the connector needs to know about whoever stated a clause.
type Author struct {
	Fingerprint string
	Roles       []string
}

// ClauseSource is a durable statement the connector considers as a clause.
type ClauseSource struct {
	Event   string
	Actor   string
	Body    map[string]string
	Stale   bool
	Retired bool
}

// ClausesFrom reads the live admission clauses out of durable statements.
//
// This is the connector's half of the doorstep, and it is deliberately
// suspicious. The charter fixes who may state a clause — an actor holding
// `operator` — but the fold does not enforce charters, so a statement that
// looks like a clause is not one until the connector has checked who signed it.
// A connector that honoured any well-formed body would let any participant
// widen its own admission.
//
// Retired and stale clauses are skipped. Retiring a clause is how a bad
// admission is withdrawn, and it has to stop admitting immediately rather than
// merely flaring what it already let in.
func ClausesFrom(sources []ClauseSource, authors map[string]Author) []Clause {
	clauses := make([]Clause, 0, len(sources))
	for _, source := range sources {
		if source.Body[ClauseKey] != ClauseConnector {
			continue
		}
		if source.Retired || source.Stale {
			continue
		}
		if !authorized(authors[source.Actor]) {
			continue
		}
		clauses = append(clauses, parseClause(source))
	}
	return clauses
}

// authorized reports whether this actor may state a clause. The charter says
// `operator` and no one else; ratifier is deliberately not enough, because a
// clause decides what foreign content enters the log.
func authorized(author Author) bool {
	for _, role := range author.Roles {
		if role == "operator" {
			return true
		}
	}
	return false
}

func parseClause(source ClauseSource) Clause {
	clause := Clause{
		Event:  source.Event,
		State:  strings.TrimSpace(source.Body["state"]),
		Labels: splitList(source.Body["labels"]),
	}
	for _, field := range splitList(source.Body["issues"]) {
		if number, err := strconv.Atoi(field); err == nil && number > 0 {
			clause.Numbers = append(clause.Numbers, number)
		}
	}
	return clause
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	list := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			list = append(list, trimmed)
		}
	}
	if len(list) == 0 {
		return nil
	}
	return list
}
