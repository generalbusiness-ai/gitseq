package workroom

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

// The projection of an artifact-heavy log reads the same intervals of the log
// over and over, and these tests hold down the answers the indexes now give for
// them. Every case here is about a fact the fold already stated; what changed is
// only how often it is recomputed, so each test is written against the
// projection rather than against the index.

// costReceipt is a merge receipt whose declared successor stands above the paths
// the merge changed, which is the shape that makes post-frontier artifacts
// count: an artifact covers a changed path when it stands at it or above it.
func costReceipt(t *testing.T, id, approval string) Record {
	t.Helper()
	return event(t, id, agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
		"merge_approval": approval, "merge_candidate": "head1", "merge_target_pre_head": "base",
		"merge_head": "merged", "merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
		"merge_changed_paths": `["spike/late.ts","spike/mid.ts"]`, "merge_left_live": `{}`,
	}}, approval)
}

// costArtifact is one ordinary publication at a path, by the implementer.
func costArtifact(t testing.TB, id, path, commit string, rests ...string) Record {
	t.Helper()
	return event(t, id, agent, SchemaState, State{Kind: KindArtifact, Text: "work at " + path,
		Body: map[string]string{"path": path, "commit": commit}}, rests...)
}

// costRecords assembles the approval a receipt needs, then the receipt, then
// whatever the caller wants to place after it in order, and finally the planned
// retirement of the reviewed candidate. Without that last act the candidate
// stays live beside the successor at the same path, and every count in these
// tests would be one higher for a reason that has nothing to do with them.
func costRecords(t *testing.T, after ...Record) []Record {
	t.Helper()
	records := []Record{
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved",
			Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		costReceipt(t, "merge", "approval"),
	}
	records = append(records, after...)
	records = append(records, event(t, "retire-r5", agent, SchemaSupersede,
		Supersede{Target: "r5", Text: "the merge replaced the reviewed candidate"}, "r5", "merge", "successor"))
	return reviewRecords(t, records...)
}

// The interval is the one the receipt opened and the successor closed. An
// artifact published inside it is a debt the merge did not account for; one
// published after the successor belongs to whatever comes next, and counting it
// here would attach a later lane's work to this merge.
func TestPostReceiptAccountingStopsAtTheSuccessor(t *testing.T) {
	inside := Fold(costRecords(t,
		costArtifact(t, "mid", "spike/mid.ts", "interim", "merge"),
		costArtifact(t, "successor", "spike", "merged", "merge"),
	))
	published := artifactByEvent(t, inside, "successor")
	if published.LivePredecessors != 1 || !published.SuccessionUnrecorded {
		t.Fatalf("artifact published inside the interval: live_predecessors=%d unrecorded=%v, want 1 and true",
			published.LivePredecessors, published.SuccessionUnrecorded)
	}
	if inside.OmittedSupersessions != 1 {
		t.Fatalf("omitted supersessions = %d, want the one unaccounted interval artifact", inside.OmittedSupersessions)
	}

	outside := Fold(costRecords(t,
		costArtifact(t, "successor", "spike", "merged", "merge"),
		costArtifact(t, "late", "spike/late.ts", "later", "merge"),
	))
	closed := artifactByEvent(t, outside, "successor")
	if closed.LivePredecessors != 0 || closed.SuccessionUnrecorded {
		t.Fatalf("artifact published after the successor: live_predecessors=%d unrecorded=%v, want 0 and false",
			closed.LivePredecessors, closed.SuccessionUnrecorded)
	}
	if outside.OmittedSupersessions != 0 {
		t.Fatalf("omitted supersessions = %d, want none: the later artifact is not this merge's debt", outside.OmittedSupersessions)
	}
}

// The interval walk is resumed, not restarted, and every answer is bounded by
// the successor that asked. Both facts are read directly here, and out of order
// on purpose: the projection asks in ascending order, and an answer that would
// be wrong in any other order is an answer that depends on its caller.
func TestIntervalWalkAnswersEachSuccessorAndRepeats(t *testing.T) {
	// Two successors at one path, with covered artifacts before each of them,
	// one artifact outside the declared successor paths entirely, and one that
	// stands under the successor path at a file the merge did not change. The
	// last is the interesting one: it belongs to the successor's tree and is
	// still not this merge's business.
	records := costRecords(t,
		costArtifact(t, "mid", "spike/mid.ts", "interim", "merge"),
		costArtifact(t, "successor", "spike", "merged", "merge"),
		costArtifact(t, "elsewhere", "docs/readme.md", "interim", "merge"),
		costArtifact(t, "unchanged-file", "spike/untouched.ts", "interim", "merge"),
		costArtifact(t, "mid2", "spike/late.ts", "interim", "merge"),
		costArtifact(t, "second-successor", "spike", "merged", "merge"),
	)
	state := NewFolder(records).state
	receipt := state.byID["merge"]
	first, second := state.byID["successor"], state.byID["second-successor"]
	scans := make(receiptScans)

	// The later successor first. Its interval holds the covered artifact before
	// the first successor, the first successor itself, and the second covered
	// artifact — and neither the publication outside the successor paths nor the
	// one at a file this merge did not change.
	if count, live := state.postReceiptAccounting(scans, receipt, second, "spike"); count != 3 || len(live) != 3 {
		t.Fatalf("second successor: count=%d live=%d, want 3 and 3", count, len(live))
	}
	// The earlier successor, asked afterwards, still gets its own interval.
	if count, live := state.postReceiptAccounting(scans, receipt, first, "spike"); count != 1 || len(live) != 1 {
		t.Fatalf("first successor after the second: count=%d live=%d, want 1 and 1", count, len(live))
	}
	// And asking again changes nothing: a walk that restarted would count the
	// same artifacts a second time.
	if count, _ := state.postReceiptAccounting(scans, receipt, second, "spike"); count != 3 {
		t.Fatalf("second successor asked twice: count=%d, want 3", count)
	}
}

// A retired interval artifact was counted at the receipt's frontier and is no
// longer owed. Both halves come from the same index, so a test that only
// counted would not see the second one.
func TestPostReceiptAccountingSeparatesCountedFromOwed(t *testing.T) {
	projection := Fold(costRecords(t,
		costArtifact(t, "mid", "spike/mid.ts", "interim", "merge"),
		costArtifact(t, "successor", "spike", "merged", "merge"),
		event(t, "retire-mid", agent, SchemaSupersede,
			Supersede{Target: "mid", Text: "the interval artifact was withdrawn"}, "mid", "successor"),
	))
	published := artifactByEvent(t, projection, "successor")
	if published.LivePredecessors != 1 {
		t.Fatalf("frozen count = %d, want the interval artifact still counted at the receipt's frontier",
			published.LivePredecessors)
	}
	if projection.OmittedSupersessions != 0 {
		t.Fatalf("omitted supersessions = %d, want none: the interval artifact is retired", projection.OmittedSupersessions)
	}
}

// pathCovers is called a few million times in one projection of an
// artifact-heavy log, so it may not allocate; and the separator is the whole
// rule, so each boundary is named.
func TestPathCoversBoundariesWithoutAllocating(t *testing.T) {
	for _, test := range []struct {
		name        string
		successor   string
		predecessor string
		covers      bool
	}{
		{name: "exact", successor: "docs/gs.md", predecessor: "docs/gs.md", covers: true},
		{name: "directory above", successor: "docs", predecessor: "docs/gs.md", covers: true},
		{name: "directory above, trailing slash", successor: "docs/", predecessor: "docs/gs.md", covers: true},
		{name: "directory itself, predecessor slashed", successor: "docs", predecessor: "docs/", covers: true},
		{name: "sibling sharing a prefix", successor: "doc", predecessor: "docs/gs.md"},
		{name: "same prefix, no separator", successor: "docs", predecessor: "docsets"},
		{name: "below its predecessor", successor: "docs/gs.md", predecessor: "docs"},
		{name: "empty successor", predecessor: "docs"},
		{name: "empty predecessor", successor: "docs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if covers := pathCovers(test.successor, test.predecessor); covers != test.covers {
				t.Fatalf("pathCovers(%q, %q) = %v, want %v", test.successor, test.predecessor, covers, test.covers)
			}
		})
	}
	allocations := testing.AllocsPerRun(1000, func() {
		if !pathCovers("docs/", "docs/reference/gs/artifact.md") {
			t.Fatal("the measured call does not cover, so the measurement is of the wrong branch")
		}
	})
	if allocations != 0 {
		t.Fatalf("pathCovers allocated %v objects per call, want none", allocations)
	}
}

// Every index built for a projection is built for that projection. A folder goes
// on accepting records, and the next projection must answer for them — including
// the retirement of an artifact an earlier projection counted as a debt, which
// is the fact the interval index stores beside the artifact.
func TestProjectionIndexesDoNotOutliveTheirFold(t *testing.T) {
	folder := NewFolder(costRecords(t,
		costArtifact(t, "mid", "spike/mid.ts", "interim", "merge"),
		costArtifact(t, "successor", "spike", "merged", "merge"),
	))
	first := folder.Projection()
	if first.OmittedSupersessions != 1 {
		t.Fatalf("inert fixture: omitted supersessions = %d before the append, want the interval debt",
			first.OmittedSupersessions)
	}
	folder.Append(event(t, "retire-mid", agent, SchemaSupersede,
		Supersede{Target: "mid", Text: "the interval artifact was withdrawn"}, "mid", "successor"))
	second := folder.Projection()
	if second.OmittedSupersessions != 0 {
		t.Fatalf("omitted supersessions = %d after the retirement, want none: the second projection read a stale index",
			second.OmittedSupersessions)
	}
	if !artifactByEvent(t, second, "mid").Retired {
		t.Fatal("the appended retirement did not reach the second projection at all")
	}
}

// The receipt-checkpoint answer is reused across the successors one receipt
// published, and may be reused only while the walk stays behind the receipt's
// own frontier. A rests-on edge naming an identifier that lands later is the
// case where the answer is still being decided, and the walk says so.
func TestReceiptWalkNoticesAForwardCitation(t *testing.T) {
	records := costRecords(t,
		costArtifact(t, "successor", "spike", "merged", "merge"),
	)
	state := NewFolder(records).state
	scope := state.stalenessAsOf(math.MaxInt)
	receipt := state.byID["merge"]

	behind := &receiptWalk{scope: scope, at: receipt.sequence(), frontier: receipt.index,
		plan: receipt.mergePlan, stale: map[string]bool{}, visited: map[string]bool{}}
	if !behind.causesSettledAtReceipt(receipt.record.ID) || behind.forward {
		t.Fatalf("walk over the receipt's own history: settled=%v forward=%v, want settled and behind the frontier",
			behind.causesSettledAtReceipt(receipt.record.ID), behind.forward)
	}

	// The successor lands after the receipt, so a walk that reads it has read
	// past the frontier. Nothing else about the answer changes.
	ahead := &receiptWalk{scope: scope, at: receipt.sequence(), frontier: receipt.index,
		plan: receipt.mergePlan, stale: map[string]bool{}, visited: map[string]bool{}}
	if !ahead.causesSettledAtReceipt("successor") || !ahead.forward {
		t.Fatalf("walk reaching a record after the frontier: forward=%v, want it noticed", ahead.forward)
	}
}

// The per-target supersession index is what the questions about one retirement
// are answered from, so it must hold every supersession naming that target, in
// log order, and nothing else. Order is part of the contract: succession gives
// the earliest qualifying supersession the say.
func TestSupersessionIndexHoldsEachTargetInLogOrder(t *testing.T) {
	records := reviewRecords(t,
		costArtifact(t, "other-artifact", "ui", "uihead", "r0"),
		event(t, "retire-r5-first", operator, SchemaSupersede,
			Supersede{Target: "r5", Text: "withdrawn"}, "r5"),
		event(t, "retire-ui", operator, SchemaSupersede,
			Supersede{Target: "other-artifact", Text: "withdrawn"}, "other-artifact"),
		event(t, "retire-r5-again", operator, SchemaSupersede,
			Supersede{Target: "r5", Text: "withdrawn a second time"}, "r5"),
	)
	state := NewFolder(records).state
	for target, want := range map[string][]string{
		"r5":             {"retire-r5-first", "retire-r5-again"},
		"other-artifact": {"retire-ui"},
		"r6":             nil,
	} {
		indexed := state.supersessionsByTarget[target]
		if len(indexed) != len(want) {
			t.Fatalf("supersessions of %s = %d records, want %d", target, len(indexed), len(want))
		}
		for position, record := range indexed {
			if record.record.ID != want[position] {
				t.Fatalf("supersessions of %s at %d = %s, want %s", target, position, record.record.ID, want[position])
			}
		}
	}
	// The index and the ordered slice are two views of one set, and a record in
	// one and not the other is the defect this pairing rules out.
	total := 0
	for _, indexed := range state.supersessionsByTarget {
		total += len(indexed)
	}
	if total != len(state.supersessions) {
		t.Fatalf("index holds %d supersessions, the log order holds %d", total, len(state.supersessions))
	}
}

// artifactHeavyHistory is the shape an implementation lane actually leaves: a
// run of merges, each publishing one successor at a directory above the paths it
// changed, with the individual file artifacts of that round below it. It is the
// shape the workrooms this lane was measured against hold, and the shape the
// synthetic envelope corpora do not: artifacts outnumber everything else, and
// every one of them stands inside some receipt's interval.
func artifactHeavyHistory(t testing.TB, merges, perMerge int) []Record {
	t.Helper()
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed",
			Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "agent-joins", operator, SchemaState, State{Kind: KindRoster, Text: "implementer joins",
			Body: map[string]string{"actor": agent, "kind": "agent", "name": "Implementer", "role": "participant"}}, "e0"),
		event(t, "agent-ratified", operator, SchemaRatify, Ratify{Target: "agent-joins"}, "agent-joins"),
		event(t, "other-joins", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins",
			Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "e0"),
		event(t, "other-ratified", operator, SchemaRatify, Ratify{Target: "other-joins"}, "other-joins"),
	}
	for round := range merges {
		changed := make([]string, 0, perMerge)
		for file := range perMerge {
			path := fmt.Sprintf("spike/round%04d/file%04d.ts", round, file)
			changed = append(changed, path)
			records = append(records, costArtifact(t, fmt.Sprintf("a:%d:%d", round, file), path,
				fmt.Sprintf("head:%d", round), "e0"))
		}
		encoded, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		reviewed := fmt.Sprintf("a:%d:0", round)
		request := fmt.Sprintf("request:%d", round)
		promise := fmt.Sprintf("promise:%d", round)
		approval := fmt.Sprintf("approval:%d", round)
		receipt := fmt.Sprintf("merge:%d", round)
		successor := fmt.Sprintf("successor:%d", round)
		records = append(records,
			event(t, request, operator, SchemaState, State{Kind: KindRequest, Text: "review it",
				Body: map[string]string{"to": other, "conditions": "exact head"}}, reviewed),
			event(t, promise, other, SchemaState, State{Kind: KindPromise, Text: "will review"}, request),
			event(t, approval, other, SchemaState, State{Kind: KindReport, Text: "approved",
				Body: map[string]string{"verdict": "approved", "head": fmt.Sprintf("head:%d", round), "artifact": reviewed}}, promise, reviewed),
			event(t, fmt.Sprintf("approval-ratified:%d", round), operator, SchemaRatify, Ratify{Target: approval}, approval),
			event(t, receipt, agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
				"merge_approval": approval, "merge_candidate": fmt.Sprintf("head:%d", round), "merge_target_pre_head": "base",
				"merge_head": fmt.Sprintf("merged:%d", round), "merge_retirements": fmt.Sprintf(`{%q:"spike"}`, reviewed),
				"merge_successors": `["spike"]`, "merge_changed_paths": string(encoded), "merge_left_live": `{}`,
			}}, approval),
			costArtifact(t, successor, "spike", fmt.Sprintf("merged:%d", round), receipt),
			event(t, fmt.Sprintf("retire:%d", round), agent, SchemaSupersede,
				Supersede{Target: reviewed, Text: "the merge replaced the reviewed candidate"}, reviewed, receipt, successor))
	}
	return records
}

// BenchmarkFoldArtifactHeavy is the cold cost: one verified read folded from the
// beginning, which is what a resident pays when it starts.
func BenchmarkFoldArtifactHeavy(b *testing.B) {
	for _, merges := range []int{10, 40, 160} {
		records := artifactHeavyHistory(b, merges, 24)
		b.Run(fmt.Sprintf("merges-%d", merges), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

// BenchmarkProjectionArtifactHeavy is the interactive cost: the folder already
// holds the log, one act arrives, and the reader asks for the projection again.
// This is the number a durable act waits on.
func BenchmarkProjectionArtifactHeavy(b *testing.B) {
	for _, merges := range []int{10, 40, 160} {
		records := artifactHeavyHistory(b, merges, 24)
		folder := NewFolder(records)
		b.Run(fmt.Sprintf("merges-%d", merges), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				folder.Projection()
			}
		})
	}
}
