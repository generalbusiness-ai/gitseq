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

// Refusal records one clause-shaped statement the connector declined, and why.
//
// Every refusal here is silent at the point it happens, and the sum of them
// used to be a single sentence saying no clause was live. That made "the
// operator has stated nothing" and "the operator stated something I threw
// away" the same message, which is how a live, ratified, correctly scoped
// charter came to be reported as absent. Naming what was considered costs one
// line of output and is the difference between an operator fixing a clause and
// an operator being told there isn't one.
type Refusal struct {
	Event  string
	Reason string
}

// Reading is what the connector made of the statements it was shown.
type Reading struct {
	Clauses  []Clause
	Refusals []Refusal
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
// Retired clauses are refused: retiring a clause is how a bad admission is
// withdrawn, and it has to stop admitting immediately rather than merely
// flaring what it already let in.
//
// Stale clauses are admitted, and this is the correction. Stale says a basis
// underneath the clause moved; retired says the clause was withdrawn. Refusing
// on staleness made the sanctioned way to replace a charter self-defeating,
// because a replacement must cite what it replaces and what it replaces is then
// retired — so the successor is stale by construction and the connector stopped
// admitting anything. Governing correctly turned the connector off, and no
// operator action could escape it, since a charter that cites nothing bounds
// nothing. The staleness is carried on the clause instead, so a reader can see
// that the ground moved and judge it, rather than having the clause disappear.
func ClausesFrom(sources []ClauseSource, authors map[string]Author) Reading {
	reading := Reading{Clauses: make([]Clause, 0, len(sources))}
	for _, source := range sources {
		if source.Body[ClauseKey] != ClauseConnector {
			continue
		}
		if source.Retired {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "retired, so the admission was withdrawn"})
			continue
		}
		if !authorized(authors[source.Actor]) {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "stated by an actor without operator standing"})
			continue
		}
		clause, bounded := parseClause(source)
		if !bounded {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "names no issue, label, or state, so it would mean everything"})
			continue
		}
		clause.Stale = source.Stale
		reading.Clauses = append(reading.Clauses, clause)
	}
	return reading
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
