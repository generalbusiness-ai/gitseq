package statusview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Build selects the rows it will show before shaping them. That is only sound
// if the output is unchanged, and "the tests still pass" does not show it: the
// tests were written against the old shape and would keep passing through a
// wrong reordering. So the property is pinned against bytes produced by the
// previous implementation, captured from main b7f5b015 before the split.
//
// The fixture is built to be hostile to this specific change. It fills every
// list — both commitment lanes, current, stale and retired artifacts,
// ineffective and disputed attempts, and dissents with provenance — so no lane
// is silently unexercised. It varies the three artifact note flags, because
// extracting artifactState is the part most likely to drift from the switch it
// replaced. And it puts terminal control sequences and a bidi override in every
// user-controlled field, so that the day this text is escaped rather than
// merely trimmed, this test says whether the escaping moved with it.
//
// That day came. Text now escapes controls, bidi overrides and invalid UTF-8
// instead of passing them through, so these bytes were recaptured from the
// escaping implementation. The property the test pins is unchanged: selection
// happens before shaping, and the output is exact. The golden is verified to
// contain no control byte and to carry the escapes as visible text.
func TestBoundedSelectionKeepsExactlyTheOldBytes(t *testing.T) {
	want, err := os.ReadFile("testdata/bounded-summary-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(Build("genesis", "head", 999, hostileProjection(60)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("bounded summary changed:\n got %d bytes\nwant %d bytes\nfirst divergence at %d",
			len(got), len(want), firstDivergence(got, want))
	}
}

func firstDivergence(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func hostileProjection(size int) workroom.Projection {
	p := workroom.Projection{Actors: map[string]workroom.ActorState{
		"requester": {Name: "Ada"}, "performer": {Name: "Grace"}, "dissenter": {Name: "Hopper"},
	}, Provenance: map[string][]string{}}
	hostile := "\x1b[2J\x1b[1;1H OWNED \x07 ‮reversed‬ "
	for i := range size {
		req := fmt.Sprintf("request:%05d", i)
		p.Statements = append(p.Statements, workroom.Statement{
			Event: req, Actor: "requester", Kind: workroom.KindRequest,
			Text: hostile + strings.Repeat("bounded request text ", 40) + fmt.Sprint(i),
		})
		d := fmt.Sprintf("dissent:%05d", i)
		p.Statements = append(p.Statements, workroom.Statement{
			Event: d, Actor: "dissenter", Kind: workroom.KindDissent, Text: hostile + "dissenting " + fmt.Sprint(i),
		})
		p.Provenance[d] = []string{req}
		p.Commitments = append(p.Commitments,
			workroom.Commitment{Request: req, Requester: "requester", Performer: "performer", WaitingOn: "performer", Status: "promised"},
			workroom.Commitment{Request: req + ":stale", Requester: "requester", AddressedTo: "performer", Status: "stale", Stale: true},
			workroom.Commitment{Request: req + ":done", Requester: "requester", Performer: "performer", Status: "satisfied"},
		)
		p.Artifacts = append(p.Artifacts,
			workroom.Artifact{Event: fmt.Sprintf("artifact:current:%05d", i), Path: hostile + strings.Repeat("path/", 80) + fmt.Sprint(i), Commit: fmt.Sprintf("c-cur-%05d", i)},
			workroom.Artifact{Event: fmt.Sprintf("artifact:stale:%05d", i), Path: "stale/path", Commit: fmt.Sprintf("c-stale-%05d", i), Stale: true, DescribesSupersededWorld: i%3 == 0, UnableToFlare: i%5 == 0, SuccessionUnrecorded: i%7 == 0},
			workroom.Artifact{Event: fmt.Sprintf("artifact:retired:%05d", i), Path: "retired/path", Commit: fmt.Sprintf("c-ret-%05d", i), Retired: true},
		)
		p.Decisions = append(p.Decisions,
			workroom.Decision{Event: fmt.Sprintf("attempt:ineffective:%05d", i), Sequence: i * 2, Verdict: workroom.Ineffective, Reason: hostile + strings.Repeat("reason ", 80)},
			workroom.Decision{Event: fmt.Sprintf("attempt:disputed:%05d", i), Sequence: i*2 + 1, Verdict: workroom.Disputed, Reason: "disputed"},
			workroom.Decision{Event: req, Sequence: i * 10, Verdict: workroom.Effective},
			workroom.Decision{Event: d, Sequence: i*10 + 1, Verdict: workroom.Effective},
		)
	}
	return p
}
