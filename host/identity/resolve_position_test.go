package identity

import "testing"

// These focused tests isolate the recursive guard. Public integration tests
// cover the same boundaries through records, but a child anchor's own start
// and withdrawal can otherwise mask whether recursion checked its parent.
func TestEffectiveUsesParentAnchorPositionWhenTimestampsTie(t *testing.T) {
	parent := &anchorRecord{record: "parent", from: instant{position: 3, timestamp: 1000}}
	child := &anchorRecord{record: "child", parent: parent.record, from: instant{position: 1, timestamp: 1000}}
	resolution := &Resolution{byRecord: map[string]*anchorRecord{parent.record: parent}}

	if resolution.effective(child, instant{position: 2, timestamp: 1000}, 0) {
		t.Fatal("a later same-second parent anchor reached backward through delegation recursion")
	}
	if !resolution.effective(child, instant{position: 3, timestamp: 1000}, 0) {
		t.Fatal("the parent anchor did not take force at its own position")
	}
}

func TestEffectiveUsesParentRevocationPositionWhenTimestampsTie(t *testing.T) {
	revoked := instant{position: 3, timestamp: 1000}
	parent := &anchorRecord{
		record: "parent", from: instant{position: 0, timestamp: 1000}, revoked: &revoked,
	}
	child := &anchorRecord{record: "child", parent: parent.record, from: instant{position: 1, timestamp: 1000}}
	resolution := &Resolution{byRecord: map[string]*anchorRecord{parent.record: parent}}

	if !resolution.effective(child, instant{position: 2, timestamp: 1000}, 0) {
		t.Fatal("a later same-second parent revocation reached backward through delegation recursion")
	}
	if resolution.effective(child, instant{position: 3, timestamp: 1000}, 0) {
		t.Fatal("the child survived its parent's revocation position")
	}
}
