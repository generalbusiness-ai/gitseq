package workroom

import (
	"reflect"
	"testing"
)

// previewHistory is a small complete world: an operator, a participant, one
// request addressed to that participant, and the promise that claims it.
func previewHistory(t *testing.T) []Record {
	t.Helper()
	return []Record{
		event(t, "r0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "r1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "r0"),
		event(t, "r2", operator, SchemaRatify, Ratify{Target: "r1"}, "r1"),
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "do the work", Body: map[string]string{"to": agent, "conditions": "publish the exact head"}}, "r0"),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "on it"}, "request"),
	}
}

// The preview is the fold's own answer, so for every prospective record it must
// say exactly what appending that record says. A preview that could disagree
// with the log would be the second rule set this whole mechanism exists to
// avoid.
func TestPreviewGivesTheDecisionTheAppendGives(t *testing.T) {
	history := previewHistory(t)
	for _, probe := range []struct {
		name   string
		record Record
	}{
		{"artifact with no path", event(t, "next", agent, SchemaState, State{Kind: KindArtifact, Text: "head", Body: map[string]string{"commit": "abc"}}, "promise")},
		{"artifact admitted", event(t, "next", agent, SchemaState, State{Kind: KindArtifact, Text: "head", Body: map[string]string{"path": "feature.txt", "commit": "abc"}}, "promise")},
		{"report on somebody else's promise", event(t, "next", operator, SchemaState, State{Kind: KindReport, Text: "done", Body: nil}, "promise")},
		{"report admitted", event(t, "next", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise")},
		{"dangling promise", event(t, "next", agent, SchemaState, State{Kind: KindPromise, Text: "on it"}, "r0")},
		{"ratify target unknown", event(t, "next", operator, SchemaRatify, Ratify{Target: "nothing"}, "nothing")},
		{"ratify admitted", event(t, "next", operator, SchemaRatify, Ratify{Target: "r1"}, "r1")},
		{"supersede another actor's record", event(t, "next", agent, SchemaSupersede, Supersede{Target: "request", Text: "no"}, "request")},
		{"supersede own record", event(t, "next", agent, SchemaSupersede, Supersede{Target: "promise", Text: "reneged"}, "promise")},
		{"undefined kind", event(t, "next", agent, SchemaState, State{Kind: "invented", Text: "what"}, "r0")},
	} {
		t.Run(probe.name, func(t *testing.T) {
			folder := NewFolder(history)
			preview := folder.Preview(probe.record)
			appended := NewFolder(append(append([]Record(nil), history...), probe.record))
			decision, decided := appended.Projection().Decision("next")
			if !decided {
				t.Fatal("the appended record has no decision")
			}
			if preview.Verdict != decision.Verdict || preview.Reason != decision.Reason {
				t.Fatalf("preview = %s %q, append = %s %q", preview.Verdict, preview.Reason, decision.Verdict, decision.Reason)
			}
		})
	}
}

// A preview is a question, not an act. Asking it must leave the folded log
// saying exactly what it said before, and must not change what the next real
// record is decided against.
func TestPreviewLeavesTheFoldUnchanged(t *testing.T) {
	history := previewHistory(t)
	folder := NewFolder(history)
	before := folder.Projection()
	probes := []Record{
		event(t, "probe-1", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise"),
		event(t, "probe-2", agent, SchemaState, State{Kind: KindArtifact, Text: "head", Body: map[string]string{"path": "feature.txt", "commit": "abc"}}, "promise"),
		event(t, "probe-3", operator, SchemaRatify, Ratify{Target: "r1"}, "r1"),
		event(t, "probe-4", agent, SchemaSupersede, Supersede{Target: "promise", Text: "reneged"}, "promise"),
	}
	for _, probe := range probes {
		folder.Preview(probe)
	}
	if after := folder.Projection(); !reflect.DeepEqual(before, after) {
		t.Fatal("previewing changed the projection")
	}
	// The next real record must be decided against the world the previews did
	// not join, so a previewed record cannot leave anything behind that a later
	// append would read.
	report := event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise")
	folder.Append(report)
	fresh := NewFolder(append(append([]Record(nil), history...), report))
	if !reflect.DeepEqual(folder.Projection(), fresh.Projection()) {
		t.Fatal("a fold that answered previews decided the next record differently")
	}
}
