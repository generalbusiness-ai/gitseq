package github

import "testing"

func open7() Issue {
	return Issue{Owner: "o", Repo: "r", Number: 7, Title: "t", Author: "someone", State: "open"}
}

// The property that makes the doorstep safe at scale: with no clause, the
// connector admits nothing. A repository with a hundred thousand issues costs
// nothing until somebody asks for something.
func TestNoClauseAdmitsNothing(t *testing.T) {
	if got := Admit(nil, []Issue{open7()}, nil); len(got) != 0 {
		t.Fatalf("admitted %d issues with no clause, want none", len(got))
	}
	if got := Admit([]Clause{}, []Issue{open7()}, nil); len(got) != 0 {
		t.Fatalf("admitted %d issues with an empty clause list, want none", len(got))
	}
}

// "Please pick up issue #7" admits that issue and nothing else.
func TestSelectionAdmitsOnlyWhatItNames(t *testing.T) {
	clause := Clause{Event: "git:sha1:g#git:sha1:sel", Numbers: []int{7}}
	other := open7()
	other.Number = 8

	admitted := Admit([]Clause{clause}, []Issue{open7(), other}, nil)
	if len(admitted) != 1 {
		t.Fatalf("admitted %d, want 1", len(admitted))
	}
	if admitted[0].ExternalID != "o/r#7" {
		t.Errorf("admitted %q, want o/r#7", admitted[0].ExternalID)
	}
}

// Every observation records the clause that let it in, and carries it in the
// body, because retiring the clause has to be able to flare what it admitted.
func TestObservationRecordsTheAdmittingClause(t *testing.T) {
	clause := Clause{Event: "git:sha1:g#git:sha1:sel", Numbers: []int{7}}
	admitted := Admit([]Clause{clause}, []Issue{open7()}, nil)
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
func TestCriteriaAreConjunctive(t *testing.T) {
	clause := Clause{Event: "c", State: "open", Labels: []string{"bug", "confirmed"}}
	labels := map[int][]string{7: {"bug"}}

	if got := Admit([]Clause{clause}, []Issue{open7()}, labels); len(got) != 0 {
		t.Errorf("admitted an issue matching only one of two labels")
	}
	labels[7] = []string{"bug", "confirmed", "ui"}
	if got := Admit([]Clause{clause}, []Issue{open7()}, labels); len(got) != 1 {
		t.Errorf("did not admit an issue carrying both labels")
	}
}

// A closed issue does not satisfy a clause asking for open ones.
func TestCriteriaRespectState(t *testing.T) {
	clause := Clause{Event: "c", State: "open"}
	closed := open7()
	closed.State = "closed"
	if got := Admit([]Clause{clause}, []Issue{closed}, nil); len(got) != 0 {
		t.Error("a closed issue satisfied an open-only clause")
	}
	if got := Admit([]Clause{clause}, []Issue{open7()}, nil); len(got) != 1 {
		t.Error("an open issue did not satisfy an open-only clause")
	}
}

// Filtering belongs at the source, so a criteria clause has to render as
// GitHub list parameters rather than being applied after fetching everything.
func TestCriteriaRenderAsAServerSideQuery(t *testing.T) {
	query := Clause{State: "open", Labels: []string{"ui", "bug"}}.Query()
	if got := query.Get("state"); got != "open" {
		t.Errorf("state is %q", got)
	}
	// Sorted, so the same clause always produces the same request.
	if got := query.Get("labels"); got != "bug,ui" {
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
	first := Clause{Event: "first", Numbers: []int{7}}
	second := Clause{Event: "second", State: "open"}
	admitted := Admit([]Clause{first, second}, []Issue{open7()}, nil)
	if len(admitted) != 1 {
		t.Fatalf("admitted %d, want 1", len(admitted))
	}
	if admitted[0].AdmittedBy != "first" {
		t.Errorf("recorded %q, want the first admitting clause", admitted[0].AdmittedBy)
	}
}
