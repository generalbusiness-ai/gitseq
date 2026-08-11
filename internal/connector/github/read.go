package github

import (
	"slices"
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

	// Bases are the events this statement rests on, carried so admission can
	// check that a clause cites the charter being acted under.
	Bases []string

	// Effective is the fold's ruling. Projection.Statements deliberately holds
	// acts the fold refused, because the log records what was said and not only
	// what carried, so presence here is not force. Without this an
	// operator-signed clause the workroom rejected could still open the door.
	Effective bool
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
// Retired and stale clauses are both refused, and this fails closed on purpose.
//
// An earlier version admitted stale clauses, arguing that staleness only means
// a basis moved while retirement means withdrawal. The premise underneath that
// — that correct charter replacement makes a successor stale by construction —
// was false: a successor rests on stable current governance and the separate
// supersession links it to the predecessor. So nothing forced the widening, and
// the widening had a cost. The basis that moved beneath a clause may be exactly
// the scope an operator withdrew, and admitting anyway would let a clause keep
// letting foreign material in on authority that no longer stands.
//
// Retiring a clause is still how a bad admission is withdrawn, and it stops
// admitting at once rather than merely flaring what it already let in.
//
// What is kept from that attempt is the diagnosis, not the permissiveness: each
// refusal says which statement it refused and why, so an operator can repair a
// clause instead of being told there is none.
func ClausesFrom(sources []ClauseSource, authors map[string]Author, charter string) Reading {
	reading := Reading{Clauses: make([]Clause, 0, len(sources))}
	for _, source := range sources {
		if source.Body[ClauseKey] != ClauseConnector {
			continue
		}
		if !source.Effective {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "the fold gave it no force, so the workroom never granted this scope"})
			continue
		}
		if !underCharter(source, charter) {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "does not cite the charter this run acts under as a basis, so it bounds some other doorstep"})
			continue
		}
		if source.Retired {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "retired, so the admission was withdrawn"})
			continue
		}
		if source.Stale {
			reading.Refusals = append(reading.Refusals, Refusal{source.Event, "stale: a basis underneath it has moved, so its authority is no longer established; state a fresh clause on current governance"})
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
		reading.Clauses = append(reading.Clauses, clause)
	}
	return reading
}

// underCharter reports whether this clause was granted under the charter the
// run acts under.
//
// Operator standing on the author is not enough, and this gap was open until
// codex found it: a clause is a scope granted by a particular charter, so if
// admission only checks who signed it, a clause written under one charter — or
// under a charter since withdrawn — is honoured beneath any other charter the
// run happens to name.
//
// The charter must be a direct signed basis. An earlier version followed the
// whole ancestry, which was wrong for a reason worth keeping: a rests_on edge
// records that an act bears on another, and nothing in it delegates admission
// scope. Treating arbitrary ancestry as a grant invents a fallback meaning the
// signer never expressed, and almost everything in a workroom is transitively
// connected to almost everything else. Requiring the charter to be cited
// outright is the narrower contract and the smaller code.
func underCharter(source ClauseSource, charter string) bool {
	if charter == "" {
		return false
	}
	return slices.Contains(source.Bases, charter)
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
