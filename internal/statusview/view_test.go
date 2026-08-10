package statusview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func growthProjection(size int) workroom.Projection {
	projection := workroom.Projection{Actors: map[string]workroom.ActorState{
		"requester": {Name: "Ada"}, "performer": {Name: "Grace"},
	}}
	for index := range size {
		request := fmt.Sprintf("request:%05d", index)
		projection.Statements = append(projection.Statements, workroom.Statement{
			Event: request, Actor: "requester", Kind: workroom.KindRequest,
			Text: strings.Repeat("bounded request text ", 40) + fmt.Sprint(index),
		})
		projection.Commitments = append(projection.Commitments,
			workroom.Commitment{Request: request, Requester: "requester", Performer: "performer", WaitingOn: "performer", Status: "promised"},
			workroom.Commitment{Request: request + ":stale", Requester: "requester", Performer: "performer", Status: "stale"},
			workroom.Commitment{Request: request + ":done", Requester: "requester", Performer: "performer", Status: "satisfied"},
		)
		projection.Artifacts = append(projection.Artifacts,
			workroom.Artifact{Event: fmt.Sprintf("artifact:current:%05d", index), Path: strings.Repeat("path/", 80) + fmt.Sprint(index), Commit: fmt.Sprintf("commit-current-%05d", index)},
			workroom.Artifact{Event: fmt.Sprintf("artifact:stale:%05d", index), Path: "stale/path", Commit: fmt.Sprintf("commit-stale-%05d", index), Stale: true},
		)
		projection.Decisions = append(projection.Decisions,
			workroom.Decision{Event: fmt.Sprintf("attempt:ineffective:%05d", index), Verdict: workroom.Ineffective, Reason: strings.Repeat("reason ", 80)},
			workroom.Decision{Event: fmt.Sprintf("attempt:disputed:%05d", index), Verdict: workroom.Disputed, Reason: "disputed"},
		)
	}
	return projection
}

func TestOrientationCapsEffectiveRolesWithoutLosingSemanticIdentity(t *testing.T) {
	roles := []string{"participant", "operator", "ratifier"}
	for index := 0; index < 2000; index++ {
		roles = append(roles, fmt.Sprintf("custom-%04d", index))
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 7, Projection: workroom.Projection{Actors: map[string]workroom.ActorState{
		"fingerprint": {Name: "Ada", Kind: "human", MembershipEvent: "membership", Roles: roles, RoleSources: map[string][]string{"participant": {"membership"}}},
	}}}
	orientation, ok := BuildOrientation(snapshot, "fingerprint", "local")
	if !ok {
		t.Fatal("effective actor omitted")
	}
	if orientation.You.Name != "Ada" || orientation.You.Kind != "human" || orientation.You.MembershipEvent != "membership" ||
		orientation.Frontier.Depth != 7 || orientation.ProjectionVersion != OrientationProjectionVersion {
		t.Fatalf("orientation lost exact identity: %+v", orientation)
	}
	if len(orientation.You.Roles) != ListCap || orientation.You.RolesSkipped != len(roles)-ListCap {
		t.Fatalf("orientation cap = %d roles, %d skipped", len(orientation.You.Roles), orientation.You.RolesSkipped)
	}
	for _, semantic := range []string{"participant", "operator", "ratifier"} {
		if !slices.Contains(orientation.You.Roles, semantic) {
			t.Fatalf("semantic role %q was omitted: %+v", semantic, orientation.You)
		}
	}
	encoded, err := json.Marshal(orientation)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= orientationResponseLimitForTest {
		t.Fatalf("bounded orientation grew to %d bytes", len(encoded))
	}
}

const orientationResponseLimitForTest = 64 << 10

func TestBuildBoundsEveryGrowthDimensionAndCountsExactly(t *testing.T) {
	if ListCap != 20 || TextCap != 240 || DeltaCap != 50 {
		t.Fatalf("view caps moved: lists=%d text=%d delta=%d", ListCap, TextCap, DeltaCap)
	}
	const size = 10000
	projection := growthProjection(size)
	summary := Build("genesis", "head", size*2, projection)
	for name, got := range map[string]int{
		"actionable": len(summary.Actionable), "attention": len(summary.Attention),
		"current artifacts": len(summary.CurrentArtifacts), "stale artifacts": len(summary.StaleArtifacts),
		"attempts": len(summary.Attempts),
	} {
		if got != ListCap {
			t.Fatalf("%s listed %d, want %d", name, got, ListCap)
		}
	}
	if summary.ActionableOmitted != size-ListCap || summary.AttentionOmitted != size-ListCap ||
		summary.CurrentOmitted != size-ListCap || summary.StaleOmitted != size-ListCap ||
		summary.AttemptsOmitted != 2*size-ListCap {
		t.Fatalf("omitted counts are not exact: %+v", summary)
	}
	if summary.Totals.Commitments["promised"] != size || summary.Totals.Commitments["stale"] != size || summary.Totals.Commitments["satisfied"] != size {
		t.Fatalf("commitment totals = %#v", summary.Totals.Commitments)
	}
	if summary.Actionable[0].Requester != "Ada" || summary.Actionable[0].Performer != "Grace" {
		t.Fatalf("actor names were not resolved: %+v", summary.Actionable[0])
	}
	if len(summary.Actionable[0].Text) > TextCap || len(summary.CurrentArtifacts[0].Path) > TextCap {
		t.Fatal("user-controlled summary fields exceed their cap")
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 32<<10 {
		t.Fatalf("bounded summary is %d bytes, want < 32 KiB", len(encoded))
	}
	rendered := Render(summary, "test")
	if len(rendered) >= 32<<10 {
		t.Fatalf("bounded text is %d bytes, want < 32 KiB", len(rendered))
	}
	t.Logf("10k-dimension bounded sizes: %d bytes JSON, %d bytes text", len(encoded), len(rendered))
	if !bytes.Contains(rendered, []byte("Showing 20 of 10000; 9980 older omitted")) ||
		!bytes.Contains(rendered, []byte("Showing 20 of 20000; 19980 older omitted")) {
		t.Fatalf("rendered view hides exact omission counts:\n%s", rendered)
	}
	if summary.Actionable[0].Request != "request:09999" || summary.Actionable[len(summary.Actionable)-1].Request != "request:09980" {
		t.Fatalf("summary is not newest-first: %#v", summary.Actionable)
	}
	fullJSON, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fullJSON, []byte("request:00000")) || !bytes.Contains(fullJSON, []byte("request:09999")) {
		t.Fatal("full JSON omitted an entry outside the bounded window")
	}
	fullText := workroom.RenderStatus(projection)
	if rows := bytes.Count(fullText, []byte("| promised |")); rows != size {
		t.Fatalf("full text rendered %d promised rows, want %d", rows, size)
	}
}

func TestDoublingHistoryChangesOnlyBoundedRowsAndCounters(t *testing.T) {
	one, _ := json.Marshal(Build("genesis", "head", 10000, growthProjection(5000)))
	two, _ := json.Marshal(Build("genesis", "head", 20000, growthProjection(10000)))
	if growth := len(two) - len(one); growth > 64 {
		t.Fatalf("doubling history grew the bounded response by %d bytes: %d then %d", growth, len(one), len(two))
	}
	fullOne, _ := json.Marshal(growthProjection(5000))
	fullTwo, _ := json.Marshal(growthProjection(10000))
	if len(fullTwo) < len(fullOne)*19/10 {
		t.Fatalf("fixture did not actually double full history: %d then %d", len(fullOne), len(fullTwo))
	}
}

func TestTextCapIsAValidUTF8ByteCeiling(t *testing.T) {
	got := Text(strings.Repeat("界", TextCap))
	if len(got) > TextCap || !json.Valid([]byte(`{"text":`+fmt.Sprintf("%q", got)+`}`)) {
		t.Fatalf("capped text is %d bytes or invalid JSON: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("capped text does not disclose truncation: %q", got)
	}
}

func TestOpenUnclaimedRequestRemainsActionableWithoutInventedAssignment(t *testing.T) {
	projection := workroom.Projection{
		Actors:      map[string]workroom.ActorState{"requester": {Name: "Ada"}, "addressee": {Name: "Grace"}},
		Statements:  []workroom.Statement{{Event: "request", Actor: "requester", Kind: workroom.KindRequest, Text: "available work"}},
		Commitments: []workroom.Commitment{{Request: "request", Requester: "requester", AddressedTo: "addressee", Status: "open"}},
	}
	summary := Build("genesis", "head", 1, projection)
	if len(summary.Actionable) != 1 || summary.Actionable[0].Status != "open" || summary.Actionable[0].AddressedTo != "Grace" || summary.Actionable[0].Performer != "" || summary.Actionable[0].WaitingOn != "" {
		t.Fatalf("open request view invented or hid assignment: %+v", summary)
	}
	if len(summary.Attention) != 0 {
		t.Fatalf("open request was classified as terminal attention: %+v", summary.Attention)
	}
	if rendered := string(Render(summary, "")); !strings.Contains(rendered, "Ada → addressed to Grace — unclaimed") {
		t.Fatalf("rendered status hides who can claim the request:\n%s", rendered)
	}
}

// qualifierProjection pairs each interesting status with a stale twin, so a
// view that reads only the status word cannot tell the pairs apart.
func qualifierProjection() workroom.Projection {
	return workroom.Projection{
		Actors: map[string]workroom.ActorState{"requester": {Name: "Ada"}, "performer": {Name: "Grace"}},
		Statements: []workroom.Statement{
			{Event: "request:reported-stale", Actor: "requester", Kind: workroom.KindRequest, Text: "moved under the report"},
			{Event: "request:reported-clean", Actor: "requester", Kind: workroom.KindRequest, Text: "still standing"},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:reported-stale", Requester: "requester", Performer: "performer", Status: "reported", Stale: true, WaitingOn: "requester"},
			{Request: "request:reported-clean", Requester: "requester", Performer: "performer", Status: "reported", WaitingOn: "requester"},
			{Request: "request:satisfied-stale", Requester: "requester", Performer: "performer", Status: "satisfied", Stale: true},
			{Request: "request:satisfied-clean", Requester: "requester", Performer: "performer", Status: "satisfied"},
			{Request: "request:promised", Requester: "requester", Performer: "performer", Status: "promised", WaitingOn: "performer"},
			{Request: "request:never-reported", Requester: "requester", Performer: "performer", Status: "stale", Stale: true},
		},
	}
}

func findCommitment(items []Commitment, request string) *Commitment {
	for index := range items {
		if items[index].Request == request {
			return &items[index]
		}
	}
	return nil
}

// Staleness qualifies the lifecycle word; it does not replace it. A reported
// commitment whose basis was retired is still reported, still waiting on the
// requester, and still in the review lane — it has only gained a warning.
func TestStaleReportedKeepsItsStatusLaneAndQualifier(t *testing.T) {
	summary := Build("genesis", "head", 1, qualifierProjection())
	stale := findCommitment(summary.Actionable, "request:reported-stale")
	if stale == nil {
		t.Fatalf("a stale report left the actionable lane: %#v", summary)
	}
	if stale.Status != "reported" || !stale.Stale || stale.WaitingOn != "Ada" {
		t.Fatalf("stale report lost its status, qualifier, or the party who owes the next move: %#v", *stale)
	}
	if findCommitment(summary.Attention, "request:reported-stale") != nil {
		t.Fatalf("a stale report was moved out of its lifecycle lane: %#v", summary.Attention)
	}
	clean := findCommitment(summary.Actionable, "request:reported-clean")
	if clean == nil || clean.Stale {
		t.Fatalf("an unqualified report was marked stale: %#v", clean)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"stale":true`)) {
		t.Fatalf("the bounded JSON does not carry the qualifier at all:\n%s", encoded)
	}
}

// A satisfied commitment is finished, so the bounded lists leave it out. If a
// basis under it was retired the outcome is worth re-checking, and dropping
// the row is the projection lying by omission.
func TestStaleTerminalCommitmentStaysVisibleAndSettledOneDoesNot(t *testing.T) {
	summary := Build("genesis", "head", 1, qualifierProjection())
	stale := findCommitment(summary.Attention, "request:satisfied-stale")
	if stale == nil {
		t.Fatalf("a stale satisfied commitment was skipped entirely: %#v", summary)
	}
	if stale.Status != "satisfied" || !stale.Stale {
		t.Fatalf("stale satisfied commitment lost its outcome or its qualifier: %#v", *stale)
	}
	if findCommitment(summary.Actionable, "request:satisfied-stale") != nil {
		t.Fatalf("a finished commitment was presented as actionable: %#v", summary.Actionable)
	}
	if findCommitment(summary.Attention, "request:satisfied-clean") != nil || findCommitment(summary.Actionable, "request:satisfied-clean") != nil {
		t.Fatalf("a settled commitment with nothing wrong was listed as work: %#v", summary)
	}
}

func TestBoundedTotalsCountStaleBesideStatus(t *testing.T) {
	summary := Build("genesis", "head", 1, qualifierProjection())
	if summary.Totals.Commitments["reported"] != 2 || summary.Totals.Commitments["satisfied"] != 2 {
		t.Fatalf("status totals changed: %#v", summary.Totals.Commitments)
	}
	if summary.Totals.StaleCommitments["reported"] != 1 || summary.Totals.StaleCommitments["satisfied"] != 1 {
		t.Fatalf("totals cannot identify stale commitments by status: %#v", summary.Totals.StaleCommitments)
	}
	if _, listed := summary.Totals.StaleCommitments["promised"]; listed {
		t.Fatalf("a status with nothing stale was given a stale count: %#v", summary.Totals.StaleCommitments)
	}
}

func TestRenderedSummaryShowsTheStaleQualifier(t *testing.T) {
	rendered := Render(Build("genesis", "head", 1, qualifierProjection()), "test")
	for _, want := range []string{"reported 2 (1 stale)", "satisfied 2 (1 stale)", "reported (stale)", "satisfied (stale)"} {
		if !bytes.Contains(rendered, []byte(want)) {
			t.Fatalf("rendered status is missing %q:\n%s", want, rendered)
		}
	}
	if bytes.Contains(rendered, []byte("promised 1 (")) {
		t.Fatalf("rendered status qualified a commitment that is not stale:\n%s", rendered)
	}
	// A commitment that was never reported has no outcome to preserve, so
	// "stale" is already its whole status. Saying it twice is noise.
	if bytes.Contains(rendered, []byte("stale (stale)")) || bytes.Contains(rendered, []byte("stale 1 (1 stale)")) {
		t.Fatalf("rendered status repeats the word stale:\n%s", rendered)
	}
	if !bytes.Contains(rendered, []byte("- stale — Ada")) {
		t.Fatalf("a never-reported stale commitment lost its row:\n%s", rendered)
	}
}

// The lane tables must name only words the fold can produce. "requested" sat
// in the actionable table for a long time while projectCommitments emitted
// "open", so the table read as if it covered a state that cannot occur.
func TestLaneTablesNameOnlyStatusesTheFoldEmits(t *testing.T) {
	emitted := map[string]bool{
		"open": true, "promised": true, "reported": true, "satisfied": true,
		"stale": true, "cancelled": true, "reneged": true, "withdrawn": true,
	}
	for _, table := range []map[string]bool{actionable, terminal} {
		for status := range table {
			if !emitted[status] {
				t.Fatalf("lane table names %q, which foldState.projectCommitments never emits", status)
			}
		}
	}
	for status := range emitted {
		if actionable[status] && terminal[status] {
			t.Fatalf("%q is both actionable and terminal", status)
		}
	}
}

// The browser's Active umbrella uses this same matrix. Keep the consolidated
// status view authoritative and explicit: open, promised, and reported are
// globally actionable for every actor; lifecycle stale and terminal states
// are not. Staleness on a reported row remains only a qualifier.
func TestActionableLifecycleMatrix(t *testing.T) {
	tests := []struct {
		request string
		status  string
		stale   bool
		want    bool
	}{
		{request: "open", status: "open", want: true},
		{request: "promised", status: "promised", want: true},
		{request: "reported", status: "reported", want: true},
		{request: "reported-stale", status: "reported", stale: true, want: true},
		{request: "stale", status: "stale", stale: true},
		{request: "satisfied", status: "satisfied"},
		{request: "withdrawn", status: "withdrawn"},
		{request: "cancelled", status: "cancelled"},
		{request: "reneged", status: "reneged"},
	}
	projection := workroom.Projection{}
	for _, test := range tests {
		projection.Commitments = append(projection.Commitments, workroom.Commitment{
			Request: test.request, Requester: "requester", Status: test.status, Stale: test.stale,
		})
	}
	summary := Build("genesis", "head", 1, projection)
	for _, test := range tests {
		got := findCommitment(summary.Actionable, test.request) != nil
		if got != test.want {
			t.Errorf("status %q stale=%t actionable=%t, want %t", test.status, test.stale, got, test.want)
		}
	}
	if len(summary.Actionable) != 4 {
		t.Fatalf("global actionable total = %d, want 4: %#v", len(summary.Actionable), summary.Actionable)
	}
}

func TestWarmDepth20000SummaryLatencyAndSize(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation is not a production latency measurement")
	}
	projection := growthProjection(20000)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(Build("genesis", "head", 20000, projection))
	}))
	defer server.Close()
	durations := make([]time.Duration, 0, 100)
	for range 100 {
		started := time.Now()
		response, err := http.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(body) >= 64<<10 {
			t.Fatalf("warm summary response = %d bytes, want < 64 KiB", len(body))
		}
		durations = append(durations, time.Since(started))
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	p99 := durations[98]
	t.Logf("warm depth-20k bounded summary p99: %s", p99)
	if p99 >= 500*time.Millisecond {
		t.Fatalf("warm depth-20k summary p99 = %s, want < 500ms", p99)
	}
}
