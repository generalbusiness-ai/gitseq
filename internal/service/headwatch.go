package service

import (
	"context"
	"sync"
	"time"
)

// headWatchInterval is the one clock on which the resident notices ordinary
// Git progress written by another process. It is the same 250 ms every wait
// used to tick on its own; what changed is that there is now one such clock
// per log rather than one per waiter.
const headWatchInterval = 250 * time.Millisecond

// headWatch is the shared per-log head clock. While at least one wait is
// open it reads the log's head ref once per interval and advances a
// generation whenever the answer differs from the last one — a move, a
// rewind, or a read that fails, which is news too: a waiter that sees a new
// generation asks the verified snapshot again and surfaces whatever that
// says. With no wait open it does nothing, so an idle resident spends no Git
// processes on it, and with sixty-four waits open it still spends one per
// interval.
//
// It is a notice, not a verifier and not a cache. It holds the head string
// only to compare against the next read; every answer a waiter returns still
// comes from the workspace snapshot, verified as before.
//
// No Git process ever runs under the mutex. Joining and leaving are bookkeeping
// only, so a waiter that is cancelled while another waiter's read is slow
// leaves at once and gives its long-poll slot back. Each clock owns the
// context its reads run under and cancels it when the last wait releases;
// the next clock starts only after the previous one has fully exited, so two
// reads for one log never overlap.
type headWatch struct {
	read     func(context.Context) (string, error)
	interval time.Duration

	mu         sync.Mutex
	waiters    int
	generation uint64
	head       string
	// changed is closed and replaced each time the generation advances, so a
	// waiter blocked on it wakes at once instead of on its own next tick.
	changed chan struct{}
	// clock is the running clock, nil while no wait is open. retiring is the
	// clock most recently cancelled, whose goroutine may still be finishing a
	// read; a new clock waits for it before reading.
	clock    *headClock
	retiring *headClock
}

// headClock is one lifetime of the clock goroutine: the context its reads
// run under and the channel closed when the goroutine has exited, which is
// after any in-flight read has returned.
type headClock struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func newHeadWatch(read func(context.Context) (string, error)) *headWatch {
	return &headWatch{read: read, interval: headWatchInterval, changed: make(chan struct{})}
}

// acquire registers one open wait and returns the release that unregisters
// it. It never blocks on I/O: the first acquisition starts the clock, whose
// goroutine takes the baseline read on its own. Release is idempotent; the
// last release cancels the clock's context, which ends a read in progress.
func (h *headWatch) acquire() (release func()) {
	h.mu.Lock()
	h.waiters++
	if h.waiters == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		clock := &headClock{cancel: cancel, done: make(chan struct{})}
		previous := h.retiring
		h.clock = clock
		go h.run(ctx, clock, previous)
	}
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.waiters--
			if h.waiters == 0 && h.clock != nil {
				h.clock.cancel()
				h.retiring, h.clock = h.clock, nil
			}
			h.mu.Unlock()
		})
	}
}

// current is what a waiter compares against: the generation, the channel
// that closes when it next advances, and the head the clock last read (empty
// until the baseline read lands). Read it before the snapshot it guards, so a
// change landing between the two is seen on the next pass instead of lost;
// and compare the head too, so a move that lands between a waiter's snapshot
// and the clock's baseline read is seen as well.
func (h *headWatch) current() (uint64, <-chan struct{}, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.generation, h.changed, h.head
}

func (h *headWatch) run(ctx context.Context, clock *headClock, previous *headClock) {
	defer close(clock.done)
	if previous != nil {
		// A retired clock may still be inside a read, and may itself be
		// waiting on the clock before it. Wait for it even when this clock
		// is already cancelled: a clock's done must mean every read before
		// it has returned, or a third clock could start a read while the
		// first one's is still running.
		<-previous.done
	}
	if ctx.Err() != nil {
		return
	}
	// The baseline: the first read compares against nothing and advances
	// nothing. A waiter that snapshotted before it lands compares its own
	// head against this one on its next pass.
	head, err := h.read(ctx)
	if ctx.Err() != nil {
		return
	}
	h.mu.Lock()
	h.head = head
	if err != nil {
		h.head = ""
	}
	h.mu.Unlock()
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		head, err := h.read(ctx)
		if ctx.Err() != nil {
			return
		}
		h.mu.Lock()
		if err != nil || head != h.head {
			// A failed read bumps every tick it persists. Waiters then ask
			// the snapshot each tick, exactly as they did before this clock
			// existed, and the snapshot says what is wrong.
			h.head = head
			if err != nil {
				h.head = ""
			}
			h.generation++
			close(h.changed)
			h.changed = make(chan struct{})
		}
		h.mu.Unlock()
	}
}
