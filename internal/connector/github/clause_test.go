package github

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

func open7() Issue {
	return Issue{Owner: "o", Repo: "r", Number: 7, Title: "t", Author: "someone", State: "open"}
}

// reader records what was asked of GitHub, not merely what came back. The
// finding that made this necessary was invisible to a test that only checked
// results: the connector produced the right observations while reading the
// whole tracker to get them.
type reader struct {
	issues  map[int]Issue
	queries []url.Values
	numbers []int
}

func (r *reader) List(_ context.Context, _, _ string, query url.Values) ([]Issue, error) {
	r.queries = append(r.queries, query)
	listed := make([]Issue, 0, len(r.issues))
	for _, issue := range r.issues {
		listed = append(listed, issue)
	}
	return listed, nil
}

func (r *reader) Number(_ context.Context, _, _ string, number int) (Issue, bool, error) {
	r.numbers = append(r.numbers, number)
	issue, found := r.issues[number]
	return issue, found, nil
}

func fetch(t *testing.T, source *reader, clauses ...Clause) ([]Observation, []int) {
	t.Helper()
	observations, missing, err := Fetch(context.Background(), source, "o", "r", clauses)
	if err != nil {
		t.Fatal(err)
	}
	return observations, missing
}

// The property that makes the doorstep safe at scale: with no clause the
// connector admits nothing — and, just as importantly, reads nothing. A
// repository with a hundred thousand issues costs nothing until somebody asks
// for something.
func TestNoClauseReadsNothingAndAdmitsNothing(t *testing.T) {
	for _, clauses := range [][]Clause{nil, {}} {
		source := &reader{issues: map[int]Issue{7: open7()}}
		admitted, _ := fetch(t, source, clauses...)
		if len(admitted) != 0 {
			t.Errorf("admitted %d issues with no clause, want none", len(admitted))
		}
		if len(source.queries) != 0 || len(source.numbers) != 0 {
			t.Errorf("read GitHub with no clause: %d lists, %d single reads", len(source.queries), len(source.numbers))
		}
	}
}

// "Please pick up issue #7" admits that issue and nothing else, and costs one
// request for the issue it names rather than a walk of the tracker.
func TestSelectionReadsOnlyWhatItNames(t *testing.T) {
	other := open7()
	other.Number = 8
	source := &reader{issues: map[int]Issue{7: open7(), 8: other}}

	admitted, _ := fetch(t, source, Clause{Event: "git:sha1:g#git:sha1:sel", Numbers: []int{7}})
	if len(admitted) != 1 {
		t.Fatalf("admitted %d, want 1", len(admitted))
	}
	if admitted[0].ExternalID != "o/r#7" {
		t.Errorf("admitted %q, want o/r#7", admitted[0].ExternalID)
	}
	if len(source.queries) != 0 {
		t.Errorf("a selection clause listed the repository %d times; it should ask for what it named", len(source.queries))
	}
	if len(source.numbers) != 1 || source.numbers[0] != 7 {
		t.Errorf("read issues %v, want exactly [7]", source.numbers)
	}
}

// A clause may name an issue that was deleted or never existed. That is worth
// reporting and is not a failure: refusing to observe anything because one
// named issue is gone would make an unrelated deletion stop the connector.
func TestSelectionReportsWhatGitHubDidNotReturn(t *testing.T) {
	source := &reader{issues: map[int]Issue{7: open7()}}
	admitted, missing := fetch(t, source, Clause{Event: "c", Numbers: []int{7, 404}})
	if len(admitted) != 1 {
		t.Errorf("admitted %d, want the one issue that exists", len(admitted))
	}
	if len(missing) != 1 || missing[0] != 404 {
		t.Errorf("missing = %v, want [404]", missing)
	}
}

// Every observation records the clause that let it in, and carries it in the
// body, because retiring the clause has to be able to flare what it admitted.
func TestObservationRecordsTheAdmittingClause(t *testing.T) {
	clause := Clause{Event: "git:sha1:g#git:sha1:sel", Numbers: []int{7}}
	admitted, _ := fetch(t, &reader{issues: map[int]Issue{7: open7()}}, clause)
	if len(admitted) != 1 {
		t.Fatal("nothing admitted")
	}
	if admitted[0].AdmittedBy != clause.Event {
		t.Errorf("AdmittedBy is %q, want the clause event", admitted[0].AdmittedBy)
	}
	if admitted[0].Body["admitted_by"] != clause.Event {
		t.Errorf("body admitted_by is %q, want the clause event", admitted[0].Body["admitted_by"])
	}
}

// Criteria are conjunctive. A clause that widened as you added detail would be
// a poor instrument for bounding an attack surface.
//
// Labels come from the issue itself. They used to arrive in a separate map the
// command never supplied, which made every label criterion admit nothing at all
// while still looking correct here.
func TestCriteriaAreConjunctive(t *testing.T) {
	clause := Clause{Event: "c", State: "open", Labels: []string{"bug", "confirmed"}}

	partial := open7()
	partial.Labels = []string{"bug"}
	if admitted, _ := fetch(t, &reader{issues: map[int]Issue{7: partial}}, clause); len(admitted) != 0 {
		t.Error("admitted an issue matching only one of two labels")
	}

	full := open7()
	full.Labels = []string{"bug", "confirmed", "ui"}
	if admitted, _ := fetch(t, &reader{issues: map[int]Issue{7: full}}, clause); len(admitted) != 1 {
		t.Error("did not admit an issue carrying both labels")
	}
}

// The local re-check is the authority. GitHub filtering the list is
// convenience; a filter applied only at the far end is one somebody else
// controls, so a server that returns more than was asked for must not widen
// what the clause admits.
func TestTheLocalCheckStillBoundsAGenerousServer(t *testing.T) {
	unlabelled := open7()
	closed := open7()
	closed.Number, closed.State = 8, "closed"
	source := &reader{issues: map[int]Issue{7: unlabelled, 8: closed}}

	admitted, _ := fetch(t, source, Clause{Event: "c", State: "open", Labels: []string{"bug"}})
	if len(admitted) != 0 {
		t.Errorf("admitted %d issues the clause does not match", len(admitted))
	}
}

// A closed issue does not satisfy a clause asking for open ones.
func TestCriteriaRespectState(t *testing.T) {
	clause := Clause{Event: "c", State: "open"}
	closed := open7()
	closed.State = "closed"
	if admitted, _ := fetch(t, &reader{issues: map[int]Issue{7: closed}}, clause); len(admitted) != 0 {
		t.Error("a closed issue satisfied an open-only clause")
	}
	if admitted, _ := fetch(t, &reader{issues: map[int]Issue{7: open7()}}, clause); len(admitted) != 1 {
		t.Error("an open issue did not satisfy an open-only clause")
	}
}

// Filtering belongs at the source, so a criteria clause has to reach GitHub as
// list parameters rather than being applied after fetching everything.
func TestCriteriaReachGitHubAsAQuery(t *testing.T) {
	source := &reader{issues: map[int]Issue{}}
	fetch(t, source, Clause{Event: "c", State: "open", Labels: []string{"ui", "bug"}})
	if len(source.queries) != 1 {
		t.Fatalf("made %d list requests, want 1", len(source.queries))
	}
	if got := source.queries[0].Get("state"); got != "open" {
		t.Errorf("state is %q", got)
	}
	// Sorted, so the same clause always produces the same request.
	if got := source.queries[0].Get("labels"); got != "bug,ui" {
		t.Errorf("labels is %q, want a stable sorted list", got)
	}
	if got := (Clause{}).Query().Get("state"); got != "all" {
		t.Errorf("an unstated state became %q, want all", got)
	}
}

// An issue matching two clauses is observed once, recording the first that
// admitted it. A second act would be a replay anyway; the record should say
// what actually caused the observation.
func TestOverlappingClausesObserveOnce(t *testing.T) {
	source := &reader{issues: map[int]Issue{7: open7()}}
	admitted, _ := fetch(t, source,
		Clause{Event: "first", Numbers: []int{7}},
		Clause{Event: "second", State: "open"})
	if len(admitted) != 1 {
		t.Fatalf("admitted %d, want 1", len(admitted))
	}
	if admitted[0].AdmittedBy != "first" {
		t.Errorf("recorded %q, want the first admitting clause", admitted[0].AdmittedBy)
	}
}

// A read that fails stops the run rather than being reported as an empty
// tracker. Treating a transport error as "nothing matched" would let an outage
// look like a repository nobody had filed anything in.
func TestAReadFailureStopsTheRun(t *testing.T) {
	_, _, err := Fetch(context.Background(), failing{}, "o", "r", []Clause{{Event: "c", State: "open"}})
	if err == nil {
		t.Fatal("a failed read was reported as success")
	}
}

type failing struct{}

func (failing) List(context.Context, string, string, url.Values) ([]Issue, error) {
	return nil, errors.New("github is down")
}

func (failing) Number(context.Context, string, string, int) (Issue, bool, error) {
	return Issue{}, false, errors.New("github is down")
}
