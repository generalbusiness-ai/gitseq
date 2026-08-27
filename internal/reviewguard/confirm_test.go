package reviewguard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// scriptedRead serves each stage's projection on successive reads: every
// stage lists the extra statements that read observes beyond the quiet lane.
// Reads past the last stage hold the final world. The frontier stays fixed
// across reads, so a difference between two reads is exactly the staged
// movement and nothing else.
func scriptedRead(stages ...[]workroom.Statement) (ReadFunc, *int) {
	calls := 0
	return func() (Basis, []News, workroom.Projection, error) {
		stage := stages[len(stages)-1]
		if calls < len(stages) {
			stage = stages[calls]
		}
		projection := reviewLane(head, stage...)
		projection.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: head}}
		calls++
		basis, news, err := ReviewBasis(Read{
			Projection:          projection,
			ReviewerFingerprint: "reviewer",
			FrontierEvent:       "frontier",
			NoCheckout:          true,
		}, "artifact", "promise")
		return basis, news, projection, err
	}, &calls
}

func newsStatement(event string) workroom.Statement {
	return workroom.Statement{
		Event: event, Actor: "stranger", Kind: workroom.KindAssert,
		Body: map[string]string{"head": head},
	}
}

func TestConfirmBuildsFromTheConfirmingReadInExactlyThreeReads(t *testing.T) {
	read, calls := scriptedRead(nil, nil, nil)
	body, restsOn, err := Confirm(read, []string{"artifact"}, nil, VerdictApproved, "APPROVED exact head")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Fatalf("confirmation reads = %d, want 3", *calls)
	}
	if body["verdict"] != VerdictApproved || body["head"] != head || body["artifact"] != "artifact" {
		t.Fatalf("verdict body = %#v", body)
	}
	if body["review_path"] != ReviewPath || body["review_frontier"] != "frontier" {
		t.Fatalf("guard fields lost: %#v", body)
	}
	// The quiet lane's matched news is the promise and the primary artifact,
	// both already cited: the canonical array records them without demanding
	// separate acknowledgments.
	if body["head_news_acknowledged"] != `["promise","artifact"]` {
		t.Fatalf("acknowledged array = %s", body["head_news_acknowledged"])
	}
	if got := strings.Join(restsOn, ","); got != "promise,request,artifact" {
		t.Fatalf("rests_on = %s, want the promise, request, and primary artifact", got)
	}
}

func TestConfirmRefusesMovementBetweenTheFirstTwoReads(t *testing.T) {
	read, calls := scriptedRead(nil, []workroom.Statement{newsStatement("early-news")})
	_, _, err := Confirm(read, []string{"artifact"}, nil, VerdictApproved, "must not be signed")
	if err == nil || err.Error() != "review basis changed while validating; rerun and acknowledge any head news it names" {
		t.Fatalf("movement during validation error = %v", err)
	}
	if *calls != 2 {
		t.Fatalf("reads before refusing = %d, want 2", *calls)
	}
}

func TestConfirmRefusesMovementBetweenTheLastTwoReads(t *testing.T) {
	read, calls := scriptedRead(nil, nil, []workroom.Statement{newsStatement("late-news")})
	_, _, err := Confirm(read, []string{"artifact"}, nil, VerdictApproved, "must not be signed")
	if err == nil || err.Error() != "review basis changed before signing; rerun and acknowledge any head news it names" {
		t.Fatalf("movement before signing error = %v", err)
	}
	if *calls != 3 {
		t.Fatalf("reads before refusing = %d, want 3", *calls)
	}
}

func TestConfirmValidatesTheCitedSetBeforeReadingAgain(t *testing.T) {
	read, calls := scriptedRead(nil)
	_, _, err := Confirm(read, []string{"artifact", "ghost"}, nil, VerdictApproved, "must not be signed")
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) || !strings.Contains(err.Error(), "not effective") {
		t.Fatalf("cited-set error = %v", err)
	}
	if *calls != 1 {
		t.Fatalf("reads before refusing = %d, want 1", *calls)
	}
}

func TestConfirmHoldsAcknowledgmentsToTheConfirmingReadNews(t *testing.T) {
	stages := []workroom.Statement{newsStatement("noted")}
	read, _ := scriptedRead(stages, stages, stages)
	_, _, err := Confirm(read, []string{"artifact"}, nil, VerdictApproved, "must not be signed")
	if err == nil || !strings.Contains(err.Error(), "not acknowledged") || !strings.Contains(err.Error(), quoted("noted")) {
		t.Fatalf("unacknowledged news error = %v", err)
	}

	read, calls := scriptedRead(stages, stages, stages)
	body, restsOn, err := Confirm(read, []string{"artifact"}, []string{"noted"}, VerdictApproved, "APPROVED with news seen")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Fatalf("confirmation reads = %d, want 3", *calls)
	}
	want := `["promise","artifact","noted"]`
	if body["head_news_acknowledged"] != want {
		t.Fatalf("acknowledged array = %s, want %s", body["head_news_acknowledged"], want)
	}
	if got := strings.Join(restsOn, ","); got != "promise,request,artifact,noted" {
		t.Fatalf("rests_on = %s, want the verdict citations plus the acknowledged news", got)
	}
}

// A caller can make an acknowledgment value arbitrarily hostile; the refusal
// may name it but may not carry it whole.
func TestConfirmBoundsCallerSuppliedValuesInRefusals(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	read, _ := scriptedRead(nil)
	_, _, err := Confirm(read, []string{"artifact"}, []string{"frontier", huge, huge}, VerdictApproved, "must not be signed")
	if err == nil || !strings.Contains(err.Error(), "is given twice") {
		t.Fatalf("duplicate acknowledgment error = %v", err)
	}
	message := err.Error()
	if len(message) > 300 {
		t.Fatalf("duplicate acknowledgment message carries %d bytes, want a bounded quotation", len(message))
	}
	if !strings.Contains(message, fmt.Sprintf("%q", huge[:echoCap])) {
		t.Fatalf("bounded message lost its useful diagnostic prefix: %s", message)
	}
}
