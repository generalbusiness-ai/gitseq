package github

import "testing"

const connectorActor = "connector-fingerprint"

func issue() Issue {
	return Issue{
		Owner: "generalbusiness-ai", Repo: "gitseq", Number: 7,
		Title:  "gs init does not say which directory it wrote to",
		Author: "someone", URL: "https://github.com/generalbusiness-ai/gitseq/issues/7",
	}
}

// The property the whole inbound half rests on: observing the same issue twice
// must produce one act, not two. The kernel enforces this through the dedup key,
// and the dedup key is only as stable as the idempotency key we hand it.
func TestObservingTheSameIssueTwiceUsesOneKey(t *testing.T) {
	first := ObserveIssue(issue())
	second := ObserveIssue(issue())
	if first.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("same issue produced different keys: %q and %q", first.IdempotencyKey, second.IdempotencyKey)
	}
	// The namespace is a prefix on the key rather than the kernel's idempotency
	// namespace, which is set per workroom. Without it the connector's
	// observation of an object could collide with a later connector operation
	// naming the same one.
	if first.IdempotencyKey != Namespace+":generalbusiness-ai/gitseq#7" {
		t.Errorf("key is %q, want the namespaced external identifier", first.IdempotencyKey)
	}
}

// Editing an issue must not reopen the duplicate. A key derived from the title,
// the body, or an updated-at stamp would pass the test above and still append a
// second act the first time somebody fixes a typo.
func TestEditingAnIssueDoesNotChangeItsKey(t *testing.T) {
	before := ObserveIssue(issue())
	edited := issue()
	edited.Title = "gs init does not say where it wrote"
	edited.Body = "expanded description"
	after := ObserveIssue(edited)
	if before.IdempotencyKey != after.IdempotencyKey {
		t.Fatalf("editing changed the key from %q to %q", before.IdempotencyKey, after.IdempotencyKey)
	}
}

// The foreign principal is data in the body. It is never an identity, and the
// connector never signs as them.
func TestObservationCarriesThePrincipalAsData(t *testing.T) {
	observation := ObserveIssue(issue())
	if got := observation.Body["on_behalf_of"]; got != "someone" {
		t.Errorf("on_behalf_of is %q, want the GitHub login", got)
	}
	if got := observation.Body["source"]; got != "github" {
		t.Errorf("source is %q, want github", got)
	}
	if got := observation.Body["external_id"]; got != "generalbusiness-ai/gitseq#7" {
		t.Errorf("external_id is %q", got)
	}
}

// The connector keeps no database. Its memory is a fold over durable acts, so a
// restart with an empty disk reconstructs exactly what it knew.
func TestCorrespondenceFoldsFromDurableActs(t *testing.T) {
	seen := Fold([]Statement{
		{Event: "git:sha1:g#git:sha1:aaa", Actor: connectorActor, Effective: true, Body: map[string]string{"source": "github", "external_id": "generalbusiness-ai/gitseq#7"}},
		{Event: "git:sha1:g#git:sha1:bbb", Actor: connectorActor, Effective: true, Body: map[string]string{"source": "slack", "external_id": "C123/456"}},
		{Event: "git:sha1:g#git:sha1:ccc", Actor: connectorActor, Effective: true, Body: map[string]string{"text": "an ordinary act with no source"}},
	}, connectorActor)
	if got := seen["generalbusiness-ai/gitseq#7"]; got != "git:sha1:g#git:sha1:aaa" {
		t.Errorf("issue 7 maps to %q, want the observing event", got)
	}
	if _, exists := seen["C123/456"]; exists {
		t.Error("a foreign system that is not github leaked into the github correspondence")
	}
	if len(seen) != 1 {
		t.Errorf("correspondence has %d entries, want 1", len(seen))
	}
}

// An act naming the same object twice does not move the correspondence. The
// first observation is the one that happened.
func TestFoldKeepsTheFirstObservation(t *testing.T) {
	seen := Fold([]Statement{
		{Event: "git:sha1:g#git:sha1:first", Actor: connectorActor, Effective: true, Body: map[string]string{"source": "github", "external_id": "o/r#1"}},
		{Event: "git:sha1:g#git:sha1:second", Actor: connectorActor, Effective: true, Body: map[string]string{"source": "github", "external_id": "o/r#1"}},
	}, connectorActor)
	if got := seen["o/r#1"]; got != "git:sha1:g#git:sha1:first" {
		t.Errorf("correspondence points at %q, want the first observation", got)
	}
}

// Re-polling a repository must not resubmit what is already in the log.
func TestUnobservedSkipsWhatTheLogAlreadyHolds(t *testing.T) {
	one := ObserveIssue(issue())
	other := issue()
	other.Number = 8
	two := ObserveIssue(other)

	seen := Fold([]Statement{
		{Event: "git:sha1:g#git:sha1:aaa", Actor: connectorActor, Effective: true, Body: map[string]string{"source": "github", "external_id": one.ExternalID}},
	}, connectorActor)
	fresh := Unobserved([]Observation{one, two}, seen)
	if len(fresh) != 1 {
		t.Fatalf("got %d fresh observations, want 1", len(fresh))
	}
	if fresh[0].ExternalID != two.ExternalID {
		t.Errorf("kept %q, want the unobserved issue", fresh[0].ExternalID)
	}
}

// A second poll with nothing new must submit nothing at all.
func TestASecondPollSubmitsNothing(t *testing.T) {
	observation := ObserveIssue(issue())
	seen := Fold([]Statement{
		{Event: "git:sha1:g#git:sha1:aaa", Actor: connectorActor, Effective: true, Body: map[string]string{"source": "github", "external_id": observation.ExternalID}},
	}, connectorActor)
	if fresh := Unobserved([]Observation{observation}, seen); len(fresh) != 0 {
		t.Errorf("a repeat poll produced %d observations, want none", len(fresh))
	}
}

func TestForeignStatementCannotSuppressTheConnectorObservation(t *testing.T) {
	observation := ObserveIssue(issue())
	seen := Fold([]Statement{
		{
			Event:     "git:sha1:g#git:sha1:forged",
			Actor:     "ordinary-participant",
			Effective: true,
			Body: map[string]string{
				"source": "github", "external_id": observation.ExternalID,
			},
		},
	}, connectorActor)

	fresh := Unobserved([]Observation{observation}, seen)
	if len(fresh) != 1 || fresh[0].ExternalID != observation.ExternalID {
		t.Fatalf("foreign statement suppressed the genuine observation: %+v", fresh)
	}
}
