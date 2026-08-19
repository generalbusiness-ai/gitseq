package statusview

import (
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

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
