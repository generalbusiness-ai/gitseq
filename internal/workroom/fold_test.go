package workroom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unsafe"
)

func TestResidentFolderSharesRepeatedDurableStrings(t *testing.T) {
	text := strings.Repeat("same durable text ", 256)
	body := map[string]string{"conditions": strings.Repeat("same condition ", 128)}
	records := []Record{
		event(t, "seed", operator, SchemaState, State{Kind: KindAssert, Text: text, Body: body}),
		event(t, "next", operator, SchemaState, State{Kind: KindAssert, Text: text, Body: body}, "seed"),
	}
	records[0].ID = strings.Clone("seed")
	records[1].RestsOn[0] = strings.Clone("seed")
	folder := NewFolder(nil)
	folder.Append(records[0])
	for index := range records[0].Payload {
		records[0].Payload[index] ^= 0xff
	}
	folder.Append(records[1])
	for index := range records[1].Payload {
		records[1].Payload[index] ^= 0xff
	}
	first := folder.state.records[0].body.(*State)
	second := folder.state.records[1].body.(*State)
	if unsafe.StringData(first.Text) != unsafe.StringData(second.Text) {
		t.Fatal("repeated durable text retained separate backing storage")
	}
	if unsafe.StringData(first.Body["conditions"]) != unsafe.StringData(second.Body["conditions"]) {
		t.Fatal("repeated body value retained separate backing storage")
	}
	if unsafe.StringData(folder.state.records[0].record.ID) != unsafe.StringData(folder.state.records[1].record.RestsOn[0]) {
		t.Fatal("cited event id retained separate backing storage")
	}
	if got := folder.Projection(); len(got.Statements) != 2 || got.Statements[0].Text != text || got.Statements[1].Body["conditions"] != body["conditions"] {
		t.Fatalf("string sharing changed the projection: %+v", got.Statements)
	}
}

func TestRejectedPayloadDoesNotPolluteResidentStringPool(t *testing.T) {
	unique := strings.Repeat("rejected durable text ", 512)
	cases := []struct {
		name     string
		payload  string
		rejected []string
	}{
		{
			name:     "unknown field",
			payload:  fmt.Sprintf(`{"kind":"assert","text":%q,"unknown":"x"}`, unique),
			rejected: []string{unique},
		},
		{
			name:    "duplicate text",
			payload: fmt.Sprintf(`{"kind":"assert","text":%q,"text":%q,"text":%q}`, unique+" first", unique+" second", unique+" third"),
			rejected: []string{
				unique + " first",
				unique + " second",
				unique + " third",
			},
		},
		{
			name:     "noncanonical escape",
			payload:  `{"kind":"assert","text":"\u007a rejected durable text"}`,
			rejected: []string{"z rejected durable text"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			folder := NewFolder([]Record{event(t, "seed", operator, SchemaState, State{Kind: KindAssert, Text: "seed"})})
			before := len(folder.state.strings)
			folder.Append(Record{
				ID:      "rejected",
				Actor:   operator,
				Schema:  SchemaState,
				Payload: []byte(test.payload),
			})
			decision, ok := folder.Projection().Decision("rejected")
			if !ok || decision.Verdict != Ineffective {
				t.Fatalf("rejected payload decision = %+v, %v", decision, ok)
			}
			if got := len(folder.state.strings); got != before+1 {
				t.Fatalf("resident string count = %d, want %d (only the event ID)", got, before+1)
			}
			for _, value := range test.rejected {
				if _, retained := folder.state.strings[value]; retained {
					t.Fatalf("rejected payload retained %q", value)
				}
			}
		})
	}
}

const (
	operator = "actor:operator"
	agent    = "actor:agent"
	other    = "actor:other"
	// A participant with no part in any review: not the implementer, not the
	// approver, not a ratifier. What such an actor can do is the question the
	// merge receipt authority has to answer.
	bystander = "actor:bystander"
)

func event(t testing.TB, id, actor, schema string, payload any, rests ...string) Record {
	t.Helper()
	encoded, err := Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Record{ID: id, Actor: actor, Schema: schema, Payload: encoded, RestsOn: rests}
}

func BenchmarkFoldRosterHeavy(b *testing.B) {
	for _, actors := range []int{100, 1000, 5000} {
		records := rosterHeavyHistory(b, actors)
		b.Run(fmt.Sprintf("actors-%d", actors), func(b *testing.B) {
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

func BenchmarkFoldRequestHeavy(b *testing.B) {
	for _, requests := range []int{1000, 5000, 20000} {
		records := requestHeavyHistory(b, requests)
		b.Run(fmt.Sprintf("requests-%d", requests), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

// BenchmarkFoldDeclaredKindChurn holds the two factors whose intersection the
// definition refresh is superlinear in: a growing catalog of declared kinds,
// and the retirement residue every retire/restore cycle leaves behind. A
// refresh that materialises the whole retirement set costs one pass over that
// residue per definition event, so cost grows with their product.
func BenchmarkFoldDeclaredKindChurn(b *testing.B) {
	for _, kinds := range []int{100, 400, 1600} {
		records := declaredKindChurnHistory(b, kinds)
		b.Run(fmt.Sprintf("kinds-%d", kinds), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

func BenchmarkFolderAppendToProjectionRequestHeavy(b *testing.B) {
	records := requestHeavyHistory(b, 20000)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		folder := NewFolder(records)
		record := event(b, fmt.Sprintf("append:%d", index), operator, SchemaState, State{
			Kind: KindRequest, Text: "request", Body: map[string]string{"to": agent, "conditions": "done"},
		}, "e0")
		b.StartTimer()
		folder.Append(record)
		projection := folder.Projection()
		if len(projection.Commitments) != 20001 {
			b.Fatalf("commitments = %d", len(projection.Commitments))
		}
	}
}

// mergeReceiptHeavyHistory models the workroom's real proportions: a log of
// the given depth carrying the given number of complete merge-receipt lanes —
// request, promise, predecessor, candidate, approval, ratification, receipt,
// successor, and the cross-author supersession the receipt authorizes — spread
// evenly through ordinary filler records. Receipt admission runs a
// world-staleness pass over the folded prefix, so the cost being measured
// depends on both the depth and the receipt count, which is why the fixture
// pins both to the measured repository rather than to a convenient shape.
// When chained, each lane's candidate rests on the previous lane's successor,
// the way real work stands on the work before it, so a receipt's ancestor
// closure spans every earlier lane instead of one hop — the shape that costs
// the closure-restricted staleness pass the most.
func mergeReceiptHeavyHistory(tb testing.TB, depth, receipts int, chained bool) []Record {
	tb.Helper()
	records := []Record{
		event(tb, "r0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(tb, "r1", operator, SchemaState, State{Kind: KindRoster, Text: "implementer joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Implementer", "role": "participant"}}, "r0"),
		event(tb, "r2", operator, SchemaRatify, Ratify{Target: "r1"}, "r1"),
		event(tb, "r3", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "r0"),
		event(tb, "r4", operator, SchemaRatify, Ratify{Target: "r3"}, "r3"),
	}
	lane := func(index int) []Record {
		id := func(name string) string { return fmt.Sprintf("%s-%d", name, index) }
		path := fmt.Sprintf("lane/%d", index)
		head := fmt.Sprintf("head-%d", index)
		merged := fmt.Sprintf("merged-%d", index)
		ground := "r0"
		if chained && index > 0 {
			ground = fmt.Sprintf("successor-%d", index-1)
		}
		return []Record{
			event(tb, id("request"), operator, SchemaState, State{Kind: KindRequest, Text: "review it", Body: map[string]string{"to": other, "conditions": "exact head"}}, "r0"),
			event(tb, id("promise"), other, SchemaState, State{Kind: KindPromise, Text: "will review"}, id("request")),
			event(tb, id("predecessor"), operator, SchemaState, State{Kind: KindArtifact, Text: "older implementation", Body: map[string]string{"path": path, "commit": "base"}}, "r0"),
			event(tb, id("candidate"), agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": path, "commit": head}}, ground),
			event(tb, id("approval"), other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": head, "artifact": id("candidate")}}, id("promise"), id("candidate")),
			event(tb, id("ratify"), operator, SchemaRatify, Ratify{Target: id("approval")}, id("approval")),
			event(tb, id("merge"), agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
				"merge_approval": id("approval"), "merge_candidate": head, "merge_target_pre_head": "base", "merge_head": merged,
				"merge_retirements": fmt.Sprintf(`{"%s":"%s"}`, id("predecessor"), path), "merge_successors": fmt.Sprintf(`["%s"]`, path),
			}}, id("approval")),
			event(tb, id("successor"), agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": path, "commit": merged}}, id("merge")),
			event(tb, id("retire"), agent, SchemaSupersede, Supersede{Target: id("predecessor"), Text: "merge succession"}, id("predecessor"), id("merge"), id("successor")),
		}
	}
	filler := depth - len(records) - receipts*len(lane(0))
	if filler < 0 {
		tb.Fatalf("depth %d cannot hold %d receipt lanes", depth, receipts)
	}
	perLane := filler / receipts
	next := 0
	for index := 0; index < receipts; index++ {
		for count := 0; count < perLane; count++ {
			records = append(records, event(tb, fmt.Sprintf("note-%d", next), agent, SchemaState, State{Kind: KindAssert, Text: "routine progress"}, "r0"))
			next++
		}
		records = append(records, lane(index)...)
	}
	for len(records) < depth {
		records = append(records, event(tb, fmt.Sprintf("note-%d", next), agent, SchemaState, State{Kind: KindAssert, Text: "routine progress"}, "r0"))
		next++
	}
	return records
}

// BenchmarkFoldMergeReceiptHeavy measures a whole fold at the measured
// repository's proportions — 8,900 records, 132 merge receipts — because
// receipt admission is the one judgement that reads a staleness pass over the
// prefix, and its cost only shows at depth.
func BenchmarkFoldMergeReceiptHeavy(b *testing.B) {
	for _, shape := range []struct {
		name    string
		chained bool
	}{{"independent", false}, {"chained", true}} {
		records := mergeReceiptHeavyHistory(b, 8900, 132, shape.chained)
		projection := Fold(records)
		for _, lane := range []int{0, 65, 131} {
			decision, _ := projection.Decision(fmt.Sprintf("retire-%d", lane))
			if decision.Verdict != Effective {
				b.Fatalf("%s lane %d cross-author supersession = %+v; the benchmark would measure refused receipts", shape.name, lane, decision)
			}
		}
		b.Run(shape.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

func TestFolderDoesNotRetainTransportPayloads(t *testing.T) {
	payload, err := Encode(State{Kind: KindAssert, Text: "decoded"})
	if err != nil {
		t.Fatal(err)
	}
	folder := NewFolder([]Record{{
		ID: "event", Actor: "actor", Schema: SchemaState, Payload: payload,
		Attachments: map[string][]byte{"evidence": []byte("large transport bytes")},
	}})
	if len(folder.state.records) != 1 {
		t.Fatalf("records = %d", len(folder.state.records))
	}
	retained := folder.state.records[0].record
	if retained.Payload != nil || retained.Attachments != nil {
		t.Fatalf("folder retained transport bytes: payload=%d attachments=%d", len(retained.Payload), len(retained.Attachments))
	}
}

func requestHeavyHistory(t testing.TB, requests int) []Record {
	t.Helper()
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "agent", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "agent-ratified", operator, SchemaRatify, Ratify{Target: "agent"}, "agent"),
	}
	for index := range requests {
		id := fmt.Sprintf("request:%d", index)
		records = append(records, event(t, id, operator, SchemaState, State{
			Kind: KindRequest, Text: "request", Body: map[string]string{"to": agent, "conditions": "done"},
		}, "e0"))
	}
	return records
}

func declaredKindChurnHistory(t testing.TB, kinds int) []Record {
	t.Helper()
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
	}
	for index := range kinds {
		name := fmt.Sprintf("kind-%d", index)
		definition := KindDefinition{
			Name: Kind(name), Fields: present("topic"), Basis: []BasisConstraint{},
			Satisfier: "role:ratifier", Render: RenderNote, Staleness: StalenessPropagates,
			Lifecycle: LifecycleNone, Guidance: "Churned definition.",
		}
		declare := fmt.Sprintf("declare:%d", index)
		ratify := fmt.Sprintf("declare-ratification:%d", index)
		retire := fmt.Sprintf("retire:%d", index)
		restore := fmt.Sprintf("restore:%d", index)
		note := fmt.Sprintf("note:%d", index)
		records = append(records,
			event(t, declare, operator, SchemaState, kindDefinitionState(t, definition), "e0"),
			event(t, ratify, operator, SchemaRatify, Ratify{Target: declare}, declare),
			event(t, retire, operator, SchemaSupersede, Supersede{Target: declare, Text: "retire"}, declare),
			event(t, restore, operator, SchemaSupersede, Supersede{Target: retire, Text: "restore"}, retire),
			event(t, note, operator, SchemaState, State{Kind: Kind(name), Text: "note", Body: map[string]string{"topic": name}}, "e0"),
		)
	}
	return records
}

func rosterHeavyHistory(t testing.TB, actors int) []Record {
	t.Helper()
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
	}
	for index := range actors {
		actor := fmt.Sprintf("actor:%d", index)
		membership := fmt.Sprintf("membership:%d", index)
		membershipRatification := fmt.Sprintf("membership-ratification:%d", index)
		grant := fmt.Sprintf("grant:%d", index)
		grantRatification := fmt.Sprintf("grant-ratification:%d", index)
		proposal := fmt.Sprintf("proposal:%d", index)
		ratification := fmt.Sprintf("proposal-ratification:%d", index)
		records = append(records,
			event(t, membership, operator, SchemaState, State{Kind: KindRoster, Text: "join", Body: map[string]string{"actor": actor, "kind": "agent", "name": actor, "role": "participant"}}, "e0"),
			event(t, membershipRatification, operator, SchemaRatify, Ratify{Target: membership}, membership),
			event(t, grant, operator, SchemaState, State{Kind: KindRoster, Text: "grant", Body: map[string]string{"actor": actor, "kind": "agent", "name": actor, "role": "ratifier"}}, membership),
			event(t, grantRatification, operator, SchemaRatify, Ratify{Target: grant}, grant),
			event(t, proposal, operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
			event(t, ratification, actor, SchemaRatify, Ratify{Target: proposal}, proposal),
		)
	}
	return records
}

func golden(t *testing.T) Projection {
	t.Helper()
	return Fold(goldenRecords(t))
}

func goldenRecords(t testing.TB) []Record {
	t.Helper()
	return []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "Operator begins the workroom", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "Agent joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "Build the projector", Body: map[string]string{"to": agent, "conditions": "golden projection is byte-stable"}}, "e0"),
		event(t, "e4", agent, SchemaState, State{Kind: KindPromise, Text: "I will build it"}, "e3"),
		event(t, "e5", agent, SchemaState, State{Kind: KindReport, Text: "Projection passes", Body: map[string]string{"result": "go test ./..."}}, "e4"),
		event(t, "e6", agent, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e7", operator, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e8", agent, SchemaState, State{Kind: KindArtifact, Text: "Projector implementation", Body: map[string]string{"path": "internal/workroom", "commit": "abc123"}}, "e3", "e7"),
		event(t, "e9", operator, SchemaSupersede, Supersede{Target: "e3", Text: "Requirements changed"}, "e3"),
	}
}

// preconditions is the second pinned transcript. It exercises the two places
// where declared kinds changed what a legacy-shaped record projects: a
// retirement now blocks ratification until it is itself contested, and a body
// that omits a required field now decodes and is judged by the fold rather than
// refused by the encoder.
func preconditions(t *testing.T) Projection {
	t.Helper()
	return Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "Operator begins the workroom", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "Adopt the projection contract"}, "e0"),
		event(t, "e2", operator, SchemaSupersede, Supersede{Target: "e1", Text: "Withdrawn before adoption"}, "e1"),
		event(t, "e3", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e4", operator, SchemaState, State{Kind: KindRequest, Text: "Ship the projector", Body: map[string]string{"to": operator}}, "e0"),
		event(t, "e5", operator, SchemaState, State{Kind: KindArtifact, Text: "Half a citation", Body: map[string]string{"path": "internal/workroom"}}, "e0"),
		event(t, "e6", operator, SchemaSupersede, Supersede{Target: "e2", Text: "Restore the proposal"}, "e2"),
		event(t, "e7", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
	})
}

func kindDefinitionState(t testing.TB, definition KindDefinition) State {
	t.Helper()
	fields, err := json.Marshal(definition.Fields)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := json.Marshal(definition.Basis)
	if err != nil {
		t.Fatal(err)
	}
	return State{Kind: KindKindDef, Text: "Define " + string(definition.Name), Body: map[string]string{
		"name": string(definition.Name), "fields": string(fields), "basis": string(basis),
		"satisfier": definition.Satisfier, "render": string(definition.Render),
		"staleness": string(definition.Staleness), "lifecycle": string(definition.Lifecycle),
		"guidance": definition.Guidance,
	}}
}

func TestGoldenCommitmentAndArtifactProjection(t *testing.T) {
	projection := golden(t)
	if got := projection.Commitments[0].Status; got != "cancelled" {
		t.Fatalf("commitment status = %q", got)
	}
	if !projection.Artifacts[0].Stale {
		t.Fatal("artifact did not become stale when its governing request died")
	}
	decision, _ := projection.Decision("e6")
	if decision.Verdict != Ineffective {
		t.Fatalf("agent self-ratification = %s", decision.Verdict)
	}
	decision, _ = projection.Decision("e7")
	if decision.Verdict != Effective {
		t.Fatalf("requester satisfaction = %s: %s", decision.Verdict, decision.Reason)
	}
}

func TestResidentFolderMatchesWholeFoldAtEveryPrefix(t *testing.T) {
	records := goldenRecords(t)
	folder := NewFolder(nil)
	for index, record := range records {
		folder.Append(record)
		got, err := json.Marshal(folder.Projection())
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(Fold(records[:index+1]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("prefix %d incremental projection differs\ngot  %s\nwant %s", index+1, got, want)
		}
	}
}

func TestResidentFolderProjectionMutationDoesNotCorruptState(t *testing.T) {
	folder := NewFolder(goldenRecords(t))
	projection := folder.Projection()
	projection.Statements[0].Body["actor"] = "attacker"
	projection.Statements[0].Body["role"] = "none"

	fresh := folder.Projection()
	if fresh.Statements[0].Body["actor"] != operator || fresh.Statements[0].Body["role"] != "operator" {
		t.Fatalf("projection mutation corrupted resident state: %+v", fresh.Statements[0].Body)
	}
}

func TestReportAwaitsRequester(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "do it", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
		event(t, "e4", agent, SchemaState, State{Kind: KindPromise, Text: "yes"}, "e3"),
		event(t, "e5", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "e4"),
	}
	projection := Fold(records)
	if got := projection.Commitments[0].Status; got != "reported" {
		t.Fatalf("status = %q", got)
	}
	if got := projection.Commitments[0].WaitingOn; got != operator {
		t.Fatalf("waiting on = %q", got)
	}
}

func TestArtifactOnPromiseDischargesTheReportObligation(t *testing.T) {
	records := worldRecords(t,
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "implement it", Body: map[string]string{"to": agent, "conditions": "exact head is published"}}, "w0"),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
		event(t, "artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation at its exact head", Body: map[string]string{"path": "internal/workroom", "commit": "head1"}}, "promise"),
	)
	commitment := Fold(records).Commitments[0]
	if commitment.Status != "awaiting-merge" || commitment.Report != "artifact" || commitment.WaitingOn != agent {
		t.Fatalf("artifact-backed commitment = %+v, want awaiting-merge waiting on the performer", commitment)
	}
}

func TestOnlyThePromisorsSinglePromiseArtifactActsAsAReport(t *testing.T) {
	records := worldRecords(t,
		event(t, "other-membership", operator, SchemaState, State{Kind: KindRoster, Text: "other joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "participant"}}, "w0"),
		event(t, "other-ratified", operator, SchemaRatify, Ratify{Target: "other-membership"}, "other-membership"),
		event(t, "request-1", operator, SchemaState, State{Kind: KindRequest, Text: "one", Body: map[string]string{"to": agent, "conditions": "done"}}, "w0"),
		event(t, "promise-1", agent, SchemaState, State{Kind: KindPromise, Text: "one"}, "request-1"),
		event(t, "request-2", operator, SchemaState, State{Kind: KindRequest, Text: "two", Body: map[string]string{"to": agent, "conditions": "done"}}, "w0"),
		event(t, "promise-2", agent, SchemaState, State{Kind: KindPromise, Text: "two"}, "request-2"),
		event(t, "foreign", other, SchemaState, State{Kind: KindArtifact, Text: "another actor's artifact", Body: map[string]string{"path": "foreign", "commit": "head1"}}, "promise-1"),
		event(t, "ambiguous", agent, SchemaState, State{Kind: KindArtifact, Text: "one artifact for two promises", Body: map[string]string{"path": "ambiguous", "commit": "head2"}}, "promise-1", "promise-2"),
	)
	projection := Fold(records)
	for _, commitment := range projection.Commitments {
		if commitment.Status != "promised" || commitment.Report != "" || commitment.WaitingOn != agent {
			t.Fatalf("non-conforming artifact discharged %+v", commitment)
		}
	}
}

func TestMergeOfApprovedArtifactClosesItsImplementationCommitment(t *testing.T) {
	projection := Fold(implementationMergeRecords(t, true))
	var implementation, review Commitment
	for _, commitment := range projection.Commitments {
		switch commitment.Request {
		case "implementation-request":
			implementation = commitment
		case "review-request":
			review = commitment
		}
	}
	if implementation.Status != "satisfied" || implementation.Report != "implementation-artifact" || implementation.WaitingOn != "" {
		t.Fatalf("merged implementation commitment = %+v", implementation)
	}
	if review.Status != "satisfied" || review.Report != "approval" {
		t.Fatalf("explicit review ratification changed = %+v", review)
	}
	reviewProjection := reviewFor(t, projection, "approval")
	if !reviewProjection.Ratified || reviewProjection.Independence != IndependenceIndependent {
		t.Fatalf("review gate weakened = %+v", reviewProjection)
	}
}

func TestMergeClosesACommitmentReportedByAnyReviewedPlannedArtifact(t *testing.T) {
	records := worldRecords(t,
		event(t, "reviewer-membership", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "w0"),
		event(t, "reviewer-ratified", operator, SchemaRatify, Ratify{Target: "reviewer-membership"}, "reviewer-membership"),
		event(t, "implementation-request", operator, SchemaState, State{Kind: KindRequest, Text: "implement it", Body: map[string]string{"to": agent, "conditions": "approved head is merged"}}, "w0"),
		event(t, "implementation-promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will implement it"}, "implementation-request"),
		event(t, "primary-artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "internal/workroom", "commit": "head1"}}, "implementation-promise"),
		event(t, "latest-artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "documentation", Body: map[string]string{"path": "docs", "commit": "head1"}}, "implementation-promise"),
		event(t, "review-request", agent, SchemaState, State{Kind: KindRequest, Text: "review the exact head", Body: map[string]string{"to": other, "conditions": "independent approval"}}, "primary-artifact", "latest-artifact"),
		event(t, "review-promise", other, SchemaState, State{Kind: KindPromise, Text: "I will review it"}, "review-request"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "primary-artifact"}}, "review-promise", "primary-artifact", "latest-artifact"),
		event(t, "approval-ratified", agent, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"primary-artifact":"internal/workroom","latest-artifact":"docs"}`, "merge_successors": `["docs","internal/workroom"]`,
		}}, "approval"),
	)
	for _, commitment := range Fold(records).Commitments {
		if commitment.Request != "implementation-request" {
			continue
		}
		if commitment.Status != "satisfied" || commitment.Report != "latest-artifact" || commitment.WaitingOn != "" {
			t.Fatalf("multi-path implementation commitment = %+v", commitment)
		}
		return
	}
	t.Fatal("implementation commitment was not projected")
}

func TestMergeDoesNotCloseAnotherAuthorsCommitment(t *testing.T) {
	records := worldRecords(t,
		event(t, "reviewer-membership", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "w0"),
		event(t, "reviewer-ratified", operator, SchemaRatify, Ratify{Target: "reviewer-membership"}, "reviewer-membership"),
		event(t, "bystander-membership", operator, SchemaState, State{Kind: KindRoster, Text: "second implementer joins", Body: map[string]string{"actor": bystander, "kind": "agent", "name": "Second implementer", "role": "participant"}}, "w0"),
		event(t, "bystander-ratified", operator, SchemaRatify, Ratify{Target: "bystander-membership"}, "bystander-membership"),
		event(t, "primary-request", operator, SchemaState, State{Kind: KindRequest, Text: "implement package", Body: map[string]string{"to": agent, "conditions": "approved head is merged"}}, "w0"),
		event(t, "primary-promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will implement the package"}, "primary-request"),
		event(t, "primary-artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "package implementation", Body: map[string]string{"path": "internal/workroom", "commit": "head1"}}, "primary-promise"),
		event(t, "foreign-request", operator, SchemaState, State{Kind: KindRequest, Text: "implement one file", Body: map[string]string{"to": bystander, "conditions": "approved head is merged"}}, "w0"),
		event(t, "foreign-promise", bystander, SchemaState, State{Kind: KindPromise, Text: "I will implement the file"}, "foreign-request"),
		event(t, "foreign-artifact", bystander, SchemaState, State{Kind: KindArtifact, Text: "another actor's file", Body: map[string]string{"path": "internal/workroom/fold.go", "commit": "head1"}}, "foreign-promise"),
		event(t, "review-request", agent, SchemaState, State{Kind: KindRequest, Text: "review the exact head", Body: map[string]string{"to": other, "conditions": "independent approval"}}, "primary-artifact", "foreign-artifact"),
		event(t, "review-promise", other, SchemaState, State{Kind: KindPromise, Text: "I will review it"}, "review-request"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "primary-artifact"}}, "review-promise", "primary-artifact", "foreign-artifact"),
		event(t, "approval-ratified", agent, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"primary-artifact":"internal/workroom","foreign-artifact":"internal/workroom"}`, "merge_successors": `["internal/workroom"]`,
		}}, "approval"),
	)
	projection := Fold(records)
	seen := make(map[string]bool)
	for _, commitment := range projection.Commitments {
		switch commitment.Request {
		case "primary-request":
			seen[commitment.Request] = true
			if commitment.Status != "satisfied" || commitment.Report != "primary-artifact" || commitment.WaitingOn != "" {
				t.Fatalf("merger's own commitment = %+v", commitment)
			}
		case "foreign-request":
			seen[commitment.Request] = true
			if commitment.Status != "awaiting-merge" || commitment.Report != "foreign-artifact" || commitment.WaitingOn != commitment.Performer {
				t.Fatalf("merge closed another author's commitment = %+v", commitment)
			}
		}
	}
	if !seen["primary-request"] || !seen["foreign-request"] {
		t.Fatalf("implementation commitments missing: %+v", seen)
	}
}

func TestMergeDoesNotCloseCommitmentWithoutExplicitApprovalRatification(t *testing.T) {
	projection := Fold(implementationMergeRecords(t, false))
	for _, commitment := range projection.Commitments {
		if commitment.Request != "implementation-request" {
			continue
		}
		if commitment.Status != "awaiting-merge" || commitment.Report != "implementation-artifact" || commitment.WaitingOn != commitment.Performer {
			t.Fatalf("unratified approval closed implementation commitment = %+v", commitment)
		}
		return
	}
	t.Fatal("implementation commitment was not projected")
}

func TestMergeRemainsTerminalForItsImplementationCommitment(t *testing.T) {
	records := append(implementationMergeRecords(t, true),
		event(t, "duplicate-report", agent, SchemaState, State{Kind: KindReport, Text: "duplicate completion claim"}, "implementation-promise"),
	)
	for _, commitment := range Fold(records).Commitments {
		if commitment.Request != "implementation-request" {
			continue
		}
		if commitment.Status != "satisfied" || commitment.Report != "implementation-artifact" || commitment.WaitingOn != "" {
			t.Fatalf("duplicate report reopened merged implementation = %+v", commitment)
		}
		return
	}
	t.Fatal("implementation commitment was not projected")
}

func implementationMergeRecords(t *testing.T, ratified bool) []Record {
	records := worldRecords(t,
		event(t, "reviewer-membership", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "w0"),
		event(t, "reviewer-ratified", operator, SchemaRatify, Ratify{Target: "reviewer-membership"}, "reviewer-membership"),
		event(t, "implementation-request", operator, SchemaState, State{Kind: KindRequest, Text: "implement it", Body: map[string]string{"to": agent, "conditions": "approved head is merged"}}, "w0"),
		event(t, "implementation-promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will implement it"}, "implementation-request"),
		event(t, "implementation-artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "internal/workroom", "commit": "head1"}}, "implementation-promise"),
		event(t, "review-request", agent, SchemaState, State{Kind: KindRequest, Text: "review the exact head", Body: map[string]string{"to": other, "conditions": "independent approval"}}, "implementation-artifact"),
		event(t, "review-promise", other, SchemaState, State{Kind: KindPromise, Text: "I will review it"}, "review-request"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "implementation-artifact"}}, "review-promise", "implementation-artifact"),
	)
	if ratified {
		records = append(records, event(t, "approval-ratified", agent, SchemaRatify, Ratify{Target: "approval"}, "approval"))
	}
	records = append(records, event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
		"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
		"merge_retirements": `{"implementation-artifact":"internal/workroom"}`, "merge_successors": `["internal/workroom"]`,
	}}, "approval"))
	if ratified {
		records = append(records,
			event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "internal/workroom", "commit": "merged"}}, "merge"),
			event(t, "candidate-retired", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "the merge published its successor"}, "implementation-artifact", "merge", "successor"),
		)
	}
	return records
}

func TestArtifactWithoutMergeStillClosesThroughAnExplicitReport(t *testing.T) {
	records := worldRecords(t,
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "investigate", Body: map[string]string{"to": agent, "conditions": "result recorded"}}, "w0"),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
		event(t, "artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "supporting material", Body: map[string]string{"path": "notes/result.md", "commit": "head1"}}, "promise"),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "investigation complete"}, "promise"),
		event(t, "report-ratified", operator, SchemaRatify, Ratify{Target: "report"}, "report"),
	)
	commitment := Fold(records).Commitments[0]
	if commitment.Status != "satisfied" || commitment.Report != "report" || commitment.WaitingOn != "" {
		t.Fatalf("non-merge commitment = %+v", commitment)
	}
}

func TestExplicitReportKeepsAuthorityOverALaterUnmergedArtifact(t *testing.T) {
	records := worldRecords(t,
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "investigate", Body: map[string]string{"to": agent, "conditions": "result recorded"}}, "w0"),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "investigation complete"}, "promise"),
		event(t, "report-ratified", operator, SchemaRatify, Ratify{Target: "report"}, "report"),
		event(t, "artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "supporting material", Body: map[string]string{"path": "notes/result.md", "commit": "head1"}}, "promise"),
	)
	commitment := Fold(records).Commitments[0]
	if commitment.Status != "satisfied" || commitment.Report != "report" || commitment.WaitingOn != "" {
		t.Fatalf("later artifact displaced explicit report = %+v", commitment)
	}
}

func TestStaleCommitmentPreservesReportedTerminalState(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "basis", operator, SchemaState, State{Kind: KindAssert, Text: "basis"}, "e0"),
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "do it", Body: map[string]string{"to": agent, "conditions": "done"}}, "basis"),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "yes"}, "request"),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise"),
		event(t, "satisfied", operator, SchemaRatify, Ratify{Target: "report"}, "report"),
		event(t, "basis-retired", operator, SchemaSupersede, Supersede{Target: "basis", Text: "basis changed"}, "basis"),
	}
	projection := Fold(records)
	commitment := projection.Commitments[0]
	if commitment.Status != "satisfied" || commitment.Report != "report" || !commitment.Stale || commitment.WaitingOn != "" {
		t.Fatalf("stale satisfied commitment = %+v", commitment)
	}
	if status := RenderStatus(projection); !bytes.Contains(status, []byte("| satisfied | stale |")) {
		t.Fatalf("status omitted stale qualifier:\n%s", status)
	}
	reportedRecords := append([]Record(nil), records[:7]...)
	reportedRecords = append(reportedRecords, records[8])
	reported := Fold(reportedRecords).Commitments[0]
	if reported.Status != "reported" || reported.Report != "report" || !reported.Stale || reported.WaitingOn != operator {
		t.Fatalf("stale reported commitment = %+v", reported)
	}

	unreportedRecords := append([]Record(nil), records[:6]...)
	unreportedRecords = append(unreportedRecords, records[8])
	unreported := Fold(unreportedRecords)
	commitment = unreported.Commitments[0]
	if commitment.Status != "stale" || commitment.Report != "" || !commitment.Stale || commitment.WaitingOn != agent {
		t.Fatalf("unreported stale commitment changed semantics = %+v", commitment)
	}
}

func TestUnclaimedRequestIsOpenWithoutWaitingOnItsAddressee(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", operator, SchemaState, State{Kind: KindRequest, Text: "Please do this", Body: map[string]string{"to": agent, "conditions": "done"}}, "w0"),
	)
	open := Fold(records).Commitments[0]
	if open.Status != "open" || open.AddressedTo != agent || open.Performer != "" || open.Promise != "" || open.WaitingOn != "" {
		t.Fatalf("unclaimed request projects as %+v", open)
	}
	encoded, err := json.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte(`"performer"`), []byte(`"promise"`), []byte(`"waiting_on"`)} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("unclaimed request JSON asserts %s: %s", forbidden, encoded)
		}
	}
	for _, want := range [][]byte{[]byte(`"addressed_to":"actor:agent"`), []byte(`"status":"open"`)} {
		if !bytes.Contains(encoded, want) {
			t.Fatalf("unclaimed request JSON omits %s: %s", want, encoded)
		}
	}
	page := RenderStatus(Fold(records))
	// The request column carries both the readable sequence and the exact event
	// name a command accepts. This test remains about the unclaimed assignment.
	if !bytes.Contains(page, []byte("| open |  | actor:operator | addressed to actor:agent — unclaimed | #4 w3 |  |")) {
		t.Fatalf("status page does not render the request as addressed and unclaimed:\n%s", page)
	}

	records = append(records, event(t, "w4", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "w3"))
	promised := Fold(records).Commitments[0]
	if promised.Status != "promised" || promised.Performer != agent || promised.WaitingOn != agent {
		t.Fatalf("live promise does not wait on its performer: %+v", promised)
	}

	records = append(records, event(t, "w5", agent, SchemaState, State{Kind: KindReport, Text: "Done"}, "w4"))
	reported := Fold(records).Commitments[0]
	if reported.Status != "reported" || reported.WaitingOn != operator {
		t.Fatalf("live report does not wait on its requester: %+v", reported)
	}
}

// Staleness is a qualifier beside the status, not a replacement for it, and it
// does not release the party who still owes something. A promise with no report
// still reads stale, because there is no outcome to preserve, but the promisor
// is still the one being waited on.
func TestStaleCommitmentKeepsItsWaitingParty(t *testing.T) {
	t.Run("promise", func(t *testing.T) {
		records := worldRecords(t,
			event(t, "w3", operator, SchemaState, State{Kind: KindAssert, Text: "basis"}, "w0"),
			event(t, "w4", operator, SchemaState, State{Kind: KindRequest, Text: "Please do this", Body: map[string]string{"to": agent, "conditions": "done"}}, "w3"),
			event(t, "w5", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "w4"),
			event(t, "w6", operator, SchemaSupersede, Supersede{Target: "w3", Text: "basis changed"}, "w3"),
		)
		commitment := Fold(records).Commitments[0]
		if commitment.Status != "stale" || !commitment.Stale || commitment.WaitingOn != agent {
			t.Fatalf("stale promise projects as %+v", commitment)
		}
	})

	// A reported commitment keeps its outcome when the world moves under it.
	// Staleness is a qualifier beside the status, not a replacement for it:
	// flattening this to "stale" hid a kept promise and dropped the requester
	// who still owes it a ratification. The promise case above has no outcome
	// to preserve, so it still projects as stale with nobody waiting.
	t.Run("report keeps its outcome and its waiting party", func(t *testing.T) {
		records := worldRecords(t,
			event(t, "w3", operator, SchemaState, State{Kind: KindAssert, Text: "basis"}, "w0"),
			event(t, "w4", operator, SchemaState, State{Kind: KindRequest, Text: "Please do this", Body: map[string]string{"to": agent, "conditions": "done"}}, "w0"),
			event(t, "w5", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "w4"),
			event(t, "w6", agent, SchemaState, State{Kind: KindReport, Text: "Done"}, "w5", "w3"),
			event(t, "w7", operator, SchemaSupersede, Supersede{Target: "w3", Text: "basis changed"}, "w3"),
		)
		commitment := Fold(records).Commitments[0]
		if commitment.Status != "reported" || !commitment.Stale || commitment.WaitingOn != operator {
			t.Fatalf("stale report projects as %+v", commitment)
		}
	})
}

func TestDanglingWrongActorAndAmbiguousActsAreTotal(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", agent, SchemaState, State{Kind: KindPromise, Text: "unasked promise"}, "e0"),
		event(t, "e4", operator, SchemaState, State{Kind: KindRequest, Text: "one", Body: map[string]string{"to": agent, "conditions": "one"}}, "e0"),
		event(t, "e5", operator, SchemaState, State{Kind: KindRequest, Text: "two", Body: map[string]string{"to": agent, "conditions": "two"}}, "e0"),
		event(t, "e6", agent, SchemaState, State{Kind: KindPromise, Text: "ambiguous"}, "e4", "e5"),
		event(t, "e7", other, SchemaState, State{Kind: KindPromise, Text: "wrong actor"}, "e4"),
	})
	if len(projection.Decisions) != 8 {
		t.Fatalf("got %d decisions for 8 events", len(projection.Decisions))
	}
	want := map[string]Verdict{"e3": Ineffective, "e6": Disputed, "e7": Ineffective}
	for event, verdict := range want {
		decision, _ := projection.Decision(event)
		if decision.Verdict != verdict {
			t.Errorf("%s = %s, want %s", event, decision.Verdict, verdict)
		}
	}
}

func TestRosterSupersessionRevokesAndResurrectionRestoresAuthority(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "Carol joins", Body: map[string]string{"actor": other, "name": "Carol", "role": "ratifier"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindPropose, Text: "before demotion"}, "e0"),
		event(t, "e4", other, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaSupersede, Supersede{Target: "e1", Text: "demote Carol"}, "e1"),
		event(t, "e6", operator, SchemaState, State{Kind: KindPropose, Text: "while demoted"}, "e0"),
		event(t, "e7", other, SchemaRatify, Ratify{Target: "e6"}, "e6"),
		event(t, "e8", operator, SchemaSupersede, Supersede{Target: "e5", Text: "restore Carol"}, "e5"),
		event(t, "e9", operator, SchemaState, State{Kind: KindAssert, Text: "after restoration"}, "e0"),
		event(t, "e10", other, SchemaRatify, Ratify{Target: "e9"}, "e9"),
	})
	for event, want := range map[string]Verdict{"e4": Effective, "e7": Ineffective, "e10": Effective} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", event, decision.Verdict, decision.Reason, want)
		}
	}
	actor := projection.Actors[other]
	if got := actor.Kind; got != "unspecified" {
		t.Fatalf("legacy actor kind = %q, want unspecified", got)
	}
	if got := actor.Roles; len(got) != 2 || got[0] != "participant" || got[1] != "ratifier" {
		t.Fatalf("restored roles = %#v", got)
	}
}

func TestMembershipRevocationRevokesDependentAuthority(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "grant ratifier", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "ratifier"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaState, State{Kind: KindPropose, Text: "before departure"}, "e0"),
		event(t, "e6", agent, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e7", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove agent"}, "e1"),
		event(t, "e8", operator, SchemaState, State{Kind: KindPropose, Text: "after departure"}, "e0"),
		event(t, "e9", agent, SchemaRatify, Ratify{Target: "e8"}, "e8"),
	})
	for event, want := range map[string]Verdict{"e6": Effective, "e9": Ineffective} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", event, decision.Verdict, decision.Reason, want)
		}
	}
	// The principal stays visible because its signatures are permanent, but it
	// keeps no roles and is marked retired: e9 above proves the authority is
	// gone, and this proves a reader can tell the retired from the live.
	state, exists := projection.Actors[agent]
	if !exists {
		t.Fatal("retired member vanished from the roster it once acted in")
	}
	if !state.Retired || len(state.Roles) != 0 || state.Name != "Agent" || state.MembershipEvent != "e1" {
		t.Fatalf("retired member projection = %+v", state)
	}
}

func TestFoundingOperatorSeedCannotBeRetired(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaSupersede, Supersede{Target: "e0", Text: "retire the founder"}, "e0"),
	})
	decision, _ := projection.Decision("e1")
	if decision.Verdict != Ineffective || decision.Reason != "founding operator seed cannot be retired" {
		t.Fatalf("founder retirement = %+v", decision)
	}
	if actor := projection.Actors[operator]; actor.Retired || !contains(actor.Roles, "operator") {
		t.Fatalf("founder after attempted retirement = %+v", actor)
	}
}

func TestRatifierCannotRetireOperatorMembershipBasis(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "agent becomes operator", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "operator"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaState, State{Kind: KindRoster, Text: "other joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "participant"}}, "e0"),
		event(t, "e6", operator, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e7", operator, SchemaState, State{Kind: KindRoster, Text: "other becomes ratifier", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "ratifier"}}, "e5"),
		event(t, "e8", operator, SchemaRatify, Ratify{Target: "e7"}, "e7"),
		event(t, "e9", other, SchemaSupersede, Supersede{Target: "e1", Text: "remove the operator"}, "e1"),
	})
	decision, _ := projection.Decision("e9")
	if decision.Verdict != Ineffective || decision.Reason != "operator standing is required to change an operator's membership" {
		t.Fatalf("lower-standing retirement = %+v", decision)
	}
	if actor := projection.Actors[agent]; actor.Retired || !contains(actor.Roles, "operator") {
		t.Fatalf("operator after lower-standing retirement = %+v", actor)
	}
}

func TestAuthorityGrantCannotBeSelfRatified(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "ratifier joins", Body: map[string]string{"actor": other, "name": "Other", "role": "ratifier"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", other, SchemaState, State{Kind: KindRoster, Text: "self-conferral", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "operator"}}, "e1"),
		event(t, "e4", other, SchemaRatify, Ratify{Target: "e3"}, "e3"),
	})
	decision, _ := projection.Decision("e4")
	if decision.Verdict != Ineffective || decision.Reason != "authority grant cannot be authored or ratified by its beneficiary" {
		t.Fatalf("self-ratified authority = %+v", decision)
	}
	if roles := projection.Actors[other].Roles; contains(roles, "operator") {
		t.Fatalf("self-ratification conferred operator: %#v", roles)
	}
}

func TestAuthorityGrantCannotBeSelfAuthored(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "other joins", Body: map[string]string{"actor": other, "name": "Other", "role": "ratifier"}}, "e0"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", agent, SchemaState, State{Kind: KindRoster, Text: "write a spare grant", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "operator"}}, "e1"),
		event(t, "e6", other, SchemaRatify, Ratify{Target: "e5"}, "e5"),
	})
	decision, _ := projection.Decision("e6")
	if decision.Verdict != Ineffective || decision.Reason != "authority grant cannot be authored or ratified by its beneficiary" {
		t.Fatalf("self-authored authority = %+v", decision)
	}
	if roles := projection.Actors[agent].Roles; contains(roles, "operator") {
		t.Fatalf("self-authored grant conferred operator: %#v", roles)
	}
}

func TestRatifierCannotMintOperatorForAnotherActor(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "ratifier joins", Body: map[string]string{"actor": other, "name": "Other", "role": "ratifier"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", other, SchemaState, State{Kind: KindRoster, Text: "mint a second operator", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "operator"}}, "e3"),
		event(t, "e6", other, SchemaRatify, Ratify{Target: "e5"}, "e5"),
	})
	decision, _ := projection.Decision("e6")
	if decision.Verdict != Ineffective || decision.Reason != "operator standing is required to ratify an operator grant" {
		t.Fatalf("ratifier-minted operator = %+v", decision)
	}
	if roles := projection.Actors[agent].Roles; contains(roles, "operator") {
		t.Fatalf("ratifier minted operator for another actor: %#v", roles)
	}
}

func TestDormantOperatorGrantCannotBeRevivedWithoutPresentAuthority(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "other becomes operator", Body: map[string]string{"actor": other, "name": "Other", "role": "operator"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", other, SchemaSupersede, Supersede{Target: "e1", Text: "step down"}, "e1"),
		event(t, "e4", other, SchemaSupersede, Supersede{Target: "e3", Text: "restore hidden operator grant"}, "e3"),
	})
	decision, _ := projection.Decision("e4")
	if decision.Verdict != Ineffective || decision.Reason != "operator standing is required to restore an operator grant" {
		t.Fatalf("dormant operator revival = %+v", decision)
	}
	if actor := projection.Actors[other]; !actor.Retired || contains(actor.Roles, "operator") {
		t.Fatalf("actor after dormant revival attempt = %+v", actor)
	}
}

func TestDormantOperatorGrantCannotBeRevivedThroughMembershipRestoration(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "agent becomes operator", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "operator"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaState, State{Kind: KindRoster, Text: "other joins", Body: map[string]string{"actor": other, "name": "Other", "role": "ratifier"}}, "e0"),
		event(t, "e6", operator, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e7", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove operator membership"}, "e1"),
		event(t, "e8", other, SchemaSupersede, Supersede{Target: "e7", Text: "revive dormant operator"}, "e7"),
	})
	decision, _ := projection.Decision("e8")
	if decision.Verdict != Ineffective || decision.Reason != "operator standing is required to restore operator-bearing membership" {
		t.Fatalf("membership restoration = %+v", decision)
	}
	if actor := projection.Actors[agent]; !actor.Retired || contains(actor.Roles, "operator") {
		t.Fatalf("operator revived through membership = %+v", actor)
	}
}

func TestRosterProjectsRetiredAndDormantAuthoritySources(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "grant ratifier", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "ratifier"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaSupersede, Supersede{Target: "e3", Text: "retire ratifier grant"}, "e3"),
		event(t, "e6", operator, SchemaState, State{Kind: KindRoster, Text: "grant operator", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "operator"}}, "e1"),
		event(t, "e7", operator, SchemaRatify, Ratify{Target: "e6"}, "e6"),
		event(t, "e8", operator, SchemaSupersede, Supersede{Target: "e1", Text: "retire membership"}, "e1"),
	})
	actor := projection.Actors[agent]
	if got := actor.RetiredRoleSources["ratifier"]; len(got) != 1 || got[0] != "e3" {
		t.Fatalf("retired role sources = %#v", actor.RetiredRoleSources)
	}
	if got := actor.DormantRoleSources["operator"]; len(got) != 1 || got[0] != "e6" {
		t.Fatalf("dormant role sources = %#v", actor.DormantRoleSources)
	}
}

func TestModernOperatorGrantPreservesAgentKindAndMembershipBasis(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "grant operator", Body: map[string]string{"actor": agent, "kind": "human", "name": "Wrong name", "role": "operator"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
	})
	actor := projection.Actors[agent]
	if actor.Name != "Agent" || actor.Kind != "agent" || actor.MembershipEvent != "e1" {
		t.Fatalf("operator grant changed identity or membership basis: %+v", actor)
	}
	want := []string{"operator", "participant", "ratifier"}
	if len(actor.Roles) != len(want) {
		t.Fatalf("roles = %#v, want %#v", actor.Roles, want)
	}
	for index := range want {
		if actor.Roles[index] != want[index] {
			t.Fatalf("roles = %#v, want %#v", actor.Roles, want)
		}
	}
}

func TestAuthorityGrantCannotSubstituteForMembership(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "grant without membership", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "ratifier"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "not a participant", Body: map[string]string{"to": other, "conditions": "never"}}, "e0"),
		event(t, "e4", operator, SchemaState, State{Kind: KindPropose, Text: "not a ratifier"}, "e0"),
		event(t, "e5", other, SchemaRatify, Ratify{Target: "e4"}, "e4"),
	})
	for event, reason := range map[string]string{
		"e3": "requested performer is not in the live roster",
		"e5": "actor lacks ratifier role",
	} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != Ineffective || decision.Reason != reason {
			t.Errorf("%s = %+v, want ineffective %q", event, decision, reason)
		}
	}
	if _, exists := projection.Actors[other]; exists {
		t.Fatalf("authority-only record projected a participant: %+v", projection.Actors[other])
	}
}

func TestRequestRejectsPerformerOutsideLiveRoster(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRequest, Text: "unknown", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
	})
	decision, _ := projection.Decision("e1")
	if decision.Verdict != Ineffective || decision.Reason != "requested performer is not in the live roster" {
		t.Fatalf("request decision = %#v", decision)
	}
}

func TestDuplicateIDCannotPoisonOriginal(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e0", other, SchemaState, State{Kind: KindAssert, Text: "duplicate"}),
		event(t, "e1", operator, SchemaState, State{Kind: KindAssert, Text: "claim"}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
	})
	if projection.Decisions[0].Verdict != Effective || projection.Decisions[1].Verdict != Disputed || projection.Decisions[3].Verdict != Effective {
		t.Fatalf("duplicate poisoned decisions: %#v", projection.Decisions)
	}
	if len(projection.Statements) != 2 || !projection.Statements[1].Ratified {
		t.Fatalf("duplicate poisoned statements: %#v", projection.Statements)
	}
}

func TestSupersedingSupersessionResurrectsTarget(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaSupersede, Supersede{Target: "e1", Text: "withdraw"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e2", Text: "undo withdrawal"}, "e2"),
	})
	if projection.Statements[1].Retired {
		t.Fatal("proposal did not resurrect")
	}
}

func TestIncrementalRetirementHandlesMultipleCausesAndNestedResurrection(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaSupersede, Supersede{Target: "e1", Text: "first retirement"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "second retirement"}, "e1"),
		event(t, "e4", operator, SchemaSupersede, Supersede{Target: "e2", Text: "remove first cause"}, "e2"),
		event(t, "e5", operator, SchemaSupersede, Supersede{Target: "e3", Text: "remove second cause"}, "e3"),
		event(t, "e6", operator, SchemaSupersede, Supersede{Target: "e4", Text: "restore first cause"}, "e4"),
	}
	wantRetired := []bool{false, true, true, true, false, true}
	for offset, want := range wantRetired {
		projection := Fold(records[:offset+2])
		var got bool
		for _, statement := range projection.Statements {
			if statement.Event == "e1" {
				got = statement.Retired
				break
			}
		}
		if got != want {
			t.Errorf("prefix through e%d: retired = %v, want %v", offset+1, got, want)
		}
	}
}

func TestProjectionIsByteStable(t *testing.T) {
	projection := golden(t)
	one, err := RenderJSON(projection)
	if err != nil {
		t.Fatal(err)
	}
	two, err := RenderJSON(golden(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("JSON projection is not byte-stable")
	}
	want, err := os.ReadFile("testdata/legacy_projection.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, want) {
		t.Fatalf("legacy projection bytes changed; update only through an explicit migration review\n%s", one)
	}
	if !bytes.Equal(RenderStatus(projection), RenderStatus(golden(t))) {
		t.Fatal("status projection is not byte-stable")
	}
}

func TestNormalizeRosterStateOwnsLegacyKindVocabulary(t *testing.T) {
	tests := []struct {
		name, kind, role, wantKind, wantAuthority string
		wantMembership                            bool
	}{
		{name: "modern membership", kind: "agent", role: "participant", wantMembership: true, wantKind: "agent"},
		{name: "modern authority", kind: "agent", role: "ratifier", wantKind: "agent", wantAuthority: "ratifier"},
		{name: "modern seed", kind: "human", role: "operator", wantKind: "human", wantAuthority: "operator"},
		{name: "legacy agent", role: "agent", wantMembership: true, wantKind: "agent"},
		{name: "legacy human", role: "human", wantMembership: true, wantKind: "human"},
		{name: "legacy service", role: "service", wantMembership: true, wantKind: "service"},
		{name: "legacy operator", role: "operator", wantMembership: true, wantKind: "human", wantAuthority: "operator"},
		{name: "legacy authority", role: "ratifier", wantMembership: true, wantKind: "unspecified", wantAuthority: "ratifier"},
		{name: "legacy participant", role: "participant", wantMembership: true, wantKind: "unspecified"},
		{name: "empty legacy role", wantMembership: true, wantKind: "unspecified"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeRosterState(test.kind, test.role)
			if got.membership != test.wantMembership || got.kind != test.wantKind || got.authorityRole != test.wantAuthority {
				t.Fatalf("normalizeRosterState() = %+v, want membership=%v kind=%q authority=%q", got, test.wantMembership, test.wantKind, test.wantAuthority)
			}
		})
	}
	for _, word := range []string{"agent", "human", "service"} {
		if !IsActorKind(word) {
			t.Errorf("IsActorKind(%q) = false", word)
		}
	}
	// "unspecified" is the kind the normalizer derives for words it does not
	// recognise, so it must not read back as a kind the vocabulary defines.
	// This is the case the authority clause in IsActorKind exists to exclude.
	for _, word := range []string{"", "participant", "operator", "ratifier", "unspecified"} {
		if IsActorKind(word) {
			t.Errorf("IsActorKind(%q) = true", word)
		}
	}
}

func TestLegacyHumanAndServiceRosterRecordsUseNormalizedMembership(t *testing.T) {
	service := "actor:service"
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Operator", "role": "operator"}}),
		event(t, "human", operator, SchemaState, State{Kind: KindRoster, Text: "human joins", Body: map[string]string{"actor": other, "name": "Human", "role": "human"}}, "e0"),
		event(t, "human-ratified", operator, SchemaRatify, Ratify{Target: "human"}, "human"),
		event(t, "service", operator, SchemaState, State{Kind: KindRoster, Text: "service joins", Body: map[string]string{"actor": service, "name": "Service", "role": "service"}}, "e0"),
		event(t, "service-ratified", operator, SchemaRatify, Ratify{Target: "service"}, "service"),
	})

	for _, test := range []struct {
		actor, name, kind, membership string
	}{
		{actor: other, name: "Human", kind: "human", membership: "human"},
		{actor: service, name: "Service", kind: "service", membership: "service"},
	} {
		got := projection.Actors[test.actor]
		if got.Name != test.name || got.Kind != test.kind || got.MembershipEvent != test.membership {
			t.Errorf("actor %q identity = %+v", test.actor, got)
		}
		if len(got.Roles) != 1 || got.Roles[0] != "participant" {
			t.Errorf("actor %q roles = %#v, want participant only", test.actor, got.Roles)
		}
		if sources := got.RoleSources["participant"]; len(sources) != 1 || sources[0] != test.membership {
			t.Errorf("actor %q participant sources = %#v", test.actor, sources)
		}
	}
}

func TestPreconditionProjectionIsPinned(t *testing.T) {
	projection := preconditions(t)
	for eventID, want := range map[string]Decision{
		"e3": {Event: "e3", Sequence: 4, Verdict: Ineffective, Reason: "retired statement cannot be ratified"},
		"e4": {Event: "e4", Sequence: 5, Verdict: Ineffective, Reason: "request state requires body.conditions"},
		"e5": {Event: "e5", Sequence: 6, Verdict: Ineffective, Reason: "artifact state requires body.commit"},
		"e7": {Event: "e7", Sequence: 8, Verdict: Effective, Reason: "authorized ratification"},
	} {
		decision, _ := projection.Decision(eventID)
		if decision != want {
			t.Errorf("%s = %+v, want %+v", eventID, decision, want)
		}
	}
	if len(projection.Artifacts) != 0 || len(projection.Commitments) != 0 {
		t.Fatalf("a refused request or artifact reached the work projection: %+v %+v", projection.Commitments, projection.Artifacts)
	}
	encoded, err := RenderJSON(projection)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RenderJSON(preconditions(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatal("precondition projection is not byte-stable")
	}
	want, err := os.ReadFile("testdata/precondition_projection.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("precondition projection bytes changed; update only through an explicit migration review\n%s", encoded)
	}
}

func TestDeclaredKindIsPositionAwareAndReturnsToUndefinedWhenRetired(t *testing.T) {
	definition := KindDefinition{
		Name: "review-note",
		Fields: []FieldConstraint{
			{Operator: FieldPresent, Name: "topic"},
			{Operator: FieldPresent, Name: "priority"},
			{Operator: FieldOneOf, Name: "priority", Values: []string{"high", "low"}},
		},
		Basis: []BasisConstraint{}, Satisfier: "role:ratifier", Render: RenderNote,
		Staleness: StalenessTerminal, Lifecycle: LifecycleNone,
		Guidance: "Keep one review finding with an explicit priority.",
	}
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: "review-note", Text: "too early", Body: map[string]string{"topic": "fold", "priority": "high"}}),
		event(t, "e2", operator, SchemaState, kindDefinitionState(t, definition), "e0"),
		event(t, "e3", operator, SchemaRatify, Ratify{Target: "e2"}, "e2"),
		event(t, "e4", operator, SchemaState, State{Kind: "review-note", Text: "declared", Body: map[string]string{"topic": "fold", "priority": "high"}}),
		event(t, "e5", operator, SchemaState, State{Kind: "review-note", Text: "bad value", Body: map[string]string{"topic": "fold", "priority": "urgent"}}),
		event(t, "e6", operator, SchemaRatify, Ratify{Target: "e4"}, "e4"),
		event(t, "e7", operator, SchemaSupersede, Supersede{Target: "e2", Text: "retire the definition"}, "e2"),
		event(t, "e8", operator, SchemaState, State{Kind: "review-note", Text: "undefined again", Body: map[string]string{"topic": "fold", "priority": "low"}}),
		event(t, "e9", operator, SchemaSupersede, Supersede{Target: "e7", Text: "restore the definition"}, "e7"),
		event(t, "e10", operator, SchemaState, State{Kind: "review-note", Text: "defined again", Body: map[string]string{"topic": "fold", "priority": "low"}}),
	}
	prefix := Evaluate(records[:7])
	for eventID, want := range map[string]Verdict{"e1": UndefinedKind, "e4": Effective, "e5": Ineffective, "e6": Effective} {
		decision, _ := prefix.Projection.Decision(eventID)
		if decision.Verdict != want {
			t.Errorf("%s verdict = %s (%s), want %s", eventID, decision.Verdict, decision.Reason, want)
		}
	}
	var active *KindDefinition
	for index := range prefix.Vocabulary.Definitions {
		if prefix.Vocabulary.Definitions[index].Name == definition.Name {
			active = &prefix.Vocabulary.Definitions[index]
		}
	}
	if active == nil || active.Source != "e2" || active.RatifiedBy != "e3" || active.Guidance != definition.Guidance {
		t.Fatalf("active definition = %+v", active)
	}
	retired := Evaluate(records[:9])
	decision, _ := retired.Projection.Decision("e8")
	if decision.Verdict != UndefinedKind {
		t.Fatalf("post-retirement verdict = %+v", decision)
	}
	for _, current := range retired.Vocabulary.Definitions {
		if current.Name == definition.Name {
			t.Fatalf("retired definition remained active: %+v", current)
		}
	}
	full := Evaluate(records)
	decision, _ = full.Projection.Decision("e10")
	if decision.Verdict != Effective {
		t.Fatalf("restored definition verdict = %+v", decision)
	}
	if got := full.Projection.OpaqueKinds["review-note"]; len(got) != 2 || got[0] != "e1" || got[1] != "e8" {
		t.Fatalf("undefined kind projection = %#v", got)
	}
}

func TestRefusedArtifactDoesNotProjectAsLive(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindArtifact, Text: "half a citation", Body: map[string]string{"path": "internal/workroom"}}, "e0"),
		event(t, "e2", operator, SchemaState, State{Kind: KindArtifact, Text: "whole citation", Body: map[string]string{"path": "internal/workroom", "commit": "abc123"}}, "e0"),
	})
	decision, _ := projection.Decision("e1")
	if decision.Verdict != Ineffective || decision.Reason != "artifact state requires body.commit" {
		t.Fatalf("partly filled artifact decision = %+v", decision)
	}
	if len(projection.Artifacts) != 1 || projection.Artifacts[0].Event != "e2" {
		t.Fatalf("artifacts = %#v", projection.Artifacts)
	}
	if count := bytes.Count(RenderStatus(projection), []byte("| current |")); count != 1 {
		t.Fatalf("status showed %d current artifacts, want 1:\n%s", count, RenderStatus(projection))
	}
}

// Who may ratify a statement is settled when that statement is admitted, and
// redefining the kind afterwards does not reopen it. The fold has always
// decided this way — decideRatify reads the definition captured on the target
// — but until now it kept the answer to itself, so every reader outside the
// fold had to guess, and the only material to guess from is the current
// vocabulary. That guess is wrong in both directions the moment a satisfier is
// revised, and both directions are asserted here.
//
// The projected field and the decisions are asserted together on purpose.
// Either alone can be satisfied by a projection that publishes a satisfier the
// fold does not use; together they pin that the value a reader is handed is
// the value the fold will judge by.
func TestProjectedSatisfierIsTheOneAdmittedWithTheStatement(t *testing.T) {
	steward := KindDefinition{
		Name: "finding", Fields: present("topic"), Basis: []BasisConstraint{},
		Satisfier: "role:steward", Render: RenderNote, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "Ratified by a steward.",
	}
	rider := steward
	rider.Satisfier = "role:rider"
	rider.Guidance = "Ratified by a rider."
	member := func(id, actor, role, basis string) Record {
		return event(t, id, operator, SchemaState, State{Kind: KindRoster, Text: "roster",
			Body: map[string]string{"actor": actor, "kind": "agent", "name": actor, "role": role}}, basis)
	}
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		member("join-a", agent, "participant", "e0"),
		event(t, "join-a-r", operator, SchemaRatify, Ratify{Target: "join-a"}, "join-a"),
		member("steward-grant", agent, "steward", "join-a"),
		event(t, "steward-grant-r", operator, SchemaRatify, Ratify{Target: "steward-grant"}, "steward-grant"),
		member("join-b", other, "participant", "e0"),
		event(t, "join-b-r", operator, SchemaRatify, Ratify{Target: "join-b"}, "join-b"),
		member("rider-grant", other, "rider", "join-b"),
		event(t, "rider-grant-r", operator, SchemaRatify, Ratify{Target: "rider-grant"}, "rider-grant"),
		// The kind as first defined, and one statement admitted under it.
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, steward), "e0"),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "early", operator, SchemaState, State{Kind: "finding", Text: "admitted under the steward rule", Body: map[string]string{"topic": "fold"}}, "e0"),
		// The kind redefined, and one statement admitted under that.
		event(t, "d1s", operator, SchemaSupersede, Supersede{Target: "d1", Text: "replace the satisfier"}, "d1"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, rider), "e0"),
		event(t, "d2r", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "late", operator, SchemaState, State{Kind: "finding", Text: "admitted under the rider rule", Body: map[string]string{"topic": "fold"}}, "e0"),
		// The steward may still ratify the early statement, though the kind no
		// longer names stewards; and may not reach the late one, though the
		// kind named stewards when the steward's own grant was made.
		event(t, "steward-early", agent, SchemaRatify, Ratify{Target: "early"}, "early"),
		event(t, "steward-late", agent, SchemaRatify, Ratify{Target: "late"}, "late"),
		event(t, "rider-late", other, SchemaRatify, Ratify{Target: "late"}, "late"),
		event(t, "rider-early", other, SchemaRatify, Ratify{Target: "early"}, "early"),
	})
	for id, want := range map[string]Verdict{
		"steward-early": Effective,
		"steward-late":  Ineffective,
		"rider-late":    Effective,
		"rider-early":   Ineffective,
	} {
		decision, _ := projection.Decision(id)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", id, decision.Verdict, decision.Reason, want)
		}
	}
	satisfiers := make(map[string]string)
	for _, statement := range projection.Statements {
		satisfiers[statement.Event] = statement.Satisfier
	}
	for id, want := range map[string]string{"early": "role:steward", "late": "role:rider"} {
		if got := satisfiers[id]; got != want {
			t.Errorf("projected satisfier for %s = %q, want %q — a reader given this cannot agree with the fold", id, got, want)
		}
	}
}

// A supersession of a supersession contests a retirement. It must not thereby
// install a definition whose ratification an actor already replaced, and it
// must not disturb any decision already emitted.
func TestRestoredDefinitionDoesNotDisplaceALaterRatifiedOne(t *testing.T) {
	first := KindDefinition{
		Name: "finding", Fields: present("topic"), Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderNote, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "First guidance.",
	}
	second := first
	second.Fields = present("severity")
	second.Guidance = "Second guidance."
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, first), "e0"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, second), "e0"),
		event(t, "d2r", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "n1", operator, SchemaState, State{Kind: "finding", Text: "under the second", Body: map[string]string{"severity": "high"}}, "e0"),
		event(t, "d2s", operator, SchemaSupersede, Supersede{Target: "d2", Text: "retire the second"}, "d2"),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d2ss", operator, SchemaSupersede, Supersede{Target: "d2s", Text: "contest the retirement"}, "d2s"),
		event(t, "n2", operator, SchemaState, State{Kind: "finding", Text: "under the first", Body: map[string]string{"topic": "fold"}}, "e0"),
		event(t, "n3", operator, SchemaState, State{Kind: "finding", Text: "second fields only", Body: map[string]string{"severity": "high"}}, "e0"),
	}
	result := Evaluate(records)
	for eventID, want := range map[string]Verdict{"d2r": Effective, "n1": Effective, "d1r": Effective, "n2": Effective, "n3": Ineffective} {
		decision, _ := result.Projection.Decision(eventID)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", eventID, decision.Verdict, decision.Reason, want)
		}
	}
	var governing *KindDefinition
	for index := range result.Vocabulary.Definitions {
		if result.Vocabulary.Definitions[index].Name == "finding" {
			governing = &result.Vocabulary.Definitions[index]
		}
	}
	if governing == nil || governing.Source != "d1" || governing.RatifiedBy != "d1r" || governing.Guidance != first.Guidance {
		t.Fatalf("governing definition = %+v, want the version ratified last", governing)
	}
	for length := 1; length <= len(records); length++ {
		prefix := Fold(records[:length])
		for _, decision := range prefix.Decisions {
			settled, _ := result.Projection.Decision(decision.Event)
			if settled != decision {
				t.Fatalf("prefix of %d changed the decision for %s: %+v, want %+v", length, decision.Event, decision, settled)
			}
		}
	}
}

// A statement may be ratified more than once: retire the ratification or the
// statement, restore it, and ratify it again. Force arrives at the newest of
// those ratifications, so the selector must compare that position and not the
// oldest one still standing. Here the first version is ratified, retired,
// beaten by a second version, then restored and ratified again — the second
// version's own ratification is older than the re-ratification, so the first
// version governs.
func TestRepeatedRatificationCarriesItsLatestPosition(t *testing.T) {
	first := KindDefinition{
		Name: "finding", Fields: present("topic"), Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderNote, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "First guidance.",
	}
	second := first
	second.Fields = present("severity")
	second.Guidance = "Second guidance."
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, first), "e0"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, second), "e0"),
		event(t, "d1r1", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d1s", operator, SchemaSupersede, Supersede{Target: "d1", Text: "retire the first"}, "d1"),
		event(t, "d2r", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "d2s", operator, SchemaSupersede, Supersede{Target: "d2", Text: "retire the second"}, "d2"),
		event(t, "d1ss", operator, SchemaSupersede, Supersede{Target: "d1s", Text: "restore the first"}, "d1s"),
		event(t, "d1r2", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d2ss", operator, SchemaSupersede, Supersede{Target: "d2s", Text: "restore the second"}, "d2s"),
		event(t, "n1", operator, SchemaState, State{Kind: "finding", Text: "under the first", Body: map[string]string{"topic": "fold"}}, "e0"),
	}
	result := Evaluate(records)
	for eventID, want := range map[string]Verdict{"d1r1": Effective, "d2r": Effective, "d1r2": Effective, "n1": Effective} {
		decision, _ := result.Projection.Decision(eventID)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", eventID, decision.Verdict, decision.Reason, want)
		}
	}
	var governing *KindDefinition
	for index := range result.Vocabulary.Definitions {
		if result.Vocabulary.Definitions[index].Name == "finding" {
			governing = &result.Vocabulary.Definitions[index]
		}
	}
	if governing == nil || governing.Source != "d1" || governing.RatifiedBy != "d1r2" || governing.Guidance != first.Guidance {
		t.Fatalf("governing definition = %+v, want d1 ratified by d1r2", governing)
	}
	for length := 1; length <= len(records); length++ {
		prefix := Fold(records[:length])
		for _, decision := range prefix.Decisions {
			settled, _ := result.Projection.Decision(decision.Event)
			if settled != decision {
				t.Fatalf("prefix of %d changed the decision for %s: %+v, want %+v", length, decision.Event, decision, settled)
			}
		}
	}
}

func TestPrefixBindingBindsARecordThatEndsAtItsTransition(t *testing.T) {
	activation := State{Kind: KindFoldActivation, Text: "activate the next fold", Body: map[string]string{
		"fold": "spike/internal/workroom@abc123", "entry": "gitseq/spike/internal/workroom",
		"interface": "workroom-fold@1", "toolchain": "go1.25.0", "prefix": "genesis",
	}}
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaStateLegacy, activation),
		event(t, "e2", operator, SchemaRatifyLegacy, Ratify{Target: "e1"}, "e1"),
	}
	unratified := Evaluate(records[:2]).Vocabulary.Binding
	if unratified.Status != "unbound" || len(unratified.Transitions) != 0 {
		t.Fatalf("unratified activation binding = %+v", unratified)
	}
	bound := Evaluate(records).Vocabulary.Binding
	if bound.Status != "bound" || bound.Reason != "" || len(bound.Transitions) != 1 {
		t.Fatalf("binding at the transition = %+v", bound)
	}
	if bound.Transitions[0].Activation != "e1" || bound.Transitions[0].Ratification != "e2" || !bound.Transitions[0].Prefix {
		t.Fatalf("transition = %+v", bound.Transitions[0])
	}
}

func TestCurrentStateCannotActivateAFoldButLegacyHistoryStillReplays(t *testing.T) {
	activation := State{Kind: KindFoldActivation, Text: "activate the next fold", Body: map[string]string{
		"fold": "spike/internal/workroom@abc123", "entry": "gitseq/spike/internal/workroom",
		"interface": "workroom-fold@1", "toolchain": "go1.25.0", "prefix": "genesis",
	}}
	seed := event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}})

	current := Evaluate([]Record{seed, event(t, "new", operator, SchemaState, activation, "e0")})
	decision, _ := current.Projection.Decision("new")
	if decision.Verdict != UndefinedKind || len(current.Vocabulary.Binding.Transitions) != 0 {
		t.Fatalf("current activation = %+v binding=%+v, want undefined with no transition", decision, current.Vocabulary.Binding)
	}
	for _, definition := range current.Vocabulary.Definitions {
		if definition.Name == KindFoldActivation {
			t.Fatalf("current vocabulary still advertises fold activation: %+v", definition)
		}
	}

	for _, schema := range []string{SchemaStateLegacy, SchemaStateV1} {
		legacy := Evaluate([]Record{
			seed,
			event(t, "old", operator, schema, activation, "e0"),
			event(t, "old-ratified", operator, SchemaRatifyLegacy, Ratify{Target: "old"}, "old"),
		})
		decision, _ = legacy.Projection.Decision("old")
		if decision.Verdict != Effective || legacy.Vocabulary.Binding.Status != "bound" || len(legacy.Vocabulary.Binding.Transitions) != 1 {
			t.Fatalf("%s activation = %+v binding=%+v, want historical bridge", schema, decision, legacy.Vocabulary.Binding)
		}
	}
	refused := Evaluate([]Record{
		seed,
		event(t, "old", operator, SchemaStateLegacy, activation, "e0"),
		event(t, "new-ratification", operator, SchemaRatify, Ratify{Target: "old"}, "old"),
	})
	decision, _ = refused.Projection.Decision("new-ratification")
	if decision.Verdict != Ineffective || decision.Reason != "fold activation moved to the host binding" || len(refused.Vocabulary.Binding.Transitions) != 0 {
		t.Fatalf("current ratification of legacy activation = %+v binding=%+v", decision, refused.Vocabulary.Binding)
	}
}

func TestKindDefinitionCannotCarryAFoldCodePointer(t *testing.T) {
	definition := KindDefinition{
		Name: "finding", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderNote, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "Record one finding.",
	}
	state := kindDefinitionState(t, definition)
	state.Body["fold"] = "internal/workroom@abc123"
	result := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "definition", operator, SchemaState, state, "e0"),
	})
	decision, _ := result.Decision("definition")
	if decision.Verdict != Uninterpretable || !strings.Contains(decision.Reason, "fold is not part") {
		t.Fatalf("kind definition carrying code = %+v", decision)
	}
}

func TestIdentifierGrammarRefusesNamesNoActorCouldSatisfy(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		mutate       func(*KindDefinition)
	}{
		{name: "blank kind name", reason: `kind name " " is not an identifier`, mutate: func(d *KindDefinition) { d.Name = " " }},
		{name: "spaced role", reason: `role name " ratifier" is not an identifier`, mutate: func(d *KindDefinition) { d.Satisfier = "role: ratifier" }},
		{name: "spaced field", reason: `field constraint name " topic" is not an identifier`, mutate: func(d *KindDefinition) { d.Fields = present(" topic") }},
		{name: "shouting basis kind", reason: `basis kind "Assert" is not an identifier`, mutate: func(d *KindDefinition) { d.Basis = countKinds(0, 1, "Assert") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := KindDefinition{
				Name: "finding", Fields: present("topic"), Basis: []BasisConstraint{},
				Satisfier: "role:ratifier", Render: RenderNote, Staleness: StalenessPropagates,
				Lifecycle: LifecycleNone, Guidance: "Keep one finding.",
			}
			test.mutate(&definition)
			projection := Fold([]Record{
				event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
				event(t, "d0", operator, SchemaState, kindDefinitionState(t, definition), "e0"),
				event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
			})
			decision, _ := projection.Decision("d0")
			if decision.Verdict != Uninterpretable || !strings.Contains(decision.Reason, test.reason) {
				t.Fatalf("definition decision = %+v, want uninterpretable %q", decision, test.reason)
			}
			ratification, _ := projection.Decision("d0r")
			if ratification.Verdict != Ineffective || ratification.Reason != "ratify target is not effective" {
				t.Fatalf("ratification of an unusable definition = %+v", ratification)
			}
		})
	}
}

// Roster is the one ordinary-looking kind the fold reads directly: membership
// and authority are extracted from its body, not from its definition. Letting
// it be redefined would let a participant confer authority on themselves.
func TestRosterCannotBeRedefinedToEscalateAuthority(t *testing.T) {
	roster := KindDefinition{
		Name: KindRoster, Fields: present("actor", "name", "role"), Basis: []BasisConstraint{},
		Satisfier: "role:participant", Render: RenderGovernance, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "Let any participant confer authority.",
	}
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "d0", operator, SchemaState, kindDefinitionState(t, roster), "e0"),
		event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
		event(t, "g0", agent, SchemaState, State{Kind: KindRoster, Text: "self-promotion", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "operator"}}, "e1"),
		event(t, "g0r", agent, SchemaRatify, Ratify{Target: "g0"}, "g0"),
	})
	decision, _ := projection.Decision("d0")
	if decision.Verdict != Uninterpretable || !strings.Contains(decision.Reason, `kind "roster" is interpreted by the fold and cannot be redefined`) {
		t.Fatalf("roster redefinition = %+v", decision)
	}
	ratification, _ := projection.Decision("d0r")
	if ratification.Verdict != Ineffective || ratification.Reason != "ratify target is not effective" {
		t.Fatalf("roster redefinition ratification = %+v", ratification)
	}
	escalation, _ := projection.Decision("g0r")
	if escalation.Verdict != Ineffective || escalation.Reason != "authority grant cannot be authored or ratified by its beneficiary" {
		t.Fatalf("self-ratified grant = %+v", escalation)
	}
	if roles := projection.Actors[agent].Roles; contains(roles, "operator") || contains(roles, "ratifier") {
		t.Fatalf("participant escalated to %#v", roles)
	}
}

func TestInvalidConstraintAlgebraIsTypedUninterpretable(t *testing.T) {
	for _, test := range []struct {
		name, fields, basis, reason string
	}{
		{name: "invalid regex", fields: `[{"op":"matches","name":"topic","pattern":"["}]`, basis: `[]`, reason: "error parsing regexp"},
		{name: "null fields", fields: `null`, basis: `[]`, reason: "fields must be a JSON array"},
		{name: "null basis", fields: `[]`, basis: `null`, reason: "basis must be a JSON array"},
		// A rejected operand is quoted back into the reason verbatim, so a
		// definition can put any words at all into the uninterpretable reason
		// channel — including the fold's own words for the one uninterpretable
		// case a later interpreter binding does resolve. Readers of that
		// channel must therefore compare the whole reason, never search it.
		{
			name:   "operand quoting the unbound-interpreter reason",
			fields: `[{"op":"type","name":"topic","type":"interpreter execution is not held"}]`, basis: `[]`,
			reason: `uninterpretable kind definition: unsupported type "interpreter execution is not held"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := State{Kind: KindKindDef, Text: test.name, Body: map[string]string{
				"name": "bad", "fields": test.fields, "basis": test.basis,
				"satisfier": "none", "render": "note", "staleness": "propagates", "lifecycle": "none", "guidance": "never active",
			}}
			projection := Fold([]Record{
				event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
				event(t, "e1", operator, SchemaState, invalid),
				event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
			})
			definition, _ := projection.Decision("e1")
			if definition.Verdict != Uninterpretable || !strings.Contains(definition.Reason, test.reason) {
				t.Fatalf("invalid definition = %+v", definition)
			}
			ratification, _ := projection.Decision("e2")
			if ratification.Verdict != Ineffective || ratification.Reason != "ratify target is not effective" {
				t.Fatalf("invalid definition ratification = %+v", ratification)
			}
		})
	}
}

func TestDefinitionReplacementRequiresRetiringTheLivePredecessor(t *testing.T) {
	first := KindDefinition{
		Name: "finding", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderNote, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "First guidance.",
	}
	second := first
	second.Guidance = "Second guidance."
	result := Evaluate([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, first)),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, second), "d1"),
		event(t, "d2r0", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "d1s", operator, SchemaSupersede, Supersede{Target: "d1", Text: "replace the definition"}, "d1"),
		event(t, "d2r1", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
	})
	refused, _ := result.Projection.Decision("d2r0")
	if refused.Verdict != Disputed || !strings.Contains(refused.Reason, "live predecessor") {
		t.Fatalf("replacement without retirement = %+v", refused)
	}
	accepted, _ := result.Projection.Decision("d2r1")
	if accepted.Verdict != Effective {
		t.Fatalf("replacement after retirement = %+v", accepted)
	}
	for _, definition := range result.Vocabulary.Definitions {
		if definition.Name == second.Name && (definition.Source != "d2" || definition.Guidance != second.Guidance) {
			t.Fatalf("replacement definition = %+v", definition)
		}
	}
}

func TestDeclaredLifecycleKindsDriveTheCommitmentLoop(t *testing.T) {
	workOrder := KindDefinition{
		Name: "work-order", Fields: present("to", "conditions"), Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderCommitment, Staleness: StalenessPropagates,
		Lifecycle: LifecycleRequest, Guidance: "Request a concrete unit of work.",
	}
	undertaking := KindDefinition{
		Name: "undertaking", Fields: []FieldConstraint{}, Basis: countKinds(1, 1, "work-order"),
		Satisfier: SatisfierNone, Render: RenderCommitment, Staleness: StalenessPropagates,
		Lifecycle: LifecyclePromise, Guidance: "Accept one work order.",
	}
	delivery := KindDefinition{
		Name: "delivery", Fields: []FieldConstraint{}, Basis: countKinds(1, 1, "undertaking"),
		Satisfier: SatisfierOriginatingRequester, Render: RenderResult, Staleness: StalenessPropagates,
		Lifecycle: LifecycleReport, Guidance: "Report an undertaking to its requester.",
	}
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "d0", operator, SchemaState, kindDefinitionState(t, workOrder), "e0"),
		event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, undertaking), "d0r"),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, delivery), "d1r"),
		event(t, "d2r", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "r0", operator, SchemaState, State{Kind: "work-order", Text: "build", Body: map[string]string{"to": agent, "conditions": "tests pass"}}),
		event(t, "p0", agent, SchemaState, State{Kind: "undertaking", Text: "I will"}, "r0"),
		event(t, "x0", agent, SchemaState, State{Kind: "delivery", Text: "done"}, "p0"),
		event(t, "x0r", operator, SchemaRatify, Ratify{Target: "x0"}, "x0"),
	})
	if len(projection.Commitments) != 1 || projection.Commitments[0].Status != "satisfied" {
		t.Fatalf("declared lifecycle commitment = %+v", projection.Commitments)
	}
	if projection.Commitments[0].Request != "r0" || projection.Commitments[0].Promise != "p0" || projection.Commitments[0].Report != "x0" {
		t.Fatalf("declared lifecycle lineage = %+v", projection.Commitments[0])
	}
}

func TestDeclaredLifecycleKindsRemainTotalWithoutExpectedBasis(t *testing.T) {
	undertaking := KindDefinition{
		Name: "undertaking", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderCommitment, Staleness: StalenessPropagates,
		Lifecycle: LifecyclePromise, Guidance: "An intentionally incomplete promise definition.",
	}
	delivery := KindDefinition{
		Name: "delivery", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierOriginatingRequester, Render: RenderResult, Staleness: StalenessPropagates,
		Lifecycle: LifecycleReport, Guidance: "An intentionally incomplete report definition.",
	}
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		// The agent has to be a live participant for this test to reach what it
		// is about. Without the membership rows below the fold refuses p0 and
		// x0 as unrostered authorship, which is correct but is not the
		// lifecycle-totality question this test asks.
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e1r", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "d0", operator, SchemaState, kindDefinitionState(t, undertaking)),
		event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, delivery)),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "p0", agent, SchemaState, State{Kind: "undertaking", Text: "no request"}),
		event(t, "x0", agent, SchemaState, State{Kind: "delivery", Text: "no promise"}),
	})
	for eventID, reason := range map[string]string{
		"p0": "promise lifecycle basis count is 0, want exactly one request",
		// The lifecycle rule now admits either shape, and a declared kind that
		// wants only one narrows it through its own basis constraint rather
		// than through this message.
		"x0": "report lifecycle basis count is 0, want exactly one promise or request",
	} {
		decision, _ := projection.Decision(eventID)
		if decision.Verdict != Ineffective || decision.Reason != reason {
			t.Errorf("%s decision = %+v", eventID, decision)
		}
	}
}

func TestFoldActivationRecordsPrefixBoundaryThenNamesExecutionGap(t *testing.T) {
	activation := State{Kind: KindFoldActivation, Text: "activate the next fold", Body: map[string]string{
		"fold": "spike/internal/workroom@abc123", "entry": "gitseq/spike/internal/workroom",
		"interface": "workroom-fold@1", "toolchain": "go1.25.0", "prefix": "genesis",
	}}
	result := Evaluate([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaStateLegacy, activation),
		event(t, "e2", operator, SchemaRatifyLegacy, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindAssert, Text: "after the seam"}),
	})
	if result.Vocabulary.Binding.Status != "uninterpretable" || len(result.Vocabulary.Binding.Transitions) != 1 {
		t.Fatalf("binding = %+v", result.Vocabulary.Binding)
	}
	transition := result.Vocabulary.Binding.Transitions[0]
	if transition.Activation != "e1" || transition.Ratification != "e2" || !transition.Prefix {
		t.Fatalf("transition = %+v", transition)
	}
	decision, _ := result.Projection.Decision("e3")
	if decision.Verdict != Uninterpretable || decision.Reason != "uninterpretable: activated interpreter execution is not held" {
		t.Fatalf("post-seam decision = %+v", decision)
	}
}

func TestProvenanceRendersBranchesAtTheirActualDepth(t *testing.T) {
	projection := Projection{Provenance: map[string][]string{"artifact": {"decision", "commit"}, "decision": {"seed"}}}
	want := "artifact\n  decision\n    seed\n  commit\n"
	if got := string(RenderProvenance(projection, "artifact")); got != want {
		t.Fatalf("provenance:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmptyCollectionsRenderAsArrays(t *testing.T) {
	projection := Fold(nil)
	encoded, err := RenderJSON(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range [][]byte{[]byte(`"decisions": []`), []byte(`"acts": []`), []byte(`"statements": []`), []byte(`"commitments": []`), []byte(`"artifacts": []`)} {
		if !bytes.Contains(encoded, field) {
			t.Fatalf("projection omitted stable empty array %s: %s", field, encoded)
		}
	}
}

func TestProjectionIncludesEverySemanticReplyAct(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "reason"}, "e1"),
	}
	for index := range records {
		records[index].Timestamp = int64(100 + index)
	}
	projection := Fold(records)
	if len(projection.Acts) != 2 {
		t.Fatalf("acts = %#v", projection.Acts)
	}
	if projection.Acts[0].Type != "ratify" || projection.Acts[0].Target != "e1" || projection.Acts[0].Verdict != Effective {
		t.Fatalf("ratify projection = %#v", projection.Acts[0])
	}
	if projection.Acts[1].Type != "supersede" || projection.Acts[1].Text != "reason" {
		t.Fatalf("supersede projection = %#v", projection.Acts[1])
	}
	if projection.Statements[1].Timestamp != 101 || projection.Acts[0].Timestamp != 102 || projection.Acts[1].Timestamp != 103 {
		t.Fatalf("projection lost event timestamps: statements=%#v acts=%#v", projection.Statements, projection.Acts)
	}
}

func TestCanonicalPayloadRejectsAlternateEncoding(t *testing.T) {
	if _, err := Decode(SchemaState, []byte(`{"text":"x","kind":"assert"}`)); err == nil {
		t.Fatal("accepted non-canonical field order")
	}
}

func TestParticipantRosterKindIsJudgedByTheFold(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "participant"}}),
	})
	decision, _ := projection.Decision("e1")
	if decision.Verdict != Ineffective || decision.Reason != "participant roster state requires body.kind" {
		t.Fatalf("participant roster decision = %+v", decision)
	}
}

// worldRecords seeds a roster and appends the case under test.
func worldRecords(t testing.TB, extra ...Record) []Record {
	t.Helper()
	base := []Record{
		event(t, "w0", operator, SchemaState, State{Kind: KindRoster, Text: "Operator begins the workroom", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "w1", operator, SchemaState, State{Kind: KindRoster, Text: "Agent joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "w0"),
		event(t, "w2", operator, SchemaRatify, Ratify{Target: "w1"}, "w1"),
	}
	return append(base, extra...)
}

func artifactByEvent(t *testing.T, projection Projection, id string) Artifact {
	t.Helper()
	for _, artifact := range projection.Artifacts {
		if artifact.Event == id {
			return artifact
		}
	}
	t.Fatalf("no artifact projected for %s", id)
	return Artifact{}
}

func statementByEvent(t *testing.T, projection Projection, id string) Statement {
	t.Helper()
	for _, statement := range projection.Statements {
		if statement.Event == id {
			return statement
		}
	}
	t.Fatalf("no statement projected for %s", id)
	return Statement{}
}

// A retired artifact under a document means the world moved, and the mark must
// survive artifact-provenance hops. A reasoning statement still becomes stale,
// but does not claim that every later argument describes the old world.
func TestRetiredArtifactMarksDependentsAsDescribingASupersededWorld(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI reference page", Body: map[string]string{"path": "docs/reference/gs.md", "commit": "bbb222"}}, "w3"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI reference index", Body: map[string]string{"path": "docs/reference/index.md", "commit": "bbb222"}}, "w4"),
		event(t, "w8", agent, SchemaState, State{Kind: KindAssert, Text: "The reference documents every subcommand"}, "w4"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation, replacing aaa111", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ccc333"}}, "w0"),
		event(t, "w7", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ccc333"}, "w3", "w6"),
	)
	projection := Fold(records)

	page := artifactByEvent(t, projection, "w4")
	if !page.Stale || !page.DescribesSupersededWorld {
		t.Fatalf("page resting on a retired artifact: stale=%v world=%v", page.Stale, page.DescribesSupersededWorld)
	}
	index := artifactByEvent(t, projection, "w5")
	if !index.Stale || !index.DescribesSupersededWorld {
		t.Fatalf("artifact-provenance hop lost the distinction: stale=%v world=%v", index.Stale, index.DescribesSupersededWorld)
	}
	claim := statementByEvent(t, projection, "w8")
	if !claim.Stale || claim.DescribesSupersededWorld {
		t.Fatalf("reasoning hop carried the world flag: stale=%v world=%v", claim.Stale, claim.DescribesSupersededWorld)
	}
}

func TestWorldStalenessStopsAtAReviewReasoningChain(t *testing.T) {
	records := reviewRecords(t,
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "direct-answer", agent, SchemaState, State{Kind: KindArtifact, Text: "answer resting directly on the review", Body: map[string]string{"path": "spike/direct-answer", "commit": "head2"}}, "approval"),
		event(t, "follow-up", operator, SchemaState, State{Kind: KindRequest, Text: "follow the reviewed result", Body: map[string]string{"to": agent, "conditions": "answer the review"}}, "approval"),
		event(t, "follow-up-promise", agent, SchemaState, State{Kind: KindPromise, Text: "answer the review"}, "follow-up"),
		event(t, "answer", agent, SchemaState, State{Kind: KindArtifact, Text: "later answer", Body: map[string]string{"path": "spike/answer", "commit": "head2"}}, "follow-up-promise"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "implementation replaced"}, "r5"),
	)
	projection := Fold(records)
	verdict := statementByEvent(t, projection, "approval")
	if !verdict.Stale || !verdict.DescribesSupersededWorld {
		t.Fatalf("verdict directly naming retired artifact: stale=%v world=%v", verdict.Stale, verdict.DescribesSupersededWorld)
	}
	directAnswer := artifactByEvent(t, projection, "direct-answer")
	if !directAnswer.Stale || directAnswer.DescribesSupersededWorld {
		t.Fatalf("artifact directly answering world-stale reasoning: stale=%v world=%v", directAnswer.Stale, directAnswer.DescribesSupersededWorld)
	}
	for _, event := range []string{"follow-up", "follow-up-promise"} {
		statement := statementByEvent(t, projection, event)
		if !statement.Stale || statement.DescribesSupersededWorld {
			t.Fatalf("%s across reasoning edge: stale=%v world=%v", event, statement.Stale, statement.DescribesSupersededWorld)
		}
	}
	answer := artifactByEvent(t, projection, "answer")
	if !answer.Stale || answer.DescribesSupersededWorld {
		t.Fatalf("artifact answering stale request: stale=%v world=%v", answer.Stale, answer.DescribesSupersededWorld)
	}
}

// Being withdrawn and standing over a withdrawal are two facts, and an artifact
// has to carry them apart. Fused into one boolean they read the same to every
// caller, and a gate that must refuse the first while admitting the second has
// nothing to read.
func TestArtifactCarriesRetirementAndStalenessApart(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "feature built on it", Body: map[string]string{"path": "spike/cmd/feature", "commit": "bbb222"}}, "w3"),
		event(t, "w5", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced"}, "w3"),
	)
	projection := Fold(records)

	replaced := artifactByEvent(t, projection, "w3")
	if !replaced.Retired || replaced.Stale {
		t.Fatalf("the superseded artifact: retired=%v stale=%v, want retired and not stale", replaced.Retired, replaced.Stale)
	}
	standing := artifactByEvent(t, projection, "w4")
	if standing.Retired || !standing.Stale {
		t.Fatalf("the artifact above it: retired=%v stale=%v, want stale and not retired", standing.Retired, standing.Stale)
	}
	// bbb222 is still bbb222. Staleness is a reason to re-read the reasoning,
	// never evidence that the pointer stopped pointing.
	if standing.Commit != "bbb222" {
		t.Fatalf("stale artifact commit = %q", standing.Commit)
	}
}

// Ordinary staleness must not claim the world moved. The golden fixture's
// artifact goes stale because its governing request died, which is the
// argument dying, not the code changing.
func TestRetiredArgumentIsStaleWithoutDescribingASupersededWorld(t *testing.T) {
	artifact := golden(t).Artifacts[0]
	if !artifact.Stale {
		t.Fatal("golden artifact is no longer stale; the fixture no longer tests this")
	}
	if artifact.DescribesSupersededWorld {
		t.Fatal("a retired request was reported as the world moving")
	}
}

// The succession warning is a to-do, so doing the work must clear it.
func TestSuccessionWarningAppearsUntilThePredecessorIsSuperseded(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation, replacing aaa111", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ccc333"}}, "w0"),
		event(t, "w5", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ccc333"}, "w3", "w4"),
	)

	before := Fold(records[:len(records)-1])
	if !artifactByEvent(t, before, "w4").SuccessionUnrecorded {
		t.Fatal("a live predecessor at the same path raised no warning")
	}
	after := Fold(records)
	if artifactByEvent(t, after, "w4").SuccessionUnrecorded {
		t.Fatal("warning survived the supersession that answers it")
	}
	if artifactByEvent(t, after, "w3").SuccessionUnrecorded {
		t.Fatal("the first artifact at a path warned about a predecessor it does not have")
	}
}

// A different path is a different thing; the fold must not infer that two
// spellings mean the same tree.
func TestSuccessionWarningComparesPathsExactly(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaStateLegacy, State{Kind: KindArtifact, Text: "CLI and its docs", Body: map[string]string{"path": "spike/cmd/gs,docs", "commit": "ccc333"}}, "w0"),
	)
	if artifactByEvent(t, Fold(records), "w4").SuccessionUnrecorded {
		t.Fatal("a comma-joined path was treated as succeeding one of its members")
	}
}

func TestArtifactAdmissionRefusesWholeRepositoryAndCommaJoinedPathsProspectively(t *testing.T) {
	records := worldRecords(t,
		event(t, "legacy-dot", agent, SchemaStateLegacy, State{Kind: KindArtifact, Text: "historical repository pointer", Body: map[string]string{"path": ".", "commit": "aaa111"}}, "w0"),
		event(t, "new-dot", agent, SchemaState, State{Kind: KindArtifact, Text: "new repository pointer", Body: map[string]string{"path": ".", "commit": "bbb222"}}, "w0"),
		event(t, "new-comma", agent, SchemaState, State{Kind: KindArtifact, Text: "two paths pretending to be one", Body: map[string]string{"path": "a,b", "commit": "ccc333"}}, "w0"),
	)
	projection := Fold(records)
	if decision, _ := projection.Decision("legacy-dot"); decision.Verdict != Effective {
		t.Fatalf("legacy artifact was reinterpreted: %+v", decision)
	}
	for _, id := range []string{"new-dot", "new-comma"} {
		decision, _ := projection.Decision(id)
		if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "artifact path") {
			t.Fatalf("%s decision = %+v", id, decision)
		}
	}
}

func TestArtifactAdmissionUsesTheGovernedRenderClass(t *testing.T) {
	definition := KindDefinition{
		Name: "release-bundle", Fields: present("path", "commit"), Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderArtifact, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "Point to a release bundle.",
	}
	records := worldRecords(t,
		event(t, "define", operator, SchemaState, kindDefinitionState(t, definition), "w0"),
		event(t, "define-ratified", operator, SchemaRatify, Ratify{Target: "define"}, "define"),
		event(t, "bad", agent, SchemaState, State{Kind: "release-bundle", Text: "bad path", Body: map[string]string{"path": "a,b", "commit": "head"}}, "w0"),
	)
	decision, _ := Fold(records).Decision("bad")
	if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "artifact path") {
		t.Fatalf("custom artifact path decision = %+v", decision)
	}
}

func TestMergeReceiptAuthorizesCrossAuthorArtifactSupersession(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "older implementation", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("retire")
	if decision.Verdict != Effective || decision.Reason != "merge approval authorized artifact succession" {
		t.Fatalf("cross-author merge succession = %+v", decision)
	}
	if !artifactByEvent(t, projection, "predecessor").Retired {
		t.Fatal("authorized merge succession left the predecessor live")
	}
}

// One reviewed head is one body of work, and a body of work spans the paths it
// changes. `gs review` cites a single artifact, so reading only that one left a
// head touching four maintained trees able to succeed the pointer in exactly
// one of them: the merge refused itself, and the other three stayed on a
// predecessor nothing would ever supersede. The approval now reaches every path
// where the implementer stood an artifact at the head it approved.
func TestMergeReceiptReachesEveryPathTheApprovalReviewed(t *testing.T) {
	records := reviewRecords(t,
		event(t, "kernel-old", operator, SchemaState, State{Kind: KindArtifact, Text: "older kernel", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "docs-old", operator, SchemaState, State{Kind: KindArtifact, Text: "older docs", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		// The implementer's own candidates, both at the approved head, both
		// standing before the reviewer signs. The verdict can name only one.
		event(t, "docs-candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "docs at the reviewed head", Body: map[string]string{"path": "docs", "commit": "head1"}}, "r0"),
		// The reviewer signs the set: both artifacts are bases of the verdict.
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5", "docs-candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"kernel-old":"spike","docs-old":"docs"}`, "merge_successors": `["spike","docs"]`,
		}}, "approval"),
		event(t, "kernel-new", agent, SchemaState, State{Kind: KindArtifact, Text: "current kernel", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "docs-new", agent, SchemaState, State{Kind: KindArtifact, Text: "current docs", Body: map[string]string{"path": "docs", "commit": "merged"}}, "merge"),
		event(t, "retire-kernel", agent, SchemaSupersede, Supersede{Target: "kernel-old", Text: "merge succession"}, "kernel-old", "merge", "kernel-new"),
		event(t, "retire-docs", agent, SchemaSupersede, Supersede{Target: "docs-old", Text: "merge succession"}, "docs-old", "merge", "docs-new"),
	)
	projection := Fold(records)
	for _, act := range []string{"retire-kernel", "retire-docs"} {
		decision, _ := projection.Decision(act)
		if decision.Verdict != Effective || decision.Reason != "merge approval authorized artifact succession" {
			t.Fatalf("%s = %+v", act, decision)
		}
	}
	for _, predecessor := range []string{"kernel-old", "docs-old"} {
		if !artifactByEvent(t, projection, predecessor).Retired {
			t.Fatalf("%s stayed live", predecessor)
		}
	}
}

// The part that keeps the widening from being a licence, and the case an
// earlier design missed. Ordering the implementer's claims before the verdict
// stops them being minted afterwards and does nothing about seeding: publish a
// candidate at an unrelated path first, then obtain an approval citing only the
// legitimate one, and an inferred set would have handed over that lineage. Only
// what the reviewer cited reaches anything, so this artifact — the
// implementer's own, at the approved head, standing well before the verdict —
// reaches nothing.
func TestMergeReceiptDoesNotReachAnArtifactTheReviewerDidNotCite(t *testing.T) {
	records := reviewRecords(t,
		event(t, "docs-old", operator, SchemaState, State{Kind: KindArtifact, Text: "older docs", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		// Seeded before the review is even requested, and never cited by it.
		event(t, "docs-candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "an uncited claim at docs", Body: map[string]string{"path": "docs", "commit": "head1"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"docs-old":"docs"}`, "merge_successors": `["docs"]`,
		}}, "approval"),
		event(t, "docs-new", agent, SchemaState, State{Kind: KindArtifact, Text: "current docs", Body: map[string]string{"path": "docs", "commit": "merged"}}, "merge"),
		event(t, "retire-docs", agent, SchemaSupersede, Supersede{Target: "docs-old", Text: "not reviewed"}, "docs-old", "merge", "docs-new"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("retire-docs")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("retirement on an uncited path = %+v", decision)
	}
	if artifactByEvent(t, projection, "docs-old").Retired {
		t.Fatal("an uncited path retired another actor's artifact")
	}
}

// The reach is the implementer's own claim, at the head that was approved.
// A third party standing an artifact beside them extends nothing, and neither
// does the implementer's own artifact for some other commit.
func TestMergeReceiptReachIsTheImplementersOwnClaimAtTheApprovedHead(t *testing.T) {
	for _, probe := range []struct {
		name, author, commit string
	}{
		{name: "another actor at the approved head", author: operator, commit: "head1"},
		{name: "the implementer at another head", author: agent, commit: "head2"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			records := reviewRecords(t,
				event(t, "docs-old", operator, SchemaState, State{Kind: KindArtifact, Text: "older docs", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
				event(t, "docs-claim", probe.author, SchemaState, State{Kind: KindArtifact, Text: "a claim at docs", Body: map[string]string{"path": "docs", "commit": probe.commit}}, "r0"),
				// Cited by the reviewer, so only the fold's checks on the member
				// itself — its owner and its commit — can refuse it.
				event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5", "docs-claim"),
				event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
				event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
					"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
					"merge_retirements": `{"docs-old":"docs"}`, "merge_successors": `["docs"]`,
				}}, "approval"),
				event(t, "docs-new", agent, SchemaState, State{Kind: KindArtifact, Text: "current docs", Body: map[string]string{"path": "docs", "commit": "merged"}}, "merge"),
				event(t, "retire-docs", agent, SchemaSupersede, Supersede{Target: "docs-old", Text: "out of reach"}, "docs-old", "merge", "docs-new"),
			)
			decision, _ := Fold(records).Decision("retire-docs")
			if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
				t.Fatalf("retirement = %+v", decision)
			}
		})
	}
}

// The circular case, which is ordinary rather than exotic: the successor rests
// on the very predecessor this merge retires, because that is what the work was
// built on. Nothing may need pre-retiring to break the loop — the successor
// must come out live and current, or `gs merge` would refuse its own result.
func TestMergeSuccessorRestingOnThePredecessorItRetiresStaysCurrent(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "the basis the work was built on", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		// The successor cites the predecessor as its causal basis and the merge
		// as its authority, and the same merge withdraws that predecessor.
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge", "predecessor"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("retire")
	if decision.Verdict != Effective {
		t.Fatalf("circular causal-basis retirement = %+v", decision)
	}
	successor := artifactByEvent(t, projection, "successor")
	if successor.Retired || successor.Stale || successor.DescribesSupersededWorld {
		t.Fatalf("successor resting on its own retired basis: retired=%v stale=%v world=%v",
			successor.Retired, successor.Stale, successor.DescribesSupersededWorld)
	}
	if !artifactByEvent(t, projection, "predecessor").Retired {
		t.Fatal("the predecessor stayed live")
	}
}

// Assigned work commonly reaches the behavior it replaces through an
// intermediate pointer rather than citing the predecessor directly. The
// merge's successor still owns the retirement news in that shape: the
// intermediate flares, while the successor published by the merge stays
// current because every cause below the intermediate is named in its plan.
func TestMergeSuccessorThroughIntermediateProvenanceStaysCurrent(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "the basis the work was built on", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "intermediate", agent, SchemaState, State{Kind: KindArtifact, Text: "a pointer standing on the replaced behavior", Body: map[string]string{"path": "notes/design.md", "commit": "head1"}}, "predecessor"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		// The successor never cites the predecessor directly. Its only route to
		// that retired artifact is through the now-stale intermediate pointer.
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge", "intermediate"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("retire")
	if decision.Verdict != Effective {
		t.Fatalf("intermediate-provenance retirement = %+v", decision)
	}
	intermediate := artifactByEvent(t, projection, "intermediate")
	if !intermediate.Stale {
		t.Fatal("the intermediate pointer did not inherit the predecessor retirement")
	}
	successor := artifactByEvent(t, projection, "successor")
	if successor.Retired || successor.Stale || successor.DescribesSupersededWorld {
		t.Fatalf("successor resting on intermediate provenance: retired=%v stale=%v world=%v",
			successor.Retired, successor.Stale, successor.DescribesSupersededWorld)
	}
}

// The exception belongs to the merge's own successor and to nobody else. The
// signed plan says which retirement is hidden; it says nothing about who may
// hide it, so citing a receipt and one of its planned predecessors must not buy
// a record silence about a basis it had no part in replacing. Here the borrower
// stands at another commit and at no path the merge published, and it goes
// stale exactly as it would have without the receipt.
func TestMergePlanIsNotLentToARecordTheMergeDidNotPublish(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "the basis being replaced", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge", "predecessor"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
		// Not published by this merge: another commit, another path. It cites
		// the receipt and the retired predecessor and must gain nothing by it.
		event(t, "borrower", agent, SchemaState, State{Kind: KindArtifact, Text: "an unrelated pointer borrowing the receipt", Body: map[string]string{"path": "ui", "commit": "elsewhere"}}, "merge", "predecessor"),
	)
	projection := Fold(records)
	if successor := artifactByEvent(t, projection, "successor"); successor.Stale {
		t.Fatal("the merge's own successor went stale from the predecessor it replaced")
	}
	borrower := artifactByEvent(t, projection, "borrower")
	if !borrower.Stale {
		t.Fatal("a record the merge never published suppressed staleness by citing the receipt")
	}
	if !borrower.DescribesSupersededWorld {
		t.Fatal("the borrower rests on a retired artifact and does not say so")
	}
}

// The transitive arm of the exemption, reached by a merge that deletes a
// maintained path. The direct arm above handles a receipt or successor resting
// straight on a plan-named predecessor; here the retirement reaches the receipt
// only through the approval chain. The plan retires the approved candidate
// itself, `merge_successors` is the empty list `gs merge` writes for a
// deletion, and a no-successor retirement takes no authority from a merge, so
// the implementer withdraws their own artifact. Nothing answers the move: the
// approval that cited the candidate goes stale, and so does any later record
// over the same basis. The receipt must not — every live cause below its stale
// basis is the one retirement its own signed plan names, which is its own act
// and not news to it. Without this arm the fold would flare every
// deletion-style receipt at birth, the merge refusing its own result.
func TestDeletionMergeReceiptOutlivesTheStalenessItsOwnPlanCauses(t *testing.T) {
	records := reviewRecords(t,
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":""}`, "merge_successors": `[]`,
		}}, "approval"),
		// The retirement rests on the target and the receipt and cites no
		// successor, the shape the merge's own retirement act takes for a
		// deleted path.
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "r5", Text: "path deleted by merge"}, "r5", "merge"),
		// A later record over the same basis, holding no plan of its own.
		event(t, "follow-up", operator, SchemaState, State{Kind: KindAssert, Text: "noting the approval"}, "approval"),
	)
	projection := Fold(records)
	// The fold admitting the whole sequence is the point: this construction is
	// available to any real actor, so the arm it exercises is not dead code.
	for _, act := range []string{"approval", "approval-ratified", "merge", "retire", "follow-up"} {
		decision, _ := projection.Decision(act)
		if decision.Verdict != Effective {
			t.Fatalf("%s = %+v", act, decision)
		}
	}
	decision, _ := projection.Decision("retire")
	if decision.Reason != "authorized supersession" {
		t.Fatalf("deletion-style retirement took authority it should not hold: %+v", decision)
	}
	if !artifactByEvent(t, projection, "r5").Retired {
		t.Fatal("the candidate stayed live")
	}
	approval := statementByEvent(t, projection, "approval")
	if !approval.Stale || !approval.DescribesSupersededWorld {
		t.Fatalf("approval with its cited candidate deleted and nothing answering: stale=%v world=%v",
			approval.Stale, approval.DescribesSupersededWorld)
	}
	if followUp := statementByEvent(t, projection, "follow-up"); !followUp.Stale {
		t.Fatal("a planless record over the stale approval did not flare")
	}
	if receipt := statementByEvent(t, projection, "merge"); receipt.Stale {
		t.Fatal("the receipt flared over the retirement its own plan names")
	}
}

func TestMergeReceiptDoesNotAuthorizeAnUnrelatedCrossAuthorRetirement(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "planned predecessor", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "unrelated", operator, SchemaState, State{Kind: KindArtifact, Text: "unrelated", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "successor", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "unrelated", Text: "not in plan"}, "unrelated", "merge", "successor"),
	)
	decision, _ := Fold(records).Decision("retire")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("unrelated retirement = %+v", decision)
	}
}

// The exploit as it was actually run against the previous head. A bare
// participant with no part in the approval and no merge copied a public
// ratified approval into an assert of its own, wrote itself a retirement plan,
// and retired an artifact belonging to the operator. The verdict came back
// effective, "merge approval authorized artifact succession". Because a retired
// artifact makes `gs merge` refuse and takes the documentation gate red, that
// was a one-signature denial of merge available to every participant.
//
// The receipt must now be signed by the actor whose implementation the approval
// covers, so a stranger's copy of it carries no plan at all.
func TestMergeReceiptFromABystanderAuthorizesNothing(t *testing.T) {
	records := reviewRecords(t,
		event(t, "bystander-join", operator, SchemaState, State{Kind: KindRoster, Text: "bystander joins", Body: map[string]string{"actor": bystander, "kind": "agent", "name": "Bystander", "role": "participant"}}, "r0"),
		event(t, "bystander-ratified", operator, SchemaRatify, Ratify{Target: "bystander-join"}, "bystander-join"),
		event(t, "victim", operator, SchemaState, State{Kind: KindArtifact, Text: "the operator's pointer", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		// Everything below is written by the bystander alone.
		event(t, "forged", bystander, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "invented",
			"merge_retirements": `{"victim":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "forged-successor", bystander, SchemaState, State{Kind: KindArtifact, Text: "invented successor", Body: map[string]string{"path": "spike", "commit": "invented"}}, "forged"),
		event(t, "steal", bystander, SchemaSupersede, Supersede{Target: "victim", Text: "minted authority"}, "victim", "forged", "forged-successor"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("steal")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("forged merge receipt supersession = %+v", decision)
	}
	if artifactByEvent(t, projection, "victim").Retired {
		t.Fatal("a bare participant retired another actor's artifact")
	}
}

// The exploit from the other side, and the one the bystander check could not
// reach: the author of the approved implementation is a bare participant too,
// and every remaining field of a receipt is written by that same actor. This
// fold holds no repository, so an invented merge head cannot be opened and no
// diff can be read; nothing here says the merge ever happened. The probe signs
// a receipt for an approval covering `spike`, names an operator-authored
// artifact at the unrelated path `docs`, publishes its own successor there, and
// retires it. What stops it is the reviewer's signed choice of artifact, which
// the implementer cannot write.
func TestMergeReceiptFromTheImplementerReachesNoUnrelatedTree(t *testing.T) {
	records := reviewRecords(t,
		event(t, "victim", operator, SchemaState, State{Kind: KindArtifact, Text: "the operator's pointer, in another tree", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		// r5 stands at spike and is the agent's own work, independently
		// approved. Everything below is the agent writing its own authority.
		event(t, "forged", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "invented",
			"merge_retirements": `{"victim":"docs"}`, "merge_successors": `["docs"]`,
		}}, "approval"),
		event(t, "forged-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "invented successor", Body: map[string]string{"path": "docs", "commit": "invented"}}, "forged"),
		event(t, "steal", agent, SchemaSupersede, Supersede{Target: "victim", Text: "minted authority"}, "victim", "forged", "forged-successor"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("steal")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("implementer-forged merge receipt supersession = %+v", decision)
	}
	if artifactByEvent(t, projection, "victim").Retired {
		t.Fatal("an approval for spike retired another actor's artifact under docs")
	}
}

// The same bound from the other side. The implementer really did merge, and the
// target is inside the approved tree, but a receipt reaches only what its merge
// republished: a target the successor does not cover takes no authority from
// it, whatever the plan claims.
func TestMergeReceiptReachesOnlyWhatItsSuccessorCovers(t *testing.T) {
	records := reviewRecords(t,
		event(t, "victim", operator, SchemaState, State{Kind: KindArtifact, Text: "inside the approved tree, outside the successor", Body: map[string]string{"path": "spike/other.go", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"victim":"spike/sub"}`, "merge_successors": `["spike/sub"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike/sub", "commit": "merged"}}, "merge"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "victim", Text: "not covered by the successor"}, "victim", "merge", "successor"),
	)
	decision, _ := Fold(records).Decision("retire")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("retirement outside the republished tree = %+v", decision)
	}
}

// A deleted path has no successor to bound the claim, so the merge takes no
// cross-author authority over it. The target's own author or a ratifier
// retires it, which is what the free-standing rule already said.
func TestMergeReceiptTakesNoAuthorityOverABareRetirement(t *testing.T) {
	records := reviewRecords(t,
		event(t, "gone", operator, SchemaState, State{Kind: KindArtifact, Text: "a deleted file", Body: map[string]string{"path": "spike/gone.go", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"gone":""}`, "merge_successors": `[]`,
		}}, "approval"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "gone", Text: "the file is gone"}, "gone", "merge"),
	)
	decision, _ := Fold(records).Decision("retire")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("bare cross-author retirement = %+v", decision)
	}
}

func TestMergeSuccessorStaysCurrentWhenTheReviewedPredecessorIsRetired(t *testing.T) {
	records := reviewRecords(t,
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "r5", Text: "the merge replaced the reviewed candidate"}, "r5", "merge", "successor"),
	)
	projection := Fold(records)
	if predecessor := artifactByEvent(t, projection, "r5"); !predecessor.Retired {
		t.Fatal("reviewed predecessor stayed live")
	}
	receipt := statementByEvent(t, projection, "merge")
	if receipt.Stale || receipt.DescribesSupersededWorld {
		t.Fatalf("merge receipt flared from its intended retirement: stale=%v world=%v", receipt.Stale, receipt.DescribesSupersededWorld)
	}
	successor := artifactByEvent(t, projection, "successor")
	if successor.Retired || successor.Stale || successor.DescribesSupersededWorld {
		t.Fatalf("merge successor after predecessor retirement: retired=%v stale=%v world=%v", successor.Retired, successor.Stale, successor.DescribesSupersededWorld)
	}
}

func TestMergeReceiptDoesNotHideLaterApprovalRetirement(t *testing.T) {
	records := reviewRecords(t,
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-candidate", agent, SchemaSupersede, Supersede{Target: "r5", Text: "planned"}, "r5", "merge", "successor"),
		event(t, "retire-approval", other, SchemaSupersede, Supersede{Target: "approval", Text: "review withdrawn later"}, "approval"),
	)
	projection := Fold(records)
	if !statementByEvent(t, projection, "merge").Stale || !artifactByEvent(t, projection, "successor").Stale {
		t.Fatal("later approval retirement was hidden by the merge transition")
	}
}

func TestFreeStandingSupersessionStillRequiresAuthorOrRatifier(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "older implementation", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "no merge authority"}, "predecessor"),
	)
	decision, _ := Fold(records).Decision("retire")
	if decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("free-standing cross-author supersession = %+v", decision)
	}
}

func TestMergeLeftLiveSiblingAccountingIsVerifiedAndStable(t *testing.T) {
	leftLive := `{"sibling":{"class":"sibling","commitment":"sibling-promise"}}`
	records := reviewRecords(t,
		event(t, "sibling-request", operator, SchemaState, State{Kind: KindRequest, Text: "implement sibling", Body: map[string]string{"to": agent, "conditions": "publish sibling"}}, "r0"),
		event(t, "sibling-promise", agent, SchemaState, State{Kind: KindPromise, Text: "will implement sibling"}, "sibling-request"),
		// The normal provenance direction is artifact -> promise: the promise
		// cannot cite an artifact which did not exist when it was signed.
		event(t, "sibling", agent, SchemaState, State{Kind: KindArtifact, Text: "sibling candidate", Body: map[string]string{"path": "spike", "commit": "sibling-head"}}, "sibling-promise"),
		event(t, "loose", operator, SchemaState, State{Kind: KindArtifact, Text: "unaccounted candidate", Body: map[string]string{"path": "spike", "commit": "loose-head"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		leftLiveReceipt(t, "merge", "approval", "head1", "merged", `{"r5":"spike"}`, leftLive),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
		event(t, "retire-sibling", agent, SchemaSupersede, Supersede{Target: "sibling", Text: "lane closed later"}, "sibling"),
		event(t, "retire-loose", operator, SchemaSupersede, Supersede{Target: "loose", Text: "cleaned up later"}, "loose"),
	)
	projection := Fold(records)
	successor := artifactByEvent(t, projection, "successor")
	if successor.LivePredecessors != 1 || !successor.SuccessionUnrecorded {
		t.Fatalf("receipt-time accounting moved after later retirements: %+v", successor)
	}
	if len(successor.MergeLeftLive) != 2 || !successor.MergeLeftLive[0].Verified || successor.MergeLeftLive[0].Artifact != "sibling" || successor.MergeLeftLive[1].Artifact != "loose" || successor.MergeLeftLive[1].Reason != "not classified by receipt" {
		t.Fatalf("sibling testimony = %+v", successor.MergeLeftLive)
	}
	status := string(RenderStatus(projection))
	if !strings.Contains(status, "left live at merge: sibling under #") || !strings.Contains(status, "succession not recorded") {
		t.Fatalf("status omitted accounting or warning:\n%s", status)
	}
}

func TestMergeLeftLiveUsesClosestCoveringSuccessorForNestedArtifacts(t *testing.T) {
	records := reviewRecords(t,
		event(t, "sibling-request", operator, SchemaState, State{Kind: KindRequest, Text: "nested sibling", Body: map[string]string{"to": agent, "conditions": "publish sibling"}}, "r0"),
		event(t, "sibling-promise", agent, SchemaState, State{Kind: KindPromise, Text: "will publish nested sibling"}, "sibling-request"),
		event(t, "sibling", agent, SchemaState, State{Kind: KindArtifact, Text: "nested sibling", Body: map[string]string{"path": "dir/a", "commit": "sibling-head"}}, "sibling-promise"),
		event(t, "abandoned", operator, SchemaState, State{Kind: KindArtifact, Text: "nested abandoned", Body: map[string]string{"path": "dir/b", "commit": "abandoned-head"}}, "r0"),
		event(t, "unverified", operator, SchemaState, State{Kind: KindArtifact, Text: "nested bad testimony", Body: map[string]string{"path": "dir/c", "commit": "unverified-head"}}, "r0"),
		event(t, "missing", operator, SchemaState, State{Kind: KindArtifact, Text: "nested omitted testimony", Body: map[string]string{"path": "dir/d", "commit": "missing-head"}}, "r0"),
		event(t, "dir-candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "reviewed directory", Body: map[string]string{"path": "dir", "commit": "head1"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5", "dir-candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "nested merge", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"spike","dir-candidate":"dir"}`, "merge_successors": `["dir","spike"]`,
			"merge_changed_paths": `["dir/a/file","dir/b/file","dir/c/file","dir/d/file"]`,
			"merge_left_live":     `{"sibling":{"class":"sibling","commitment":"sibling-promise"},"abandoned":{"class":"abandoned"},"unverified":{"class":"sibling","commitment":"missing-commitment"}}`,
		}}, "approval"),
		event(t, "dir-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged directory", Body: map[string]string{"path": "dir", "commit": "merged"}}, "merge"),
	)
	projection := Fold(records)
	successor := artifactByEvent(t, projection, "dir-successor")
	if successor.LivePredecessors != 2 || !successor.SuccessionUnrecorded {
		t.Fatalf("nested missing and unverified claims did not preserve debt: %+v", successor)
	}
	if len(successor.MergeLeftLive) != 4 {
		t.Fatalf("nested accounting attached %d entries, want 4: %+v", len(successor.MergeLeftLive), successor.MergeLeftLive)
	}
	verified := map[string]bool{}
	for _, entry := range successor.MergeLeftLive {
		verified[entry.Artifact] = entry.Verified
	}
	if !verified["sibling"] || !verified["abandoned"] || verified["unverified"] {
		t.Fatalf("nested verification = %+v", verified)
	}
	status := string(RenderStatus(projection))
	if count := strings.Count(status, "left live at merge: sibling under"); count != 1 {
		t.Fatalf("verified sibling rendered %d times, want exactly once:\n%s", count, status)
	}
	for _, id := range []string{"sibling", "abandoned", "unverified"} {
		if !strings.Contains(status, name(id, projection.sequences())) {
			t.Fatalf("status omitted nested artifact %s:\n%s", id, status)
		}
	}
}

func TestMergeLeftLiveClosestSuccessorIsOrderIndependent(t *testing.T) {
	for _, paths := range [][]string{{"dir", "dir/sub"}, {"dir/sub", "dir"}} {
		if got := closestCoveringPath(paths, "dir/sub/file"); got != "dir/sub" {
			t.Fatalf("closest successor for %#v = %q, want dir/sub", paths, got)
		}
	}
	if got := closestCoveringPath([]string{"dir/sub/file"}, "dir/sub"); got != "" {
		t.Fatalf("narrow successor covered its parent predecessor as %q", got)
	}
}

func TestMergeChangedPathsBoundsClaimsAndPostFrontierAccounting(t *testing.T) {
	records := reviewRecords(t,
		event(t, "wide", operator, SchemaState, State{Kind: KindArtifact, Text: "wide changed scope", Body: map[string]string{"path": "dir", "commit": "wide-head"}}, "r0"),
		event(t, "nested", operator, SchemaState, State{Kind: KindArtifact, Text: "nested changed scope", Body: map[string]string{"path": "dir/a/nested", "commit": "nested-head"}}, "r0"),
		event(t, "sibling-tree", operator, SchemaState, State{Kind: KindArtifact, Text: "unrelated sibling tree", Body: map[string]string{"path": "dir/b", "commit": "sibling-tree-head"}}, "r0"),
		event(t, "dir-candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "reviewed directory", Body: map[string]string{"path": "dir", "commit": "head1"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "dir-candidate"}}, "reviewer-promise", "dir-candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "directory merge", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"dir-candidate":"dir"}`, "merge_successors": `["dir"]`,
			"merge_changed_paths": `["dir/a/nested/file.go"]`,
			"merge_left_live":     `{"nested":{"class":"abandoned"},"sibling-tree":{"class":"abandoned"},"wide":{"class":"abandoned"}}`,
		}}, "approval"),
		event(t, "post-outside", operator, SchemaState, State{Kind: KindArtifact, Text: "post-frontier unrelated sibling", Body: map[string]string{"path": "dir/b", "commit": "post-outside"}}, "r0"),
		event(t, "post-covered", operator, SchemaState, State{Kind: KindArtifact, Text: "post-frontier changed scope", Body: map[string]string{"path": "dir/a/nested", "commit": "post-covered"}}, "r0"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged directory", Body: map[string]string{"path": "dir", "commit": "merged"}}, "merge"),
		event(t, "retire-candidate", agent, SchemaSupersede, Supersede{Target: "dir-candidate", Text: "merged"}, "dir-candidate", "merge", "successor"),
		event(t, "retire-sibling-tree", operator, SchemaSupersede, Supersede{Target: "sibling-tree", Text: "unrelated lane closed"}, "sibling-tree"),
	)
	projection := Fold(records)
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	got := make(map[string]LeftLiveAccounting, len(accounting))
	for _, entry := range accounting {
		got[entry.Artifact] = entry
	}
	if !got["wide"].Verified || !got["nested"].Verified {
		t.Fatalf("covering artifact testimony was refused: %+v", got)
	}
	if got["sibling-tree"].Verified || !strings.Contains(got["sibling-tree"].Reason, "does not cover") {
		t.Fatalf("unrelated dir/b testimony = %+v", got["sibling-tree"])
	}
	successor := artifactByEvent(t, projection, "successor")
	if successor.LivePredecessors != 1 {
		t.Fatalf("post-frontier changed-path count = %d, want 1 (dir/a only; dir/b ignored)", successor.LivePredecessors)
	}
	// wide and nested are live abandoned claims. post-covered is a third,
	// distinct debt: it landed after the receipt and is nested beneath the
	// broader dir successor, so exact-path succession alone cannot find it.
	if projection.OmittedSupersessions != 3 {
		t.Fatalf("owed supersessions = %d, want 3 including post-covered", projection.OmittedSupersessions)
	}
}

func TestMergeChangedPathsProtectsNestedSiblingsUntilNamedCommitmentEnds(t *testing.T) {
	records := reviewRecords(t,
		event(t, "request-one", operator, SchemaState, State{Kind: KindRequest, Text: "first sibling", Body: map[string]string{"to": agent, "conditions": "publish first"}}, "r0"),
		event(t, "promise-one", agent, SchemaState, State{Kind: KindPromise, Text: "first sibling"}, "request-one"),
		event(t, "sibling-one", agent, SchemaState, State{Kind: KindArtifact, Text: "first protected sibling", Body: map[string]string{"path": "dir/a", "commit": "one"}}, "promise-one"),
		event(t, "request-two", operator, SchemaState, State{Kind: KindRequest, Text: "second sibling", Body: map[string]string{"to": agent, "conditions": "publish second"}}, "r0"),
		event(t, "promise-two", agent, SchemaState, State{Kind: KindPromise, Text: "second sibling"}, "request-two"),
		event(t, "sibling-two", agent, SchemaState, State{Kind: KindArtifact, Text: "second protected sibling", Body: map[string]string{"path": "dir/a", "commit": "two"}}, "promise-two"),
		event(t, "dir-candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "reviewed exact candidate", Body: map[string]string{"path": "dir/a", "commit": "head1"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "dir-candidate"}}, "reviewer-promise", "dir-candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "directory merge", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"dir-candidate":"dir"}`, "merge_successors": `["dir"]`,
			"merge_changed_paths": `["dir/a/file.go"]`,
			"merge_left_live":     `{"sibling-one":{"class":"sibling","commitment":"promise-one"},"sibling-two":{"class":"sibling","commitment":"promise-two"}}`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged directory", Body: map[string]string{"path": "dir", "commit": "merged"}}, "merge"),
		event(t, "retire-candidate", agent, SchemaSupersede, Supersede{Target: "dir-candidate", Text: "merged"}, "dir-candidate", "merge", "successor"),
		event(t, "retire-promise-one", agent, SchemaSupersede, Supersede{Target: "promise-one", Text: "first lane ended"}, "promise-one"),
	)
	beforeSettlement := Fold(records[:len(records)-1])
	if beforeSettlement.OmittedSupersessions != 0 || artifactByEvent(t, beforeSettlement, "successor").LivePredecessors != 0 {
		t.Fatalf("two protected nested siblings owed debt before settlement: omitted=%d successor=%+v", beforeSettlement.OmittedSupersessions, artifactByEvent(t, beforeSettlement, "successor"))
	}
	afterSettlement := Fold(records)
	if afterSettlement.OmittedSupersessions != 1 {
		t.Fatalf("ended named protection left debt %d, want 1", afterSettlement.OmittedSupersessions)
	}
	if successor := artifactByEvent(t, afterSettlement, "successor"); successor.LivePredecessors != 0 || successor.SuccessionUnrecorded {
		t.Fatalf("historical receipt accounting was rewritten by settlement: %+v", successor)
	}
}

func TestMergeChangedPathsRequiresCanonicalProspectiveField(t *testing.T) {
	valid := []string{`[]`, `["dir/a","dir/b"]`}
	for _, raw := range valid {
		if _, ok := parseMergeChangedPaths(raw, true); !ok {
			t.Errorf("canonical changed paths refused: %s", raw)
		}
	}
	invalid := []string{"", `null`, ` ["dir/a"]`, `["dir/b","dir/a"]`, `["dir/a","dir/a"]`, `[""]`}
	for _, raw := range invalid {
		if _, ok := parseMergeChangedPaths(raw, raw != ""); ok {
			t.Errorf("noncanonical changed paths admitted: %q", raw)
		}
	}
}

func TestMergeAccountingOneFieldOnlyFailsClosed(t *testing.T) {
	tests := []struct {
		name, left, changed, reason string
		leftPresent, changedPresent bool
	}{
		{name: "changed paths only", changed: `["spike"]`, changedPresent: true, reason: "merge_left_live is absent"},
		{name: "left live only", left: `{}`, leftPresent: true, reason: "merge_changed_paths is absent or invalid"},
		{name: "empty left with null frontier", left: `{}`, leftPresent: true, changed: `null`, changedPresent: true, reason: "merge_changed_paths is absent or invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]string{
				"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
				"merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
			}
			if test.leftPresent {
				body["merge_left_live"] = test.left
			}
			if test.changedPresent {
				body["merge_changed_paths"] = test.changed
			}
			projection := Fold(reviewRecords(t,
				event(t, "loose", operator, SchemaState, State{Kind: KindArtifact, Text: "unclassified", Body: map[string]string{"path": "spike", "commit": "loose"}}, "r0"),
				event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
				event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
				event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "prospective seam", Body: body}, "approval"),
				event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
				event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
			))
			accounting := statementByEvent(t, projection, "merge").MergeLeftLive
			found := false
			for _, entry := range accounting {
				found = found || strings.Contains(entry.Reason, test.reason)
			}
			if !found {
				t.Fatalf("prospective seam testimony = %+v, want %q", accounting, test.reason)
			}
			if successor := artifactByEvent(t, projection, "successor"); successor.LivePredecessors == 0 || !successor.SuccessionUnrecorded {
				t.Fatalf("incomplete prospective receipt cleared debt: %+v", successor)
			}
			if decision, _ := projection.Decision("retire-r5"); decision.Verdict != Effective {
				t.Fatalf("accounting seam changed retirement authority: %+v", decision)
			}
		})
	}
}

func TestMergeAccountingDeletionOmissionStaysVisibleOnReceipt(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "gone-left", operator, SchemaState, State{Kind: KindArtifact, Text: "unclassified deletion candidate", Body: map[string]string{"path": "gone", "commit": "other"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "merge with unclassified deletion", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
			"merge_changed_paths": `["gone/file","spike"]`, "merge_left_live": `{}`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
	))
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "gone-left" || accounting[0].Reason != "not classified by receipt" {
		t.Fatalf("deletion omission testimony = %+v", accounting)
	}
	status := string(RenderStatus(projection))
	if !strings.Contains(status, name("gone-left", projection.sequences())) || !strings.Contains(status, "not classified by receipt") {
		t.Fatalf("deletion omission was not rendered:\n%s", status)
	}
	if decision, _ := projection.Decision("retire-r5"); decision.Verdict != Effective {
		t.Fatalf("deletion omission changed retirement authority: %+v", decision)
	}
}

func TestMergeLeftLiveProtectedSiblingIsNotRetirementDebt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "sibling-request", operator, SchemaState, State{Kind: KindRequest, Text: "keep sibling live", Body: map[string]string{"to": agent, "conditions": "publish sibling"}}, "r0"),
		event(t, "sibling-promise", agent, SchemaState, State{Kind: KindPromise, Text: "will publish sibling"}, "sibling-request"),
		event(t, "sibling", agent, SchemaState, State{Kind: KindArtifact, Text: "protected sibling", Body: map[string]string{"path": "spike", "commit": "sibling-head"}}, "sibling-promise"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		leftLiveReceipt(t, "merge", "approval", "head1", "merged", `{"r5":"spike"}`, `{"sibling":{"class":"sibling","commitment":"sibling-promise"}}`),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
	)
	projection := Fold(records)
	successor := artifactByEvent(t, projection, "successor")
	if successor.SuccessionUnrecorded || successor.LivePredecessors != 0 {
		t.Fatalf("protected sibling became an unrecorded succession: %+v", successor)
	}
	if projection.OmittedSupersessions != 0 {
		t.Fatalf("protected sibling counted as retirement debt: %d", projection.OmittedSupersessions)
	}
	if strings.Contains(string(RenderStatus(projection)), "supersessions still owed") {
		t.Fatal("status says the protected sibling must be retired")
	}
}

func TestMergeLeftLiveCarriedWiderPointerIsCurrentWithoutRetirementDebt(t *testing.T) {
	records := reviewRecords(t)
	records[5] = event(t, "r5", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "docs/how-to/page.md", "commit": "head1"}}, "r0")
	records = append(records,
		event(t, "carried", operator, SchemaState, State{Kind: KindArtifact, Text: "current directory pointer", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"docs/how-to/page.md"}`, "merge_successors": `["docs/how-to/page.md"]`,
			"merge_changed_paths": `["docs/how-to/page.md"]`, "merge_left_live": `{"carried":{"class":"carried"}}`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "docs/how-to/page.md", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
	)
	projection := Fold(records)
	successor := artifactByEvent(t, projection, "successor")
	if successor.SuccessionUnrecorded || successor.LivePredecessors != 0 {
		t.Fatalf("carried pointer left the landed path unaccounted: %+v", successor)
	}
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 {
		t.Fatalf("carried receipt accounting = %+v", accounting)
	}
	entry := accounting[0]
	if !entry.Verified || entry.Class != "carried" || entry.Commitment != "" {
		t.Fatalf("carried accounting = %+v", entry)
	}
	if projection.OmittedSupersessions != 0 {
		t.Fatalf("carried pointer became retirement debt: %d", projection.OmittedSupersessions)
	}
	status := string(RenderStatus(projection))
	if !strings.Contains(status, "carried current artifact "+name("carried", projection.sequences())) || strings.Contains(status, "retirement owed by") {
		t.Fatalf("carried pointer rendered as cleanup work:\n%s", status)
	}
}

func TestMergeLeftLiveWrongTestimonyWarnsWithoutChangingReceiptAuthority(t *testing.T) {
	tests := []struct {
		name       string
		before     []Record
		testimony  string
		wantReason string
	}{
		{
			name: "settled commitment",
			before: []Record{
				event(t, "candidate-request", operator, SchemaState, State{Kind: KindRequest, Text: "candidate", Body: map[string]string{"to": agent, "conditions": "candidate"}}, "r0"),
				event(t, "candidate-promise", agent, SchemaState, State{Kind: KindPromise, Text: "candidate"}, "candidate-request"),
				event(t, "left", agent, SchemaState, State{Kind: KindArtifact, Text: "left candidate", Body: map[string]string{"path": "spike", "commit": "left-head"}}, "candidate-promise"),
				event(t, "settled-report", agent, SchemaState, State{Kind: KindReport, Text: "done", Body: map[string]string{"commit": "left-head"}}, "candidate-promise", "left"),
				event(t, "settled-ratified", operator, SchemaRatify, Ratify{Target: "settled-report"}, "settled-report"),
			},
			testimony:  `{"left":{"class":"sibling","commitment":"settled-report"}}`,
			wantReason: "settled",
		},
		{
			name:       "dangling artifact and commitment",
			testimony:  `{"missing":{"class":"sibling","commitment":"also-missing"}}`,
			wantReason: "unresolved",
		},
		{
			name:       "retirement overlap",
			testimony:  `{"r5":{"class":"abandoned"}}`,
			wantReason: "also classified for retirement",
		},
		{
			name: "carried names commitment",
			before: []Record{
				event(t, "left", operator, SchemaState, State{Kind: KindArtifact, Text: "claimed carried pointer", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
			},
			testimony:  `{"left":{"class":"carried","commitment":"promise"}}`,
			wantReason: "carried testimony must not name a commitment",
		},
		{
			name: "carried exact path",
			before: []Record{
				event(t, "left", operator, SchemaState, State{Kind: KindArtifact, Text: "claimed carried pointer", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
			},
			testimony:  `{"left":{"class":"carried"}}`,
			wantReason: "not wider",
		},
		{
			name: "outside successor coverage",
			before: []Record{
				event(t, "elsewhere", operator, SchemaState, State{Kind: KindArtifact, Text: "unrelated candidate", Body: map[string]string{"path": "elsewhere", "commit": "elsewhere-head"}}, "r0"),
			},
			testimony:  `{"elsewhere":{"class":"abandoned"}}`,
			wantReason: "does not cover a declared changed path",
		},
		{
			name: "ineffective provenance does not protect",
			before: []Record{
				event(t, "candidate-request", operator, SchemaState, State{Kind: KindRequest, Text: "candidate", Body: map[string]string{"to": agent, "conditions": "candidate"}}, "r0"),
				event(t, "candidate-promise", agent, SchemaState, State{Kind: KindPromise, Text: "candidate"}, "candidate-request"),
				// Missing conditions makes this request ineffective. Traversal
				// must stop here rather than borrowing the promise behind it.
				event(t, "ineffective-middle", agent, SchemaState, State{Kind: KindRequest, Text: "not admitted", Body: map[string]string{"to": agent}}, "candidate-promise"),
				event(t, "left", agent, SchemaState, State{Kind: KindArtifact, Text: "left candidate", Body: map[string]string{"path": "spike", "commit": "left-head"}}, "ineffective-middle"),
			},
			testimony:  `{"left":{"class":"sibling","commitment":"candidate-promise"}}`,
			wantReason: "does not name or reach",
		},
		{
			name: "malformed present field",
			before: []Record{
				event(t, "left", operator, SchemaState, State{Kind: KindArtifact, Text: "left candidate", Body: map[string]string{"path": "spike", "commit": "left-head"}}, "r0"),
			},
			testimony:  `{not-json`,
			wantReason: "not valid JSON",
		},
		{
			name: "null present field",
			before: []Record{
				event(t, "left", operator, SchemaState, State{Kind: KindArtifact, Text: "left candidate", Body: map[string]string{"path": "spike", "commit": "left-head"}}, "r0"),
			},
			testimony:  `null`,
			wantReason: "not valid JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tail := append([]Record(nil), test.before...)
			tail = append(tail,
				event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
				event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
				leftLiveReceipt(t, "merge", "approval", "head1", "merged", `{"r5":"spike"}`, test.testimony),
				event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
				event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
			)
			projection := Fold(reviewRecords(t, tail...))
			decision, _ := projection.Decision("retire-r5")
			if decision.Verdict != Effective {
				t.Fatalf("testimony changed receipt retirement authority: %+v", decision)
			}
			accounting := statementByEvent(t, projection, "merge").MergeLeftLive
			foundReason := false
			for _, entry := range accounting {
				foundReason = foundReason || (!entry.Verified && strings.Contains(entry.Reason, test.wantReason))
			}
			if !foundReason {
				t.Fatalf("unverified testimony = %+v", accounting)
			}
			if !strings.Contains(string(RenderStatus(projection)), "UNVERIFIED left-live testimony") {
				t.Fatal("status hid unverified testimony")
			}
		})
	}
}

func TestMergeLeftLiveAbandonedRequiresExistingBareSupersessionAuthority(t *testing.T) {
	records := reviewRecords(t,
		event(t, "abandoned", other, SchemaState, State{Kind: KindArtifact, Text: "abandoned candidate", Body: map[string]string{"path": "spike", "commit": "abandoned-head"}}, "r0"),
		event(t, "abandoned-two", operator, SchemaState, State{Kind: KindArtifact, Text: "second abandoned candidate", Body: map[string]string{"path": "spike", "commit": "abandoned-head-two"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		leftLiveReceipt(t, "merge", "approval", "head1", "merged", `{"r5":"spike"}`, `{"abandoned":{"class":"abandoned"},"abandoned-two":{"class":"abandoned"}}`),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
		event(t, "stranger-retire", agent, SchemaSupersede, Supersede{Target: "abandoned", Text: "not mine"}, "abandoned"),
		event(t, "author-retire", other, SchemaSupersede, Supersede{Target: "abandoned", Text: "my abandoned candidate"}, "abandoned"),
	)
	projection := Fold(records)
	successor := artifactByEvent(t, projection, "successor")
	if successor.SuccessionUnrecorded || len(successor.MergeLeftLive) != 2 || !successor.MergeLeftLive[0].Verified || !successor.MergeLeftLive[1].Verified {
		t.Fatalf("abandoned accounting = %+v", successor)
	}
	stranger, _ := projection.Decision("stranger-retire")
	author, _ := projection.Decision("author-retire")
	if stranger.Verdict != Ineffective || author.Verdict != Effective {
		t.Fatalf("abandoned authority changed: stranger=%+v author=%+v", stranger, author)
	}
	if projection.OmittedSupersessions != 1 {
		t.Fatalf("live abandoned candidate debt = %d, want 1", projection.OmittedSupersessions)
	}
	status := string(RenderStatus(projection))
	for _, id := range []string{"abandoned", "abandoned-two"} {
		if !strings.Contains(status, "abandoned artifact "+name(id, projection.sequences())) || !strings.Contains(status, "retirement owed by") {
			t.Fatalf("status omitted distinguishable abandoned duty for %s:\n%s", id, status)
		}
	}
}

func TestMergeLeftLiveDeletedPathRemainsVisibleOnReceipt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "gone-old", operator, SchemaState, State{Kind: KindArtifact, Text: "deleted predecessor", Body: map[string]string{"path": "gone", "commit": "base"}}, "r0"),
		event(t, "gone-left", operator, SchemaState, State{Kind: KindArtifact, Text: "abandoned deleted-path candidate", Body: map[string]string{"path": "gone", "commit": "other-head"}}, "r0"),
		event(t, "gone-candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "reviewed deletion", Body: map[string]string{"path": "gone", "commit": "head1"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5", "gone-candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "merge with deletion", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"spike","gone-candidate":"","gone-old":""}`, "merge_successors": `["spike"]`,
			"merge_changed_paths": `["gone/file"]`,
			"merge_left_live":     `{"gone-left":{"class":"abandoned"}}`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
	)
	projection := Fold(records)
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || !accounting[0].Verified || accounting[0].Artifact != "gone-left" {
		t.Fatalf("deleted-path accounting = %+v", accounting)
	}
	if got := artifactByEvent(t, projection, "successor").MergeLeftLive; len(got) != 0 {
		t.Fatalf("deleted-path prompt attached to unrelated successor: %+v", got)
	}
	status := string(RenderStatus(projection))
	if !strings.Contains(status, "abandoned artifact "+name("gone-left", projection.sequences())) || !strings.Contains(status, "retirement owed by") {
		t.Fatalf("deleted-path prompt disappeared:\n%s", status)
	}
}

func TestMergeLeftLivePostFrontierRemainsUnaccountedUntilNextReceipt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		leftLiveReceipt(t, "merge", "approval", "head1", "merged", `{"r5":"spike"}`, `{}`),
		event(t, "race", operator, SchemaState, State{Kind: KindArtifact, Text: "published after receipt frontier", Body: map[string]string{"path": "spike", "commit": "race-head"}}, "r0"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "first merge"}, "r5", "merge", "successor"),
		event(t, "review2-request", operator, SchemaState, State{Kind: KindRequest, Text: "review next merge", Body: map[string]string{"to": other, "conditions": "exact head"}}, "successor"),
		event(t, "review2-promise", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "review2-request"),
		event(t, "approval2", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "merged", "artifact": "successor"}}, "review2-promise", "successor"),
		event(t, "approval2-ratified", operator, SchemaRatify, Ratify{Target: "approval2"}, "approval2"),
		leftLiveReceipt(t, "merge2", "approval2", "merged", "merged2", `{"successor":"spike"}`, `{"race":{"class":"abandoned"}}`),
		event(t, "successor2", agent, SchemaState, State{Kind: KindArtifact, Text: "next merged implementation", Body: map[string]string{"path": "spike", "commit": "merged2"}}, "merge2"),
	)
	projection := Fold(records)
	first := artifactByEvent(t, projection, "successor")
	if first.LivePredecessors != 1 || !first.SuccessionUnrecorded {
		t.Fatalf("post-frontier artifact was not retained as unaccounted: %+v", first)
	}
	second := artifactByEvent(t, projection, "successor2")
	if second.LivePredecessors != 0 || second.SuccessionUnrecorded || len(second.MergeLeftLive) != 1 || !second.MergeLeftLive[0].Verified {
		t.Fatalf("next receipt did not account for the race: %+v", second)
	}
}

func TestReceiptWithoutMergeLeftLiveKeepsFoldTimeSuccessionBehavior(t *testing.T) {
	records := reviewRecords(t,
		event(t, "left", operator, SchemaState, State{Kind: KindArtifact, Text: "temporarily live", Body: map[string]string{"path": "spike", "commit": "left-head"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "historical receipt", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-left", operator, SchemaSupersede, Supersede{Target: "left", Text: "later cleanup"}, "left"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
	)
	projection := Fold(records)
	successor := artifactByEvent(t, projection, "successor")
	if successor.LivePredecessors != 0 || successor.SuccessionUnrecorded || successor.MergeLeftLive != nil {
		t.Fatalf("historical receipt was reinterpreted: %+v", successor)
	}
	if statementByEvent(t, projection, "merge").MergeLeftLive != nil {
		t.Fatal("absent historical field projected prospective testimony")
	}
}

func leftLiveReceipt(t *testing.T, id, approval, candidate, merged, retirements, leftLive string) Record {
	t.Helper()
	return event(t, id, agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
		"merge_approval": approval, "merge_candidate": candidate, "merge_target_pre_head": "base", "merge_head": merged,
		"merge_retirements": retirements, "merge_successors": `["spike"]`, "merge_changed_paths": `["spike"]`, "merge_left_live": leftLive,
	}}, approval)
}

// An artifact with no basis can never go stale, so its silence must not read
// as currency.
func TestUnbridgedArtifactIsMarkedUnableToFlare(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "Unbridged work", Body: map[string]string{"path": "spike", "commit": "aaa111"}}),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "Bridged work", Body: map[string]string{"path": "ui", "commit": "bbb222"}}, "w0"),
	)
	projection := Fold(records)
	if !artifactByEvent(t, projection, "w3").UnableToFlare {
		t.Fatal("an artifact citing nothing was not marked unable to flare")
	}
	if artifactByEvent(t, projection, "w4").UnableToFlare {
		t.Fatal("an artifact citing a basis was marked unable to flare")
	}
}

// The marks are for humans, so they must reach the human page and not only
// the JSON.
func TestStatusPageCarriesTheArtifactMarks(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI reference page", Body: map[string]string{"path": "docs/reference/gs.md", "commit": "bbb222"}}, "w3"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "Unbridged work", Body: map[string]string{"path": "ui", "commit": "ccc333"}}),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation again", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ddd444"}}, "w0"),
		event(t, "w7", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ddd444"}, "w3", "w6"),
	)
	page := string(RenderStatus(Fold(records)))
	for _, want := range []string{
		"STALE — describes a superseded world",
		"unable to flare",
		"cite no basis and can never go stale",
	} {
		if !bytes.Contains([]byte(page), []byte(want)) {
			t.Fatalf("status page omits %q\n%s", want, page)
		}
	}
}

// One unrecorded succession at a long-lived path repeats on every later link
// of that chain, so the page must report situations as well as rows. A mark
// that trains readers to scroll past it is worse than no mark.
func TestStatusPageCountsSuccessionPathsAsWellAsArtifacts(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "spike", "commit": "a3"}}, "w0"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "four", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
		event(t, "w7", agent, SchemaState, State{Kind: KindArtifact, Text: "five", Body: map[string]string{"path": "ui", "commit": "b2"}}, "w0"),
	)
	projection := Fold(records)
	// Three supersessions are owed: a1 and a2 on the spike path, b1 on ui.
	// Not two, which is the path count.
	if projection.OmittedSupersessions != 3 {
		t.Fatalf("owed supersessions = %d, want 3", projection.OmittedSupersessions)
	}
	page := RenderStatus(projection)
	want := "3 artifacts across 2 paths follow a live artifact covering the merge without superseding or accounting for it; supersessions still owed: 3"
	if !bytes.Contains(page, []byte(want)) {
		t.Fatalf("page does not separate owed acts from rows and paths, want %q\n%s", want, page)
	}
}

// A predecessor owes its retirement only while something live stands in its
// place. Withdraw the successor and the predecessor is the current artifact
// for its path again, so following an owed-supersession warning there would
// retire the current artifact for a replacement that no longer exists.
func TestWithdrawnSuccessorLeavesNothingOwed(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", agent, SchemaSupersede, Supersede{Target: "w4", Text: "withdrawn; a1 stands again"}, "w4"),
		// Positive control: a live successor on another path still owes one.
		// Without it this passes for an implementation that counts nothing.
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
		event(t, "w7", agent, SchemaState, State{Kind: KindArtifact, Text: "four", Body: map[string]string{"path": "ui", "commit": "b2"}}, "w0"),
	)
	projection := Fold(records)
	withdrawn := artifactByEvent(t, projection, "w4")
	if !withdrawn.Retired {
		t.Fatal("a2 is not retired; the supersession was ineffective and the case is untested")
	}
	if projection.OmittedSupersessions != 1 {
		t.Fatalf("owed supersessions = %d, want 1 (b1 only; a1's only successor was withdrawn)", projection.OmittedSupersessions)
	}
	// The row stays as history — a2 really did follow a live a1 — while the
	// owed count says there is nothing left to do about it.
	if !withdrawn.SuccessionUnrecorded {
		t.Fatal("a2 stopped recording that it followed a live a1")
	}
}

// Rows and owed acts agree in the fixture above, so it cannot show the
// aggregate is the right quantity. A fourth artifact on one path makes them
// differ: four rows notice a live ancestor while four supersessions are owed
// and a naive sum of per-row figures would give seven.
func TestOwedSupersessionsDivergeFromRowCount(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "spike", "commit": "a3"}}, "w0"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "four", Body: map[string]string{"path": "spike", "commit": "a4"}}, "w0"),
		event(t, "w7", agent, SchemaState, State{Kind: KindArtifact, Text: "five", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
		event(t, "w8", agent, SchemaState, State{Kind: KindArtifact, Text: "six", Body: map[string]string{"path": "ui", "commit": "b2"}}, "w0"),
	)
	projection := Fold(records)
	if projection.OmittedSupersessions != 4 {
		t.Fatalf("owed supersessions = %d, want 4 (a1, a2, a3, b1)", projection.OmittedSupersessions)
	}
	sum := 0
	for _, artifact := range projection.Artifacts {
		sum += artifact.LivePredecessors
		if artifact.Commit == "a4" && artifact.LivePredecessors != 3 {
			t.Fatalf("a4 live predecessors = %d, want 3", artifact.LivePredecessors)
		}
	}
	if sum == projection.OmittedSupersessions {
		t.Fatalf("per-row sum %d equals the owed count; the fixture cannot tell them apart", sum)
	}
}

// Retiring the immediate predecessor must not clear the warning while an
// earlier artifact at the same path is still live.
func TestRetiringOnePredecessorDoesNotHideAnEarlierLiveOne(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", operator, SchemaSupersede, Supersede{Target: "w4", Text: "retire the middle one"}, "w4"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "spike", "commit": "a3"}}, "w0"),
	)
	projection := Fold(records)
	seen := false
	for _, artifact := range projection.Artifacts {
		if artifact.Commit != "a3" {
			continue
		}
		seen = true
		if !artifact.SuccessionUnrecorded {
			t.Fatal("a3 reports a recorded succession while a1 is still live")
		}
		if artifact.LivePredecessors != 1 {
			t.Fatalf("a3 live predecessors = %d, want 1 (a1 only; a2 retired)", artifact.LivePredecessors)
		}
	}
	if !seen {
		t.Fatal("a3 missing from the projection")
	}
}

// A statement citing only events the log does not contain is as unable to
// flare as one citing nothing: supersede needs a resolvable target.
func TestArtifactCitingOnlyUnresolvableBasesCannotFlare(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "dangling", Body: map[string]string{"path": "spike", "commit": "a1"}}, "does-not-exist"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "anchored", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
	)
	projection := Fold(records)
	checked := 0
	for _, artifact := range projection.Artifacts {
		switch artifact.Commit {
		case "a1":
			checked++
			if !artifact.UnableToFlare {
				t.Fatal("artifact citing only an unresolvable basis reads as able to flare")
			}
		case "b1":
			// Positive control: one resolvable basis is a handle a future
			// supersession can take. Without this the assertion above passes
			// for an implementation marking every artifact unable to flare.
			checked++
			if artifact.UnableToFlare {
				t.Fatal("artifact with a resolvable basis marked unable to flare")
			}
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d artifacts, want 2", checked)
	}
}

// reviewRecords builds the shape the loop produces: an operator seeds the
// roster and admits two agents, one files an artifact for a head, and each
// holds a live promise it can report a verdict against.
func reviewRecords(t *testing.T, tail ...Record) []Record {
	t.Helper()
	records := []Record{
		event(t, "r0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "r1", operator, SchemaState, State{Kind: KindRoster, Text: "implementer joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Implementer", "role": "participant"}}, "r0"),
		event(t, "r2", operator, SchemaRatify, Ratify{Target: "r1"}, "r1"),
		event(t, "r3", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "r0"),
		event(t, "r4", operator, SchemaRatify, Ratify{Target: "r3"}, "r3"),
		event(t, "r5", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "spike", "commit": "head1"}}, "r0"),
		event(t, "r6", operator, SchemaState, State{Kind: KindRequest, Text: "review it", Body: map[string]string{"to": other, "conditions": "exact head"}}, "r5"),
		event(t, "reviewer-promise", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "r6"),
		event(t, "r7", operator, SchemaState, State{Kind: KindRequest, Text: "review your own", Body: map[string]string{"to": agent, "conditions": "exact head"}}, "r5"),
		event(t, "implementer-promise", agent, SchemaState, State{Kind: KindPromise, Text: "will review"}, "r7"),
	}
	return append(records, tail...)
}

func reviewFor(t *testing.T, projection Projection, report string) Review {
	t.Helper()
	review, found := projection.Review(report)
	if !found {
		t.Fatalf("report %s is not projected as a review", report)
	}
	return review
}

// The named artifact is the ordinary path: gs review writes body.artifact.
func TestReviewNamingArtifactReportsIndependenceByFingerprint(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "v2", agent, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "implementer-promise", "r5"),
	))
	independent := reviewFor(t, projection, "v1")
	if independent.Independence != IndependenceIndependent || independent.Implementer != agent || independent.ResolvedBy != "named" {
		t.Fatalf("independent review = %+v", independent)
	}
	self := reviewFor(t, projection, "v2")
	if self.Independence != IndependenceSelfReview || self.Reviewer != agent {
		t.Fatalf("self-signed review = %+v", self)
	}
	status := RenderStatus(projection)
	if !bytes.Contains(status, []byte("SELF-SIGNED")) {
		t.Fatal("status does not surface a self-signed verdict")
	}
	if !bytes.Contains(status, []byte("1 independent, 1 self-signed, 0 unresolved")) {
		t.Fatalf("status review counts missing:\n%s", status)
	}
}

// Naming an artifact is a claim, not a link. The projection is what a reader
// and `gs merge` both trust to say whose work a verdict judged, so it takes a
// name only when the record can vouch for it: the report has to rest on that
// artifact, and that artifact has to stand at the head the verdict claims.
// Trusting the name alone let a reviewer claim one head, name someone else's
// artifact for another head, and have the projection call it an independent
// review of work the reviewer had written.
func TestNamedArtifactIsTakenOnlyWhenCitedAndAtTheClaimedHead(t *testing.T) {
	projection := Fold(reviewRecords(t,
		// The reviewer implements a second head of their own.
		event(t, "v0", other, SchemaState, State{Kind: KindArtifact, Text: "the reviewer's own head", Body: map[string]string{"path": "ui", "commit": "head9"}}, "r0"),
		// Judges their own head, but names the other agent's artifact for a
		// different head and never rests on it.
		event(t, "borrowed", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head9", "artifact": "r5"}}, "reviewer-promise"),
		// Rests on the artifact it names, but claims a head that artifact is
		// not at, so the record contradicts itself.
		event(t, "mismatched", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head9", "artifact": "r5"}}, "reviewer-promise", "r5"),
		// Cited and at the claimed head: the ordinary case, still resolved.
		event(t, "sound", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
	))
	// The borrowed name buys nothing. What is left is the head the verdict
	// claims, and at that head the reviewer is the implementer.
	borrowed := reviewFor(t, projection, "borrowed")
	if borrowed.Independence != IndependenceSelfReview || borrowed.Artifact != "v0" || borrowed.ResolvedBy != "head" {
		t.Fatalf("a verdict naming an artifact it never cited = %+v", borrowed)
	}
	mismatched := reviewFor(t, projection, "mismatched")
	if mismatched.Independence != IndependenceUnresolved || mismatched.Artifact != "" || mismatched.ResolvedBy != "" {
		t.Fatalf("a verdict naming an artifact for another head = %+v", mismatched)
	}
	sound := reviewFor(t, projection, "sound")
	if sound.Independence != IndependenceIndependent || sound.Artifact != "r5" || sound.ResolvedBy != "named" {
		t.Fatalf("a cited artifact at the claimed head = %+v", sound)
	}
}

// Every case above that exercises the citation half of the named-artifact rule
// also fails its head half, so the citation is never the condition that decides
// them. This case makes it the only one: the verdict names an artifact that
// does stand at the head it claims, and never rests on it. Taking the name
// there would let a label the reviewer typed stand in for a link the log can
// follow. Uncited, the answer has to come from somewhere the record can vouch
// for — the head, when the head can answer — and where the head cannot answer,
// the verdict resolves to nothing rather than to the name.
func TestNamedArtifactAtTheClaimedHeadStillNeedsItsCitation(t *testing.T) {
	// One implementer at head1, so the head fallback can answer. The artifact
	// is the same either way; how it was found is not, and that is the whole
	// difference between a followed citation and a trusted label.
	uncited := Fold(reviewRecords(t,
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise"),
	))
	fallback := reviewFor(t, uncited, "v1")
	if fallback.Artifact != "r5" || fallback.ResolvedBy != "head" {
		// Not fatal: the contested case below is a separate projection and
		// pins the same rule where the fallback has no answer, so a run that
		// loses the citation should report both losses, not just the first.
		t.Errorf("an uncited name at the claimed head = %+v", fallback)
	}
	// A second author at the same head, so the head cannot answer. Taking the
	// name here would turn a question the record cannot settle into an
	// independent review of whichever artifact the reviewer happened to type.
	contested := Fold(reviewRecords(t,
		event(t, "v0", other, SchemaState, State{Kind: KindArtifact, Text: "the reviewer's own path at the same head", Body: map[string]string{"path": "ui", "commit": "head1"}}, "r0"),
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise"),
	))
	unsettled := reviewFor(t, contested, "v1")
	if unsettled.Independence != IndependenceUnresolved || unsettled.Artifact != "" || unsettled.ResolvedBy != "" {
		t.Fatalf("an uncited name at a contested head = %+v", unsettled)
	}
}

// A hand-written verdict that rests on exactly one artifact still answers the
// question; a verdict that rests on several does not, and says so.
func TestReviewResolvesImplementerFromSingleArtifactBasis(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v0", agent, SchemaState, State{Kind: KindArtifact, Text: "docs", Body: map[string]string{"path": "docs", "commit": "head2"}}, "r0"),
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved"}}, "reviewer-promise", "r5"),
		event(t, "v2", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved"}}, "reviewer-promise", "r5", "v0"),
	))
	single := reviewFor(t, projection, "v1")
	if single.Independence != IndependenceIndependent || single.ResolvedBy != "basis" || single.Artifact != "r5" {
		t.Fatalf("single-basis review = %+v", single)
	}
	ambiguous := reviewFor(t, projection, "v2")
	if ambiguous.Independence != IndependenceUnresolved || ambiguous.Artifact != "" {
		t.Fatalf("ambiguous review = %+v", ambiguous)
	}
}

// One implementer filing several path artifacts at one head is the common
// shape, so agreeing authors at the reviewed commit resolve it; disagreeing
// authors leave the question open rather than picking one.
func TestReviewResolvesImplementerFromReviewedHead(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v0", agent, SchemaState, State{Kind: KindArtifact, Text: "docs at the same head", Body: map[string]string{"path": "docs", "commit": "head1"}}, "r0"),
		event(t, "v1", other, SchemaState, State{Kind: KindArtifact, Text: "another author", Body: map[string]string{"path": "ui", "commit": "head3"}}, "r0"),
		event(t, "v2", agent, SchemaState, State{Kind: KindArtifact, Text: "shared head", Body: map[string]string{"path": "spike", "commit": "head3"}}, "r0"),
		event(t, "v3", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1"}}, "reviewer-promise"),
		event(t, "v4", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head3"}}, "reviewer-promise"),
	))
	agreed := reviewFor(t, projection, "v3")
	if agreed.Independence != IndependenceIndependent || agreed.ResolvedBy != "head" || agreed.Implementer != agent {
		t.Fatalf("head-resolved review = %+v", agreed)
	}
	contested := reviewFor(t, projection, "v4")
	if contested.Independence != IndependenceUnresolved || contested.ResolvedBy != "" {
		t.Fatalf("contested head review = %+v", contested)
	}
}

// A verdict naming nothing at all is the failure this projection exists to
// show: the chain is well formed and the record still cannot answer.
func TestReviewWithoutAnyArtifactReferenceIsUnresolved(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved"}}, "reviewer-promise"),
	))
	review := reviewFor(t, projection, "v1")
	if review.Independence != IndependenceUnresolved || review.Implementer != "" {
		t.Fatalf("unreferenced review = %+v", review)
	}
	if !bytes.Contains(RenderStatus(projection), []byte("unresolved")) {
		t.Fatal("status does not surface an unresolved review")
	}
}

// A report with no verdict is ordinary completion, not a review.
func TestReportWithoutVerdictIsNotAReview(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "ready for review"}, "reviewer-promise"),
	))
	if len(projection.Reviews) != 0 {
		t.Fatalf("reviews = %+v, want none", projection.Reviews)
	}
}

// A withdrawn verdict keeps its projected independence but must not be listed
// as something to act on.
func TestRetiredSelfReviewStaysProjectedAndLeavesTheStatusList(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v1", agent, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "implementer-promise", "r5"),
		event(t, "v2", agent, SchemaSupersede, Supersede{Target: "v1", Text: "withdrawn"}, "v1"),
	))
	review := reviewFor(t, projection, "v1")
	if review.Independence != IndependenceSelfReview || !review.Retired {
		t.Fatalf("retired self-review = %+v", review)
	}
	if bytes.Contains(RenderStatus(projection), []byte("SELF-SIGNED")) {
		t.Fatal("status lists a withdrawn self-review as outstanding")
	}
}

// A principal readmitted after retirement is live again, and the roster must
// not keep showing the retirement that its new membership replaced.
func TestReadmittedPrincipalIsLiveRatherThanRetired(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "instance joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "claude.2", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "retire claude.2"}, "e1"),
		event(t, "e4", operator, SchemaState, State{Kind: KindRoster, Text: "instance rejoins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "claude.2", "role": "participant"}}, "e0"),
		event(t, "e5", operator, SchemaRatify, Ratify{Target: "e4"}, "e4"),
	})
	state := projection.Actors[agent]
	if state.Retired || state.MembershipEvent != "e4" || len(state.Roles) == 0 {
		t.Fatalf("readmitted principal = %+v", state)
	}
}

// A retired principal stays visible and holds nothing: a request addressed to
// it is ineffective, exactly as one addressed to a stranger.
func TestRequestToRetiredPrincipalIsIneffective(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "instance joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "claude.2", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "while live", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
		event(t, "e4", operator, SchemaSupersede, Supersede{Target: "e1", Text: "retire claude.2"}, "e1"),
		event(t, "e5", operator, SchemaState, State{Kind: KindRequest, Text: "after retirement", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
	})
	for event, want := range map[string]Verdict{"e3": Effective, "e5": Ineffective} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != want {
			t.Fatalf("%s = %s (%s), want %s", event, decision.Verdict, decision.Reason, want)
		}
	}
	if state := projection.Actors[agent]; !state.Retired {
		t.Fatalf("retired principal = %+v", state)
	}
}

// The roster the reference page describes is the one projected here, so the
// page and the fold have to agree about what retirement does. They did not:
// two "measured" passages said a retired membership leaves the roster, and a
// later section on the same page said the principal stays with `retired: true`
// and no roles. A reader who believed the first half would go looking for the
// absence of a name that is plainly there.
func TestReferencePageAgreesThatRetiredPrincipalsStayOnTheRoster(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Alice", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "Bob joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Bob", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "retire Bob"}, "e1"),
	})
	state, listed := projection.Actors[agent]
	if !listed || !state.Retired || len(state.Roles) != 0 || state.Name != "Bob" {
		t.Fatalf("retired principal projection = %+v (listed %v)", state, listed)
	}

	page, err := os.ReadFile("../../docs/concepts/actors.md")
	if err != nil {
		t.Fatal(err)
	}
	// The page is hard-wrapped, so a claim can straddle a line break.
	unwrapped := strings.Join(strings.Fields(string(page)), " ")
	for _, contradiction := range []string{
		"disappears from the roster",
		"absent from the roster",
		"no longer on the roster",
	} {
		if strings.Contains(unwrapped, contradiction) {
			t.Errorf("docs/concepts/actors.md says a retired principal is %q, but the fold keeps it listed", contradiction)
		}
	}
	if !strings.Contains(unwrapped, "is left on the roster with `retired: true` and no roles") {
		t.Error("docs/concepts/actors.md no longer states what retiring a seeded membership actually leaves behind")
	}
	if !strings.Contains(unwrapped, "from `[participant]` to retired with no roles") {
		t.Error("docs/concepts/actors.md no longer states what superseding a membership actually leaves behind")
	}
}

func TestRegenerateGoldens(t *testing.T) {
	if os.Getenv("REGEN_GOLDENS") == "" {
		t.Skip("set REGEN_GOLDENS=1 to rewrite the pinned projections")
	}
	one, err := RenderJSON(golden(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("testdata/legacy_projection.golden.json", one, 0o644); err != nil {
		t.Fatal(err)
	}
	two, err := RenderJSON(preconditions(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("testdata/precondition_projection.golden.json", two, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The number is only worth naming an event by if every reader gets the same
// one. It is derived from the fold's own per-record index, so a re-fold of the
// same log must produce identical numbers — and the founding seed must be #1,
// because an off-by-one in something people type at each other never stops
// costing.
func TestSequenceIsStableAcrossARefold(t *testing.T) {
	first := golden(t)
	second := golden(t)
	if len(first.Statements) == 0 {
		t.Fatal("the golden log projects no statements, so this proves nothing")
	}
	if len(first.Statements) != len(second.Statements) {
		t.Fatalf("re-folding produced %d statements then %d", len(first.Statements), len(second.Statements))
	}
	for index, statement := range first.Statements {
		if other := second.Statements[index]; statement.Sequence != other.Sequence || statement.Event != other.Event {
			t.Fatalf("re-fold moved %s from #%d to %s #%d",
				statement.Event, statement.Sequence, other.Event, other.Sequence)
		}
		if statement.Sequence < 1 {
			t.Errorf("%s has sequence %d; the founding seed is #1 and nothing is #0", statement.Event, statement.Sequence)
		}
	}
	// Positions are the log's, not the statement list's: statements skip
	// ratify and supersede records, so their numbers are not 1..n.
	seen := map[int]string{}
	for _, statement := range first.Statements {
		if previous, clash := seen[statement.Sequence]; clash {
			t.Errorf("#%d names both %s and %s", statement.Sequence, previous, statement.Event)
		}
		seen[statement.Sequence] = statement.Event
	}
	for _, decision := range first.Decisions {
		if decision.Sequence < 1 {
			t.Errorf("decision %s has sequence %d", decision.Event, decision.Sequence)
		}
	}
}

// The surfaces, not the fold. The fold's numbers were already pinned above,
// and that is exactly why this test exists: review found three renderers still
// abbreviating event identifiers while every fold test stayed green. A number
// nobody displays is not a name, so the guarantee has to be asserted where a
// reader actually meets it.
//
// It is mutation-sensitive by construction: it fails if any event-bearing row
// drops either the readable #N or the exact event identifier. Git object IDs
// remain abbreviated because Git can resolve those; neither half of an event
// name can stand in for the other at a durable boundary.
func TestRenderedSurfacesNameEventsByNumberAndCanonicalID(t *testing.T) {
	projection := golden(t)
	if len(projection.Commitments) == 0 || len(projection.Artifacts) == 0 {
		t.Fatal("the golden log has no commitments or artifacts, so this proves nothing")
	}
	rendered := string(RenderStatus(projection))

	sequences := projection.sequences()
	for _, event := range eventsOn(projection) {
		sequence := sequences[event]
		want := event
		if sequence > 0 {
			want = fmt.Sprintf("#%d %s", sequence, event)
		}
		if !strings.Contains(rendered, want) {
			t.Errorf("the status renderer omits exact event name %q", want)
		}
	}
}

func TestRenderStatusKeepsLongCanonicalEventID(t *testing.T) {
	const request = "git:sha1:0123456789abcdef0123456789abcdef01234567#git:sha1:89abcdef0123456789abcdef0123456789abcdef"
	projection := Projection{
		Commitments: []Commitment{{
			Request: request, Requester: operator, Performer: agent,
			WaitingOn: agent, Status: "promised",
		}},
		Decisions: []Decision{{Event: request, Sequence: 41, Verdict: Effective}},
	}

	rendered := string(RenderStatus(projection))
	if want := "#41 " + request; !strings.Contains(rendered, want) {
		t.Fatalf("status omits the canonical event name %q:\n%s", want, rendered)
	}
	if abbreviated := short(request); strings.Contains(rendered, abbreviated) {
		t.Fatalf("status abbreviates the event as %q instead of preserving its canonical ID:\n%s", abbreviated, rendered)
	}
}

// eventsOn collects the identifiers the status renderer names in a row.
func eventsOn(projection Projection) []string {
	var events []string
	for _, commitment := range projection.Commitments {
		events = append(events, commitment.Request)
	}
	for _, artifact := range projection.Artifacts {
		events = append(events, artifact.Event)
	}
	for _, review := range projection.Reviews {
		events = append(events, review.Report)
	}
	for _, decision := range projection.Decisions {
		if decision.Verdict != Effective {
			events = append(events, decision.Event)
		}
	}
	return events
}

// successionRecords is one complete implementation loop, run the way the
// discipline says to run it: a request, its promise, the artifact standing at
// the branch head, an independent approval of that exact head, and the merge
// receipt that lands it and declares which pointer it will move. What each
// test adds is the retirement, which is the act under examination.
func successionRecords(t *testing.T, tail ...Record) []Record {
	t.Helper()
	return worldRecords(t,
		append([]Record{
			event(t, "reviewer-membership", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "w0"),
			event(t, "reviewer-ratified", operator, SchemaRatify, Ratify{Target: "reviewer-membership"}, "reviewer-membership"),
			event(t, "implementation-request", operator, SchemaState, State{Kind: KindRequest, Text: "implement it", Body: map[string]string{"to": agent, "conditions": "approved head is merged"}}, "w0"),
			event(t, "implementation-promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will implement it"}, "implementation-request"),
			event(t, "implementation-artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation at the branch head", Body: map[string]string{"path": "internal/workroom", "commit": "head1"}}, "implementation-promise"),
			event(t, "review-request", agent, SchemaState, State{Kind: KindRequest, Text: "review the exact head", Body: map[string]string{"to": other, "conditions": "independent approval"}}, "implementation-artifact"),
			event(t, "review-promise", other, SchemaState, State{Kind: KindPromise, Text: "I will review it"}, "review-request"),
			event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "implementation-artifact"}}, "review-promise", "implementation-artifact"),
			event(t, "approval-ratified", agent, SchemaRatify, Ratify{Target: "approval"}, "approval"),
			event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
				"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
				"merge_retirements": `{"implementation-artifact":"internal/workroom"}`, "merge_successors": `["internal/workroom"]`,
			}}, "approval"),
		}, tail...)...)
}

func commitmentByRequest(t *testing.T, projection Projection, request string) Commitment {
	t.Helper()
	for _, commitment := range projection.Commitments {
		if commitment.Request == request {
			return commitment
		}
	}
	t.Fatalf("no commitment projected for %s", request)
	return Commitment{}
}

// The merge step is not news to the loop that produced it. When the act that
// withdraws the branch artifact publishes a successor covering the same path,
// the pointer moved rather than being condemned, and the reasoning that stood
// on it — the approval, the report, the commitment — is answered, not
// undermined. Marking that chain stale said every finished loop needed
// re-reading, which is the flare that taught readers to ignore flares.
func TestSuccessionLeavesTheCompletedLoopUnflared(t *testing.T) {
	projection := Fold(successionRecords(t,
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "landed on main", Body: map[string]string{"path": "internal/workroom", "commit": "merged"}}, "merge", "implementation-artifact"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "landed as merged"}, "implementation-artifact", "successor"),
	))

	branch := artifactByEvent(t, projection, "implementation-artifact")
	if !branch.Retired || !branch.Succeeded {
		t.Fatalf("branch artifact: retired=%v succeeded=%v, want both", branch.Retired, branch.Succeeded)
	}
	approval := statementByEvent(t, projection, "approval")
	if approval.Stale || approval.DescribesSupersededWorld {
		t.Fatalf("approval: stale=%v world=%v, want neither", approval.Stale, approval.DescribesSupersededWorld)
	}
	for _, id := range []string{"implementation-request", "implementation-promise", "review-request", "review-promise", "merge"} {
		if statementByEvent(t, projection, id).Stale {
			t.Errorf("%s went stale on the merge that completed it", id)
		}
	}
	commitment := commitmentByRequest(t, projection, "implementation-request")
	if commitment.Status != "satisfied" || commitment.Stale {
		t.Errorf("implementation commitment = %+v, want satisfied and not stale", commitment)
	}
	if review := commitmentByRequest(t, projection, "review-request"); review.Stale {
		t.Errorf("review commitment = %+v, want not stale", review)
	}
}

// The other direction. A pointer withdrawn with nowhere to go is a
// condemnation, and everything resting on it has to be told.
func TestRetirementWithoutASuccessorStillFlaresTheLoop(t *testing.T) {
	projection := Fold(successionRecords(t,
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "the behaviour was deleted"}, "implementation-artifact"),
	))

	branch := artifactByEvent(t, projection, "implementation-artifact")
	if !branch.Retired || branch.Succeeded {
		t.Fatalf("branch artifact: retired=%v succeeded=%v, want retired and not succeeded", branch.Retired, branch.Succeeded)
	}
	approval := statementByEvent(t, projection, "approval")
	if !approval.Stale || !approval.DescribesSupersededWorld {
		t.Fatalf("approval: stale=%v world=%v, want both", approval.Stale, approval.DescribesSupersededWorld)
	}
	if commitment := commitmentByRequest(t, projection, "review-request"); !commitment.Stale {
		t.Errorf("review commitment = %+v, want stale", commitment)
	}
}

// The boundary the structural reading gets wrong. "A later live artifact
// exists at the same path" would rescue an artifact retired as wrong the
// moment anyone published at that path again, however unrelated the later work
// is. Succession is a fact the retiring act states, not one a bystander can
// supply afterwards.
func TestLaterArtifactAtTheSamePathDoesNotRescueACondemnedOne(t *testing.T) {
	projection := Fold(successionRecords(t,
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "retired as wrong; the claim was never true"}, "implementation-artifact"),
		event(t, "unrelated", agent, SchemaState, State{Kind: KindArtifact, Text: "different work, same tree", Body: map[string]string{"path": "internal/workroom", "commit": "later"}}, "w0"),
	))

	if artifactByEvent(t, projection, "implementation-artifact").Succeeded {
		t.Fatal("an unrelated later artifact at the same path rescued a condemned one")
	}
	if approval := statementByEvent(t, projection, "approval"); !approval.Stale {
		t.Error("the approval of a condemned artifact stopped flaring")
	}
}

// Succession is followed to its end. A successor later retired with no
// successor of its own condemns the behaviour, and the approval, requests,
// promises and commitments that stood on the predecessor must flare then,
// or a finished loop would look current after its replacement was found
// wrong. A successor replaced by a further successor keeps answering.
func TestCondemnedSuccessorFlaresWhatStoodOnThePredecessor(t *testing.T) {
	condemned := Fold(successionRecords(t,
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "landed on main", Body: map[string]string{"path": "internal/workroom", "commit": "merged"}}, "merge", "implementation-artifact"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "landed as merged"}, "implementation-artifact", "successor"),
		event(t, "condemn", agent, SchemaSupersede, Supersede{Target: "successor", Text: "the behaviour was wrong and is withdrawn"}, "successor"),
	))
	if branch := artifactByEvent(t, condemned, "implementation-artifact"); !branch.Retired || !branch.Succeeded {
		t.Fatalf("branch artifact: retired=%v succeeded=%v, want both", branch.Retired, branch.Succeeded)
	}
	approval := statementByEvent(t, condemned, "approval")
	if !approval.Stale || !approval.DescribesSupersededWorld {
		t.Fatalf("approval after the successor was condemned: stale=%v world=%v, want both", approval.Stale, approval.DescribesSupersededWorld)
	}
	for _, id := range []string{"review-request", "review-promise", "merge"} {
		if !statementByEvent(t, condemned, id).Stale {
			t.Errorf("%s stayed current after the successor was condemned", id)
		}
	}
	if review := commitmentByRequest(t, condemned, "review-request"); !review.Stale {
		t.Errorf("review commitment = %+v, want stale", review)
	}

	replaced := Fold(successionRecords(t,
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "landed on main", Body: map[string]string{"path": "internal/workroom", "commit": "merged"}}, "merge", "implementation-artifact"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "landed as merged"}, "implementation-artifact", "successor"),
		event(t, "successor2", agent, SchemaState, State{Kind: KindArtifact, Text: "a later merge", Body: map[string]string{"path": "internal/workroom", "commit": "merged2"}}, "successor"),
		event(t, "retire2", agent, SchemaSupersede, Supersede{Target: "successor", Text: "replaced by merged2"}, "successor", "successor2"),
	))
	if approval := statementByEvent(t, replaced, "approval"); approval.Stale || approval.DescribesSupersededWorld {
		t.Fatalf("approval after a second succession: stale=%v world=%v, want neither", approval.Stale, approval.DescribesSupersededWorld)
	}

	chained := Fold(successionRecords(t,
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "landed on main", Body: map[string]string{"path": "internal/workroom", "commit": "merged"}}, "merge", "implementation-artifact"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "landed as merged"}, "implementation-artifact", "successor"),
		event(t, "successor2", agent, SchemaState, State{Kind: KindArtifact, Text: "a later merge", Body: map[string]string{"path": "internal/workroom", "commit": "merged2"}}, "successor"),
		event(t, "retire2", agent, SchemaSupersede, Supersede{Target: "successor", Text: "replaced by merged2"}, "successor", "successor2"),
		event(t, "condemn2", agent, SchemaSupersede, Supersede{Target: "successor2", Text: "withdrawn as wrong"}, "successor2"),
	))
	if approval := statementByEvent(t, chained, "approval"); !approval.Stale || !approval.DescribesSupersededWorld {
		t.Fatalf("approval after the end of the chain was condemned: stale=%v world=%v, want both", approval.Stale, approval.DescribesSupersededWorld)
	}
}

// Succession answers the reasoning that stood on the artifact. It does not
// answer a page that describes the behaviour: the code moved, so the prose has
// to be re-read against it. That flare is what the documentation set is for,
// and it must survive the same merge that quiets the loop.
func TestSuccessionStillFlaresTheDocumentationAboveIt(t *testing.T) {
	projection := Fold(successionRecords(t,
		event(t, "page", agent, SchemaState, State{Kind: KindArtifact, Text: "the page describing it", Body: map[string]string{"path": "docs/reference/workroom.md", "commit": "head1"}}, "implementation-artifact"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "landed on main", Body: map[string]string{"path": "internal/workroom", "commit": "merged"}}, "merge", "implementation-artifact"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "implementation-artifact", Text: "landed as merged"}, "implementation-artifact", "successor"),
	))

	page := artifactByEvent(t, projection, "page")
	if !page.Stale || !page.DescribesSupersededWorld {
		t.Fatalf("page above a succeeded artifact: stale=%v world=%v, want both", page.Stale, page.DescribesSupersededWorld)
	}
}

// Which successor counts. The retiring act has to name an artifact that stands
// over the retired path — the same string, or a directory containing it.
// Anything narrower leaves the rest of that tree with no pointer, and anything
// elsewhere says nothing about this path at all.
func TestSuccessionFollowsOnlyACoveringSuccessor(t *testing.T) {
	for _, test := range []struct {
		name          string
		retiredPath   string
		successorPath string
		wantSucceeded bool
	}{
		{name: "same path", retiredPath: "cmd/gs", successorPath: "cmd/gs", wantSucceeded: true},
		{name: "widened to the directory", retiredPath: "internal/app/app.go", successorPath: "internal/app", wantSucceeded: true},
		{name: "narrowed inside the directory", retiredPath: "internal/workroom", successorPath: "internal/workroom/fold.go"},
		{name: "unrelated path", retiredPath: "docs", successorPath: "ui"},
		{name: "prefix that is not a directory", retiredPath: "internal/appraisal", successorPath: "internal/app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := Fold(worldRecords(t,
				event(t, "old", agent, SchemaState, State{Kind: KindArtifact, Text: "predecessor", Body: map[string]string{"path": test.retiredPath, "commit": "aaa111"}}, "w0"),
				event(t, "new", agent, SchemaState, State{Kind: KindArtifact, Text: "successor", Body: map[string]string{"path": test.successorPath, "commit": "bbb222"}}, "w0"),
				event(t, "retire", agent, SchemaSupersede, Supersede{Target: "old", Text: "withdrawn"}, "old", "new"),
			))
			if got := artifactByEvent(t, projection, "old").Succeeded; got != test.wantSucceeded {
				t.Fatalf("succeeded = %v, want %v for %q retired naming %q", got, test.wantSucceeded, test.retiredPath, test.successorPath)
			}
		})
	}
}

// A supersession the fold refused withdrew nothing, so it cannot be read as
// having moved a pointer either.
func TestRefusedSupersessionRecordsNoSuccession(t *testing.T) {
	projection := Fold(worldRecords(t,
		event(t, "old", agent, SchemaState, State{Kind: KindArtifact, Text: "predecessor", Body: map[string]string{"path": "cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "new", agent, SchemaState, State{Kind: KindArtifact, Text: "successor", Body: map[string]string{"path": "cmd/gs", "commit": "bbb222"}}, "w0"),
		// Not resting first on its target, which the fold refuses.
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "old", Text: "withdrawn"}, "new", "old"),
	))
	old := artifactByEvent(t, projection, "old")
	if old.Retired || old.Succeeded {
		t.Fatalf("refused supersession: retired=%v succeeded=%v, want neither", old.Retired, old.Succeeded)
	}
}

// Removal from the roster was advisory before this boundary existed: the fold
// read a valid signature and admitted the record, so anyone who kept a key or
// another clone could keep speaking with force. Custody of a key is evidence of
// who signed; it is not standing permission to go on signing.
// The kinds come from the catalog rather than a list written here, so a
// starter kind added later joins this guard without anybody remembering to.
// An earlier head named four kinds by hand and called the boundary covered;
// naming them is exactly the mistake, because the guard sits before kind
// semantics precisely so that no kind can escape it.
//
// The bodies are deliberately incomplete. A kind refused for a missing field
// would pass this test for the wrong reason, so membership has to be decided
// before any kind-specific field or lifecycle basis can give the act some
// other meaning.
func departedActorHistory(t testing.TB, legacy bool) []Record {
	t.Helper()
	seed := map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}
	membership := map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}
	if legacy {
		// A membership recorded before the roster carried a kind field. The
		// guard has to hold over history it did not write, or every workroom
		// older than it is exempt.
		delete(seed, "kind")
		membership = map[string]string{"actor": agent, "name": "Agent", "role": "agent"}
	}
	return []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: seed}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: membership}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "remove", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove agent"}, "e1"),
	}
}

func TestADepartedParticipantCannotAuthorState(t *testing.T) {
	kinds := []Kind{}
	for kind := range starterCatalog() {
		kinds = append(kinds, kind)
	}
	// A kind the catalog has declared but nothing has defined, and one it has
	// never heard of. Both must be refused for the membership reason, not for
	// being unrecognised: an unknown kind is the easiest way to smuggle an act
	// past a guard that switches on known ones.
	kinds = append(kinds, Kind("future-kind"))

	for _, legacy := range []bool{false, true} {
		for _, kind := range kinds {
			records := append(append([]Record{}, departedActorHistory(t, legacy)...),
				event(t, "after", agent, SchemaState, State{Kind: kind, Text: "after removal"}, "e0"),
			)
			decision, _ := Fold(records).Decision("after")
			if decision.Verdict != Ineffective || decision.Reason != departedAuthorReason {
				t.Errorf("legacy=%v %s authored after removal = %s (%s), want %q: a retained key is not speaking authority",
					legacy, kind, decision.Verdict, decision.Reason, departedAuthorReason)
			}
		}
	}
}

// The other half of the same claim, and the half a refusal test cannot make:
// that these kinds were authorable in the first place. Without it the guard
// could be refusing everything and every assertion above would still hold.
func TestALiveParticipantCanAuthorTheSameKinds(t *testing.T) {
	base := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
	}
	for _, test := range []struct {
		kind Kind
		body map[string]string
	}{
		{KindAssert, nil},
		{KindPropose, nil},
		{KindRequest, map[string]string{"to": operator, "conditions": "x"}},
		{KindArtifact, map[string]string{"path": "spike", "commit": "head1"}},
	} {
		records := append(append([]Record{}, base...),
			event(t, "live", agent, SchemaState, State{Kind: test.kind, Text: "while a member", Body: test.body}, "e0"),
		)
		if decision, _ := Fold(records).Decision("live"); decision.Verdict != Effective {
			t.Errorf("%s authored while a member = %s (%s), want effective", test.kind, decision.Verdict, decision.Reason)
		}
	}
}

// A report is the kind that carries a commitment to satisfaction, so it is
// worth pinning on both sides of the removal rather than trusting that it
// behaves like the rest. Filed before removal it stands; filed after, it does
// not, and the commitment must not read as satisfied either way.
func TestADepartedPerformerCannotFileAReport(t *testing.T) {
	for _, test := range []struct {
		name         string
		afterRemoval bool
	}{
		{name: "filed before removal"},
		{name: "filed after removal", afterRemoval: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := []Record{
				event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
				event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
				event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
				event(t, "req", operator, SchemaState, State{Kind: KindRequest, Text: "do it", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
				event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "req"),
			}
			remove := event(t, "remove", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove agent"}, "e1")
			report := event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise")
			if test.afterRemoval {
				records = append(records, remove, report)
			} else {
				records = append(records, report, remove)
			}
			decision, _ := Fold(records).Decision("report")
			if test.afterRemoval {
				if decision.Verdict != Ineffective || decision.Reason != departedAuthorReason {
					t.Fatalf("report after removal = %+v, want %q", decision, departedAuthorReason)
				}
				return
			}
			if decision.Verdict != Effective {
				t.Fatalf("report before removal = %+v, want effective: removal is not retroactive", decision)
			}
		})
	}
}

// Restoration has to restore. A boundary that removed authority permanently
// would make the roster a one-way door and quietly turn every removal into an
// expulsion nobody voted for.
func TestRestoredMembershipRestoresAuthorship(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "remove", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove agent"}, "e1"),
		event(t, "gap", agent, SchemaState, State{Kind: KindAssert, Text: "while removed"}, "e0"),
		event(t, "rejoin", operator, SchemaState, State{Kind: KindRoster, Text: "agent rejoins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "rejoin-ratify", operator, SchemaRatify, Ratify{Target: "rejoin"}, "rejoin"),
		event(t, "resumed", agent, SchemaState, State{Kind: KindAssert, Text: "after rejoining"}, "e0"),
	})
	if decision, _ := projection.Decision("gap"); decision.Verdict != Ineffective {
		t.Errorf("statement in the gap = %s (%s), want ineffective", decision.Verdict, decision.Reason)
	}
	if decision, _ := projection.Decision("resumed"); decision.Verdict != Effective {
		t.Errorf("statement after rejoining = %s (%s), want effective: removal is not expulsion", decision.Verdict, decision.Reason)
	}
}

// The originating-requester ratify path is the one that reaches furthest,
// because gs merge consumes a ratified review approval. A departed requester
// able to ratify could still put a head into main long after it stopped being
// a participant, which is authorship of force by another name.
func TestADepartedRequesterCannotRatifyAReviewApproval(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "requester joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Requester", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "e0"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "req", agent, SchemaState, State{Kind: KindRequest, Text: "review the head", Body: map[string]string{"to": other, "conditions": "exact head"}}, "e0"),
		event(t, "promise", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "req"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "APPROVED", Body: map[string]string{"verdict": "approved", "head": "head1"}}, "promise"),
		event(t, "remove", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove requester"}, "e1"),
		event(t, "ratify", agent, SchemaRatify, Ratify{Target: "approval"}, "approval"),
	})
	// The reason matters as much as the verdict. Asserting only that it is not
	// effective lets the test pass on any unrelated refusal, which is how a
	// guard can be deleted with every test still green.
	decision, _ := projection.Decision("ratify")
	if decision.Verdict != Ineffective || decision.Reason != departedRequesterReason {
		t.Fatalf("departed requester ratified an approval = %s (%s), want %q: this is what gs merge consumes", decision.Verdict, decision.Reason, departedRequesterReason)
	}
}

// The defect this head first shipped with, and the reason the review was
// refused. decideSupersede has three authority paths past the target-is-mine
// branch, and only two of them ever asked about membership. The ratifier path
// asks by accident and correctly: a role grant is active only while the
// membership it rests on is, so a departed ratifier already fails. The merge
// receipt path asked nothing at all, so an actor could be removed from the
// roster and still reach into another author's artifact — the exact boundary
// the head documented as closed while leaving it open.
//
// The history is the authorized cross-author succession, unchanged, with the
// implementer's membership retired after its receipt and successor are already
// effective. Everything the receipt path checks still passes; only standing
// has gone.
func TestADepartedActorCannotUseAnOldMergeReceipt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "older implementation", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		// Removed only now: the receipt, the plan and the successor all stand.
		event(t, "remove-implementer", operator, SchemaSupersede, Supersede{Target: "r1", Text: "implementer leaves"}, "r1"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	decision, _ := projection.Decision("retire")
	if decision.Verdict != Ineffective || decision.Reason != departedReceiptReason {
		t.Fatalf("departed actor using an old merge receipt = %+v, want %q: a retained key is not cross-author authority", decision, departedReceiptReason)
	}
	if artifactByEvent(t, projection, "predecessor").Retired {
		t.Fatal("a departed actor retired another author's artifact")
	}
}

// One power survives departure, and only one. Withdrawing your own pointer is
// not authorship of new force, and a departed actor that could not retract its
// own artifact would leave the log asserting something nobody can correct.
func TestADepartedActorMayStillSupersedeItsOwnAct(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "mine", agent, SchemaState, State{Kind: KindArtifact, Text: "my artifact", Body: map[string]string{"path": "spike", "commit": "head1"}}, "e0"),
		event(t, "remove", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove agent"}, "e1"),
		event(t, "retract", agent, SchemaSupersede, Supersede{Target: "mine", Text: "withdrawing my own pointer"}, "mine"),
	})
	decision, _ := projection.Decision("retract")
	if decision.Verdict != Effective {
		t.Fatalf("departed actor retracting its own act = %s (%s), want effective", decision.Verdict, decision.Reason)
	}
}

// datedWorld folds a chain in which a page rests on two artifact bases, each
// retired at a different point, and returns the date the fold gives the page.
// The bases are cited in the order given, so one call with each order shows
// whether citation order can change the answer. Nothing here supplies the
// number the test checks: the fold derives it from the retirements.
func datedWorld(t *testing.T, first, second string) Artifact {
	t.Helper()
	return artifactByEvent(t, Fold(datedWorldRecords(t, first, second)), "page")
}

// datedWorldRecords is the fixture itself, so a test can read the sequence the
// fold assigned a retirement rather than hard-coding a number that drifts with
// the shared preamble.
func datedWorldRecords(t *testing.T, first, second string) []Record {
	t.Helper()
	return reviewRecords(t,
		event(t, "old-base", agent, SchemaState, State{Kind: KindArtifact, Text: "old base", Body: map[string]string{"path": "old", "commit": "head1"}}, "r0"),
		event(t, "new-base", agent, SchemaState, State{Kind: KindArtifact, Text: "new base", Body: map[string]string{"path": "new", "commit": "head1"}}, "r0"),
		event(t, "old-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "old base moved", Body: map[string]string{"path": "old", "commit": "head2"}}, "r0"),
		event(t, "retire-old", agent, SchemaSupersede, Supersede{Target: "old-base", Text: "the old base moved"}, "old-base", "old-successor"),
		event(t, "new-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "new base moved", Body: map[string]string{"path": "new", "commit": "head2"}}, "r0"),
		event(t, "retire-new", agent, SchemaSupersede, Supersede{Target: "new-base", Text: "the new base moved"}, "new-base", "new-successor"),
		event(t, "page", agent, SchemaState, State{Kind: KindArtifact, Text: "page", Body: map[string]string{"path": "page", "commit": "head1"}}, first, second),
	)
}

// Citation order must not decide the date. The fold examines every basis and
// keeps the earliest active cause, so an older retirement cannot be hidden
// behind a newer one by writing the newer basis first.
func TestWorldDateIsTheEarliestCauseWhicheverOrderTheBasesAreCitedIn(t *testing.T) {
	oldFirst := datedWorld(t, "old-base", "new-base")
	newFirst := datedWorld(t, "new-base", "old-base")
	for _, page := range []Artifact{oldFirst, newFirst} {
		if !page.DescribesSupersededWorld {
			t.Fatal("the page does not describe a superseded world, so this test proves nothing")
		}
		if page.WorldSupersededAt == 0 {
			t.Fatal("the fold dated nothing, so the orders cannot be compared")
		}
	}
	if oldFirst.WorldSupersededAt != newFirst.WorldSupersededAt {
		t.Fatalf("citation order changed the date: old-first=%d new-first=%d — a signer can hide an older cause behind a newer one",
			oldFirst.WorldSupersededAt, newFirst.WorldSupersededAt)
	}
	// Stability alone is not the rule. Taking the LATEST active cause is also
	// order-stable and still wrong: a verdict between the two retirements would
	// flip from refuse to admit. So the value is asserted too, against the
	// earlier of the two retirements the fixture actually files.
	retirement, found := Fold(datedWorldRecords(t, "old-base", "new-base")).Decision("retire-old")
	if !found {
		t.Fatal("the fixture's earlier retirement is not projected, so its position cannot be compared")
	}
	earliest := retirement.Sequence
	if oldFirst.WorldSupersededAt != earliest {
		t.Fatalf("world dated at %d, want %d, the earlier retirement: an order-stable rule that takes the latest cause admits a merge the reviewer had already been shown",
			oldFirst.WorldSupersededAt, earliest)
	}
}

// A supersession that has itself been superseded is not a cause. Two
// retirements account for one moved world here; withdrawing the earlier leaves
// the world moved and the date must advance to the one still accounting for it.
func TestWithdrawingOneRetirementLeavesTheDateOfTheOneThatRemains(t *testing.T) {
	base := []Record{
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "ground", Body: map[string]string{"path": "ground", "commit": "head1"}}, "r0"),
		event(t, "early-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "ground moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0"),
		event(t, "retire-early", agent, SchemaSupersede, Supersede{Target: "ground", Text: "first retirement"}, "ground", "early-successor"),
		event(t, "page", agent, SchemaState, State{Kind: KindArtifact, Text: "page", Body: map[string]string{"path": "page", "commit": "head1"}}, "ground"),
		event(t, "late-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "ground moved again", Body: map[string]string{"path": "ground", "commit": "head3"}}, "r0"),
		event(t, "retire-late", agent, SchemaSupersede, Supersede{Target: "ground", Text: "second retirement"}, "ground", "late-successor"),
	}
	before := artifactByEvent(t, Fold(reviewRecords(t, base...)), "page")
	if !before.DescribesSupersededWorld || before.WorldSupersededAt == 0 {
		t.Fatalf("page before withdrawal: world=%v at=%d", before.DescribesSupersededWorld, before.WorldSupersededAt)
	}
	withdrawn := append(append([]Record(nil), base...),
		event(t, "withdraw-early", agent, SchemaSupersede, Supersede{Target: "retire-early", Text: "the first retirement is withdrawn"}, "retire-early"))
	after := artifactByEvent(t, Fold(reviewRecords(t, withdrawn...)), "page")
	if !after.DescribesSupersededWorld {
		t.Fatal("withdrawing one of two retirements un-moved the world, so the remaining cause is not counted")
	}
	if after.WorldSupersededAt <= before.WorldSupersededAt {
		t.Fatalf("date stayed at %d after withdrawing the earlier retirement, was %d: the withdrawn cause is still counted",
			after.WorldSupersededAt, before.WorldSupersededAt)
	}
}

// World staleness crosses a direct retirement edge from an artifact and then
// only artifact-to-artifact edges. A reasoning statement standing on a
// world-stale artifact records that its argument moved without claiming to
// describe the old world, so it must carry neither the flag nor a date.
func TestAReasoningEdgeDoesNotInheritTheWorldDate(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "ground", Body: map[string]string{"path": "ground", "commit": "head1"}}, "r0"),
		// Bare, with no successor. A succeeded retirement is answered rather
		// than news, so it deliberately does not flare the reasoning that
		// stood on it, and would leave this test asserting nothing.
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the ground was condemned"}, "ground"),
		event(t, "page", agent, SchemaState, State{Kind: KindArtifact, Text: "page describing the ground", Body: map[string]string{"path": "page", "commit": "head1"}}, "ground"),
		event(t, "note", agent, SchemaState, State{Kind: KindAssert, Text: "an assert resting on the page"}, "page"),
	)
	projection := Fold(records)
	page := artifactByEvent(t, projection, "page")
	if !page.DescribesSupersededWorld || page.WorldSupersededAt == 0 {
		t.Fatalf("page: world=%v at=%d — the artifact edge did not carry a dated world, so the reasoning edge proves nothing", page.DescribesSupersededWorld, page.WorldSupersededAt)
	}
	note := statementByEvent(t, projection, "note")
	if !note.Stale {
		t.Fatal("the assert is not stale, so it is not standing on the moved argument at all")
	}
	if note.DescribesSupersededWorld || note.WorldSupersededAt != 0 {
		t.Fatalf("a reasoning edge inherited the world flag or its date: world=%v at=%d", note.DescribesSupersededWorld, note.WorldSupersededAt)
	}
}

// receiptOverAMovedWorld builds a signed merge receipt appended straight to the
// log -- never through gs merge -- for an approval whose artifact rests on a
// base retired either before or after the verdict. It returns whether the fold
// admitted the retirement the receipt claims authority for. This is the door a
// check living only in the CLI does not defend.
func receiptOverAMovedWorld(t *testing.T, retireBeforeVerdict bool) bool {
	t.Helper()
	ground := event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0")
	candidate := event(t, "candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "spike", "commit": "head1"}}, "ground")
	moved := event(t, "ground-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "the base moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0")
	retire := event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the base moved on"}, "ground", "ground-successor")
	approval := event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "candidate"}}, "reviewer-promise", "candidate")
	ratify := event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval")
	receipt := event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
		"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
		"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
	}}, "approval")
	successor := event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge")
	predecessor := event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0")
	claim := event(t, "retire-predecessor", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor")

	tail := []Record{ground, candidate, predecessor, moved}
	if retireBeforeVerdict {
		tail = append(tail, retire, approval, ratify)
	} else {
		tail = append(tail, approval, ratify, retire)
	}
	tail = append(tail, receipt, successor, claim)
	decision, _ := Fold(reviewRecords(t, tail...)).Decision("retire-predecessor")
	return decision.Verdict == Effective
}

// A receipt appended directly to the log must obey the same temporal rule the
// CLI applies. The fold is the authority boundary: gs merge is one door, and a
// signed receipt is another. An approval whose artifact already described a
// superseded world when it was signed mints no cross-author retirement
// authority through either.
func TestADirectReceiptCannotSpendAnApprovalOverAWorldThatHadAlreadyMoved(t *testing.T) {
	if receiptOverAMovedWorld(t, true) {
		t.Fatal("a receipt appended straight to the log spent an approval whose artifact was world-stale at the verdict, and took cross-author retirement authority with it")
	}
}

// The admitting half, so the refusal above cannot pass by refusing everything.
// The reviewer had no chance to see a retirement that landed after they signed.
func TestADirectReceiptStillSpendsAnApprovalWhenTheWorldMovedAfterward(t *testing.T) {
	if !receiptOverAMovedWorld(t, false) {
		t.Fatal("a retirement after the verdict blocked a receipt the reviewer's own judgement still covers")
	}
}

// A co-signed member is held to the same temporal rule as the primary. If a
// member that had already described a superseded world at the verdict still
// widened a receipt's reach, the bound the reviewer's signature is supposed to
// place on cross-author retirement would be exactly the part that leaks.
func TestACoSignedMemberOverAlreadyMovedWorldDoesNotWidenTheReceipt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "ground-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "the base moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0"),
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "moved before the verdict"}, "ground", "ground-successor"),
		// Co-signed, at the reviewed head, by the implementer -- and world-stale
		// before the reviewer ever looked at it.
		event(t, "cosigned", agent, SchemaState, State{Kind: KindArtifact, Text: "docs at the reviewed head", Body: map[string]string{"path": "docs", "commit": "head1"}}, "ground"),
		event(t, "foreign-docs", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's docs pointer", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5", "cosigned"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"foreign-docs":"docs"}`, "merge_successors": `["docs"]`,
		}}, "approval"),
		event(t, "docs-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "docs merged", Body: map[string]string{"path": "docs", "commit": "merged"}}, "merge"),
		event(t, "retire-foreign", agent, SchemaSupersede, Supersede{Target: "foreign-docs", Text: "merge succession"}, "foreign-docs", "merge", "docs-successor"),
	)
	decision, _ := Fold(records).Decision("retire-foreign")
	if decision.Verdict == Effective {
		t.Fatal("a co-signed member that was already world-stale at the verdict widened the receipt onto another actor's docs pointer")
	}
	// The positive control. The same shape with the co-signed member's world
	// moving AFTER the verdict must widen the receipt, or the refusal above
	// proves nothing: an earlier version of this test refused because
	// foreign-docs was declared after the receipt and the plan reached no
	// artifact at all, which passes with the whole rule deleted.
	if !coSignedWidensWhenTheWorldMovedAfter(t) {
		t.Fatal("a co-signed member whose world moved after the verdict failed to widen the receipt, so the refusal above is not evidence of the temporal rule")
	}
}

// coSignedWidensWhenTheWorldMovedAfter is the co-signed fixture with the
// retirement sequenced after the approval instead of before it.
func coSignedWidensWhenTheWorldMovedAfter(t *testing.T) bool {
	t.Helper()
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "ground-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "the base moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0"),
		event(t, "cosigned", agent, SchemaState, State{Kind: KindArtifact, Text: "docs at the reviewed head", Body: map[string]string{"path": "docs", "commit": "head1"}}, "ground"),
		event(t, "foreign-docs", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's docs pointer", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5", "cosigned"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "moved after the verdict"}, "ground", "ground-successor"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"foreign-docs":"docs"}`, "merge_successors": `["docs"]`,
		}}, "approval"),
		event(t, "docs-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "docs merged", Body: map[string]string{"path": "docs", "commit": "merged"}}, "merge"),
		event(t, "retire-foreign", agent, SchemaSupersede, Supersede{Target: "foreign-docs", Text: "merge succession"}, "foreign-docs", "merge", "docs-successor"),
	)
	decision, _ := Fold(records).Decision("retire-foreign")
	return decision.Verdict == Effective
}

// An ineffective supersession is not a retirement and must not date anything.
func TestAnIneffectiveSupersessionIsNotAWorldCause(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "page", agent, SchemaState, State{Kind: KindArtifact, Text: "page", Body: map[string]string{"path": "page", "commit": "head1"}}, "ground"),
		// other did not write the base and holds no ratifier, so the fold
		// refuses this supersession rather than recording a retirement.
		event(t, "stranger-retire", other, SchemaSupersede, Supersede{Target: "ground", Text: "not mine to retire"}, "ground"),
	)
	projection := Fold(records)
	if decision, _ := projection.Decision("stranger-retire"); decision.Verdict == Effective {
		t.Fatalf("the stranger's supersession was admitted, so this test cannot tell an ineffective cause from an effective one: %+v", decision)
	}
	page := artifactByEvent(t, projection, "page")
	if page.DescribesSupersededWorld || page.WorldSupersededAt != 0 {
		t.Fatalf("an ineffective supersession moved the world: world=%v at=%d", page.DescribesSupersededWorld, page.WorldSupersededAt)
	}
}

// A receipt whose approval names an artifact that was world-stale at the verdict
// is refused, with the retirement declared before the approval rather than
// after. An earlier version of this comment claimed the retirement was withdrawn
// after the receipt; its records contained no withdrawal at all, so the comment
// described a scenario the fixture never built. The fail-closed path it claimed
// to cover is unreachable from the fold's own output -- retired(b) and b being
// in the active index are the same fact -- so it is asserted at the CLI
// boundary, where a stale cache can produce a flag with no date, rather than
// pretended at here.
func TestADirectReceiptRefusesAnApprovalNamingAWorldStaleArtifact(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "spike", "commit": "head1"}}, "ground"),
		event(t, "ground-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "the base moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0"),
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "moved before the verdict"}, "ground", "ground-successor"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "candidate"}}, "reviewer-promise", "candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-predecessor", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	decision, _ := Fold(records).Decision("retire-predecessor")
	if decision.Verdict == Effective {
		t.Fatal("a receipt spent an approval over a world that had already moved before the verdict")
	}
}

// Isolates the PRIMARY artifact guard. The approval's named artifact was
// already world-stale at the verdict, while a co-signed member is healthy and
// still yields a path. Without the primary check the receipt survives on the
// healthy member and carries cross-author retirement authority with it, so the
// other receipt tests -- which refuse through the co-signed path -- do not cover
// this guard at all.
func TestADirectReceiptRefusesWhenOnlyThePrimaryArtifactWasAlreadyStale(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "ground-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "the base moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0"),
		// The primary: world-stale before the reviewer ever looked.
		event(t, "primary", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "spike", "commit": "head1"}}, "ground"),
		// A healthy co-signed member at another path, resting on nothing retired.
		event(t, "cosigned", agent, SchemaState, State{Kind: KindArtifact, Text: "docs at the reviewed head", Body: map[string]string{"path": "docs", "commit": "head1"}}, "r0"),
		event(t, "foreign-docs", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's docs pointer", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "moved before the verdict"}, "ground", "ground-successor"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "primary"}}, "reviewer-promise", "primary", "cosigned"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"foreign-docs":"docs"}`, "merge_successors": `["docs"]`,
		}}, "approval"),
		event(t, "docs-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "docs merged", Body: map[string]string{"path": "docs", "commit": "merged"}}, "merge"),
		event(t, "retire-foreign", agent, SchemaSupersede, Supersede{Target: "foreign-docs", Text: "merge succession"}, "foreign-docs", "merge", "docs-successor"),
	)
	projection := Fold(records)
	primary := artifactByEvent(t, projection, "primary")
	cosigned := artifactByEvent(t, projection, "cosigned")
	if !primary.DescribesSupersededWorld || cosigned.DescribesSupersededWorld {
		t.Fatalf("fixture wrong: primary world=%v cosigned world=%v — this test only isolates the primary guard when exactly the primary is stale",
			primary.DescribesSupersededWorld, cosigned.DescribesSupersededWorld)
	}
	if decision, _ := projection.Decision("retire-foreign"); decision.Verdict == Effective {
		t.Fatal("a receipt whose named artifact was already world-stale at the verdict survived on a healthy co-signed member")
	}
}

// The interposed-artifact attack. The implementer authors `mid` themselves and
// supersedes it after the verdict, so a walk that stops at the first retired
// basis sees only that later retirement and never reaches `ground`, which was
// retired BEFORE the reviewer signed. The published date inherits through
// retired artifacts and says the world had already moved; a receipt boundary
// that stops early disagrees and admits the merge. Both must answer the same.
func TestAnInterposedRetirementDoesNotHideAnOlderCauseFromTheReceipt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "ground-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "the base moved", Body: map[string]string{"path": "ground", "commit": "head2"}}, "r0"),
		event(t, "mid", agent, SchemaState, State{Kind: KindArtifact, Text: "interposed", Body: map[string]string{"path": "mid", "commit": "head0"}}, "ground"),
		event(t, "candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "spike", "commit": "head1"}}, "mid"),
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		// Before the verdict.
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the base moved on"}, "ground", "ground-successor"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "candidate"}}, "reviewer-promise", "candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		// After the verdict, and authored by the same implementer.
		event(t, "mid-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "interposed moved", Body: map[string]string{"path": "mid", "commit": "head2"}}, "r0"),
		event(t, "retire-mid", agent, SchemaSupersede, Supersede{Target: "mid", Text: "interposed retirement"}, "mid", "mid-successor"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-predecessor", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	candidate := artifactByEvent(t, projection, "candidate")
	approval := statementByEvent(t, projection, "approval")
	if !candidate.DescribesSupersededWorld || candidate.WorldSupersededAt == 0 {
		t.Fatalf("candidate world=%v at=%d: the fixture does not describe a moved world", candidate.DescribesSupersededWorld, candidate.WorldSupersededAt)
	}
	if candidate.WorldSupersededAt > approval.Sequence {
		t.Fatalf("published date %d is later than the verdict at %d, so the older cause is not the one being inherited and this test proves nothing",
			candidate.WorldSupersededAt, approval.Sequence)
	}
	if decision, _ := projection.Decision("retire-predecessor"); decision.Verdict == Effective {
		t.Fatal("an interposed post-verdict retirement hid an older pre-verdict cause, and the receipt took cross-author retirement authority")
	}
}

// A receipt over a successor whose predecessor the merge itself retired. The
// projection keeps such a successor current — the retirement is the merge's
// own signed act, not news — so a reviewer approving that successor signs
// over a world that never moved for them, and the receipt spending that
// approval must be admitted. A receipt boundary that walks the artifact graph
// without honouring the merge-plan exemption sees the retired predecessor
// under the successor and refuses a verdict that was sound when made: the
// exact divergence between the projection's rule and a second walker.
func TestAReceiptOverAMergeSuccessorRestingOnItsRetiredPredecessorIsAdmitted(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "the pointer the first merge replaces", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged1",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		// The successor cites the predecessor as its causal basis, and the
		// same merge withdraws that predecessor.
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged1"}}, "merge", "predecessor"),
		event(t, "retire", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
		// The second round reviews the successor itself.
		event(t, "request2", operator, SchemaState, State{Kind: KindRequest, Text: "review the successor", Body: map[string]string{"to": other, "conditions": "exact head"}}, "successor"),
		event(t, "promise2", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "request2"),
		event(t, "foreign", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "spike", "commit": "other-base"}}, "r0"),
		event(t, "approval2", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "merged1", "artifact": "successor"}}, "promise2", "successor"),
		event(t, "approval2-ratified", operator, SchemaRatify, Ratify{Target: "approval2"}, "approval2"),
		event(t, "merge2", agent, SchemaState, State{Kind: KindAssert, Text: "second merge", Body: map[string]string{
			"merge_approval": "approval2", "merge_candidate": "merged1", "merge_target_pre_head": "merged1", "merge_head": "merged2",
			"merge_retirements": `{"foreign":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval2"),
		event(t, "successor2", agent, SchemaState, State{Kind: KindArtifact, Text: "second successor", Body: map[string]string{"path": "spike", "commit": "merged2"}}, "merge2"),
		event(t, "retire-foreign", agent, SchemaSupersede, Supersede{Target: "foreign", Text: "merge succession"}, "foreign", "merge2", "successor2"),
	)
	projection := Fold(records)
	// A successor over a live predecessor is clean with the exemption deleted,
	// so first prove the merge's own retirement actually took: without it this
	// fixture admits the receipt while exercising nothing.
	if predecessor := artifactByEvent(t, projection, "predecessor"); !predecessor.Retired {
		t.Fatal("the predecessor was never retired, so the merge-plan exemption is not what admits this receipt")
	}
	successor := artifactByEvent(t, projection, "successor")
	if successor.DescribesSupersededWorld || successor.Stale {
		t.Fatalf("the projection flags the merge's own successor (world=%v stale=%v), so this test cannot isolate the receipt boundary",
			successor.DescribesSupersededWorld, successor.Stale)
	}
	decision, found := projection.Decision("retire-foreign")
	if !found {
		t.Fatal("the cross-author supersession was never decided, so nothing here reached the receipt boundary at all")
	}
	if decision.Verdict != Effective {
		t.Fatalf("a receipt over a successor whose predecessor its own merge retired was refused: %+v — the receipt boundary disagrees with the projection about the merge-plan exemption", decision)
	}
}

// stalenessModeDefinition declares an artifact-rendered kind whose governed
// staleness mode is the one under test, so a receipt fixture can stand a
// candidate on a ground the vocabulary says cannot flare it.
func stalenessModeDefinition(name string, mode StalenessMode) KindDefinition {
	return KindDefinition{
		Name:   Kind(name),
		Fields: []FieldConstraint{{Operator: FieldPresent, Name: "path"}, {Operator: FieldPresent, Name: "commit"}},
		Basis:  []BasisConstraint{}, Satisfier: "role:ratifier", Render: RenderArtifact,
		Staleness: mode, Lifecycle: LifecycleNone,
		Guidance: "Fixture kind for the receipt boundary's staleness-mode tests.",
	}
}

// A ground whose governing definition is StalenessExempt cannot flare what
// stands on it: the projection keeps the candidate current however the ground
// moves. The receipt boundary must honour the same governed mode — a walker
// with its own edge rules sees a retired artifact basis, calls the candidate
// world-stale, and refuses an approval the projection says was signed over a
// world that never moved.
func TestAReceiptHonoursAnExemptGroundRetiredBeforeTheVerdict(t *testing.T) {
	definition := stalenessModeDefinition("exempt-ground", StalenessExempt)
	records := reviewRecords(t,
		event(t, "def", operator, SchemaState, kindDefinitionState(t, definition), "r0"),
		event(t, "def-ratified", operator, SchemaRatify, Ratify{Target: "def"}, "def"),
		event(t, "ground", agent, SchemaState, State{Kind: "exempt-ground", Text: "exempt ground", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "impl", "commit": "head1"}}, "ground"),
		// Before the verdict, and bare: a condemnation, not a succession.
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the exempt ground moved"}, "ground"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "candidate"}}, "reviewer-promise", "candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "impl", "commit": "base"}}, "r0"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"impl"}`, "merge_successors": `["impl"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "impl", "commit": "merged"}}, "merge"),
		event(t, "retire-predecessor", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	// A candidate over a live ground is clean whatever the mode says, so first
	// prove the ground was actually retired: a fixture whose retirement quietly
	// failed admits this receipt while exercising nothing about exemption.
	if ground := artifactByEvent(t, projection, "ground"); !ground.Retired {
		t.Fatal("the exempt ground was never retired, so a receipt admitted here says nothing about the exempt mode")
	}
	candidate := artifactByEvent(t, projection, "candidate")
	if candidate.DescribesSupersededWorld || candidate.Stale {
		t.Fatalf("the projection flags the candidate over an exempt ground (world=%v stale=%v), so the fixture does not isolate the exempt mode",
			candidate.DescribesSupersededWorld, candidate.Stale)
	}
	decision, found := projection.Decision("retire-predecessor")
	if !found {
		t.Fatal("the cross-author supersession was never decided, so nothing here reached the receipt boundary at all")
	}
	if decision.Verdict != Effective {
		t.Fatalf("a receipt was refused over an exempt ground the projection says cannot flare the candidate: %+v", decision)
	}
}

// A terminal page catches the moved world and does not pass it on. The
// candidate standing on it stays current in the projection, so the approval
// over that candidate is signed over an unmoved world and the receipt must be
// admitted. A walker that ignores the governed mode crosses the terminal page
// to the retired ground beneath it and refuses.
func TestAReceiptHonoursATerminalPageThatCaughtTheMovedWorld(t *testing.T) {
	definition := stalenessModeDefinition("terminal-page", StalenessTerminal)
	records := reviewRecords(t,
		event(t, "def", operator, SchemaState, kindDefinitionState(t, definition), "r0"),
		event(t, "def-ratified", operator, SchemaRatify, Ratify{Target: "def"}, "def"),
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "mid", agent, SchemaState, State{Kind: "terminal-page", Text: "terminal page over the base", Body: map[string]string{"path": "mid", "commit": "head0"}}, "ground"),
		event(t, "candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "impl", "commit": "head1"}}, "mid"),
		// Before the verdict, and bare.
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the base was condemned"}, "ground"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "candidate"}}, "reviewer-promise", "candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "impl", "commit": "base"}}, "r0"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"predecessor":"impl"}`, "merge_successors": `["impl"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged", Body: map[string]string{"path": "impl", "commit": "merged"}}, "merge"),
		event(t, "retire-predecessor", agent, SchemaSupersede, Supersede{Target: "predecessor", Text: "merge succession"}, "predecessor", "merge", "successor"),
	)
	projection := Fold(records)
	mid := artifactByEvent(t, projection, "mid")
	candidate := artifactByEvent(t, projection, "candidate")
	if !mid.DescribesSupersededWorld || candidate.DescribesSupersededWorld {
		t.Fatalf("fixture wrong: mid world=%v candidate world=%v — terminal must catch the moved world without passing it on",
			mid.DescribesSupersededWorld, candidate.DescribesSupersededWorld)
	}
	decision, found := projection.Decision("retire-predecessor")
	if !found {
		t.Fatal("the cross-author supersession was never decided, so nothing here reached the receipt boundary at all")
	}
	if decision.Verdict != Effective {
		t.Fatalf("a receipt was refused over a terminal page the projection says stopped the moved world: %+v", decision)
	}
}

// Two receipts in one log, one cause between their verdicts. The first
// approval was signed before the ground moved and its receipt is admitted;
// the second was signed after and its receipt is refused. Each receipt must be
// judged at its own verdict's position — a boundary that reuses one cutoff for
// both, or dates against the receipt instead of the verdict, answers at least
// one of them wrongly.
func TestTwoReceiptsInOneLogAreEachJudgedAtTheirOwnVerdict(t *testing.T) {
	records := reviewRecords(t,
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "cand-a", agent, SchemaState, State{Kind: KindArtifact, Text: "lane A implementation", Body: map[string]string{"path": "lane-a", "commit": "head-a"}}, "ground"),
		event(t, "cand-b", agent, SchemaState, State{Kind: KindArtifact, Text: "lane B implementation", Body: map[string]string{"path": "lane-b", "commit": "head-b"}}, "ground"),
		event(t, "approval-a", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head-a", "artifact": "cand-a"}}, "reviewer-promise", "cand-a"),
		event(t, "approval-a-ratified", operator, SchemaRatify, Ratify{Target: "approval-a"}, "approval-a"),
		// After verdict A, before verdict B.
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the base was condemned"}, "ground"),
		event(t, "request-b", operator, SchemaState, State{Kind: KindRequest, Text: "review lane B", Body: map[string]string{"to": other, "conditions": "exact head"}}, "cand-b"),
		event(t, "promise-b", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "request-b"),
		event(t, "approval-b", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head-b", "artifact": "cand-b"}}, "promise-b", "cand-b"),
		event(t, "approval-b-ratified", operator, SchemaRatify, Ratify{Target: "approval-b"}, "approval-b"),
		event(t, "pred-a", operator, SchemaState, State{Kind: KindArtifact, Text: "lane A pointer", Body: map[string]string{"path": "lane-a", "commit": "base"}}, "r0"),
		event(t, "pred-b", operator, SchemaState, State{Kind: KindArtifact, Text: "lane B pointer", Body: map[string]string{"path": "lane-b", "commit": "base"}}, "r0"),
		event(t, "merge-a", agent, SchemaState, State{Kind: KindAssert, Text: "lane A merged", Body: map[string]string{
			"merge_approval": "approval-a", "merge_candidate": "head-a", "merge_target_pre_head": "base", "merge_head": "merged-a",
			"merge_retirements": `{"pred-a":"lane-a"}`, "merge_successors": `["lane-a"]`,
		}}, "approval-a"),
		event(t, "succ-a", agent, SchemaState, State{Kind: KindArtifact, Text: "lane A successor", Body: map[string]string{"path": "lane-a", "commit": "merged-a"}}, "merge-a"),
		event(t, "retire-pred-a", agent, SchemaSupersede, Supersede{Target: "pred-a", Text: "merge succession"}, "pred-a", "merge-a", "succ-a"),
		event(t, "merge-b", agent, SchemaState, State{Kind: KindAssert, Text: "lane B merged", Body: map[string]string{
			"merge_approval": "approval-b", "merge_candidate": "head-b", "merge_target_pre_head": "base", "merge_head": "merged-b",
			"merge_retirements": `{"pred-b":"lane-b"}`, "merge_successors": `["lane-b"]`,
		}}, "approval-b"),
		event(t, "succ-b", agent, SchemaState, State{Kind: KindArtifact, Text: "lane B successor", Body: map[string]string{"path": "lane-b", "commit": "merged-b"}}, "merge-b"),
		event(t, "retire-pred-b", agent, SchemaSupersede, Supersede{Target: "pred-b", Text: "merge succession"}, "pred-b", "merge-b", "succ-b"),
	)
	projection := Fold(records)
	// Both verdicts are judged against the one cause between them, so first
	// prove that cause actually landed: with the ground never retired, lane A
	// passes for the wrong reason and lane B's refusal asserts nothing.
	if ground := artifactByEvent(t, projection, "ground"); !ground.Retired {
		t.Fatal("the ground was never retired, so neither verdict has a moved world to be judged against")
	}
	admitted, foundA := projection.Decision("retire-pred-a")
	if !foundA {
		t.Fatal("lane A's supersession was never decided, so nothing here reached the receipt boundary at all")
	}
	if admitted.Verdict != Effective {
		t.Fatalf("lane A's receipt was refused although the world moved only after its verdict: %+v", admitted)
	}
	refused, foundB := projection.Decision("retire-pred-b")
	if !foundB {
		t.Fatal("lane B's supersession was never decided, and a missing decision reads as refused: the assertion below would pass on an empty projection")
	}
	if refused.Verdict == Effective {
		t.Fatal("lane B's receipt was admitted although its reviewer had already been shown the moved world")
	}
}

// A Terminal basis catches a moved world without passing it on, and can later
// be directly retired itself. Its own date then records the cause it stopped,
// and inheriting that date across the Terminal edge would date everything
// above it by a cause the vocabulary says never reached it. Concretely: the
// ground is condemned, the terminal page catches it, a reviewer signs over the
// candidate while stalenessAsOf holds it not world-stale, and only then is the
// page itself retired. The candidate's published date must be the page's own
// retirement — after the verdict — not the stopped cause from before it, or
// the published date and the admitted verdict contradict each other. Found by
// randomized differential; this pins the counterexample deterministically.
func TestADirectlyRetiredTerminalBasisDoesNotDateByTheCauseItStopped(t *testing.T) {
	definition := stalenessModeDefinition("terminal-page", StalenessTerminal)
	records := reviewRecords(t,
		event(t, "def", operator, SchemaState, kindDefinitionState(t, definition), "r0"),
		event(t, "def-ratified", operator, SchemaRatify, Ratify{Target: "def"}, "def"),
		event(t, "ground", agent, SchemaState, State{Kind: KindArtifact, Text: "the base", Body: map[string]string{"path": "ground", "commit": "head0"}}, "r0"),
		event(t, "mid", agent, SchemaState, State{Kind: "terminal-page", Text: "terminal page over the base", Body: map[string]string{"path": "mid", "commit": "head0"}}, "ground"),
		event(t, "candidate", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "impl", "commit": "head1"}}, "mid"),
		// The stopped cause, before the verdict, and bare.
		event(t, "retire-ground", agent, SchemaSupersede, Supersede{Target: "ground", Text: "the base was condemned"}, "ground"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "candidate"}}, "reviewer-promise", "candidate"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		// The terminal page itself, directly retired after the verdict.
		event(t, "retire-mid", agent, SchemaSupersede, Supersede{Target: "mid", Text: "the page was condemned"}, "mid"),
	)
	projection := Fold(records)
	stopped, foundStopped := projection.Decision("retire-ground")
	direct, foundDirect := projection.Decision("retire-mid")
	if !foundStopped || stopped.Verdict != Effective || !foundDirect || direct.Verdict != Effective {
		t.Fatalf("fixture wrong: retire-ground found=%v %+v, retire-mid found=%v %+v — without both retirements there is no stopped cause and no direct one",
			foundStopped, stopped, foundDirect, direct)
	}
	mid := artifactByEvent(t, projection, "mid")
	if !mid.DescribesSupersededWorld || mid.WorldSupersededAt != stopped.Sequence {
		t.Fatalf("mid world=%v at=%d, want the stopped cause at %d: the terminal page never caught the older cause, so there is nothing here to inherit wrongly",
			mid.DescribesSupersededWorld, mid.WorldSupersededAt, stopped.Sequence)
	}
	verdict := statementByEvent(t, projection, "approval")
	if !(stopped.Sequence < verdict.Sequence && verdict.Sequence < direct.Sequence) {
		t.Fatalf("fixture wrong: stopped=%d verdict=%d direct=%d — the verdict must sit between the two causes for the date to be able to contradict it",
			stopped.Sequence, verdict.Sequence, direct.Sequence)
	}
	candidate := artifactByEvent(t, projection, "candidate")
	if !candidate.DescribesSupersededWorld {
		t.Fatal("the candidate does not describe a superseded world after its terminal basis was directly retired, so there is no date to check")
	}
	if candidate.WorldSupersededAt != direct.Sequence {
		t.Fatalf("candidate dated %d, want %d, the terminal basis's own retirement: inheriting the stopped cause dates the candidate before the verdict at %d, which stalenessAsOf holds was signed over an unmoved world",
			candidate.WorldSupersededAt, direct.Sequence, verdict.Sequence)
	}
}

// A succession declared after the verdict must not reach back into it. The
// first merge's plan retires the predecessor, which is then condemned bare —
// the plan exemption keeps the merge's successor current, so a reviewer
// approving that successor signs over a world that has not moved for them.
// Only after the verdict does a late supersession declare a pointer as the
// predecessor's successor — a pointer that was itself condemned before the
// verdict, so the edge completes a condemned chain. Taken from end-of-log
// topology, that edge condemns the plan retroactively and revokes receipt
// authority the reviewer's signature had already earned; the scope must build
// its successor topology from supersessions at or before its own position.
// Found by randomized differential; this pins it deterministically.
func TestASuccessionDeclaredAfterTheVerdictDoesNotCondemnThePlanAtIt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "the pointer the first merge replaces", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged1",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged1"}}, "merge", "predecessor"),
		// Condemned bare by its own author: no successor declared here.
		event(t, "condemn-predecessor", operator, SchemaSupersede, Supersede{Target: "predecessor", Text: "condemned with no successor"}, "predecessor"),
		// A pointer at the same path, itself condemned before the verdict.
		event(t, "stray", operator, SchemaState, State{Kind: KindArtifact, Text: "a pointer that did not survive", Body: map[string]string{"path": "spike", "commit": "stray"}}, "r0"),
		event(t, "condemn-stray", operator, SchemaSupersede, Supersede{Target: "stray", Text: "condemned"}, "stray"),
		// The second review, of the first merge's successor.
		event(t, "request2", operator, SchemaState, State{Kind: KindRequest, Text: "review the successor", Body: map[string]string{"to": other, "conditions": "exact head"}}, "successor"),
		event(t, "promise2", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "request2"),
		event(t, "foreign", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "spike", "commit": "other-base"}}, "r0"),
		event(t, "approval2", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "merged1", "artifact": "successor"}}, "promise2", "successor"),
		event(t, "approval2-ratified", operator, SchemaRatify, Ratify{Target: "approval2"}, "approval2"),
		// After the verdict: the late succession names the condemned stray as
		// where the predecessor's behaviour went.
		event(t, "late-succession", operator, SchemaSupersede, Supersede{Target: "predecessor", Text: "a successor declared late"}, "predecessor", "stray"),
		event(t, "merge2", agent, SchemaState, State{Kind: KindAssert, Text: "second merge", Body: map[string]string{
			"merge_approval": "approval2", "merge_candidate": "merged1", "merge_target_pre_head": "merged1", "merge_head": "merged2",
			"merge_retirements": `{"foreign":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval2"),
		event(t, "successor2", agent, SchemaState, State{Kind: KindArtifact, Text: "second successor", Body: map[string]string{"path": "spike", "commit": "merged2"}}, "merge2"),
		event(t, "retire-foreign", agent, SchemaSupersede, Supersede{Target: "foreign", Text: "merge succession"}, "foreign", "merge2", "successor2"),
	)
	projection := Fold(records)
	// With end-of-log topology the late edge does condemn the plan: the
	// successor is world-stale now. That is the fixture's proof that position
	// is the only thing separating the projection's answer from the verdict's,
	// so an admitted receipt below is evidence of the temporal rule and not of
	// a chain that condemns nothing.
	successor := artifactByEvent(t, projection, "successor")
	if !successor.DescribesSupersededWorld {
		t.Fatal("the late succession does not condemn the plan even with end-of-log topology, so this fixture cannot show a post-verdict edge reaching back")
	}
	decision, found := projection.Decision("retire-foreign")
	if !found {
		t.Fatal("the cross-author supersession was never decided, so nothing here reached the receipt boundary at all")
	}
	if decision.Verdict != Effective {
		t.Fatalf("a successor edge declared after the verdict reached back, condemned the plan at it, and revoked receipt authority the reviewer's signature had earned: %+v", decision)
	}
}

// A successor artifact filed after the verdict must not complete a chain at
// it. Record IDs can be pre-signed, so a supersession filed before the verdict
// can rest on an ID the log does not yet contain: at the verdict the chain
// from the plan's predecessor ends at the interim pointer, condemned, so the
// reviewed successor flares and the receipt boundary must refuse. Resolving
// that basis with end-of-log knowledge instead adds the successor edge
// retroactively — the supersession is positioned but the thing it points at
// is not — and the condemned chain is quietly rescued by an artifact the
// reviewer could not have seen, minting receipt authority from a post-verdict
// fact. The companion test above moves the supersession past the verdict;
// this one moves the basis, the half a record-only cutoff does not filter.
func TestABasisMaterialisedAfterTheVerdictDoesNotServeAsASuccessorAtIt(t *testing.T) {
	records := reviewRecords(t,
		event(t, "predecessor", operator, SchemaState, State{Kind: KindArtifact, Text: "the pointer the first merge replaces", Body: map[string]string{"path": "spike", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged1",
			"merge_retirements": `{"predecessor":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged1"}}, "merge", "predecessor"),
		// The predecessor's behaviour moves to an interim pointer at the same
		// path, which is then condemned resting on a pre-signed ID that
		// resolves to nothing the log yet contains. At the verdict below the
		// chain ends here, condemned.
		event(t, "interim", operator, SchemaState, State{Kind: KindArtifact, Text: "an interim pointer", Body: map[string]string{"path": "spike", "commit": "interim"}}, "r0"),
		event(t, "move-predecessor", operator, SchemaSupersede, Supersede{Target: "predecessor", Text: "moved to the interim pointer"}, "predecessor", "interim"),
		event(t, "condemn-interim", operator, SchemaSupersede, Supersede{Target: "interim", Text: "condemned, naming a successor not yet filed"}, "interim", "condemned-late"),
		// The second review, of the first merge's successor.
		event(t, "request2", operator, SchemaState, State{Kind: KindRequest, Text: "review the successor", Body: map[string]string{"to": other, "conditions": "exact head"}}, "successor"),
		event(t, "promise2", other, SchemaState, State{Kind: KindPromise, Text: "will review"}, "request2"),
		event(t, "foreign", operator, SchemaState, State{Kind: KindArtifact, Text: "someone else's pointer", Body: map[string]string{"path": "spike", "commit": "other-base"}}, "r0"),
		event(t, "approval2", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "merged1", "artifact": "successor"}}, "promise2", "successor"),
		event(t, "approval2-ratified", operator, SchemaRatify, Ratify{Target: "approval2"}, "approval2"),
		// After the verdict: the pre-signed ID materialises as an artifact at
		// the same path, completing the chain with end-of-log knowledge.
		event(t, "condemned-late", operator, SchemaState, State{Kind: KindArtifact, Text: "the pre-signed successor, filed late", Body: map[string]string{"path": "spike", "commit": "late"}}, "r0"),
		event(t, "merge2", agent, SchemaState, State{Kind: KindAssert, Text: "second merge", Body: map[string]string{
			"merge_approval": "approval2", "merge_candidate": "merged1", "merge_target_pre_head": "merged1", "merge_head": "merged2",
			"merge_retirements": `{"foreign":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval2"),
		event(t, "successor2", agent, SchemaState, State{Kind: KindArtifact, Text: "second successor", Body: map[string]string{"path": "spike", "commit": "merged2"}}, "merge2"),
		event(t, "retire-foreign", agent, SchemaSupersede, Supersede{Target: "foreign", Text: "merge succession"}, "foreign", "merge2", "successor2"),
	)
	projection := Fold(records)
	moved, foundMoved := projection.Decision("move-predecessor")
	condemned, foundCondemned := projection.Decision("condemn-interim")
	if !foundMoved || moved.Verdict != Effective || !foundCondemned || condemned.Verdict != Effective {
		t.Fatalf("fixture wrong: move-predecessor found=%v %+v, condemn-interim found=%v %+v — without both supersessions there is no chain to complete",
			foundMoved, moved, foundCondemned, condemned)
	}
	verdict := statementByEvent(t, projection, "approval2")
	late := statementByEvent(t, projection, "condemned-late")
	if !(condemned.Sequence < verdict.Sequence && verdict.Sequence < late.Sequence) {
		t.Fatalf("fixture wrong: condemnation=%d verdict=%d basis=%d — the supersession must precede the verdict and its basis must follow it, or there is no forward reference here",
			condemned.Sequence, verdict.Sequence, late.Sequence)
	}
	// With end-of-log knowledge the materialised basis genuinely serves as
	// the interim pointer's successor and the completed chain rescues the
	// plan: the first merge's successor is not world-stale now. That is the
	// fixture's proof that the edge is real and position is the only thing
	// separating the projection's answer from the verdict's.
	if !artifactByEvent(t, projection, "interim").Succeeded {
		t.Fatal("the materialised basis never became a successor even with end-of-log knowledge, so this fixture cannot show a forward reference reaching back")
	}
	if artifactByEvent(t, projection, "successor").DescribesSupersededWorld {
		t.Fatal("the completed chain does not rescue the plan even with end-of-log knowledge, so a refused receipt below would prove nothing about position")
	}
	decision, found := projection.Decision("retire-foreign")
	if !found {
		t.Fatal("the cross-author supersession was never decided, so nothing here reached the receipt boundary at all")
	}
	if decision.Verdict == Effective {
		t.Fatalf("a basis filed after the verdict completed the chain at it, rescued the condemned plan, and minted receipt authority from a fact the reviewer could not have seen: %+v", decision)
	}
}
