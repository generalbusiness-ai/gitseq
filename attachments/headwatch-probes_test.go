package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexHeadWatchRapidRetirementChainDoesNotOverlap(t *testing.T) {
	firstStarted := make(chan struct{})
	allowFirstReturn := make(chan struct{})
	overlap := make(chan struct{}, 1)
	var active, calls atomic.Int32
	h := newHeadWatch(func(context.Context) (string, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > 1 {
			select {
			case overlap <- struct{}{}:
			default:
			}
		}
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-allowFirstReturn
		}
		return "head", nil
	})
	h.interval = time.Hour
	releaseA := h.acquire()
	<-firstStarted
	h.mu.Lock()
	a := h.clock
	h.mu.Unlock()
	releaseA()
	releaseB := h.acquire()
	h.mu.Lock()
	b := h.clock
	h.mu.Unlock()
	releaseB()
	// B is cancelled before A's already-cancelled read has actually returned.
	select {
	case <-b.done:
	case <-time.After(100 * time.Millisecond):
	}
	releaseC := h.acquire()
	h.mu.Lock()
	c := h.clock
	h.mu.Unlock()
	defer func() { releaseC(); close(allowFirstReturn); <-a.done; <-b.done; <-c.done }()
	select {
	case <-overlap:
		t.Fatal("third clock began a read while the first retired read was still active")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCodexHeadWatchTwoLifetimesDoNotOverlap(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	overlap := make(chan struct{}, 1)
	var active, calls atomic.Int32
	h := newHeadWatch(func(context.Context) (string, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > 1 {
			select {
			case overlap <- struct{}{}:
			default:
			}
		}
		if calls.Add(1) == 1 {
			close(started)
			<-unblock
		}
		return "head", nil
	})
	h.interval = time.Hour
	releaseA := h.acquire()
	<-started
	h.mu.Lock()
	a := h.clock
	h.mu.Unlock()
	releaseA()
	releaseB := h.acquire()
	h.mu.Lock()
	b := h.clock
	h.mu.Unlock()
	defer func() { releaseB(); close(unblock); <-a.done; <-b.done }()
	select {
	case <-overlap:
		t.Fatal("restarted clock entered before prior read returned")
	case <-time.After(100 * time.Millisecond):
	}
}
