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
	stop    chan struct{}
	// stopped is closed by the clock goroutine on exit, so a test can wait
	// for the last release to actually retire the clock.
	stopped chan struct{}
}

func newHeadWatch(read func(context.Context) (string, error)) *headWatch {
	return &headWatch{read: read, interval: headWatchInterval, changed: make(chan struct{})}
}

// acquire registers one open wait and returns the release that unregisters
// it. The first acquisition reads the head once, synchronously, so the clock
// compares against a baseline taken before the caller's own first snapshot
// rather than reporting the first tick as a change. A failed baseline read is
// not an error here: the waiter's own snapshot will report it, and the clock
// simply starts from an empty head.
func (h *headWatch) acquire(ctx context.Context) (release func()) {
	h.mu.Lock()
	h.waiters++
	if h.waiters == 1 {
		head, _ := h.read(ctx)
		h.head = head
		h.stop = make(chan struct{})
		h.stopped = make(chan struct{})
		go h.run(h.stop, h.stopped)
	}
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.waiters--
			if h.waiters == 0 {
				close(h.stop)
				h.stop = nil
			}
			h.mu.Unlock()
		})
	}
}

// current is the generation a waiter compares against and the channel that
// closes when it next advances. Read it before the snapshot it guards, so a
// change landing between the two is seen on the next pass instead of lost.
func (h *headWatch) current() (uint64, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.generation, h.changed
}

func (h *headWatch) run(stop, stopped chan struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		head, err := h.read(context.Background())
		h.mu.Lock()
		if err != nil || head != h.head {
			// A failed read bumps every tick it persists. Waiters then ask
			// the snapshot each tick, exactly as they did before this clock
			// existed, and the snapshot says what is wrong.
			h.head = head
			h.generation++
			close(h.changed)
			h.changed = make(chan struct{})
		}
		h.mu.Unlock()
	}
}
