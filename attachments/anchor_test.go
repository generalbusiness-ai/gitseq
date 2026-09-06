package docset

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Gate 3, second half: the acts a page names have to be real.
//
// The other basis and flare gates replay the declared graph into a scratch
// workroom, which is what lets them run at the commit that introduces a page.
// It also means they take the front matter at its word: a stand-in is minted
// for whatever a page cites, so a well-formed identifier naming nothing at all
// produces a model that flares perfectly and a set that is anchored to
// nothing. The reviewer who found that was right, and this is the part of it a
// test can settle.
//
// A basis, unlike a page's own artifact, must already exist before the commit
// that names it — an implementing commit carries the governing event in a
// trailer, so the event precedes the work. So this gate may consult the real
// record, and does.
//
// What it cannot settle is whether the artifact a page resolves to is the
// *right* one for the prose on that page. That is a judgement about what the
// text claims against what the code does, and nothing in this repository can
// make it.

func TestGateEveryNamedActResolvesToALiveRecord(t *testing.T) {
	root := mustRoot(t)
	pages := mustPages(t, root)
	acts := declaredActs(pages)
	if len(acts) == 0 {
		t.Fatal("the set names no governing acts at all")
	}

	workspace, err := app.Open(context.Background(), root)
	if err != nil {
		t.Skipf("no workroom in this checkout, so the named acts cannot be resolved here: %v", err)
	}
	genesis, _, _ := strings.Cut(acts[0], "#")
	if genesis != "git:sha1:"+workspace.View().Genesis && genesis != "git:sha256:"+workspace.View().Genesis {
		t.Skipf("the set names workroom %s and this checkout holds %s", genesis, workspace.View().Genesis)
	}
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]workroom.Kind, len(snapshot.Projection.Statements))
	for _, statement := range snapshot.Projection.Statements {
		kinds[statement.Event] = statement.Kind
	}
	artifacts := make(map[string]workroom.Artifact, len(snapshot.Projection.Artifacts))
	for _, artifact := range snapshot.Projection.Artifacts {
		artifacts[artifact.Event] = artifact
	}

	// Every fatal citation fails here, directly. There is no exception list:
	// the migration that emptied one finished, and a list kept past its last
	// entry is only a place for the next defect to be filed instead of fixed.
	for _, act := range acts {
		kind, found := kinds[act]
		artifact := artifacts[act]
		verdict := ClassifyCitation(found, kind == workroom.KindArtifact, artifact.Retired, artifact.Succeeded, artifact.Stale, artifact.Path, artifact.Commit)
		switch {
		case verdict.Fatal:
			naming := dependents(pages, act)
			sort.Strings(naming)
			t.Errorf("%s %s\n  re-anchor the pages naming it to a live artifact at an exact path in the tree:\n  %s",
				act, verdict.Reason, strings.Join(naming, "\n  "))
		case verdict.Report:
			t.Logf("%s %s:\n  %s", act, verdict.Reason, strings.Join(dependents(pages, act), "\n  "))
		}
	}
}
