package statusview

import (
	"fmt"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// reviewRequest is a live request that names an artifact, which is what makes a
// request a review request. Whether the name resolves is a separate fact.
func reviewRequest(event, artifact string) workroom.Statement {
	return workroom.Statement{Event: event, Kind: workroom.KindRequest, Body: map[string]string{"artifact": artifact}}
}

func gateSnapshot() app.Snapshot {
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "artifact:live", Path: "cmd/gs", Commit: "aaaa"},
			{Event: "artifact:gone", Path: "cmd/gs", Commit: "bbbb", Retired: true},
		},
		Statements: []workroom.Statement{
			reviewRequest("request:settled", "artifact:live"),
			reviewRequest("request:open", "artifact:live"),
			// Two requests naming one artifact. A verdict on the first must not
			// settle the second.
			reviewRequest("request:sameartifact", "artifact:live"),
			// A reference the log cannot resolve: an unexpanded placeholder.
			reviewRequest("request:placeholder", "<artifact-event>"),
			// A request naming nothing is not a review request at all.
			{Event: "request:plain", Kind: workroom.KindRequest, Body: map[string]string{"conditions": "do the thing"}},
			// A retired request has been withdrawn and is nobody's queue.
			{Event: "request:retired", Kind: workroom.KindRequest, Retired: true, Body: map[string]string{"artifact": "artifact:live"}},
			// The fold refused this report. It carries an approved verdict in
			// its body and appears in no review row, so nothing may settle on
			// it.
			{Event: "report:refused", Kind: workroom.KindReport, Body: map[string]string{"verdict": "approved", "head": "cccc"}},
			{Event: "report:worldstale", Kind: workroom.KindReport, DescribesSupersededWorld: true, Body: map[string]string{"verdict": "approved"}},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:settled", Report: "report:effective", Status: "satisfied"},
			{Request: "request:open", Status: "promised"},
			{Request: "request:sameartifact", Status: "promised"},
			{Request: "request:placeholder", Status: "open"},
			// A request whose only report is one the fold refused is still
			// awaiting its first verdict.
			{Request: "request:refused", Report: "report:refused", Status: "reported"},
		},
		Reviews: []workroom.Review{
			{Report: "report:effective", Verdict: "changes-requested", Head: "dddd"},
			{Report: "report:approved", Verdict: "approved", Ratified: true, Head: "eeee", Artifact: "artifact:live"},
			{Report: "report:unratified", Verdict: "approved", Head: "ffff"},
			{Report: "report:retired", Verdict: "approved", Ratified: true, Retired: true, Head: "1111"},
			{Report: "report:worldstale", Verdict: "approved", Ratified: true, Head: "2222"},
			// The same head approved twice is one head to ask Git about.
			{Report: "report:again", Verdict: "approved", Ratified: true, Head: "eeee"},
		},
	}
	return app.Snapshot{Genesis: "genesis", Head: "gate-head", Depth: 12, Projection: projection}
}

func TestReviewGateCountsOnlyReviewRequests(t *testing.T) {
	gate, err := BuildReviewGate(gateSnapshot(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// settled, open, sameartifact, placeholder. Not the plain request, which
	// names no artifact, and not the retired one, which was withdrawn.
	if gate.Named != 4 {
		t.Fatalf("named %d, want 4: %v", gate.Named, gate.AwaitingRequests)
	}
}

// A report the fold refused must not release an irreversible step. This is the
// difference between reading projection.Reviews, which the fold populates from
// effective reports only, and filtering statements for a body carrying an
// approved verdict.
func TestARefusedReportSettlesNothing(t *testing.T) {
	snapshot := gateSnapshot()
	snapshot.Projection.Statements = append(snapshot.Projection.Statements,
		reviewRequest("request:refused", "artifact:live"))
	gate, err := BuildReviewGate(snapshot, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(gate.AwaitingRequests, "request:refused") {
		t.Fatalf("a request whose only report the fold refused was reported as settled: %v", gate.AwaitingRequests)
	}
}

// Settlement is keyed by the request event, not by the artifact a request
// names. Several requests can name one artifact, and a verdict on one of them
// settles only that one.
func TestSettlementIsKeyedByRequestNotByArtifact(t *testing.T) {
	gate, err := BuildReviewGate(gateSnapshot(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(gate.AwaitingRequests, "request:settled") {
		t.Fatalf("a request an effective verdict settled is still counted: %v", gate.AwaitingRequests)
	}
	for _, event := range []string{"request:open", "request:sameartifact"} {
		if !containsString(gate.AwaitingRequests, event) {
			t.Fatalf("%s names the same artifact as a settled request and was settled with it: %v", event, gate.AwaitingRequests)
		}
	}
}

// Either canonical verdict settles a review. Waiting cannot turn a
// changes-requested into an approval, so counting only approvals would leave
// every closed-with-changes review in the total for ever.
func TestAChangesRequestedVerdictSettlesToo(t *testing.T) {
	gate, err := BuildReviewGate(gateSnapshot(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Awaiting != 3 {
		t.Fatalf("awaiting %d, want 3 (settled by a changes-requested verdict is settled): %v", gate.Awaiting, gate.AwaitingRequests)
	}
}

// Adversarial: a request naming an event that resolves to nothing. It is
// counted, and the failure to resolve is reported as its own number, because a
// reference dropped for being unreadable is a queue reported quieter than it is.
func TestAnUnresolvableReferenceIsCountedNotDropped(t *testing.T) {
	gate, err := BuildReviewGate(gateSnapshot(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(gate.AwaitingRequests, "request:placeholder") {
		t.Fatalf("a request naming an unexpanded placeholder left the awaiting total: %v", gate.AwaitingRequests)
	}
	if gate.Unresolved != 1 || !containsString(gate.UnresolvedReferences, "request:placeholder") {
		t.Fatalf("unresolved %d %v, want the placeholder named", gate.Unresolved, gate.UnresolvedReferences)
	}
}

func TestARetiredArtifactReferenceIsUnresolved(t *testing.T) {
	snapshot := gateSnapshot()
	snapshot.Projection.Statements = append(snapshot.Projection.Statements,
		reviewRequest("request:withdrawnbasis", "artifact:gone"))
	gate, err := BuildReviewGate(snapshot, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(gate.UnresolvedReferences, "request:withdrawnbasis") {
		t.Fatalf("a request naming a retired artifact resolved anyway: %v", gate.UnresolvedReferences)
	}
}

// Only an approval that could still be acted on counts. Counting a retired
// approval, or one describing a superseded world, makes the precondition
// unreachable — and a gate that can never be satisfied teaches its operator to
// run the irreversible step anyway.
func TestApprovedHeadsAreOnlyTheOnesStillActionable(t *testing.T) {
	gate, err := BuildReviewGate(gateSnapshot(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.ApprovedHeads) != 1 || gate.ApprovedHeads[0] != "eeee" {
		t.Fatalf("approved heads %v, want only the ratified, live, current-world head once", gate.ApprovedHeads)
	}
}

// The two sample lists shorten and say so. The approved head list does not,
// because a head omitted from a gate is a head nobody asked Git about while the
// caller read "quiet".
func TestSamplesShortenButTheGateSubjectDoesNot(t *testing.T) {
	snapshot := gateSnapshot()
	for index := 0; index < 30; index++ {
		event := fmt.Sprintf("request:bulk-%02d", index)
		snapshot.Projection.Statements = append(snapshot.Projection.Statements, reviewRequest(event, "artifact:live"))
		snapshot.Projection.Commitments = append(snapshot.Projection.Commitments, workroom.Commitment{Request: event, Status: "open"})
		snapshot.Projection.Reviews = append(snapshot.Projection.Reviews, workroom.Review{
			Report: "report:bulk-" + event, Verdict: "approved", Ratified: true, Head: fmt.Sprintf("head-%02d", index)})
	}
	gate, err := BuildReviewGate(snapshot, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Awaiting != 33 || len(gate.AwaitingRequests) != 5 || gate.AwaitingOmitted != 28 {
		t.Fatalf("the awaiting sample did not report its own shortening: %d %d %d", gate.Awaiting, len(gate.AwaitingRequests), gate.AwaitingOmitted)
	}
	if len(gate.ApprovedHeads) != 31 {
		t.Fatalf("approved heads were capped to %d; a gate that omits a head can declare quiet with work outstanding", len(gate.ApprovedHeads))
	}
	if gate.Quiet() {
		t.Fatal("a gate with 33 requests awaiting a first verdict called itself quiet")
	}
}

func TestReviewGateRefusesAnOutOfRangeLimit(t *testing.T) {
	if _, err := BuildReviewGate(gateSnapshot(), ReviewListMax+1, false); err == nil {
		t.Fatal("an unbounded sample size was admitted")
	}
}

// Adversarial: nothing outstanding at all. The honest answer is a report saying
// zero, not an empty output a reader has to interpret.
func TestAQuietQueueStillReportsItself(t *testing.T) {
	empty := app.Snapshot{Genesis: "genesis", Head: "quiet", Depth: 1}
	gate, err := BuildReviewGate(empty, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.Quiet() || gate.Named != 0 || gate.Unresolved != 0 {
		t.Fatalf("an empty log did not report itself quiet: %+v", gate)
	}
	if gate.AwaitingRequests == nil || gate.UnresolvedReferences == nil || gate.ApprovedHeads == nil {
		t.Fatalf("an empty gate rendered null where a reader expects an empty list: %+v", gate)
	}
	if gate.Frontier.Head != "quiet" {
		t.Fatalf("the gate did not name the frontier it answered at: %+v", gate.Frontier)
	}
}
