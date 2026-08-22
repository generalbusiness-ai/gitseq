package statusview

import (
	"encoding/json"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// ArtifactQuery is a published resident/MCP input contract. CLI-only state
// and provenance selectors belong to ArtifactSelection and must not leak back
// into these bytes through struct sharing.
func TestArtifactQueryWireContractIsUnchanged(t *testing.T) {
	encoded, err := json.Marshal(ArtifactQuery{Paths: []string{"ui"}, Limit: 1, Cursor: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"paths":["ui"],"limit":1,"cursor":"next"}`; got != want {
		t.Fatalf("artifact query wire bytes = %s, want %s", got, want)
	}
}

func TestLegacyLiveArtifactPageJSONBytesAreUnchanged(t *testing.T) {
	page, err := BuildArtifactPage(artifactSnapshot(), ArtifactQuery{Paths: []string{"ui"}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"frontier":{"genesis":"genesis","head":"head-one","depth":5},"paths":["ui"],"artifacts":[{"event":"ui:current","path":"ui","commit":"new","stale":false,"retired":false,"describes_superseded_world":false},{"event":"ui:stale","path":"ui","commit":"middle","stale":true,"retired":false,"describes_superseded_world":true}],"matching_total":2,"returned":2,"before":0,"remaining":0}`
	if string(encoded) != want {
		t.Fatalf("legacy live artifact page bytes changed:\n got %s\nwant %s", encoded, want)
	}
}

func TestLegacyPathOnlyCursorStillContinues(t *testing.T) {
	const cursor = "eyJ2IjoxLCJoZWFkIjoiaGVhZC1vbmUiLCJvZmZzZXQiOjEsImZpbHRlciI6IjFmNzY3ZGM4OTllODA3ODNmNGViODNlMTQ5ZDIzZmY2ZDA1MjNmYjBhNzY1Y2Y1MzgyNTI2MWYyMjAyOWUyODIifQ"
	first, err := BuildArtifactPage(artifactSnapshot(), ArtifactQuery{Paths: []string{"ui"}, Limit: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor != cursor {
		t.Fatalf("path-only cursor bytes changed:\n got %s\nwant %s", first.NextCursor, cursor)
	}
	continued, err := BuildArtifactPage(artifactSnapshot(), ArtifactQuery{Paths: []string{"ui"}, Limit: 1, Cursor: cursor}, false)
	if err != nil {
		t.Fatalf("pre-change path-only cursor was refused: %v", err)
	}
	if continued.Before != 1 || continued.Returned != 1 || continued.Artifacts[0].Event != "ui:stale" {
		t.Fatalf("pre-change cursor did not continue its page: %+v", continued)
	}
}

func artifactSnapshot() app.Snapshot {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{
		{Event: "ui:retired", Path: "ui", Commit: "old", Retired: true, Stale: true, DescribesSupersededWorld: true},
		{Event: "ui:stale", Path: "ui", Commit: "middle", Stale: true, DescribesSupersededWorld: true},
		{Event: "docs:live", Path: "docs", Commit: "docs-head"},
		{Event: "ui:current", Path: "ui", Commit: "new"},
		{Event: "nested:unrelated", Path: "ui/src", Commit: "nested"},
	}}
	return app.Snapshot{Genesis: "genesis", Head: "head-one", Depth: 5, Projection: projection}
}

func TestArtifactQueryReturnsLiveExactPathRowsWithExplicitState(t *testing.T) {
	page, err := BuildArtifactPage(artifactSnapshot(), ArtifactQuery{Paths: []string{"ui"}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.MatchingTotal != 2 || page.Returned != 2 || len(page.Artifacts) != 2 {
		t.Fatalf("unexpected live artifacts: %+v", page)
	}
	if page.Artifacts[0].Event != "ui:current" || page.Artifacts[0].Retired || page.Artifacts[0].Stale || page.Artifacts[0].DescribesSupersededWorld {
		t.Fatalf("current row has the wrong state: %+v", page.Artifacts[0])
	}
	if page.Artifacts[1].Event != "ui:stale" || page.Artifacts[1].Retired || !page.Artifacts[1].Stale || !page.Artifacts[1].DescribesSupersededWorld {
		t.Fatalf("stale live row has the wrong state: %+v", page.Artifacts[1])
	}
	for _, row := range page.Artifacts {
		if row.Path != "ui" || row.Event == "ui:retired" || row.Event == "nested:unrelated" {
			t.Fatalf("query normalized, prefix-matched, or included retired state: %+v", row)
		}
	}
}

func TestArtifactQueryIsBoundedAndHeadBound(t *testing.T) {
	query := ArtifactQuery{Paths: []string{"ui", "docs"}, Limit: 1}
	first, err := BuildArtifactPage(artifactSnapshot(), query, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Returned != 1 || first.MatchingTotal != 3 || first.Remaining != 2 || first.NextCursor == "" {
		t.Fatalf("first page does not disclose its bounds: %+v", first)
	}
	query.Cursor = first.NextCursor
	second, err := BuildArtifactPage(artifactSnapshot(), query, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Before != 1 || second.Returned != 1 || second.Artifacts[0].Event == first.Artifacts[0].Event {
		t.Fatalf("continuation did not advance: %+v", second)
	}
	moved := artifactSnapshot()
	moved.Head = "head-two"
	if _, err := BuildArtifactPage(moved, query, false); err == nil {
		t.Fatal("artifact cursor crossed a moved durable head")
	}
}

// anchorSnapshot is a chain, not a star. The whole point of following artifact
// provenance is that a page two hops from the anchor is anchored just as surely
// as a direct dependent, so the fixture has to contain a second hop for a
// one-hop implementation to fail on.
func anchorSnapshot() app.Snapshot {
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "root:one", Path: ".", Commit: "root-one"},
			{Event: "root:withdrawn", Path: ".", Commit: "root-two", Retired: true},
			{Event: "near", Path: "docs/why.md", Commit: "near"},
			{Event: "far", Path: "docs/concepts/record.md", Commit: "far"},
			{Event: "further", Path: "docs/reference/gs/status.md", Commit: "further"},
			{Event: "unrelated", Path: "ui", Commit: "unrelated"},
			{Event: "replaced", Path: "internal/workroom", Commit: "replaced", Retired: true, Succeeded: true},
		},
		Provenance: map[string][]string{
			// near rests directly on an anchor; far rests on near; further
			// rests on far. A walk that stops after one hop finds only near.
			"near":      {"root:one", "some-request"},
			"far":       {"near"},
			"further":   {"far"},
			"unrelated": {"some-request"},
			// A retirement withdraws a pointer; it does not erase the anchor
			// whatever stood on it still follows.
			"replaced": {"root:withdrawn"},
		},
	}
	return app.Snapshot{Genesis: "genesis", Head: "anchor-head", Depth: 9, Projection: projection}
}

func artifactEvents(page ArtifactPage) []string {
	events := make([]string, 0, len(page.Artifacts))
	for _, row := range page.Artifacts {
		events = append(events, row.Event)
	}
	return events
}

func TestArtifactStateSelectsEachLifecycleApart(t *testing.T) {
	for _, test := range []struct {
		state ArtifactState
		want  []string
	}{
		{ArtifactStateLive, []string{"nested:unrelated", "ui:current", "docs:live", "ui:stale"}},
		{ArtifactStateRetired, []string{"ui:retired"}},
		{ArtifactStateSucceeded, nil},
		{ArtifactStateAll, []string{"nested:unrelated", "ui:current", "docs:live", "ui:stale", "ui:retired"}},
	} {
		t.Run(string(test.state), func(t *testing.T) {
			page, err := BuildArtifactSelectionPage(artifactSnapshot(), ArtifactSelection{
				Paths: []string{"ui", "docs", "ui/src"}, State: test.state, Limit: 50}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := artifactEvents(page); len(got) != len(test.want) {
				t.Fatalf("state %q selected %v, want %v", test.state, got, test.want)
			}
			for index, want := range test.want {
				if page.Artifacts[index].Event != want {
					t.Fatalf("state %q selected %v, want %v", test.state, artifactEvents(page), test.want)
				}
			}
		})
	}
}

// Succeeded narrows retired, and a query naming one must not return the other.
// Reporting both under one name would lose exactly the distinction a caller is
// asking about: one says where the behaviour went, the other says go and look.
func TestRetiredAndSucceededAreNotTheSameSelection(t *testing.T) {
	snapshot := anchorSnapshot()
	withdrawn, err := BuildArtifactSelectionPage(snapshot, ArtifactSelection{Paths: []string{".", "internal/workroom"}, State: ArtifactStateRetired, Limit: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := artifactEvents(withdrawn); len(got) != 1 || got[0] != "root:withdrawn" {
		t.Fatalf("retired selected %v, want only the withdrawn pointer", got)
	}
	succeeded, err := BuildArtifactSelectionPage(snapshot, ArtifactSelection{Paths: []string{".", "internal/workroom"}, State: ArtifactStateSucceeded, Limit: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := artifactEvents(succeeded); len(got) != 1 || got[0] != "replaced" {
		t.Fatalf("succeeded selected %v, want only the replaced pointer", got)
	}
	if !succeeded.Artifacts[0].Succeeded || !succeeded.Artifacts[0].Retired {
		t.Fatalf("succeeded row dropped its lifecycle facts: %+v", succeeded.Artifacts[0])
	}
}

func TestArtifactQueryRefusesAnUnknownState(t *testing.T) {
	_, err := BuildArtifactSelectionPage(artifactSnapshot(), ArtifactSelection{Paths: []string{"ui"}, State: "current"}, false)
	if err == nil {
		t.Fatal("an unknown state was guessed at instead of refused")
	}
}

func TestAnchorFollowsArtifactProvenancePastTheFirstHop(t *testing.T) {
	page, err := BuildArtifactSelectionPage(anchorSnapshot(), ArtifactSelection{Reaches: ".", State: ArtifactStateAll, Limit: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := artifactEvents(page)
	for _, want := range []string{"near", "far", "further", "replaced"} {
		if !containsString(got, want) {
			t.Fatalf("anchor walk returned %v; %s reaches an artifact at `.` and is missing", got, want)
		}
	}
	if containsString(got, "unrelated") {
		t.Fatalf("anchor walk returned %v; `unrelated` rests on no artifact at all", got)
	}
	if page.MatchingTotal != 4 {
		t.Fatalf("anchor walk matched %d, want 4: %v", page.MatchingTotal, got)
	}
	// `replaced` is there only because a retired artifact is still a seed. A
	// withdrawn pointer does not erase the anchor whatever stood on it follows,
	// and dropping retired seeds would report that chain as unanchored.
	live, err := BuildArtifactSelectionPage(anchorSnapshot(), ArtifactSelection{Reaches: ".", Limit: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	if live.MatchingTotal != 3 || containsString(artifactEvents(live), "replaced") {
		t.Fatalf("the default state returned %v; live means not retired", artifactEvents(live))
	}
}

// The anchors are what everything else is anchored to. Returning them when
// asked what still points at them answers a question nobody asked, and would
// make the migration gate unreachable: the `.` artifacts always reach `.`.
func TestAnchorExcludesTheArtifactsAtTheAnchorPath(t *testing.T) {
	page, err := BuildArtifactSelectionPage(anchorSnapshot(), ArtifactSelection{Reaches: ".", State: ArtifactStateAll, Limit: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Artifacts {
		if row.Path == "." {
			t.Fatalf("the anchor path returned itself: %+v", row)
		}
	}
}

func TestAnchorAndPathsIntersectRatherThanWiden(t *testing.T) {
	page, err := BuildArtifactSelectionPage(anchorSnapshot(), ArtifactSelection{Paths: []string{"docs/why.md", "ui"}, Reaches: ".", Limit: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := artifactEvents(page); len(got) != 1 || got[0] != "near" {
		t.Fatalf("naming both a path and an anchor returned %v, want only the row satisfying both", got)
	}
}

// Adversarial: a path nobody ever recorded. The honest answer is an empty page
// that says it is empty, not a refusal that looks like a broken query.
func TestUnknownPathAnswersWithAnEmptyPage(t *testing.T) {
	page, err := BuildArtifactSelectionPage(artifactSnapshot(), ArtifactSelection{Paths: []string{"no/such/path"}, State: ArtifactStateAll, Limit: 50}, false)
	if err != nil {
		t.Fatalf("an unknown path was refused instead of answered: %v", err)
	}
	if page.MatchingTotal != 0 || page.Returned != 0 || page.Remaining != 0 || page.NextCursor != "" {
		t.Fatalf("an empty result did not say it was empty: %+v", page)
	}
}

// Adversarial: a selector matching every artifact in the log. It pages like any
// other answer; it never dumps the projection.
func TestASelectorMatchingEverythingIsPagedNotDumped(t *testing.T) {
	page, err := BuildArtifactSelectionPage(anchorSnapshot(), ArtifactSelection{Reaches: ".", State: ArtifactStateAll, Limit: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.Returned != 1 || page.MatchingTotal != 4 || page.Remaining != 3 || page.NextCursor == "" {
		t.Fatalf("a matches-everything selector did not bound itself: %+v", page)
	}
}

// The refusal on an empty path list is the thing that has always kept an
// unbounded dump off this surface, and it stays exactly where it was for every
// caller that names no anchor.
func TestNoPathsAndNoAnchorIsStillRefused(t *testing.T) {
	if _, err := BuildArtifactSelectionPage(artifactSnapshot(), ArtifactSelection{State: ArtifactStateAll, Limit: 50}, false); err == nil {
		t.Fatal("a query naming neither a path nor an anchor was admitted")
	}
}

// A cursor carries the filters it was minted under. Continuing one into a query
// with a different state would splice two result sets together and report the
// join as one page.
func TestAnArtifactCursorDoesNotCrossAChangeOfState(t *testing.T) {
	snapshot := artifactSnapshot()
	first, err := BuildArtifactSelectionPage(snapshot, ArtifactSelection{Paths: []string{"ui"}, State: ArtifactStateAll, Limit: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("the first page offered no continuation to test")
	}
	if _, err := BuildArtifactSelectionPage(snapshot, ArtifactSelection{Paths: []string{"ui"}, State: ArtifactStateLive, Limit: 1, Cursor: first.NextCursor}, false); err == nil {
		t.Fatal("a cursor crossed a change of state")
	}
}
