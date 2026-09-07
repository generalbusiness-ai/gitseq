package wireparity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The second gate in this package: Go and the browser must reach the same
// named populations. Why they are owned in one place is in workroom/work.go.
//
// The fixture is a frozen status answer both languages read. Go folds the
// named populations out of its commitments with workroom.WorkOf; the browser
// counts the same populations by running its own row selector over the same
// projection, in ui/test/work-populations.test.mjs. Both compare against the
// `work` block the fixture records, so a grouping that moves on one surface
// and not the other fails here or there rather than on somebody's screen.

type frozenStatus struct {
	Work    workroom.WorkSummary `json:"work"`
	Durable struct {
		Projection workroom.Projection `json:"projection"`
	} `json:"durable"`
}

func fixturePath() string {
	return filepath.Join("..", "..", "ui", "test", "fixtures", "work-populations.json")
}

func readFrozenStatus(t *testing.T) frozenStatus {
	t.Helper()
	raw, err := os.ReadFile(fixturePath())
	if err != nil {
		t.Fatalf("reading the shared work-population fixture: %v", err)
	}
	var frozen frozenStatus
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatalf("decoding the shared work-population fixture: %v", err)
	}
	return frozen
}

func TestGoCountsTheFixtureWorkPopulations(t *testing.T) {
	frozen := readFrozenStatus(t)
	got := workroom.WorkOf(frozen.Durable.Projection)
	if !reflect.DeepEqual(got, frozen.Work) {
		t.Errorf("the Go owner and the recorded populations disagree.\n  recorded: %+v\n  Go:       %+v", frozen.Work, got)
	}
	// The partition is the property that makes these numbers addable. The
	// overlapping counts are deliberately outside it.
	total := got.Open + got.Completed + got.ClosedNotCompleted + got.Stale + got.Other
	if total != got.Commitments {
		t.Errorf("populations sum to %d, commitments are %d", total, got.Commitments)
	}
	lanes := 0
	for _, count := range got.OpenLifecycles {
		lanes += count
	}
	if lanes != got.Open {
		t.Errorf("open lifecycles sum to %d, open is %d", lanes, got.Open)
	}
}

// The negative control the request asked for: drop one awaiting-review member
// from the frozen projection and the comparison must notice. A parity check
// nobody has watched fail is a parity check nobody knows is running.
func TestDroppingAnAwaitingReviewMemberFailsTheGoParityCheck(t *testing.T) {
	frozen := readFrozenStatus(t)
	commitments := frozen.Durable.Projection.Commitments
	dropped := false
	kept := make([]workroom.Commitment, 0, len(commitments))
	for _, commitment := range commitments {
		if !dropped && commitment.Status == "awaiting-review" {
			dropped = true
			continue
		}
		kept = append(kept, commitment)
	}
	if !dropped {
		t.Fatal("the fixture has no awaiting-review commitment to drop; the control proves nothing")
	}
	frozen.Durable.Projection.Commitments = kept
	got := workroom.WorkOf(frozen.Durable.Projection)
	if reflect.DeepEqual(got, frozen.Work) {
		t.Fatal("dropping an awaiting-review commitment changed no count; the comparison cannot detect a missing member")
	}
	if got.Open != frozen.Work.Open-1 || got.OpenLifecycles["awaiting-review"] != frozen.Work.OpenLifecycles["awaiting-review"]-1 {
		t.Errorf("the drop was not counted where it happened: open %d, awaiting-review %d", got.Open, got.OpenLifecycles["awaiting-review"])
	}
}

// The other half of the control: a surface that groups awaiting-review outside
// the open population disagrees with the recorded answer.
func TestARegroupedSurfaceFailsTheGoParityCheck(t *testing.T) {
	frozen := readFrozenStatus(t)
	regrouped := workroom.WorkOf(frozen.Durable.Projection)
	awaiting := regrouped.OpenLifecycles["awaiting-review"]
	if awaiting == 0 {
		t.Fatal("the fixture has no awaiting-review commitment; the control proves nothing")
	}
	regrouped.Open -= awaiting
	regrouped.ClosedNotCompleted += awaiting
	if reflect.DeepEqual(regrouped, frozen.Work) {
		t.Fatal("moving awaiting-review out of open matched the recorded answer; the comparison is not comparing")
	}
}
