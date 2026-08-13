package workroom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

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
	// The request column now carries the number rather than the raw event id.
	// That is the change under test elsewhere in this file, and the expectation
	// is updated deliberately rather than loosened: what this test is actually
	// about is that an unclaimed request is rendered as addressed and waiting
	// on nobody, and both of those still read exactly as before.
	if !bytes.Contains(page, []byte("| open |  | actor:operator | addressed to actor:agent — unclaimed | #4 |  |")) {
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
		event(t, "e1", operator, SchemaState, activation),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
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
		event(t, "d0", operator, SchemaState, kindDefinitionState(t, undertaking)),
		event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, delivery)),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "p0", agent, SchemaState, State{Kind: "undertaking", Text: "no request"}),
		event(t, "x0", agent, SchemaState, State{Kind: "delivery", Text: "no promise"}),
	})
	for eventID, reason := range map[string]string{
		"p0": "promise lifecycle basis count is 0, want exactly one request",
		"x0": "report lifecycle basis count is 0, want exactly one promise",
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
		event(t, "e1", operator, SchemaState, activation),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
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
// survive intermediate hops. The distinction is the whole point: only this
// kind of staleness means go and re-read the code.
func TestRetiredArtifactMarksDependentsAsDescribingASupersededWorld(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI reference page", Body: map[string]string{"path": "docs/reference/gs.md", "commit": "bbb222"}}, "w3"),
		event(t, "w5", agent, SchemaState, State{Kind: KindAssert, Text: "The reference documents every subcommand"}, "w4"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation, replacing aaa111", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ccc333"}}, "w0"),
		event(t, "w7", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ccc333"}, "w3", "w6"),
	)
	projection := Fold(records)

	page := artifactByEvent(t, projection, "w4")
	if !page.Stale || !page.DescribesSupersededWorld {
		t.Fatalf("page resting on a retired artifact: stale=%v world=%v", page.Stale, page.DescribesSupersededWorld)
	}
	claim := statementByEvent(t, projection, "w5")
	if !claim.Stale || !claim.DescribesSupersededWorld {
		t.Fatalf("second hop lost the distinction: stale=%v world=%v", claim.Stale, claim.DescribesSupersededWorld)
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

// The same bound from the other side. The implementer really did merge, but a
// receipt reaches only what its merge republished: a target the plan does not
// cover takes no authority from it, whatever the plan claims.
func TestMergeReceiptReachesOnlyWhatItsSuccessorCovers(t *testing.T) {
	records := reviewRecords(t,
		event(t, "victim", operator, SchemaState, State{Kind: KindArtifact, Text: "somewhere else entirely", Body: map[string]string{"path": "docs", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements": `{"victim":"spike"}`, "merge_successors": `["spike"]`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "current implementation", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
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
	want := "3 artifacts across 2 paths follow a live artifact at the same path without superseding it; supersessions still owed: 3"
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
// prints a hash fragment instead of #N. Git object identifiers are exempt and
// must stay exempt — a commit abbreviation resolves back through git, an event
// abbreviation resolves back through nothing.
func TestRenderedSurfacesNameEventsByNumber(t *testing.T) {
	projection := golden(t)
	if len(projection.Commitments) == 0 || len(projection.Artifacts) == 0 {
		t.Fatal("the golden log has no commitments or artifacts, so this proves nothing")
	}
	rendered := string(RenderStatus(projection))

	commits := map[string]bool{}
	for _, artifact := range projection.Artifacts {
		commits[artifact.Commit] = true
	}
	for _, review := range projection.Reviews {
		commits[review.Head] = true
	}

	// Every abbreviation the renderer emitted, checked against what it is
	// allowed to abbreviate. An actor fingerprint and a git commit may be
	// elided; an event may not.
	sequences := projection.sequences()
	for _, event := range eventsOn(projection) {
		if commits[event] {
			continue
		}
		// An event with no number is named by its identifier in full, and that
		// is correct, so its full form appearing in the output is not a defect
		// to detect. Skipping it keeps this test measuring the one thing it is
		// for: an abbreviation standing where a number belongs.
		if sequences[event] == 0 {
			continue
		}
		if abbreviated := short(event); strings.Contains(rendered, abbreviated) {
			t.Errorf("the status renderer prints %s as %q instead of its number", event, abbreviated)
		}
	}

	// And the number is actually there, so the test cannot pass by rendering
	// nothing at all.
	if !strings.Contains(rendered, "#") {
		t.Error("no event was named by number anywhere in the rendered status")
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
