package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// The attempt refs, proved against a real Git binary where a ref is a ref,
// and against an in-memory store where the compare-and-swap can be made to
// fail on purpose.

// memoryRefs is a ref store a test can make fail. It records every write with
// the expected old value it carried, which is the thing the whole allocation
// rests on: a create is a swap against an empty expected value, and a create
// that dropped it would silently overwrite a competing allocation.
type memoryRefs struct {
	mu sync.Mutex
	// values is the store's contents.
	values map[string]string
	// writes is every UpdateRef this store was asked to make, in order.
	writes []memoryWrite
	// reads counts RefValue calls, so a bound can be asserted as a call count
	// rather than as a wall clock.
	reads int
	// failWrites refuses that many writes before any is allowed to land.
	failWrites int
	// occupyOnFailure fills the ref a refused write named, which is what
	// contention looks like from the caller: the write failed and somebody
	// else's value is now there.
	occupyOnFailure bool
	// readError, when set, is returned by every RefValue.
	readError error
}

type memoryWrite struct {
	ref, newOID, oldOID string
	refused             bool
}

func newMemoryRefs() *memoryRefs { return &memoryRefs{values: map[string]string{}} }

func (m *memoryRefs) RefValue(ctx context.Context, ref string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.readError != nil {
		return "", false, m.readError
	}
	value, present := m.values[ref]
	return value, present, nil
}

func (m *memoryRefs) UpdateRef(ctx context.Context, ref, newOID, oldOID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, present := m.values[ref]
	refuse := false
	switch {
	case m.failWrites > 0:
		m.failWrites--
		refuse = true
	case oldOID == "" && present:
		refuse = true
	case oldOID != "" && (!present || current != oldOID):
		refuse = true
	}
	m.writes = append(m.writes, memoryWrite{ref: ref, newOID: newOID, oldOID: oldOID, refused: refuse})
	if refuse {
		if m.occupyOnFailure {
			m.values[ref] = strings.Repeat("f", 40)
		}
		return errors.New("cannot lock ref")
	}
	m.values[ref] = newOID
	return nil
}

const (
	tipOne = "1111111111111111111111111111111111111111"
	tipTwo = "2222222222222222222222222222222222222222"
)

func testGoverning(t *testing.T) string {
	t.Helper()
	return "git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("b", 40)
}

// The ref name itself, and the two shapes it must never produce.
func TestTheAttemptRefNamesTheGoverningHashAndOneAttempt(t *testing.T) {
	governing := testGoverning(t)
	ref, err := CheckoutAttemptRef(governing, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := "refs/gitseq/lanes/" + strings.Repeat("b", 40) + "/1"; ref != want {
		t.Fatalf("attempt ref = %q, want %q", ref, want)
	}
	// A bare `<hash>` form would make every later attempt at that record
	// unwriteable: Git cannot hold both refs/gitseq/lanes/<hash> and
	// refs/gitseq/lanes/<hash>/1. The builder refuses it rather than leaving
	// the invariant to whoever calls it.
	if _, err := CheckoutAttemptRef(governing, 0); err == nil {
		t.Fatal("attempt 0 built a ref; the bare-hash form would have collided with every numbered one")
	}
	// A fragment, a record number and prose are ways of typing an identifier
	// at a boundary. None of them may reach a ref namespace.
	for _, selector := range []string{"#12", strings.Repeat("b", 8), "", "not an identifier", "git:sha1:" + strings.Repeat("b", 40)} {
		if _, err := CheckoutAttemptRef(selector, 1); err == nil {
			t.Fatalf("%q built an attempt ref", selector)
		}
	}
}

// Allocation is a create against a missing ref: the expected old value is
// empty, which is Git's "this ref must not exist". A write that dropped the
// expected value would overwrite a competing allocation and one ref would
// then name two attempts.
func TestAllocationIsACreateAgainstAMissingRef(t *testing.T) {
	refs := newMemoryRefs()
	governing := testGoverning(t)
	attempt, err := claimCheckoutAttempt(context.Background(), refs, governing, tipOne)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Number != 1 || attempt.Tip != tipOne {
		t.Fatalf("first allocation = %+v", attempt)
	}
	if len(refs.writes) != 1 {
		t.Fatalf("one allocation made %d writes: %+v", len(refs.writes), refs.writes)
	}
	if refs.writes[0].oldOID != "" {
		t.Fatalf("the create carried expected old value %q; an empty one is what makes it a create", refs.writes[0].oldOID)
	}

	// A second allocation for the same record at the very same tip is a
	// second attempt, never a reuse. A duplicate checkout is ordinary and a
	// recut under one request shares that request's hash, so reusing the
	// first ref would make one ref name two lanes.
	second, err := claimCheckoutAttempt(context.Background(), refs, governing, tipOne)
	if err != nil {
		t.Fatal(err)
	}
	if second.Number != 2 || second.Ref == attempt.Ref {
		t.Fatalf("second allocation at one tip = %+v, want a fresh attempt beside %+v", second, attempt)
	}
	if refs.values[attempt.Ref] != tipOne || refs.values[second.Ref] != tipOne {
		t.Fatalf("both attempts are not live: %+v", refs.values)
	}
}

// A refused write is contention exactly when somebody's value is now there.
// A transient refusal leaves the ref still free, so the same attempt number
// is tried again rather than abandoned: a lock that cleared must not cost the
// caller an attempt number, and a lost race must.
func TestATransientRefusalKeepsItsAttemptAndContentionTakesTheNext(t *testing.T) {
	transient := newMemoryRefs()
	transient.failWrites = 3
	governing := testGoverning(t)
	attempt, err := claimCheckoutAttempt(context.Background(), transient, governing, tipOne)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Number != 1 {
		t.Fatalf("a transient refusal advanced the attempt to %d", attempt.Number)
	}
	if len(transient.writes) != 4 {
		t.Fatalf("three transient refusals took %d writes", len(transient.writes))
	}

	contended := newMemoryRefs()
	contended.failWrites, contended.occupyOnFailure = 3, true
	attempt, err = claimCheckoutAttempt(context.Background(), contended, governing, tipOne)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Number != 4 {
		t.Fatalf("three lost races left the attempt at %d, want 4", attempt.Number)
	}
}

// Exhaustion refuses rather than looping. The bound is asserted as a write
// count, not as a timeout: a test that proved boundedness by not hanging
// would pass just as well against a loop bounded at a million.
func TestExhaustedAllocationRefusesRatherThanSpinning(t *testing.T) {
	refs := newMemoryRefs()
	refs.failWrites, refs.occupyOnFailure = 1000, true
	_, err := claimCheckoutAttempt(context.Background(), refs, testGoverning(t), tipOne)
	if err == nil {
		t.Fatal("an allocation that never succeeded returned an attempt")
	}
	if len(refs.writes) != checkoutAttemptLimit {
		t.Fatalf("allocation made %d writes, want the %d-attempt bound", len(refs.writes), checkoutAttemptLimit)
	}
	if !strings.Contains(err.Error(), "refs/gitseq/lanes/") {
		t.Fatalf("the refusal does not name the namespace it gave up on: %v", err)
	}
}

// A store that cannot be read is not contention. Walking the whole budget and
// then refusing would hide an unreadable store behind a message about other
// processes.
func TestAnUnreadableRefStoreRefusesImmediately(t *testing.T) {
	refs := newMemoryRefs()
	refs.readError = errors.New("object store is unreadable")
	_, err := claimCheckoutAttempt(context.Background(), refs, testGoverning(t), tipOne)
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("an unreadable store answered %v", err)
	}
	if len(refs.writes) != 0 {
		t.Fatalf("an unreadable store was written to: %+v", refs.writes)
	}
}

// Pointing an attempt at the tip it already names is a no-op, and pointing it
// anywhere else is a compare-and-swap against the value this process last
// saw.
func TestPointingAnAttemptIsACompareAndSwapAndARepeatIsANoOp(t *testing.T) {
	refs := newMemoryRefs()
	governing := testGoverning(t)
	attempt, err := claimCheckoutAttempt(context.Background(), refs, governing, tipOne)
	if err != nil {
		t.Fatal(err)
	}
	writes := len(refs.writes)
	same, err := pointCheckoutAttempt(context.Background(), refs, attempt, tipOne)
	if err != nil {
		t.Fatal(err)
	}
	if same != attempt || len(refs.writes) != writes {
		t.Fatalf("pointing an attempt at its own tip wrote %+v", refs.writes[writes:])
	}

	moved, err := pointCheckoutAttempt(context.Background(), refs, attempt, tipTwo)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Tip != tipTwo || refs.values[attempt.Ref] != tipTwo {
		t.Fatalf("the attempt did not move: %+v %+v", moved, refs.values)
	}
	if last := refs.writes[len(refs.writes)-1]; last.oldOID != tipOne {
		t.Fatalf("the move carried expected old value %q, want the value last seen", last.oldOID)
	}
	// A ref somebody else has moved belongs to them: the swap carries the
	// value this process last saw, and that value is no longer there.
	const tipThree = "3333333333333333333333333333333333333333"
	refs.values[attempt.Ref] = tipThree
	if _, err := pointCheckoutAttempt(context.Background(), refs, moved, tipOne); err == nil {
		t.Fatal("a stale expected value still moved the ref")
	}
	if refs.values[attempt.Ref] != tipThree {
		t.Fatalf("the refused swap wrote anyway: %s", refs.values[attempt.Ref])
	}
}

// Competing creation against a real Git binary. Exactly one writer wins each
// number, the loser takes a fresh one, and both refs are live afterwards.
func TestConcurrentAllocationsTakeDistinctAttempts(t *testing.T) {
	f := newAssociationFixture(t)
	governing := f.seed.ID
	first := f.commit("one")
	f.git("update-ref", "refs/heads/second", first)
	second := f.commit("two")

	tips := []string{first, second}
	results := make([]CheckoutAttempt, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			results[i], errs[i] = f.w.ClaimCheckoutAttempt(f.ctx, governing, tips[i])
		}(i)
	}
	close(start)
	wait.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("allocation %d: %v", i, err)
		}
	}
	if results[0].Number == results[1].Number {
		t.Fatalf("two concurrent allocations took one attempt number: %+v %+v", results[0], results[1])
	}
	for _, attempt := range results {
		value := f.git("rev-parse", attempt.Ref)
		if value != attempt.Tip {
			t.Fatalf("%s reads %s, want %s", attempt.Ref, value, attempt.Tip)
		}
	}
	if results[0].Tip == results[1].Tip {
		t.Fatalf("the fixture gave both writers one tip, so nothing about overwriting was proved")
	}
	// A single sequential allocation is attempt 1, so the test above is not
	// passing merely because everything refuses.
	third, err := f.w.ClaimCheckoutAttempt(f.ctx, f.seed.ID, first)
	if err != nil {
		t.Fatal(err)
	}
	if third.Number != 3 {
		t.Fatalf("a third allocation took attempt %d, want the next free one", third.Number)
	}
}

// The ref outlives the checkout and the branch, which is its point. It is
// written through the store, whose repository is the common Git directory, so
// removing a checkout takes its private directory and leaves the ref.
func TestTheAttemptRefSurvivesCheckoutRemovalAndBranchDeletion(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("survives")
	created := f.create(t, checkoutIntent(f, request.ID, "survives", "request/survives"))
	if !created.Created {
		t.Fatalf("nothing was created: %v", created.Report())
	}

	f.git("worktree", "remove", "--force", created.Path)
	f.git("branch", "-D", created.Branch)

	value, present, err := f.w.Store.RefValue(f.ctx, created.Attempt.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if !present || value != created.Attempt.Tip {
		t.Fatalf("the attempt ref did not survive: present=%v value=%q want %q", present, value, created.Attempt.Tip)
	}
	// The record goes with the checkout, which is the maintenance argument
	// the adopted design made for putting it in the private Git directory.
	if _, err := readCheckoutRecordAt(created.RecordPath, f.w.config.Genesis, f.w.config.ObjectFormat); err == nil {
		t.Fatal("the checkout record outlived the checkout that owned it")
	}
}
