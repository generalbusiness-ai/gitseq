package statusview

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// One board at one frontier, carrying a row in every lane the populations name.
func workProjection() workroom.Projection {
	return workroom.Projection{
		Statements: []workroom.Statement{
			{Event: "r1", Kind: workroom.KindRequest, Text: "Run the tests"},
			{Event: "r2", Kind: workroom.KindRequest, Text: "Ship the counter"},
		},
		Commitments: []workroom.Commitment{
			{Request: "r1", Status: "open", AddressedTo: "bob"},
			{Request: "r2", Status: "reported", Performer: "bob"},
			{Request: "r2", Status: "awaiting-review", Performer: "carol", Stale: true},
			{Request: "r2", Status: "awaiting-landing", Performer: "carol", ApprovedNotLanded: true},
			{Request: "r1", Status: "satisfied"},
			{Request: "r1", Status: "withdrawn"},
			{Request: "r1", Status: "stale", Stale: true},
		},
	}
}

// The bounded page is what `gs status` prints and what the resident summary
// carries. It has to say both numbers: the lifecycle words underneath, and the
// named populations a reader compares with the board.
func TestBoundedStatusCarriesTheSharedWorkCountsAndTheLifecycleDiagnostic(t *testing.T) {
	projection := workProjection()
	summary := Build("genesis", "head", 7, projection)
	if !reflect.DeepEqual(summary.Totals.Work, workroom.WorkOf(projection)) {
		t.Fatalf("the bounded totals do not carry the shared count: %+v", summary.Totals.Work)
	}
	if summary.Totals.Work.Open != 4 || summary.Totals.Commitments["open"] != 1 {
		t.Fatalf("work open %d, lifecycle open %d; the two facts must both survive", summary.Totals.Work.Open, summary.Totals.Commitments["open"])
	}
	// The landing audit is counted once, by the owner, and read from there.
	if summary.Totals.ApprovedNotLanded != summary.Totals.Work.ArtifactLandingAudit || summary.Totals.ApprovedNotLanded != 1 {
		t.Fatalf("landing audit disagrees with itself: %d and %d", summary.Totals.ApprovedNotLanded, summary.Totals.Work.ArtifactLandingAudit)
	}
	rendered := string(Render(summary, "verified local"))
	if !strings.Contains(rendered, "Work, workroom: 4 open, 1 completed, 1 closed and not completed, 1 stale and not in flight, of 7 commitments.") {
		t.Errorf("the bounded page does not print the named populations:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Open by lifecycle: open 1, promised 0, reported 1, awaiting-review 1, awaiting-authorization 0, awaiting-landing 1.") {
		t.Errorf("the bounded page does not print the open breakdown:\n%s", rendered)
	}
	if !strings.Contains(rendered, "1 open resting on reasoning that moved, 1 under an artifact landing audit") {
		t.Errorf("the bounded page does not print the overlapping counts:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Commitments: awaiting-landing 1, awaiting-review 1 (1 stale), open 1,") {
		t.Errorf("the bounded page lost the lifecycle totals line:\n%s", rendered)
	}
	// The wire keeps its shape and gains a field; nothing was renamed away.
	encoded, err := json.Marshal(summary.Totals)
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"approved_not_landed", "commitments", "artifacts", "statements", "work"} {
		if _, ok := round[field]; !ok {
			t.Errorf("the bounded totals no longer emit %q", field)
		}
	}
}

// A summary decoded from a resident that predates these counts carries none,
// and the page must say so instead of printing a confident row of zeros.
func TestABoundedSummaryWithoutWorkCountsSaysSoRatherThanPrintingZeros(t *testing.T) {
	summary := Build("genesis", "head", 7, workProjection())
	summary.Totals.Work = workroom.WorkSummary{}
	rendered := string(Render(summary, "resident summary"))
	if !strings.Contains(rendered, "Work populations: not reported by this resident") {
		t.Errorf("an older resident's answer was rendered as real counts:\n%s", rendered)
	}
	if strings.Contains(rendered, "Work, workroom: 0 open") {
		t.Errorf("zeros were printed as if they were counted:\n%s", rendered)
	}
}

// The MCP status and wait answers list one actor's lanes and total the whole
// board. Both totals now carry the same populations every other surface
// prints, and they say in `scope` that they are workroom-wide.
func TestActorTotalsCarryTheWorkroomWideWorkCounts(t *testing.T) {
	projection := workProjection()
	durable := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 7, Projection: projection}
	want := workroom.WorkOf(projection)
	status := BuildActorStatus(durable, nexus.Snapshot{}, Cursor{}, nil, "bob", "bob", false)
	if !reflect.DeepEqual(status.Totals.Work, want) || status.Totals.Work.Scope != workroom.WorkScopeWorkroom {
		t.Errorf("MCP status totals: %+v", status.Totals.Work)
	}
	if status.Totals.ApprovedNotLanded != want.ArtifactLandingAudit {
		t.Errorf("MCP status landing audit = %d, want %d", status.Totals.ApprovedNotLanded, want.ArtifactLandingAudit)
	}
	delta := BuildWait(durable, Cursor{}, nil, false, Cursor{}, nil, "bob", "bob", false)
	if !reflect.DeepEqual(delta.Totals.Work, want) {
		t.Errorf("MCP wait totals: %+v", delta.Totals.Work)
	}
	// The lists are the actor's; the counts are the board's, and Scope says so.
	if len(status.WaitingOnYou) == want.Open {
		t.Errorf("an actor lane happened to equal the board count; pick a fixture where the two differ")
	}
}
