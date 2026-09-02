package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

// The design gives the sequencer five named refusals, of which back-pressure is
// the one that means "at capacity": refuse before chaining, cheaply, and say so.
// These tests pin all three of those words. Cheaply and before chaining are the
// same assertion from two sides — a refused submission must not reach the
// pre-append hook, which sits immediately before the payload tree is written.

// blockAt returns a failpoint that parks the first submission reaching name
// until release is closed, and a channel closed once it has arrived.
func blockAt(name string, release <-chan struct{}) (func(string), <-chan struct{}) {
	arrived := make(chan struct{})
	var once sync.Once
	return func(hit string) {
		if hit == name {
			once.Do(func() {
				close(arrived)
				<-release
			})
		}
	}, arrived
}

func TestSubmitRefusesAtCapacityBeforeChaining(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	release := make(chan struct{})
	failpoint, arrived := blockAt("before_ref_cas", release)

	var admitted int
	var admittedMu sync.Mutex
	submitter := NewSubmitter(f.store, Options{
		SigningKey:    f.signingKey,
		MaxQueueDepth: 1,
		Failpoint:     failpoint,
		PreAppend: func(context.Context, Admission) error {
			admittedMu.Lock()
			admitted++
			admittedMu.Unlock()
			return nil
		},
	})

	held := make(chan error, 1)
	go func() {
		_, err := submitter.Submit(f.ctx, f.request(t, private, "held", []byte("held"), nil))
		held <- err
	}()
	// Bound the arrival. If the first submission were itself refused it would
	// never reach the failpoint, and an unbounded wait here would report a
	// timeout minutes later instead of the refusal that caused it.
	select {
	case <-arrived:
	case err := <-held:
		t.Fatalf("first submission did not reach the sequencer: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first submission never arrived at the sequencer")
	}

	// The slot is occupied. This submission must be refused rather than queued,
	// and refused promptly: "cheaply" means it does not wait for the holder. A
	// submitter that queues instead would block here until the held submission
	// finishes, which cannot happen until this call returns — so assert on the
	// timer rather than deadlocking and reporting a timeout ten minutes later.
	refused := make(chan error, 1)
	go func() {
		_, err := submitter.Submit(f.ctx, f.request(t, private, "refused", []byte("refused"), nil))
		refused <- err
	}()
	var err error
	select {
	case err = <-refused:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("submit at capacity queued behind the held submission instead of refusing")
	}
	if !errors.Is(err, ErrBackPressure) {
		close(release)
		t.Fatalf("submit at capacity = %v, want ErrBackPressure", err)
	}

	// Before chaining: the refused submission never reached the pre-append hook,
	// so it wrote no payload tree and did no chaining work.
	admittedMu.Lock()
	seen := admitted
	admittedMu.Unlock()
	if seen != 1 {
		t.Fatalf("pre-append ran %d times, want 1 — the refusal did chaining work", seen)
	}

	close(release)
	if err := <-held; err != nil {
		t.Fatalf("held submission = %v, want success", err)
	}

	// The refusal released its slot, so the sequencer still accepts work.
	if _, err := submitter.Submit(f.ctx, f.request(t, private, "after", []byte("after"), nil)); err != nil {
		t.Fatalf("submit after refusal = %v, want success", err)
	}
	if depth := submitter.inFlight.Load(); depth != 0 {
		t.Fatalf("in-flight depth after quiescence = %d, want 0", depth)
	}
}

// TestSubmitRefusesAtCapacityBeforeParsing pins the other half of "before":
// the guard sits at method entry, ahead of the parser.
//
// TestSubmitRefusesAtCapacityBeforeChaining watches the pre-append hook, which
// runs well after intent.Verify. A guard moved to just after decoding would
// still leave that test green, so on its own it proves only "before chaining"
// and not the "before parsing" the limits page claims. A request whose signed
// intent cannot be decoded at all separates the two orderings, because the two
// refusals are distinguishable: reached the guard first means capacity, reached
// the parser first means malformed, and only one of them is ErrBackPressure.
func TestSubmitRefusesAtCapacityBeforeParsing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	// Garbage where the signed intent belongs.
	malformed := Request{Signed: intent.Signed{Intent: []byte("not a signed intent")}}

	// With a slot free this request is refused for what is wrong with it. Without
	// this half the test would also pass on a build where every refusal wrapped
	// ErrBackPressure, which would prove nothing about ordering.
	free := NewSubmitter(f.store, Options{SigningKey: f.signingKey, MaxQueueDepth: 1})
	if _, err := free.Submit(f.ctx, malformed); err == nil {
		t.Fatal("malformed submission below capacity = nil, want a decode refusal")
	} else if errors.Is(err, ErrBackPressure) {
		t.Fatalf("malformed submission below capacity = %v, want a decode refusal, not back-pressure", err)
	}

	release := make(chan struct{})
	failpoint, arrived := blockAt("before_ref_cas", release)
	submitter := NewSubmitter(f.store, Options{
		SigningKey:    f.signingKey,
		MaxQueueDepth: 1,
		Failpoint:     failpoint,
	})

	held := make(chan error, 1)
	go func() {
		_, err := submitter.Submit(f.ctx, f.request(t, private, "held", []byte("held"), nil))
		held <- err
	}()
	select {
	case <-arrived:
	case err := <-held:
		t.Fatalf("first submission did not reach the sequencer: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first submission never arrived at the sequencer")
	}

	// The slot is occupied and the request is undecodable. Whichever check runs
	// first names the refusal, so this single assertion is the ordering.
	refused := make(chan error, 1)
	go func() {
		_, err := submitter.Submit(f.ctx, malformed)
		refused <- err
	}()
	var err error
	select {
	case err = <-refused:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("malformed submission at capacity queued instead of refusing")
	}
	if !errors.Is(err, ErrBackPressure) {
		close(release)
		t.Fatalf("malformed submission at capacity = %v, want ErrBackPressure — the capacity guard runs after the parser", err)
	}

	close(release)
	if err := <-held; err != nil {
		t.Fatalf("held submission = %v, want success", err)
	}
	if depth := submitter.inFlight.Load(); depth != 0 {
		t.Fatalf("in-flight depth after quiescence = %d, want 0", depth)
	}
}

func TestZeroMaxQueueDepthLeavesTheQueueUnbounded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	release := make(chan struct{})
	failpoint, arrived := blockAt("before_ref_cas", release)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, Failpoint: failpoint})

	held := make(chan error, 1)
	go func() {
		_, err := submitter.Submit(f.ctx, f.request(t, private, "held", []byte("held"), nil))
		held <- err
	}()
	select {
	case <-arrived:
	case err := <-held:
		t.Fatalf("first submission did not reach the sequencer: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first submission never arrived at the sequencer")
	}

	// Several followers, not one: with no limit configured they must all queue
	// behind the holder rather than be refused. One follower would only show
	// that nothing was refused, which a limit of two would also satisfy.
	const followers = 4
	queued := make(chan error, followers)
	for index := 0; index < followers; index++ {
		key := fmt.Sprintf("queued-%d", index)
		go func() {
			_, err := submitter.Submit(f.ctx, f.request(t, private, key, []byte(key), nil))
			queued <- err
		}()
	}

	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < followers; index++ {
		if err := <-queued; err != nil {
			t.Fatalf("queued submission %d with no limit = %v, want success", index, err)
		}
	}
	if depth := submitter.inFlight.Load(); depth != 0 {
		t.Fatalf("in-flight depth with no limit = %d, want 0", depth)
	}
}

func TestExhaustedRetryLimitIsBackPressureNotAnonymousFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	// Lose every compare-and-swap by advancing the ref underneath each attempt.
	var extern int
	submitter := NewSubmitter(f.store, Options{
		SigningKey: f.signingKey,
		MaxRetries: 2,
		Failpoint: func(name string) {
			if name != "before_ref_cas" {
				return
			}
			extern++
			key := "external-" + string(rune('a'+extern))
			if _, err := Submit(f.ctx, f.store, f.request(t, private, key, []byte(key), nil), Options{SigningKey: f.signingKey}); err != nil {
				t.Errorf("external append %d: %v", extern, err)
			}
		},
	})

	_, err := submitter.Submit(f.ctx, f.request(t, private, "starved", []byte("starved"), nil))
	if !errors.Is(err, ErrBackPressure) {
		t.Fatalf("exhausted retry limit = %v, want ErrBackPressure", err)
	}
}
