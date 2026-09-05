package statusview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

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
		Artifacts: []workroom.Artifact{
			{Event: "artifact:current", Path: "internal/a", Commit: "commit-a"},
			{Event: "artifact:stale", Path: "internal/b", Commit: "commit-b", Stale: true},
			{Event: "artifact:retired", Path: "internal/c", Commit: "commit-c", Retired: true},
			{Event: "artifact:world", Path: "internal/d", Commit: "commit-d", Stale: true, DescribesSupersededWorld: true},
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

// A satisfied commitment is finished, so the bounded lists leave it out, and
// ordinary reasoning staleness does not bring it back. That staleness blocks
// nothing and reaches most closed commitments, so a row for each one buried
// the work still owed. The count keeps the fact.
func TestOrdinaryStalenessDoesNotReopenAClosedCommitment(t *testing.T) {
	summary := Build("genesis", "head", 1, qualifierProjection())
	for _, request := range []string{"request:satisfied-stale", "request:satisfied-clean"} {
		if findCommitment(summary.Attention, request) != nil || findCommitment(summary.Actionable, request) != nil {
			t.Fatalf("a finished commitment was listed as work: %s in %#v", request, summary)
		}
	}
	if summary.Totals.Commitments["satisfied"] != 2 || summary.Totals.StaleCommitments["satisfied"] != 1 {
		t.Fatalf("the counts stopped carrying what the lists no longer show: %#v", summary.Totals)
	}
	// A commitment nobody ever reported has no outcome to preserve, so the
	// fold gives it the status stale outright. That is unfinished work, not
	// history, and it stays listed.
	if findCommitment(summary.Attention, "request:never-reported") == nil {
		t.Fatalf("a never-reported commitment was summarized away as history: %#v", summary.Attention)
	}
}

// Ordinary staleness reaches nearly every artifact, so one "stale" figure
// covering all of it answered nothing. Retirement and a superseded world are
// the two facts a reader acts on, and each gets its own count and stays
// visible on its own row.
func TestArtifactTotalsSeparateTheLoudFactsFromOrdinaryStaleness(t *testing.T) {
	summary := Build("genesis", "head", 1, qualifierProjection())
	if summary.Totals.Artifacts != 4 || summary.Totals.StaleArtifacts != 3 ||
		summary.Totals.RetiredArtifacts != 1 || summary.Totals.WorldStaleArtifacts != 1 {
		t.Fatalf("artifact totals cannot tell the three conditions apart: %#v", summary.Totals)
	}
	rendered := Render(summary, "test")
	if !bytes.Contains(rendered, []byte("Artifacts: 1 current, 2 stale, 1 retired, 1 describing a superseded world")) {
		t.Fatalf("the totals line does not name the loud facts:\n%s", rendered)
	}
	if !bytes.Contains(rendered, []byte("internal/d@commit-d — `artifact:world` — describes a superseded world")) {
		t.Fatalf("world-staleness lost its own row:\n%s", rendered)
	}
	if !bytes.Contains(rendered, []byte("internal/c@commit-c")) {
		t.Fatalf("a retired artifact lost its own row:\n%s", rendered)
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

// Ordinary staleness is a count per lane on the default page and nothing else.
// Marking each row fired a warning on nearly every row of a real workroom and
// changed nothing a reader could do, which is what taught readers to ignore
// the marks that do mean something.
func TestRenderedSummaryCountsOrdinaryStalenessPerLaneAndMarksNoRow(t *testing.T) {
	summary := Build("genesis", "head", 1, qualifierProjection())
	rendered := Render(summary, "test")
	for _, want := range []string{"reported 2 (1 stale)", "satisfied 2 (1 stale)"} {
		if !bytes.Contains(rendered, []byte(want)) {
			t.Fatalf("rendered status is missing the per-lane count %q:\n%s", want, rendered)
		}
	}
	if bytes.Contains(rendered, []byte("(stale)")) {
		t.Fatalf("rendered status marks a row with ordinary staleness:\n%s", rendered)
	}
	if bytes.Contains(rendered, []byte("promised 1 (")) {
		t.Fatalf("rendered status qualified a commitment that is not stale:\n%s", rendered)
	}
	// A commitment that was never reported has no outcome to preserve, so
	// "stale" is already its whole status. Saying it twice is noise.
	if bytes.Contains(rendered, []byte("stale 1 (1 stale)")) {
		t.Fatalf("rendered status repeats the word stale:\n%s", rendered)
	}
	if !bytes.Contains(rendered, []byte("- stale — Ada")) {
		t.Fatalf("a never-reported stale commitment lost its row:\n%s", rendered)
	}
	// Quieting the page must not quiet the payload. The bounded JSON the
	// resident serves still carries the qualifier per row.
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"stale":true`)) {
		t.Fatalf("the bounded JSON stopped carrying the qualifier:\n%s", encoded)
	}
}

// The lane tables must name only words the fold can produce. "requested" sat
// in the actionable table for a long time while projectCommitments emitted
// "open", so the table read as if it covered a state that cannot occur.
func TestLaneTablesNameOnlyStatusesTheFoldEmits(t *testing.T) {
	emitted := map[string]bool{
		"open": true, "promised": true, "reported": true, "superseded": true, "satisfied": true,
		"awaiting-review": true, "awaiting-authorization": true, "awaiting-landing": true, "abandoned": true,
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
// status view authoritative and explicit: open, promised, reported, and
// the three awaiting-* states are globally live for every actor; lifecycle stale and
// terminal states are not. Staleness on a reported row remains only a qualifier.
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
		{request: "awaiting-review", status: "awaiting-review", want: true},
		{request: "awaiting-authorization", status: "awaiting-authorization", want: true},
		{request: "awaiting-landing", status: "awaiting-landing", want: true},
		{request: "reported-stale", status: "reported", stale: true, want: true},
		{request: "stale", status: "stale", stale: true},
		{request: "superseded", status: "superseded"},
		{request: "satisfied", status: "satisfied"},
		{request: "withdrawn", status: "withdrawn"},
		{request: "cancelled", status: "cancelled"},
		{request: "reneged", status: "reneged"},
		{request: "abandoned", status: "abandoned"},
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
	if len(summary.Actionable) != 7 {
		t.Fatalf("global actionable total = %d, want 7: %#v", len(summary.Actionable), summary.Actionable)
	}
}

func TestDepth20000SummarySize(t *testing.T) {
	projection := growthProjection(20000)
	body, err := json.Marshal(Build("genesis", "head", 20000, projection))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) >= 64<<10 {
		t.Fatalf("depth-20k summary = %d bytes, want < 64 KiB", len(body))
	}
}

func BenchmarkWarmDepth20000Summary(b *testing.B) {
	projection := growthProjection(20000)
	b.ReportAllocs()
	for b.Loop() {
		body, err := json.Marshal(Build("genesis", "head", 20000, projection))
		if err != nil {
			b.Fatal(err)
		}
		if len(body) >= 64<<10 {
			b.Fatalf("depth-20k summary = %d bytes, want < 64 KiB", len(body))
		}
	}
}

// The bounded status is a second renderer of the same events, and review found
// that the surface test guarding #N covered only the first. The fold tests were
// green, the full renderer was covered, and changing this one back to an
// abbreviation still passed — which is the same shape as a test that names a
// path it does not walk.
//
// So this pins the bounded surface directly, and is mutation-sensitive: swap
// Event rows keep the readable workroom sequence and the canonical ID a CLI
// command accepts. Git commits stay abbreviated because Git resolves those;
// neither #N nor an abbreviated event ID resolves at the durable boundary.
func TestBoundedStatusNamesEventsByNumberAndCanonicalID(t *testing.T) {
	const request = "git:sha1:genesis#git:sha1:requestevent000000000000000000000000000"
	const artifact = "git:sha1:genesis#git:sha1:artifactevent00000000000000000000000000"
	const commit = "0123456789abcdef0123456789abcdef01234567"

	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{"requester": {Name: "Ada"}, "performer": {Name: "Grace"}},
		Statements: []workroom.Statement{
			{Event: request, Actor: "requester", Kind: workroom.KindRequest, Text: "do the thing"},
		},
		Commitments: []workroom.Commitment{
			{Request: request, Requester: "requester", Performer: "performer", WaitingOn: "performer", Status: "promised"},
		},
		Artifacts: []workroom.Artifact{{Event: artifact, Path: "internal/thing", Commit: commit}},
		// Sequences live on decisions because that is the only index covering
		// every durable record. Without these the rows have no number to use.
		Decisions: []workroom.Decision{
			{Event: request, Sequence: 41, Verdict: workroom.Effective},
			{Event: artifact, Sequence: 42, Verdict: workroom.Effective},
		},
	}

	rendered := string(Render(Build("genesis", "head", 2, projection), "test"))

	for _, want := range []string{"#41", "#42"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("bounded status omits %s:\n%s", want, rendered)
		}
	}
	for _, want := range []string{request, artifact} {
		if !strings.Contains(rendered, want) {
			t.Errorf("bounded status omits canonical event %s:\n%s", want, rendered)
		}
	}
	for _, forbidden := range []string{short(request), short(artifact)} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("bounded status abbreviates an event as %q instead of naming it:\n%s", forbidden, rendered)
		}
	}
	// The commit is a git object and must still be abbreviated.
	if !strings.Contains(rendered, short(commit)) {
		t.Errorf("bounded status stopped abbreviating a git commit:\n%s", rendered)
	}
}

func TestBoundedStatusExplainsDanglingPromiseRefusal(t *testing.T) {
	summary := Summary{Attempts: []Attempt{{Event: "promise", Sequence: 8, Verdict: string(workroom.Ineffective), Reason: "dangling promise has no request"}}}
	rendered := string(Render(summary, "test"))
	for _, want := range []string{"dangling promise has no request", "Add exactly one live request event with --rests-on", "docs/reference/gs/state.md#citing"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered refusal omits %q:\n%s", want, rendered)
		}
	}
}

// A dissent takes no lifecycle and satisfies nothing, so it lands in no
// commitment lane and no artifact list. Until it was rendered it appeared
// nowhere on the human page while sitting plainly in the JSON: the act it
// objects to still read exactly as it always had, so the page told a reader an
// approval was unopposed when the record said otherwise.
func TestStatusPageShowsDissentAgainstTheActItConcerns(t *testing.T) {
	projection := workroom.Projection{
		Statements: []workroom.Statement{
			{Event: "verdict", Actor: "reviewer", Kind: workroom.KindReport, Text: "APPROVED"},
			{Event: "objection", Actor: "reviewer", Kind: workroom.KindDissent, Text: "do not act on that approval yet"},
		},
		Provenance: map[string][]string{"objection": {"verdict"}},
		Decisions: []workroom.Decision{
			{Event: "verdict", Verdict: workroom.Effective, Sequence: 1},
			{Event: "objection", Verdict: workroom.Effective, Sequence: 2},
		},
	}
	summary := Build("genesis", "head", 2, projection)
	if len(summary.Dissents) != 1 {
		t.Fatalf("dissents = %+v", summary.Dissents)
	}
	if summary.Dissents[0].Against != "verdict" {
		t.Errorf("dissent must name the act it stands against, got %q", summary.Dissents[0].Against)
	}
	rendered := string(Render(summary, ""))
	if !strings.Contains(rendered, "## Dissents") {
		t.Fatal("the human page has no dissent section")
	}
	if !strings.Contains(rendered, "do not act on that approval yet") {
		t.Error("the dissent's text is not on the page")
	}
	if !strings.Contains(rendered, "#2") || !strings.Contains(rendered, "#1") {
		t.Errorf("the page must name both the dissent and its target:\n%s", rendered)
	}
}

// A retired dissent is withdrawn, and showing it would be the opposite error.
// The dissent list is bounded like every other list on the page, and the bound
// has to be pinned by a test that fails without it. Removing the Cap call left
// the whole package green, which meant the page could grow without limit and
// the "older omitted" notice could silently stop being true — the notice is
// the only thing telling a reader that what they see is not all there is.
//
// Newest-first matters as much as the count. Cap keeps the tail and reverse
// turns it around, so an off-by-one in either direction would show the oldest
// twenty objections and call them current.
func TestDissentsAreBoundedNewestFirstWithAnHonestOmittedCount(t *testing.T) {
	const filed = ListCap + 5
	projection := workroom.Projection{Provenance: map[string][]string{}}
	for index := 1; index <= filed; index++ {
		event := fmt.Sprintf("objection-%02d", index)
		projection.Statements = append(projection.Statements, workroom.Statement{
			Event: event, Actor: "reviewer", Kind: workroom.KindDissent,
			Text: fmt.Sprintf("objection number %d", index),
		})
		projection.Decisions = append(projection.Decisions, workroom.Decision{
			Event: event, Verdict: workroom.Effective, Sequence: index,
		})
	}
	summary := Build("genesis", "head", filed, projection)

	if len(summary.Dissents) != ListCap {
		t.Errorf("dissents shown = %d, want the cap %d", len(summary.Dissents), ListCap)
	}
	if summary.DissentsOmitted != filed-ListCap {
		t.Errorf("omitted count = %d, want %d", summary.DissentsOmitted, filed-ListCap)
	}
	if got := summary.Dissents[0].Event; got != fmt.Sprintf("objection-%02d", filed) {
		t.Errorf("first dissent = %q, want the newest", got)
	}
	if got := summary.Dissents[len(summary.Dissents)-1].Event; got != fmt.Sprintf("objection-%02d", filed-ListCap+1) {
		t.Errorf("last dissent = %q, want the oldest still within the cap", got)
	}
	rendered := string(Render(summary, ""))
	notice := fmt.Sprintf("Showing %d of %d; %d older omitted.", ListCap, filed, filed-ListCap)
	if !strings.Contains(rendered, notice) {
		t.Errorf("the page does not tell the reader what it left out; want %q in:\n%s", notice, rendered)
	}
	if strings.Contains(rendered, "objection number 1\n") {
		t.Error("an omitted dissent was rendered anyway")
	}
}

func TestRetiredDissentIsNotShown(t *testing.T) {
	projection := workroom.Projection{
		Statements: []workroom.Statement{
			{Event: "objection", Actor: "reviewer", Kind: workroom.KindDissent, Text: "withdrawn objection", Retired: true},
		},
		Decisions: []workroom.Decision{{Event: "objection", Verdict: workroom.Effective, Sequence: 1}},
	}
	summary := Build("genesis", "head", 1, projection)
	if len(summary.Dissents) != 0 {
		t.Fatalf("a retired dissent was shown: %+v", summary.Dissents)
	}
	if !strings.Contains(string(Render(summary, "")), "## Dissents\n\nNone.") {
		t.Error("the section should say None when nothing stands")
	}
}
