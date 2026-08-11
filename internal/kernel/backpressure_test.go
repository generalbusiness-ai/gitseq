package kernel

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
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
	<-arrived

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

func TestZeroMaxQueueDepthLeavesTheQueueUnbounded(t *testing.T) {
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
	<-arrived

	queued := make(chan error, 1)
	go func() {
		_, err := submitter.Submit(f.ctx, f.request(t, private, "queued", []byte("queued"), nil))
		queued <- err
	}()

	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	// With no limit configured the second submission waits rather than being
	// refused. This is the behaviour every existing deployment already has.
	if err := <-queued; err != nil {
		t.Fatalf("queued submission with no limit = %v, want success", err)
	}
}

func TestExhaustedRetryLimitIsBackPressureNotAnonymousFailure(t *testing.T) {
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

// Rotation chains against the same ref and exhausts the same retry budget, so
// it reports the same refusal. Rotate has no failpoint inside its loop, so this
// exhausts the budget directly: a negative limit runs the body zero times and
// falls straight through to the error under test.
func TestRotationExhaustedRetryLimitIsBackPressure(t *testing.T) {
	f := newFixture(t, "sha1")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, filepath.Join(t.TempDir(), "next-sequencer"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: f.signingKey, MaxRetries: -1})
	if !errors.Is(err, ErrBackPressure) {
		t.Fatalf("rotation with exhausted budget = %v, want ErrBackPressure", err)
	}
}
