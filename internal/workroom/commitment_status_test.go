package workroom

import "testing"

// One vocabulary, two readings, and the two defaults that must not converge.
func TestTheTwoCommitmentReadingsShareOneVocabulary(t *testing.T) {
	for status := range unsettledStatuses {
		if settledStatuses[status] {
			t.Errorf("%q is on both lists; a word cannot be open and closed at once", status)
		}
		if !UnsettledCommitment(status) || SettledCommitment(status) {
			t.Errorf("%q reads unsettled=%v settled=%v", status, UnsettledCommitment(status), SettledCommitment(status))
		}
	}
	for status := range settledStatuses {
		if !SettledCommitment(status) || UnsettledCommitment(status) {
			t.Errorf("%q reads settled=%v unsettled=%v", status, SettledCommitment(status), UnsettledCommitment(status))
		}
	}
	// The defaults are opposite on purpose. A word neither list knows is not
	// unsettled, so a settlement warning fires; and it is not settled, so a
	// deletion decision protects. Both answers are "no", and they mean
	// different things because the questions are different.
	const unknown = "some-future-status"
	if UnsettledCommitment(unknown) {
		t.Fatal("an unknown status read as unsettled; a settlement warning would never fire for it")
	}
	if SettledCommitment(unknown) {
		t.Fatal("an unknown status read as settled; a checkout holding it could be deleted")
	}
	// Staleness is not settlement.
	if !UnsettledCommitment("stale") || SettledCommitment("stale") {
		t.Fatal("staleness was read as settlement; a stale request's work is still owed")
	}
}
