package workroom

import (
	"strings"
	"testing"
)

// Every lifecycle word the fold emits, and where it lands. The list is the
// contract: a word missing from it would be counted in no population, and the
// populations would stop adding up to the commitments.
func TestEveryEmittedLifecycleLandsInOneNamedPopulation(t *testing.T) {
	lanes := map[string]string{
		"open": "open", "promised": "open", "reported": "open",
		"awaiting-review": "open", "awaiting-authorization": "open", "awaiting-landing": "open",
		"satisfied":  "completed",
		"superseded": "closed", "cancelled": "closed", "reneged": "closed",
		"withdrawn": "closed", "abandoned": "closed",
		"stale": "stale",
	}
	for status, want := range lanes {
		projection := Projection{Commitments: []Commitment{{Request: "r", Status: status}}}
		summary := WorkOf(projection)
		got := map[string]int{
			"open": summary.Open, "completed": summary.Completed,
			"closed": summary.ClosedNotCompleted, "stale": summary.Stale, "other": summary.Other,
		}
		if got[want] != 1 {
			t.Errorf("%q was not counted as %s: %+v", status, want, summary)
		}
		if summary.Open+summary.Completed+summary.ClosedNotCompleted+summary.Stale+summary.Other != 1 {
			t.Errorf("%q was counted in more than one population: %+v", status, summary)
		}
		if want == "open" && summary.OpenLifecycles[status] != 1 {
			t.Errorf("%q is open but its lifecycle lane is %d", status, summary.OpenLifecycles[status])
		}
	}
	if len(lanes) != 13 {
		t.Fatalf("the fold emits 13 lifecycle words; this test covers %d", len(lanes))
	}
}

// A word the fold learns to emit later must be visibly uncounted rather than
// quietly dropped: the total is what every other number is read against.
func TestAnUnknownLifecycleIsCountedAsOtherAndKeepsTheTotalTrue(t *testing.T) {
	summary := WorkOf(Projection{Commitments: []Commitment{{Status: "open"}, {Status: "invented-later"}}})
	if summary.Other != 1 || summary.Open != 1 || summary.Commitments != 2 {
		t.Fatalf("unknown lifecycle mishandled: %+v", summary)
	}
	if !strings.Contains(RenderWork(summary), "1 in no named population") {
		t.Errorf("the rendered line hides the uncounted commitment:\n%s", RenderWork(summary))
	}
}

// The three rules the populations are held to, driven together: the subsets
// overlap the partition and never extend it, and ordinary staleness on a
// finished row is not "reasoning moved" work.
func TestSubsetsOverlapThePartitionWithoutExtendingIt(t *testing.T) {
	summary := WorkOf(Projection{Commitments: []Commitment{
		{Status: "awaiting-review", Stale: true},
		{Status: "awaiting-landing", ApprovedNotLanded: true},
		{Status: "satisfied", Stale: true, ApprovedNotLanded: true},
		{Status: "stale", Stale: true},
	}})
	if summary.Commitments != 4 || summary.Open != 2 || summary.Completed != 1 || summary.Stale != 1 {
		t.Fatalf("partition wrong: %+v", summary)
	}
	if summary.ReasoningMoved != 1 {
		t.Errorf("reasoning moved = %d; only the open stale row qualifies", summary.ReasoningMoved)
	}
	if summary.ArtifactLandingAudit != 2 {
		t.Errorf("landing audit = %d; it covers finished rows as well as live ones", summary.ArtifactLandingAudit)
	}
	if summary.Open+summary.Completed+summary.ClosedNotCompleted+summary.Stale+summary.Other != summary.Commitments {
		t.Errorf("subsets leaked into the partition: %+v", summary)
	}
}

// Two counts, one word. "open" is a lifecycle the fold writes on one row and
// the name of the population holding six of them, so a page has to print both
// numbers.
func TestRenderedWorkKeepsLifecycleOpenDistinctFromGroupedOpen(t *testing.T) {
	summary := WorkOf(Projection{Commitments: []Commitment{
		{Status: "open"}, {Status: "reported"}, {Status: "awaiting-review"}, {Status: "awaiting-review"},
	}})
	rendered := RenderWork(summary)
	if !strings.Contains(rendered, "Work, workroom: 4 open,") {
		t.Errorf("grouped open is not named as the workroom's own count:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Open by lifecycle: open 1, promised 0, reported 1, awaiting-review 2, awaiting-authorization 0, awaiting-landing 0.") {
		t.Errorf("the lifecycle breakdown is missing or incomplete:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Acts awaiting ratification are a separate duty and are not in the commitment total.") {
		t.Errorf("the rendered line does not hold ratification outside the total:\n%s", rendered)
	}
	if summary.Scope != WorkScopeWorkroom {
		t.Errorf("scope = %q; a count that does not say what it covers can be read as any other count", summary.Scope)
	}
}

// A proposal awaiting ratification is owed by a role holder, not a performer.
// It is a real queue and it is counted elsewhere; putting it here would make
// the commitment total a number nobody could act on.
func TestRatificationIsNotACommitmentPopulation(t *testing.T) {
	projection := Projection{
		Statements:  []Statement{{Event: "proposal", Kind: KindPropose, Satisfier: "role:ratifier"}},
		Commitments: []Commitment{{Request: "r", Status: "open"}},
	}
	summary := WorkOf(projection)
	if summary.Commitments != 1 || summary.Open != 1 {
		t.Fatalf("an unratified proposal moved a commitment count: %+v", summary)
	}
}

// The complete page prints the same two lines the bounded page prints, which
// is the only reason holding two surfaces side by side settles anything.
func TestCompletePageCarriesTheSameWorkLines(t *testing.T) {
	projection := Projection{Commitments: []Commitment{{Request: "r", Status: "promised"}, {Request: "r2", Status: "satisfied"}}}
	page := string(RenderStatus(projection))
	for _, line := range strings.Split(strings.TrimRight(RenderWork(WorkOf(projection)), "\n"), "\n") {
		if !strings.Contains(page, line) {
			t.Errorf("the complete page is missing %q:\n%s", line, page)
		}
	}
}
