package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// refCounter counts the Git processes the resident spends reading refs. It is
// the quantity the shared head clock exists to bound.
type refCounter struct {
	refs atomic.Int64
	all  atomic.Int64
}

func (o *refCounter) Record(_ context.Context, m observe.Measurement) {
	if m.Operation == observe.OperationGit {
		o.all.Add(1)
		if m.Path == observe.PathRef {
			o.refs.Add(1)
		}
	}
}

func newHeadWatchFixture(t *testing.T, records int) (*Server, *app.Workspace, string, *refCounter) {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < records; i++ {
		if _, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: fmt.Sprintf("synthetic %d", i), IdempotencyKey: fmt.Sprintf("headwatch-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	counter := &refCounter{}
	server, err := NewObserved(workspace, counter)
	if err != nil {
		t.Fatal(err)
	}
	return server, workspace, repo, counter
}

// runWaiters starts n waits against the same cursor, the i-th delayed by
// i*stagger, and returns how many of them reported a change.
func runWaiters(t *testing.T, server *Server, cursor Cursor, n int, stagger time.Duration, timeoutMS int) (changed int, elapsed []time.Duration) {
	t.Helper()
	ctx := context.Background()
	elapsed = make([]time.Duration, n)
	var wg sync.WaitGroup
	var changes atomic.Int64
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * stagger)
			began := time.Now()
			_, _, didChange, err := server.wait(ctx, WaitRequest{Cursor: cursor, TimeoutMS: timeoutMS})
			elapsed[i] = time.Since(began)
			errs[i] = err
			if didChange {
				changes.Add(1)
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	return int(changes.Load()), elapsed
}

// One head clock per log, not one per waiter. Eight waiters that start at
// different moments used to tick eight separate clocks and spend eight times
// the Git processes of one; now each spends one ref read for its own first
// snapshot and the clock spends the rest. The bound is structural, not a
// timing threshold: it allows every waiter its first read plus a few ticks of
// scheduling slack, and the old behaviour (40 against 5) is far outside it.
func TestHeadClockIsSharedAcrossStaggeredWaiters(t *testing.T) {
	server, _, _, counter := newHeadWatchFixture(t, 3)
	initial, err := server.status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	counter.refs.Store(0)
	if changed, _ := runWaiters(t, server, initial.Cursor, 1, 0, 1100); changed != 0 {
		t.Fatal("fixture changed under a single waiter")
	}
	one := counter.refs.Load()
	counter.refs.Store(0)
	if changed, _ := runWaiters(t, server, initial.Cursor, 8, 23*time.Millisecond, 1100); changed != 0 {
		t.Fatal("fixture changed under eight waiters")
	}
	eight := counter.refs.Load()
	t.Logf("ref processes: one waiter %d, eight staggered waiters %d", one, eight)
	if one == 0 {
		t.Fatal("no ref reads were observed; the fixture is not measuring")
	}
	if eight > one+8+4 {
		t.Fatalf("eight staggered waiters spent %d ref reads against %d for one: the head clock is not shared", eight, one)
	}
}

// An ordinary Git append by another process wakes an open wait through the
// shared clock. This is the omission-sensitive control for the mechanism: if
// the clock never advanced, the waiter would keep its first snapshot and
// sleep to its full timeout.
func TestExternalHeadChangeWakesAnOpenWait(t *testing.T) {
	server, _, repo, _ := newHeadWatchFixture(t, 2)
	ctx := context.Background()
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var response WaitResponse
	var changed bool
	var waitErr error
	began := time.Now()
	go func() {
		defer close(done)
		response, _, changed, waitErr = server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 6000})
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := other.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "from another process", IdempotencyKey: "headwatch-external"}); err != nil {
		t.Fatal(err)
	}
	<-done
	elapsed := time.Since(began)
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if !changed || response.Status.Durable.Head == initial.Durable.Head {
		t.Fatalf("the wait did not report the external append: changed=%v head=%s", changed, response.Status.Durable.Head)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the wait woke after %s; the clock is not noticing external progress", elapsed)
	}
	if response.Status.Durable.Depth != initial.Durable.Depth+1 {
		t.Fatalf("depth = %d, want %d", response.Status.Durable.Depth, initial.Durable.Depth+1)
	}
}

// With no wait open the clock is retired: no goroutine, no Git process. A
// resident nobody is waiting on must cost nothing for this.
func TestHeadClockRetiresWhenTheLastWaitReleases(t *testing.T) {
	server, _, _, counter := newHeadWatchFixture(t, 1)
	initial, err := server.status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runWaiters(t, server, initial.Cursor, 3, 10*time.Millisecond, 400)
	server.heads.mu.Lock()
	waiters, clock, retiring := server.heads.waiters, server.heads.clock, server.heads.retiring
	server.heads.mu.Unlock()
	if waiters != 0 || clock != nil || retiring == nil {
		t.Fatalf("after every wait returned: waiters=%d clock=%v retiring=%v", waiters, clock != nil, retiring != nil)
	}
	select {
	case <-retiring.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the clock goroutine did not exit after the last release")
	}
	counter.refs.Store(0)
	time.Sleep(3 * headWatchInterval)
	if spent := counter.refs.Load(); spent != 0 {
		t.Fatalf("an idle resident spent %d ref reads", spent)
	}
}

// A head that rewinds or disappears is news the clock passes on, and the
// waiter answers from the verified snapshot rather than from the clock: it
// either reports the new frontier or fails, and in neither case sleeps to
// its timeout pretending nothing happened.
func TestHeadClockPassesRewindsAndMissingRefsToTheSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		move func(t *testing.T, repo, ref string)
	}{
		{"rewind", func(t *testing.T, repo, ref string) {
			older, err := exec.Command("git", "-C", repo, "rev-parse", ref+"~1").Output()
			if err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("git", "-C", repo, "update-ref", ref, string(older[:40])).CombinedOutput(); err != nil {
				t.Fatalf("update-ref: %v %s", err, out)
			}
		}},
		{"missing", func(t *testing.T, repo, ref string) {
			if out, err := exec.Command("git", "-C", repo, "update-ref", "-d", ref).CombinedOutput(); err != nil {
				t.Fatalf("update-ref -d: %v %s", err, out)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, workspace, repo, _ := newHeadWatchFixture(t, 3)
			ctx := context.Background()
			initial, err := server.status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ref := kernel.Ref(workspace.View().Genesis)
			done := make(chan struct{})
			var response WaitResponse
			var changed bool
			var waitErr error
			began := time.Now()
			go func() {
				defer close(done)
				response, _, changed, waitErr = server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 6000})
			}()
			time.Sleep(150 * time.Millisecond)
			tc.move(t, repo, ref)
			<-done
			elapsed := time.Since(began)
			if elapsed > 3*time.Second {
				t.Fatalf("the wait slept %s through a %s", elapsed, tc.name)
			}
			if waitErr == nil && (!changed || response.Status.Durable.Head == initial.Durable.Head) {
				t.Fatalf("after a %s the wait reported neither an error nor a new frontier: changed=%v head=%s", tc.name, changed, response.Status.Durable.Head)
			}
			t.Logf("%s: err=%v changed=%v head=%s after %s", tc.name, waitErr, changed, response.Status.Durable.Head, elapsed)
		})
	}
}

// A live-only change still wakes a wait, and does so without a durable read
// per tick: the live half is read every tick in memory, and the snapshot is
// asked again only for the tick that carries the change.
func TestLiveOnlyChangeWakesWithoutExtraRefReads(t *testing.T) {
	server, _, _, counter := newHeadWatchFixture(t, 1)
	ctx := context.Background()
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counter.refs.Store(0)
	done := make(chan struct{})
	var response WaitResponse
	var changed bool
	var waitErr error
	began := time.Now()
	go func() {
		defer close(done)
		response, _, changed, waitErr = server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 6000})
	}()
	time.Sleep(150 * time.Millisecond)
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.hub.OpenTrustedSession("human", public, "available", time.Minute, nexus.ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}
	<-done
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if !changed || len(response.LiveChanges) == 0 || response.Status.Durable.Head != initial.Durable.Head {
		t.Fatalf("live-only change not reported: changed=%v live=%d head=%s", changed, len(response.LiveChanges), response.Status.Durable.Head)
	}
	if elapsed := time.Since(began); elapsed > 3*time.Second {
		t.Fatalf("live-only wake took %s", elapsed)
	}
	// First snapshot, the wake's snapshot, plus at most a couple of clock
	// ticks in the window: never one read per tick per waiter.
	if spent := counter.refs.Load(); spent > 6 {
		t.Fatalf("a live-only wake spent %d ref reads", spent)
	}
}

// slowRead is a controllable stand-in for the head read: it blocks until
// released or until the clock's context is cancelled, and counts how many
// reads are in flight at once.
type slowRead struct {
	mu       sync.Mutex
	inFlight int
	maxSeen  int
	started  chan struct{}
	release  chan struct{}
	calls    atomic.Int64
}

func newSlowRead() *slowRead {
	return &slowRead{started: make(chan struct{}, 64), release: make(chan struct{})}
}

func (r *slowRead) read(ctx context.Context) (string, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.inFlight++
	if r.inFlight > r.maxSeen {
		r.maxSeen = r.inFlight
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}()
	r.started <- struct{}{}
	select {
	case <-r.release:
		return "head", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Joining is bookkeeping, never I/O. While the clock's baseline read is
// blocked, a second waiter joins and leaves at once, which is what lets a
// cancelled long poll give its slot back instead of queueing behind a slow
// Git process it did not start.
func TestJoiningNeverWaitsOnTheClocksRead(t *testing.T) {
	read := newSlowRead()
	watch := newHeadWatch(read.read)
	releaseFirst := watch.acquire()
	<-read.started // the baseline read is now blocked
	joined := make(chan func(), 1)
	go func() { joined <- watch.acquire() }()
	select {
	case releaseSecond := <-joined:
		releaseSecond()
	case <-time.After(time.Second):
		t.Fatal("a joining waiter blocked behind the clock's read")
	}
	if _, _, head := watch.current(); head != "" {
		t.Fatalf("head = %q before the baseline read returned", head)
	}
	releaseFirst()
	// The last release cancels the clock's context, which ends the blocked
	// read without waiting for the release channel.
	watch.mu.Lock()
	retiring := watch.retiring
	watch.mu.Unlock()
	select {
	case <-retiring.done:
	case <-time.After(time.Second):
		t.Fatal("cancelling the clock did not end its blocked read")
	}
}

// One read at a time per log, across clock lifetimes. A clock retired while
// its read is blocked must not be joined by a restarted clock's read; the
// new clock waits for the old goroutine to finish, and rapid zero-to-one
// transitions leave exactly one goroutine alive and then none.
func TestRetiredClockReadNeverOverlapsARestartedClock(t *testing.T) {
	read := newSlowRead()
	watch := newHeadWatch(read.read)
	var releases []func()
	for i := 0; i < 5; i++ {
		release := watch.acquire()
		<-read.started // this lifetime's baseline read is in flight
		release()      // ...and now cancelled; the goroutine exits shortly
		releases = append(releases, release)
	}
	read.mu.Lock()
	maxSeen := read.maxSeen
	read.mu.Unlock()
	if maxSeen != 1 {
		t.Fatalf("%d reads were in flight at once; the clock must never overlap itself", maxSeen)
	}
	watch.mu.Lock()
	clock, retiring := watch.clock, watch.retiring
	watch.mu.Unlock()
	if clock != nil {
		t.Fatal("a clock is still installed with no wait open")
	}
	select {
	case <-retiring.done:
	case <-time.After(time.Second):
		t.Fatal("the last clock did not exit")
	}
	// Quiescent: no more reads once everything has released.
	calls := read.calls.Load()
	time.Sleep(3 * headWatchInterval)
	if later := read.calls.Load(); later != calls {
		t.Fatalf("a retired clock kept reading: %d then %d", calls, later)
	}
	for _, release := range releases {
		release() // idempotent
	}
}

// At the service boundary: a waiter cancelled while another waiter's slow
// read is in flight returns promptly with its own cancellation, and the
// remaining waiter is served normally afterwards.
func TestCancelledWaitReturnsWhileAnotherWaitersReadIsSlow(t *testing.T) {
	server, _, _, _ := newHeadWatchFixture(t, 1)
	ctx := context.Background()
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	read := newSlowRead()
	server.heads.read = read.read
	first := make(chan error, 1)
	go func() {
		_, _, _, err := server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 1500})
		first <- err
	}()
	<-read.started // the clock's baseline read is blocked on the fake
	cancelCtx, cancel := context.WithCancel(ctx)
	second := make(chan error, 1)
	began := time.Now()
	go func() {
		_, _, _, err := server.wait(cancelCtx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 6000})
		second <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled join returned %v, want context.Canceled", err)
		}
		if elapsed := time.Since(began); elapsed > time.Second {
			t.Fatalf("cancelled join took %s while a read was slow", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled waiter stayed blocked behind the slow read")
	}
	close(read.release) // let the clock proceed; the first waiter times out normally
	if err := <-first; err != nil {
		t.Fatalf("the remaining waiter failed: %v", err)
	}
}
