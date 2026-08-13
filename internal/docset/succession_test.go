package docset

import (
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The other half of the citation rule. ClassifyCitation is told whether a
// retirement said where the behaviour went; this is what decides that, and it
// has to distinguish a retirement that names a covering successor from one that
// names something else, something narrower, or nothing at all.
func TestSucceededRetirementsFollowsOnlyACoveringSuccessor(t *testing.T) {
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "same-path-old", Path: "cmd/gs", Retired: true},
			{Event: "same-path-new", Path: "cmd/gs"},
			{Event: "narrowed-old", Path: "internal/workroom", Retired: true},
			{Event: "narrowed-new", Path: "internal/workroom/fold.go"},
			{Event: "widened-old", Path: "internal/app/app.go", Retired: true},
			{Event: "widened-new", Path: "internal/app"},
			{Event: "elsewhere-old", Path: "docs", Retired: true},
			{Event: "elsewhere-new", Path: "ui"},
			{Event: "deleted", Path: "gone.go", Retired: true},
			{Event: "refused-old", Path: "Makefile", Retired: true},
			{Event: "refused-new", Path: "Makefile"},
			{Event: "live", Path: "go.mod"},
		},
		Acts: []workroom.Act{
			{Event: "a1", Type: "supersede", Target: "same-path-old", Verdict: workroom.Effective},
			{Event: "a2", Type: "supersede", Target: "narrowed-old", Verdict: workroom.Effective},
			{Event: "a3", Type: "supersede", Target: "widened-old", Verdict: workroom.Effective},
			{Event: "a4", Type: "supersede", Target: "elsewhere-old", Verdict: workroom.Effective},
			{Event: "a5", Type: "supersede", Target: "deleted", Verdict: workroom.Effective},
			{Event: "a6", Type: "supersede", Target: "refused-old", Verdict: workroom.Ineffective},
			{Event: "a7", Type: "ratify", Target: "live", Verdict: workroom.Effective},
		},
		Provenance: map[string][]string{
			"a1": {"same-path-old", "same-path-new"},
			"a2": {"narrowed-old", "narrowed-new"},
			"a3": {"widened-old", "widened-new"},
			"a4": {"elsewhere-old", "elsewhere-new"},
			"a5": {"deleted"},
			"a6": {"refused-old", "refused-new"},
			"a7": {"live"},
		},
	}
	succeeded := SucceededRetirements(projection)
	for _, event := range []string{"same-path-old", "widened-old"} {
		if !succeeded[event] {
			t.Errorf("%s was retired with a covering successor and was not recognised", event)
		}
	}
	for _, event := range []string{
		// A successor inside the retired directory does not stand over it: the
		// rest of that tree lost its pointer and nothing replaced it.
		"narrowed-old",
		// A pointer at an unrelated path says nothing about this one.
		"elsewhere-old",
		// A deletion names nothing, which is exactly the case that must stay
		// fatal for whoever cited it.
		"deleted",
		// A supersession the fold refused retired nothing.
		"refused-old",
		"live",
	} {
		if succeeded[event] {
			t.Errorf("%s was treated as succeeded", event)
		}
	}
}
