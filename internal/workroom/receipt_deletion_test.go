package workroom

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A merge that deletes a file has no successor artifact at the deleted path,
// so the reviewed-path cut that bounds cross-author retirement authority never
// reaches the deleted predecessor. The receipt still names it in the plan it
// signs, and its author still retires it. Reading the narrow authority map as
// the whole plan reported that classified, completed deletion as an omission.
func TestMergeAccountingOwnAuthorDeletionIsClassifiedByItsReceipt(t *testing.T) {
	records := ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
	projection := Fold(records)

	if accounting := statementByEvent(t, projection, "merge").MergeLeftLive; len(accounting) != 0 {
		t.Fatalf("classified own-author deletion was reported as left live: %+v", accounting)
	}
	if decision, _ := projection.Decision("retire-gone-old"); decision.Verdict != Effective || decision.Reason != "authorized supersession" {
		t.Fatalf("author retirement of the deleted file = %+v", decision)
	}
	if deleted := artifactByEvent(t, projection, "gone-old"); !deleted.Retired {
		t.Fatalf("deleted predecessor is not retired: %+v", deleted)
	}
	if covering := artifactByEvent(t, projection, "dir-successor"); covering.LivePredecessors != 0 || covering.SuccessionUnrecorded {
		t.Fatalf("classified deletion left a cleanup obligation on its covering successor: %+v", covering)
	}
	if status := string(RenderStatus(projection)); strings.Contains(status, "not classified by receipt") {
		t.Fatalf("classified deletion still rendered as unclassified:\n%s", status)
	}
	// The narrow cross-author authority map is untouched: the deleted path is
	// outside every reviewed path, so the receipt authorizes nothing there and
	// the retirement had to come from the author.
	folder := NewFolder(records)
	if hasKey(folder.state.byID["merge"].mergePlan, "gone-old") {
		t.Fatal("the receipt's authority map reached the deleted path, so the fixture proves nothing")
	}
}

// The plan classified the deletion but nobody ever retired it. That is an
// unfinished merge, and the accounting must keep saying so.
func TestMergeAccountingDeclaredDeletionWithoutRetirementStaysVisible(t *testing.T) {
	projection := Fold(ownAuthorDeletionRecords(t))

	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "gone-old" || accounting[0].Reason != "not classified by receipt" {
		t.Fatalf("undone declared deletion = %+v", accounting)
	}
	if covering := artifactByEvent(t, projection, "dir-successor"); covering.LivePredecessors != 1 || !covering.SuccessionUnrecorded {
		t.Fatalf("undone declared deletion cleared its cleanup obligation: %+v", covering)
	}
}

// A receipt cannot mint retirement authority over a stranger's artifact
// outside the reviewed paths by writing its identifier into the plan. Here the
// merge signer's supersession is refused, so nothing is retired and the
// deletion is still owed.
func TestMergeAccountingUnreviewedCrossAuthorDeletionStaysVisible(t *testing.T) {
	records := ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
	records = withAuthor(t, records, "gone-old", operator)
	projection := Fold(records)

	if decision, _ := projection.Decision("retire-gone-old"); decision.Verdict != Ineffective || decision.Reason != "actor may not supersede target" {
		t.Fatalf("merge signer retired a stranger's unreviewed artifact: %+v", decision)
	}
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "gone-old" || accounting[0].Reason != "not classified by receipt" {
		t.Fatalf("unauthorized cross-author deletion claim = %+v", accounting)
	}
	if covering := artifactByEvent(t, projection, "dir-successor"); covering.LivePredecessors != 1 || !covering.SuccessionUnrecorded {
		t.Fatalf("unauthorized deletion claim cleared its cleanup obligation: %+v", covering)
	}
}

// A ratifier may retire anyone's artifact, so the same shape with a ratifier
// signing the retirement is accounted for. This is the other half of the
// authority rule, and it is the half a guard that only compared authors would
// silently drop.
func TestMergeAccountingRatifierDeletionIsClassifiedByItsReceipt(t *testing.T) {
	records := ownAuthorDeletionRecords(t, deletionRetirement(t, operator))
	records = withAuthor(t, records, "gone-old", other)
	projection := Fold(records)

	if decision, _ := projection.Decision("retire-gone-old"); decision.Verdict != Effective || decision.Reason != "authorized supersession" {
		t.Fatalf("ratifier retirement = %+v", decision)
	}
	if accounting := statementByEvent(t, projection, "merge").MergeLeftLive; len(accounting) != 0 {
		t.Fatalf("ratifier-retired deletion was reported as left live: %+v", accounting)
	}
}

// A retirement the fold refused records nothing, and a plan entry naming an
// event that is not an artifact reaches nothing. Neither may quiet the
// accounting.
func TestMergeAccountingIneffectiveAndMalformedDeletionClaimsStayVisible(t *testing.T) {
	// bystander is on no roster, so the supersession is refused before any
	// question of standing arises.
	refused := ownAuthorDeletionRecords(t, deletionRetirement(t, bystander))
	projection := Fold(refused)
	if decision, _ := projection.Decision("retire-gone-old"); decision.Verdict != Ineffective {
		t.Fatalf("the refused-retirement fixture was admitted: %+v", decision)
	}
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "gone-old" || accounting[0].Reason != "not classified by receipt" {
		t.Fatalf("refused retirement = %+v", accounting)
	}

	// The plan now names a report and an identifier no record carries, while
	// the real deletion goes unmentioned.
	malformed := ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
	malformed = withReceiptRetirements(t, malformed, `{"r5":"spike","approval":"","no-such-event":""}`)
	projection = Fold(malformed)
	accounting = statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "gone-old" || accounting[0].Reason != "not classified by receipt" {
		t.Fatalf("malformed plan entries quieted an unclassified deletion: %+v", accounting)
	}
}

// Only the empty successor is the deletion shape. A plan entry that names any
// other successor for the artifact is claiming a surviving destination, and a
// destination outside the reviewed paths carries no authority at all — so
// however effective the retirement that follows, the claim stays visible and
// the cleanup debt stays on the covering successor. Reading the plan as a set
// of identifiers rather than as signed successor strings would let any string
// at all, including one no tree could ever carry, pass for the deletion the
// repair exists to recognise.
func TestMergeAccountingNonEmptyDeclaredSuccessorStaysVisible(t *testing.T) {
	for _, successor := range []string{"spike", "../invented-successor", "gone/old.js", " "} {
		records := ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
		records = withReceiptRetirements(t, records,
			`{"r5":"spike","gone-old":`+quoteJSON(t, successor)+`}`)
		projection := Fold(records)

		// The retirement really is effective and really did retire it: the
		// only reason the entry survives is the successor the receipt signed.
		if decision, _ := projection.Decision("retire-gone-old"); decision.Verdict != Effective {
			t.Fatalf("successor %q: the author retirement fixture was refused: %+v", successor, decision)
		}
		if deleted := artifactByEvent(t, projection, "gone-old"); !deleted.Retired {
			t.Fatalf("successor %q: the deleted predecessor is not retired: %+v", successor, deleted)
		}
		accounting := statementByEvent(t, projection, "merge").MergeLeftLive
		if len(accounting) != 1 || accounting[0].Artifact != "gone-old" || accounting[0].Reason != "not classified by receipt" {
			t.Fatalf("successor %q quieted an unsupported claim: %+v", successor, accounting)
		}
		if covering := artifactByEvent(t, projection, "dir-successor"); covering.LivePredecessors != 1 || !covering.SuccessionUnrecorded {
			t.Fatalf("successor %q cleared the cleanup obligation: %+v", successor, covering)
		}
		if status := string(RenderStatus(projection)); !strings.Contains(status, "not classified by receipt") {
			t.Fatalf("successor %q dropped the warning from the render:\n%s", successor, status)
		}
	}
}

// The deletion shape is a JSON string, and only the empty one. A plan whose
// value for the artifact is null names no successor at all — and null is what
// a Go string decode silently turns into "", manufacturing the one claim the
// repair recognises. The plan is read with its value types intact, so a null
// entry stays visible with its cleanup debt however effective the retirement
// that follows, while the explicit empty string beside it still counts. Plans
// carrying numbers, booleans, arrays or objects never reach the accounting at
// all: validateMergeReceiptNow already rejects such a plan whole, so the
// receipt carries no retirement authority and publishes no accounting, and
// the typed read below declares nothing for them either.
func TestMergeAccountingNullDeclaredSuccessorStaysVisible(t *testing.T) {
	records := ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
	records = withReceiptRetirements(t, records, `{"r5":"spike","gone-old":null}`)
	projection := Fold(records)

	// The retirement really is effective and really did retire it: the only
	// reason the entry survives is that the receipt named no successor.
	if decision, _ := projection.Decision("retire-gone-old"); decision.Verdict != Effective {
		t.Fatalf("the author retirement fixture was refused: %+v", decision)
	}
	if deleted := artifactByEvent(t, projection, "gone-old"); !deleted.Retired {
		t.Fatalf("the deleted predecessor is not retired: %+v", deleted)
	}
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "gone-old" || accounting[0].Reason != "not classified by receipt" {
		t.Fatalf("a null successor was read as the deletion shape: %+v", accounting)
	}
	if covering := artifactByEvent(t, projection, "dir-successor"); covering.LivePredecessors != 1 || !covering.SuccessionUnrecorded {
		t.Fatalf("a null successor cleared the cleanup obligation: %+v", covering)
	}
	if status := string(RenderStatus(projection)); !strings.Contains(status, "not classified by receipt") {
		t.Fatalf("a null successor dropped the warning from the render:\n%s", status)
	}

	// The same plan with the explicit empty string is still the deletion
	// shape: the type check narrows, it does not disable.
	records = ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
	records = withReceiptRetirements(t, records, `{"r5":"spike","gone-old":""}`)
	if accounting := statementByEvent(t, Fold(records), "merge").MergeLeftLive; len(accounting) != 0 {
		t.Fatalf("the explicit empty successor stopped being accounted for: %+v", accounting)
	}
}

// The typed read is the one place the deletion shape is decided, so it is
// held to the whole category of values a signed plan can carry: exactly the
// entries whose value is the JSON empty string are deletions, and no string
// conversion, whitespace, non-string value or absent entry can produce one.
func TestDeclaredDeletionsReadOnlyTheEmptyStringSuccessor(t *testing.T) {
	plan := `{"empty":"","null":null,"zero":0,"one":1,"false":false,"true":true,"array":[],"object":{},"nested":[""],"keyed":{"":""},"path":"spike","space":" ","escape":"\u0000","second-empty":""}`
	receipt := &parsedRecord{body: &State{Kind: KindAssert, Body: map[string]string{"merge_retirements": plan}}}
	got := (&foldState{}).declaredDeletions(receipt)
	want := map[string]bool{"empty": true, "second-empty": true}
	if len(got) != len(want) {
		t.Fatalf("declared deletions = %v, want %v", got, want)
	}
	for artifact := range want {
		if !got[artifact] {
			t.Fatalf("declared deletions = %v, want %v", got, want)
		}
	}
	for _, artifact := range []string{"null", "zero", "one", "false", "true", "array", "object", "nested", "keyed", "path", "space", "escape", "absent"} {
		if got[artifact] {
			t.Fatalf("%q was declared a deletion: %v", artifact, got)
		}
	}
	for _, malformed := range []string{``, `null`, `[]`, `"gone-old"`, `{"gone-old":""`} {
		receipt := &parsedRecord{body: &State{Kind: KindAssert, Body: map[string]string{"merge_retirements": malformed}}}
		if got := (&foldState{}).declaredDeletions(receipt); len(got) != 0 {
			t.Fatalf("plan %q declared deletions %v", malformed, got)
		}
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// The accounting is published from fold state the resident builds one record
// at a time, so the incremental and whole-fold answers have to agree at every
// prefix — including the prefixes between the receipt and the retirement that
// settles it.
func TestMergeAccountingDeletionAgreesAcrossIncrementalFold(t *testing.T) {
	records := ownAuthorDeletionRecords(t, deletionRetirement(t, agent))
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

// ownAuthorDeletionRecords builds the shape the workroom actually produced: a
// merge that deletes one file, reviewed only at the surviving path, whose
// receipt names the deleted predecessor with the empty successor. The
// retirement, when there is one, lands after the receipt, exactly as gs merge
// writes it.
func ownAuthorDeletionRecords(t *testing.T, tail ...Record) []Record {
	t.Helper()
	return reviewRecords(t, append([]Record{
		event(t, "gone-old", agent, SchemaState, State{Kind: KindArtifact, Text: "bundle the merge deletes",
			Body: map[string]string{"path": "gone/old.js", "commit": "base"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved",
			Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base", "merge_head": "merged",
			"merge_retirements":   `{"r5":"spike","gone-old":""}`,
			"merge_successors":    `["gone","spike"]`,
			"merge_changed_paths": `["gone/old.js","spike"]`,
			"merge_left_live":     `{}`,
		}}, "approval"),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation",
			Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "dir-successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged directory",
			Body: map[string]string{"path": "gone", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede, Supersede{Target: "r5", Text: "merged"}, "r5", "merge", "successor"),
	}, tail...)...)
}

func deletionRetirement(t *testing.T, actor string) Record {
	t.Helper()
	return event(t, "retire-gone-old", actor, SchemaSupersede,
		Supersede{Target: "gone-old", Text: "merge deleted the file at its old path"}, "gone-old", "merge")
}

// withAuthor re-signs one record. Who authored the deleted artifact is the
// only difference between the own-author case and the cross-author one, so the
// fixtures differ by that alone.
func withAuthor(t *testing.T, records []Record, id, actor string) []Record {
	t.Helper()
	changed := append([]Record(nil), records...)
	for index := range changed {
		if changed[index].ID == id {
			changed[index].Actor = actor
			return changed
		}
	}
	t.Fatalf("no record %s to re-sign", id)
	return nil
}

func withReceiptRetirements(t *testing.T, records []Record, plan string) []Record {
	t.Helper()
	changed := append([]Record(nil), records...)
	for index := range changed {
		if changed[index].ID != "merge" {
			continue
		}
		var state State
		if err := json.Unmarshal(changed[index].Payload, &state); err != nil {
			t.Fatal(err)
		}
		state.Body["merge_retirements"] = plan
		changed[index] = event(t, "merge", changed[index].Actor, changed[index].Schema, state, changed[index].RestsOn...)
		return changed
	}
	t.Fatal("no merge receipt to rewrite")
	return nil
}
