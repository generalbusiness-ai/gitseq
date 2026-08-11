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
		clause, bounded := parseClause(source)
		if !bounded {
			continue
		}
		clauses = append(clauses, clause)
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

// parseClause reads one clause body, reporting whether it bounds anything.
//
// An unbounded body must produce no clause at all, and this is the sharpest
// edge in the connector. A body marked for this connector but naming no valid
// issue and no criterion used to parse into an empty criteria clause, whose
// query renders `state=all` and whose Admits accepts everything — so a typo in
// an issue number, or a body somebody left half-written, would quietly list the
// entire tracker and admit all of it. That is precisely the unbounded read the
// doorstep exists to prevent, arriving through the doorstep itself.
//
// So the rule is that a clause must ask for something nameable: at least one
// valid issue number, or at least one label, or a state it actually states. A
// body that fails to say any of those is not a conservative clause, it is a
// clause that means everything, and it is refused.
func parseClause(source ClauseSource) (Clause, bool) {
	clause := Clause{
		Event:  source.Event,
		State:  strings.TrimSpace(source.Body["state"]),
		Labels: splitList(source.Body["labels"]),
	}

	// An issues field that was meant to name issues and named none is a broken
	// selection, not an invitation to fall through to criteria. Widening on the
	// way past a malformed field is how a typo becomes the whole tracker.
	if listed := splitList(source.Body["issues"]); len(listed) > 0 {
		for _, field := range listed {
			if number, err := strconv.Atoi(field); err == nil && number > 0 {
				clause.Numbers = append(clause.Numbers, number)
			}
		}
		return clause, len(clause.Numbers) > 0
	}

	// A state the connector does not understand is refused rather than passed
	// to GitHub, which would decide for itself what an unknown value means.
	switch clause.State {
	case "", "open", "closed", "all":
	default:
		return Clause{}, false
	}
	return clause, clause.State != "" || len(clause.Labels) > 0
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
