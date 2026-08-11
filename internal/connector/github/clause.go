package github

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

// A Clause is what admits foreign work into the room. The connector observes
// nothing by default: with no live clause it reads nothing and appends nothing.
//
// This is the doorstep, and it asks "what did we ask for" rather than "who
// filed it". A stranger's issue is admitted because somebody with authority
// pointed at it or wrote criteria that match it — which needs no roster of
// trusted foreign accounts, and fails closed.
//
// Scale is the reason it has to work this way. Repositories exist with more
// than a hundred thousand issues; a connector that enumerates a tracker and
// then filters has already paid the cost and already read the hostile input.
type Clause struct {
	// Event is the durable act that stated this clause. Every observation the
	// clause admits records it and rests on it, so retiring the clause flares
	// everything it let in.
	Event string

	// Numbers names particular issues. A selection clause: "observe #12345".
	Numbers []int

	// State and Labels are standing criteria: "observe open issues labelled
	// bug". Empty State means any state.
	State  string
	Labels []string
}

// Selection reports whether this clause names issues explicitly rather than
// describing them.
func (c Clause) Selection() bool { return len(c.Numbers) > 0 }

// Query renders a criteria clause as GitHub list parameters.
//
// Filtering belongs at the source. Asking GitHub for the matching issues costs
// one bounded request; asking for everything and discarding most of it costs
// the whole tracker, which is exactly what a hundred-thousand-issue repository
// cannot afford. Admits below still re-checks locally, because the server
// deciding what matches is convenience, not the authority.
func (c Clause) Query() url.Values {
	query := url.Values{}
	state := c.State
	if state == "" {
		state = "all"
	}
	query.Set("state", state)
	if len(c.Labels) > 0 {
		labels := append([]string(nil), c.Labels...)
		sort.Strings(labels)
		query.Set("labels", strings.Join(labels, ","))
	}
	return query
}

// Admits reports whether this clause lets one issue in.
//
// A selection clause admits exactly the issues it names. A criteria clause
// admits what matches every criterion it states — conjunctive, because a
// clause that widened as you added detail to it would be a poor instrument for
// bounding an attack surface.
func (c Clause) Admits(issue Issue) bool {
	if c.Selection() {
		for _, number := range c.Numbers {
			if number == issue.Number {
				return true
			}
		}
		return false
	}
	if c.State != "" && c.State != "all" && c.State != issue.State {
		return false
	}
	for _, wanted := range c.Labels {
		if !contains(issue.Labels, wanted) {
			return false
		}
	}
	return true
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// Reader is the part of the GitHub client Fetch needs. Naming it here keeps the
// admission logic testable without a transport, and keeps this package honest
// that the clause decides the read rather than the client.
type Reader interface {
	List(ctx context.Context, owner, repo string, query url.Values) ([]Issue, error)
	Number(ctx context.Context, owner, repo string, number int) (Issue, bool, error)
}

// Fetch reads only what the clauses ask for and returns the observations they
// admit, along with any issue numbers a selection clause named that GitHub did
// not return.
//
// The bound is the point. A selection clause costs one request per issue it
// names; a criteria clause costs one bounded walk of what matches. Neither
// enumerates the tracker, so a repository's cost is set by what somebody with
// authority asked for rather than by how many issues strangers have filed.
//
// Admits still re-checks every issue locally. The server deciding what matches
// is convenience; the clause is the authority, and a filter applied only at the
// far end is a filter somebody else controls.
//
// An issue admitted by more than one clause is observed once, attributed to the
// first clause that admitted it. The idempotency key is derived from the
// external identifier, so a second act would be a replay anyway; naming one
// clause keeps the record honest about what actually caused the observation
// rather than listing every clause that could have.
func Fetch(ctx context.Context, reader Reader, owner, repo string, clauses []Clause) ([]Observation, []int, error) {
	var observations []Observation
	var missing []int
	seen := make(map[int]bool)

	for _, clause := range clauses {
		var candidates []Issue
		if clause.Selection() {
			for _, number := range clause.Numbers {
				issue, found, err := reader.Number(ctx, owner, repo, number)
				if err != nil {
					return nil, nil, err
				}
				if !found {
					missing = append(missing, number)
					continue
				}
				candidates = append(candidates, issue)
			}
		} else {
			listed, err := reader.List(ctx, owner, repo, clause.Query())
			if err != nil {
				return nil, nil, err
			}
			candidates = listed
		}
		for _, issue := range candidates {
			if seen[issue.Number] || !clause.Admits(issue) {
				continue
			}
			seen[issue.Number] = true
			observation := ObserveIssue(issue)
			observation.AdmittedBy = clause.Event
			observation.Body["admitted_by"] = clause.Event
			observations = append(observations, observation)
		}
	}
	return observations, missing, nil
}
