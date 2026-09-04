package workroom

import (
	"math"
	"testing"
)

// The receipt checkpoint, case by case. Every fixture here is the same merge
// with one fact changed, and each test names the single condition it is holding
// down. Fixtures that share a condition say so in their comment rather than
// letting the count imply coverage that is not there.

// prospectiveReceipt is the shape the checkpoint recognises: an authorized
// retirement plan carrying both accounting fields, with a canonical frontier.
func prospectiveReceipt(t *testing.T, leftLive string) Record {
	t.Helper()
	return leftLiveReceipt(t, "merge", "approval", "head1", "merged", `{"r5":"spike"}`, leftLive)
}

// oldCause retires the review request the approval's promise stands on. It is
// an ordinary reasoning retirement, not an artifact retirement, so it makes the
// approval and the receipt stale without moving any world. Filed before the
// receipt, it is exactly what the merge settled.
func oldCause(t *testing.T) Record {
	t.Helper()
	return event(t, "retire-review-request", operator, SchemaSupersede,
		Supersede{Target: "r6", Text: "the review request was refiled before this merge"}, "r6")
}

// newCause withdraws the approval after the merge sealed. Nothing about the
// merge accounted for it.
func newCause(t *testing.T) Record {
	t.Helper()
	return event(t, "retire-approval", other, SchemaSupersede,
		Supersede{Target: "approval", Text: "review withdrawn after the merge"}, "approval")
}

// checkpointRecords assembles one merge: the approval and its ratification,
// whatever happened before the receipt, the receipt itself, the successor it
// publishes, the planned retirement of the reviewed candidate, and whatever
// happened afterwards.
func checkpointRecords(t *testing.T, receipt Record, before []Record, after ...Record) []Record {
	t.Helper()
	tail := []Record{
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved",
			Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
	}
	tail = append(tail, before...)
	tail = append(tail, receipt,
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation",
			Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-r5", agent, SchemaSupersede,
			Supersede{Target: "r5", Text: "the merge replaced the reviewed candidate"}, "r5", "merge", "successor"))
	return reviewRecords(t, append(tail, after...)...)
}

// requireStaleReceipt refuses to let a test pass on an inert fixture. Every
// case below is about a receipt that really is stale; if it is not, a fresh
// successor proves nothing at all.
func requireStaleReceipt(t *testing.T, projection Projection) {
	t.Helper()
	if !statementByEvent(t, projection, "merge").Stale {
		t.Fatal("inert fixture: the receipt is not stale, so nothing is being restored or withheld")
	}
}

// Case 1, stale-at-birth restoration. The condition held down is the checkpoint
// itself: the edge from a sealed receipt to the successor it published stops
// carrying ordinary staleness the merge already answered for. Both halves are
// asserted, because a rule that also cleared the receipt would be a different
// and wrong rule: the receipt stays historically stale, and only the successor
// begins the new epoch.
func TestReceiptCheckpointRestoresASuccessorThatWasStaleAtBirth(t *testing.T) {
	projection := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), []Record{oldCause(t)}))
	requireStaleReceipt(t, projection)
	if !artifactByEvent(t, projection, "r5").Retired {
		t.Fatal("the reviewed candidate was not retired, so this is not the merge shape at all")
	}
	successor := artifactByEvent(t, projection, "successor")
	if successor.Stale || successor.DescribesSupersededWorld {
		t.Fatalf("successor published by the merge: stale=%v world=%v, want a fresh current implementation",
			successor.Stale, successor.DescribesSupersededWorld)
	}
	// The predecessor's own row is untouched: the checkpoint is about freshness,
	// never about who is live.
	if artifactByEvent(t, projection, "successor").Retired {
		t.Fatal("the checkpoint changed the successor's live state")
	}
}

// Case 2, a valid paired prospective receipt, together with cases 9 and 10, the
// malformed half-pair and the legacy receipt without the pair. One fixture with
// one field moved, so the same staleness has opposite answers either side of
// the version seam.
//
// The conditions are named per row. "left-live absent" is held down by the
// merge_left_live presence test alone; "frontier absent" and "frontier not
// canonical" are both held down by the canonical-frontier test, and share it.
// "neither field" is caught by both tests at once and pins neither on its own —
// it is here because a legacy receipt keeping its old projection is the
// compatibility promise, not because it adds a distinct guard.
func TestReceiptCheckpointRequiresBothProspectiveFields(t *testing.T) {
	receipt := func(body map[string]string) Record {
		full := map[string]string{
			"merge_approval": "approval", "merge_candidate": "head1", "merge_target_pre_head": "base",
			"merge_head": "merged", "merge_retirements": `{"r5":"spike"}`, "merge_successors": `["spike"]`,
		}
		for key, value := range body {
			full[key] = value
		}
		return event(t, "merge", agent, SchemaState, State{Kind: KindAssert, Text: "approved candidate merged", Body: full}, "approval")
	}
	for _, test := range []struct {
		name  string
		body  map[string]string
		fresh bool
	}{
		{name: "prospective pair", body: map[string]string{"merge_changed_paths": `["spike"]`, "merge_left_live": `{}`}, fresh: true},
		{name: "left-live absent", body: map[string]string{"merge_changed_paths": `["spike"]`}},
		{name: "frontier absent", body: map[string]string{"merge_left_live": `{}`}},
		{name: "frontier not canonical", body: map[string]string{"merge_changed_paths": `["spike","alpha"]`, "merge_left_live": `{}`}},
		{name: "neither field", body: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := Fold(checkpointRecords(t, receipt(test.body), []Record{oldCause(t)}))
			requireStaleReceipt(t, projection)
			if successor := artifactByEvent(t, projection, "successor"); successor.Stale == test.fresh {
				t.Fatalf("successor stale=%v, want stale=%v", successor.Stale, !test.fresh)
			}
		})
	}
}

// Case 3, later approval retirement. The condition is the date comparison: a
// cause the receipt's own plan does not name, arising after the receipt's
// position, is news the merge never saw and still flares.
func TestReceiptCheckpointDoesNotSettleACauseThatAroseAfterTheReceipt(t *testing.T) {
	projection := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), nil, newCause(t)))
	requireStaleReceipt(t, projection)
	if successor := artifactByEvent(t, projection, "successor"); !successor.Stale {
		t.Fatal("a withdrawal filed after the merge was hidden from the successor it published")
	}
}

// Case 4, mixed old-plus-new causes. It shares the date comparison with case 3,
// and is here for what case 3 cannot show: this is the fixture that separates a
// per-cause dated walk from the cheap question "was the receipt already stale
// as of its own position". With an old cause and a new one both live, the
// receipt was stale then and is stale now, so the cheap comparison settles the
// new cause along with the old one and calls this successor fresh.
func TestReceiptCheckpointSettlesOnlyTheOldHalfOfMixedCauses(t *testing.T) {
	records := checkpointRecords(t, prospectiveReceipt(t, `{}`), []Record{oldCause(t)}, newCause(t))
	projection := Fold(records)
	requireStaleReceipt(t, projection)
	// The old cause is genuinely settled: with it alone the successor is fresh,
	// which is what makes the stale answer below attributable to the new cause.
	fresh := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), []Record{oldCause(t)}))
	if artifactByEvent(t, fresh, "successor").Stale {
		t.Fatal("control fixture is not fresh, so the mixed answer proves nothing")
	}
	if successor := artifactByEvent(t, projection, "successor"); !successor.Stale {
		t.Fatal("the new cause was settled along with the old one")
	}
}

// Case 5, receipt retirement. The condition is that the dated walk starts at
// the receipt itself, so the receipt's own withdrawal is the first cause it
// weighs and the successor is told exactly as it would be about any other
// unsettled cause. Walking the receipt's bases instead, which is the obvious
// alternative shape, loses this case and nothing else.
func TestReceiptCheckpointDoesNotSurviveTheReceiptsOwnRetirement(t *testing.T) {
	retire := event(t, "retire-merge", agent, SchemaSupersede,
		Supersede{Target: "merge", Text: "the receipt itself was withdrawn"}, "merge")
	projection := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), []Record{oldCause(t)}, retire))
	if !statementByEvent(t, projection, "merge").Retired {
		t.Fatal("the receipt was not retired, so this fixture tests nothing")
	}
	if successor := artifactByEvent(t, projection, "successor"); !successor.Stale {
		t.Fatal("a successor kept its checkpoint after its own receipt was withdrawn")
	}
}

// Case 6, unrelated receipt borrowers. Two conditions, one per sub-fixture: the
// successor must be signed by the receipt's author, and it must stand at the
// receipt's exact head and at a path the receipt declared. Both facts come from
// records the borrower did not write.
func TestReceiptCheckpointIsNotBorrowedByRecordsTheMergeDidNotPublish(t *testing.T) {
	borrowers := []Record{
		event(t, "foreign", other, SchemaState, State{Kind: KindArtifact, Text: "another actor at the same head",
			Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "stray", agent, SchemaState, State{Kind: KindArtifact, Text: "the merger, at an undeclared path",
			Body: map[string]string{"path": "elsewhere", "commit": "merged"}}, "merge"),
	}
	projection := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), []Record{oldCause(t)}, borrowers...))
	requireStaleReceipt(t, projection)
	if artifactByEvent(t, projection, "successor").Stale {
		t.Fatal("the genuine successor lost its checkpoint, so the borrowers below prove nothing")
	}
	for _, id := range []string{"foreign", "stray"} {
		if !artifactByEvent(t, projection, id).Stale {
			t.Fatalf("%s borrowed a checkpoint from a merge that did not publish it", id)
		}
	}
}

// Case 7, world-stale chains. The condition is that the checkpoint is confined
// to the one basis it is about. Every other basis of the same successor is
// examined exactly as before, so a retired artifact under it still says the
// world moved, and still says so to the page above.
func TestReceiptCheckpointLeavesWorldStalenessOnOtherBasesAlone(t *testing.T) {
	records := reviewRecords(t,
		event(t, "notes", agent, SchemaState, State{Kind: KindArtifact, Text: "a described implementation",
			Body: map[string]string{"path": "notes", "commit": "n0"}}, "r0"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved",
			Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		event(t, "approval-ratified", operator, SchemaRatify, Ratify{Target: "approval"}, "approval"),
		oldCause(t),
		prospectiveReceipt(t, `{}`),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation",
			Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge", "notes"),
		event(t, "retire-r5", agent, SchemaSupersede,
			Supersede{Target: "r5", Text: "the merge replaced the reviewed candidate"}, "r5", "merge", "successor"),
		event(t, "page", agent, SchemaState, State{Kind: KindArtifact, Text: "a page over the successor",
			Body: map[string]string{"path": "docs/page", "commit": "merged"}}, "successor"),
		event(t, "notes-next", agent, SchemaState, State{Kind: KindArtifact, Text: "the described implementation moved",
			Body: map[string]string{"path": "notes", "commit": "n1"}}, "r0"),
		event(t, "retire-notes", agent, SchemaSupersede,
			Supersede{Target: "notes", Text: "behaviour replaced at n1"}, "notes", "notes-next"),
	)
	projection := Fold(records)
	requireStaleReceipt(t, projection)
	successor := artifactByEvent(t, projection, "successor")
	if !successor.Stale || !successor.DescribesSupersededWorld {
		t.Fatalf("successor over a retired artifact: stale=%v world=%v, want both", successor.Stale, successor.DescribesSupersededWorld)
	}
	page := artifactByEvent(t, projection, "page")
	if !page.Stale || !page.DescribesSupersededWorld {
		t.Fatalf("page over that successor: stale=%v world=%v, want both", page.Stale, page.DescribesSupersededWorld)
	}
}

// Case 8, condemned successions. The condition is that the receipt's plan is
// narrowed before it is read as settlement: a planned retirement whose
// successor chain was later condemned is news to the receipt, and to the
// successor with it. Without the narrowing the plan still names the reviewed
// candidate and the merge would go on hiding a retirement that answered for
// nothing.
func TestReceiptCheckpointFlaresWhenAPlannedSuccessionIsCondemned(t *testing.T) {
	condemn := event(t, "retire-successor", agent, SchemaSupersede,
		Supersede{Target: "successor", Text: "the published successor was withdrawn with nothing in its place"}, "successor")
	projection := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), nil, condemn))
	requireStaleReceipt(t, projection)
	if successor := artifactByEvent(t, projection, "successor"); !successor.Stale {
		t.Fatal("a condemned succession kept its checkpoint")
	}
	// Without the condemnation the same fixture is fresh, so the answer above is
	// attributable to the condemnation and not to the plan being unreadable.
	live := Fold(checkpointRecords(t, prospectiveReceipt(t, `{}`), nil))
	if artifactByEvent(t, live, "successor").Stale {
		t.Fatal("the uncondemned control is stale, so the condemned answer proves nothing")
	}
}

// Case 11, left-live testimony orthogonality. The condition here is the absence
// of one: the gate reads that the pair is present and the frontier canonical,
// and never reads whether an individual claim verified. Testimony about other
// people's candidates must not decide this artifact's freshness, so the fixture
// carries a claim the fold refuses and expects the checkpoint anyway.
func TestReceiptCheckpointIgnoresUnverifiedLeftLiveTestimony(t *testing.T) {
	unverified := `{"ghost":{"class":"sibling","commitment":"no-such-commitment"}}`
	projection := Fold(checkpointRecords(t, prospectiveReceipt(t, unverified), []Record{oldCause(t)}))
	requireStaleReceipt(t, projection)
	testimony := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(testimony) == 0 {
		t.Fatal("no testimony was projected, so this fixture does not test orthogonality")
	}
	for _, entry := range testimony {
		if entry.Verified {
			t.Fatalf("testimony verified after all, so the fixture is inert: %+v", entry)
		}
	}
	if successor := artifactByEvent(t, projection, "successor"); successor.Stale {
		t.Fatal("unverified left-live testimony withheld the checkpoint")
	}
}

// Not on the ruling's list, and here because the gate would otherwise be a
// signature nobody checks. Every merge_* field is written by the actor asking
// for the checkpoint, so an assert carrying the prospective pair and a
// plausible plan must earn nothing until the independent approval chain has
// been validated. The condition is the authorized plan: this fixture leaves the
// approval unratified, which is the one thing the receipt's signer cannot fix.
//
// Through the log the two are one answer, because the fold parses the
// accounting pair only after the plan is sealed, so an unauthorized receipt is
// never prospective either. The second half separates them: the prospective
// flags are forced on over a nil plan, which is the state a future change to
// that parse order would produce.
func TestReceiptCheckpointRequiresAnAuthorizedPlan(t *testing.T) {
	records := reviewRecords(t,
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved",
			Body: map[string]string{"verdict": "approved", "head": "head1", "artifact": "r5"}}, "reviewer-promise", "r5"),
		oldCause(t),
		prospectiveReceipt(t, `{}`),
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "merged implementation",
			Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
	)
	projection := Fold(records)
	requireStaleReceipt(t, projection)
	if statementByEvent(t, projection, "merge").MergeLeftLive != nil {
		t.Fatal("an unauthorized receipt was read as prospective, so the fixture is not the one described")
	}
	if successor := artifactByEvent(t, projection, "successor"); !successor.Stale {
		t.Fatal("a receipt with no validated plan minted a checkpoint for its own signer")
	}

	state := NewFolder(records).state
	scope := state.stalenessAsOf(math.MaxInt)
	stale := scope.staleness(scope.succeededRetirements()).stale
	receipt, successor := state.byID["merge"], state.byID["successor"]
	if receipt.mergePlan != nil {
		t.Fatal("the receipt earned a plan after all, so the forced flags below test nothing")
	}
	receipt.mergeLeftLivePresent, receipt.mergeChangedPathsValid = true, true
	if scope.receiptCheckpointSettles(successor, receipt, stale, scope.succeededRetirements()) {
		t.Fatal("a prospective-looking receipt with no authorized plan settled its signer's successor")
	}
}

// Case 12, an undated cause. The ruling asks for a dated ordinary-cause
// computation with undated causes failing closed, and this is that branch.
//
// It is reached directly rather than through Fold, and deliberately so: the
// fold withdraws a superseded supersession from the retirement counter in the
// same pass that dates it, so a live retirement with no date is a state the
// append path does not produce. That makes the branch a guard against a future
// caller, not against today's log, and the only honest way to hold it down is
// to hand the scope the state it guards against.
func TestReceiptCheckpointRefusesACauseItCannotDate(t *testing.T) {
	records := checkpointRecords(t, prospectiveReceipt(t, `{}`), []Record{oldCause(t)})
	state := NewFolder(records).state
	scope := state.stalenessAsOf(math.MaxInt)
	stale := scope.staleness(scope.succeededRetirements()).stale

	receipt := state.byID["merge"]
	successor := state.byID["successor"]
	if !scope.receiptCheckpointSettles(successor, receipt, stale, scope.succeededRetirements()) {
		t.Fatal("the dated fixture does not settle, so removing the date proves nothing")
	}
	if scope.active["r6"] == 0 {
		t.Fatal("the cause under test is already undated, so the fixture is inert")
	}
	delete(scope.active, "r6")
	if scope.receiptCheckpointSettles(successor, receipt, stale, scope.succeededRetirements()) {
		t.Fatal("a live retirement the scope cannot date was read as settled")
	}
}
