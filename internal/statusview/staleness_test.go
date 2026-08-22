package statusview

import (
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func stalenessWaveSnapshot() app.Snapshot {
	effective := func(event string) workroom.Decision {
		return workroom.Decision{Event: event, Verdict: workroom.Effective}
	}
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "dot", Path: ".", Retired: true},
			{Event: "doc", Path: "docs/why.md"},
			{Event: "after-retirement", Path: "docs/after.md"},
			{Event: "unrelated", Path: "ui"},
		},
		Acts: []workroom.Act{{Event: "retire", Type: "supersede", Target: "dot"}},
		Decisions: []workroom.Decision{
			effective("dot"), effective("request"), effective("doc"),
			effective("retire"), effective("after-retirement"), effective("unrelated"),
			{Event: "refused", Verdict: workroom.Ineffective},
		},
		Provenance: map[string][]string{
			"request":          {"dot"},
			"doc":              {"request"},
			"retire":           {"dot"}, // skipped: a supersession does not stale itself
			"after-retirement": {"retire"},
			"unrelated":        {"elsewhere"},
			"refused":          {"dot"},
		},
	}
	return app.Snapshot{Genesis: "genesis", Head: "wave-head", Depth: 7, Projection: projection}
}

func TestStalenessWaveMatchesFoldPropagation(t *testing.T) {
	wave, err := BuildStalenessWave(stalenessWaveSnapshot(), ".", false)
	if err != nil {
		t.Fatal(err)
	}
	if wave.Records != 7 || wave.Reached != 3 || wave.LiveArtifacts != 3 || wave.Reaching != 1 {
		t.Fatalf("unexpected wave summary: %+v", wave)
	}
}

// This is mutation-sensitive for the two easy overcounts: following an
// ineffective record and following a supersession's edge to its own target.
func TestStalenessWaveSkipsRefusedRecordsAndRetirementSelfEdge(t *testing.T) {
	snapshot := stalenessWaveSnapshot()
	wave, err := BuildStalenessWave(snapshot, ".", false)
	if err != nil {
		t.Fatal(err)
	}
	if wave.Reached != 3 {
		t.Fatalf("wave followed a refused or retirement-self edge: %+v", wave)
	}
	if wave.Reaching != 1 {
		t.Fatalf("wave counted after-retirement as carried by the retired target: %+v", wave)
	}
}

func TestStalenessWaveRequiresAnExactPath(t *testing.T) {
	if _, err := BuildStalenessWave(stalenessWaveSnapshot(), "", false); err == nil {
		t.Fatal("empty anchor path was accepted")
	}
}
