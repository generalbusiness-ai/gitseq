package docset

import (
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Gate 3, no empty basis. Every page names at least one durable act, and the
// check runs through the fold's own unbridged-artifact mark rather than
// through a rule private to this test. A page that names nothing produces an
// artifact that cites nothing, the fold marks it `unable to flare`, and this
// gate fails — which is the same signal a reader sees in `gs status`.

func TestGateEveryPageNamesAGoverningAct(t *testing.T) {
	root := mustRoot(t)
	pages := mustPages(t, root)

	genesis := ""
	for _, page := range pages {
		if page.Title == "" {
			t.Errorf("%s: front matter has no title", page.Path)
		}
		if page.Summary == "" {
			t.Errorf("%s: front matter has no summary", page.Path)
		}
		if len(page.RestsOn) == 0 {
			t.Errorf("%s: front matter names no governing act; prose that cannot go stale is not documentation, it is a rumour", page.Path)
			continue
		}
		for _, act := range page.RestsOn {
			if !ValidEventID(act) {
				t.Errorf("%s: %q is not a canonical event identifier; an abbreviated citation resolves to nothing", page.Path, act)
				continue
			}
			workroomOf, _, _ := strings.Cut(act, "#")
			if genesis == "" {
				genesis = workroomOf
			} else if workroomOf != genesis {
				t.Errorf("%s: %q names a different workroom from the rest of the set (%s)", page.Path, act, genesis)
			}
		}
	}
}

func TestGateNoPageIsUnableToFlare(t *testing.T) {
	root := mustRoot(t)
	pages := mustPages(t, root)
	built := buildModel(t, pages)
	artifacts := built.artifacts(t)

	for _, page := range pages {
		artifact, ok := artifacts[built.artifact[page.Path]]
		if !ok {
			t.Fatalf("%s: modelled artifact missing from the projection", page.Path)
		}
		if artifact.UnableToFlare {
			t.Errorf("%s: the fold marks this page unable to flare; its silence would not be currency", page.Path)
		}
	}
}

// The mark this gate relies on has to be real. If `unable_to_flare` stopped
// being set, the gate above would pass on a set with no anchoring at all, so
// the mark is exercised directly in both directions.
func TestGateUnbridgedMarkStillFires(t *testing.T) {
	built := buildModel(t, []Page{})
	built.append(t, "unbridged", workroom.SchemaState, workroom.State{
		Kind: workroom.KindArtifact,
		Text: "a page with no basis",
		Body: map[string]string{"path": "docs/unbridged.md", "commit": fakeCommit("unbridged")},
	})
	built.append(t, "anchored", workroom.SchemaState, workroom.State{
		Kind: workroom.KindArtifact,
		Text: "a page with a basis",
		Body: map[string]string{"path": "docs/anchored.md", "commit": fakeCommit("anchored")},
	}, built.seed)

	artifacts := built.artifacts(t)
	if !artifacts["unbridged"].UnableToFlare {
		t.Error("an artifact citing nothing is not marked unable to flare; gate 3 would pass an unanchored set")
	}
	if artifacts["anchored"].UnableToFlare {
		t.Error("an artifact citing a live event is marked unable to flare")
	}
}
