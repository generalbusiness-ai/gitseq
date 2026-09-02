package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

// newWaitBoundServer builds a resident over a fresh single-actor repository.
// Callers that need a smaller long-poll budget replace waitSlots before
// serving any request; the channel is the narrowest seam the construction
// offers and the tests own the server before it is shared.
func newWaitBoundServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

// waitBoundCursor reads the current composite cursor so a subsequent wait has
// nothing new to report and stays open for its full timeout.
func waitBoundCursor(t *testing.T, baseURL string) Cursor {
	t.Helper()
	response, err := http.Get(baseURL + "/v0/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status Status
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	return status.Cursor
}

// holdWait opens one long poll on path and leaves it in flight. The returned
// cancel abandons it from the client side, and done reports the status code
// (or zero on transport error) once the poll finishes.
func holdWait(t *testing.T, baseURL, path string, body WaitRequest) (cancel context.CancelFunc, done chan int) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelCtx := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		cancelCtx()
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	done = make(chan int, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			done <- 0
			return
		}
		response.Body.Close()
		done <- response.StatusCode
	}()
	t.Cleanup(cancelCtx)
	return cancelCtx, done
}

// probeWait sends one wait and reports its status code. The five-second client
// timeout is the promptness assertion: a resident that queues a saturated wait
// instead of refusing it makes this call fail outright.
func probeWait(t *testing.T, baseURL, path string, body WaitRequest) int {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Post(baseURL+path, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("saturated wait did not answer promptly: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

// waitForSlots blocks until the shared budget holds exactly want slots, which
// is how the tests know every launched poll is past the acquire (or every
// finished poll has released) before they assert anything.
func waitForSlots(t *testing.T, server *Server, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for len(server.waitSlots) != want {
		if time.Now().After(deadline) {
			t.Fatalf("wait slots = %d, want %d", len(server.waitSlots), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The documented default budget is sixty-four polls shared across both wait
// routes. Sixty-four polls on /v0/wait must saturate it, the sixty-fifth on
// either route must get 429 at once, and abandoning the polls must return
// every slot. The fill count is written as a literal on purpose: if the
// default moves, this test must move with it.
func TestWaitBudgetOfSixtyFourIsSharedAcrossBothWaitRoutes(t *testing.T) {
	t.Parallel()
	server, httpServer := newWaitBoundServer(t)
	credential, _ := announceCredential(t, server, presenceRequest{Actor: "human"})
	cursor := waitBoundCursor(t, httpServer.URL)
	var cancels []context.CancelFunc
	for i := 0; i < 64; i++ {
		cancel, _ := holdWait(t, httpServer.URL, "/v0/wait", WaitRequest{Cursor: cursor, TimeoutMS: 20000})
		cancels = append(cancels, cancel)
	}
	waitForSlots(t, server, 64, 10*time.Second)
	if code := probeWait(t, httpServer.URL, "/v0/wait", WaitRequest{Cursor: cursor, TimeoutMS: 50}); code != http.StatusTooManyRequests {
		t.Fatalf("sixty-fifth /v0/wait = %d, want %d", code, http.StatusTooManyRequests)
	}
	if code := probeWait(t, httpServer.URL, "/v0/actor-wait", WaitRequest{Cursor: cursor, TimeoutMS: 50, Session: credential}); code != http.StatusTooManyRequests {
		t.Fatalf("/v0/actor-wait with the budget spent by /v0/wait = %d, want %d", code, http.StatusTooManyRequests)
	}
	for _, cancel := range cancels {
		cancel()
	}
	waitForSlots(t, server, 0, 10*time.Second)
}

// Actor waits saturate the same budget, and slots come back when polls finish
// on their own clock. The budget is narrowed to two through the channel seam
// so the test exercises the bound, not sixty-four goroutines.
func TestActorWaitSaturatesTheBudgetAndCompletionRestoresIt(t *testing.T) {
	t.Parallel()
	server, httpServer := newWaitBoundServer(t)
	server.waitSlots = make(chan struct{}, 2)
	credential, _ := announceCredential(t, server, presenceRequest{Actor: "human"})
	cursor := waitBoundCursor(t, httpServer.URL)
	_, firstDone := holdWait(t, httpServer.URL, "/v0/actor-wait", WaitRequest{Cursor: cursor, TimeoutMS: 1000, Session: credential})
	_, secondDone := holdWait(t, httpServer.URL, "/v0/actor-wait", WaitRequest{Cursor: cursor, TimeoutMS: 1000, Session: credential})
	waitForSlots(t, server, 2, 5*time.Second)
	if code := probeWait(t, httpServer.URL, "/v0/actor-wait", WaitRequest{Cursor: cursor, TimeoutMS: 50, Session: credential}); code != http.StatusTooManyRequests {
		t.Fatalf("third /v0/actor-wait over a budget of two = %d, want %d", code, http.StatusTooManyRequests)
	}
	for _, done := range []chan int{firstDone, secondDone} {
		select {
		case code := <-done:
			if code != http.StatusOK {
				t.Fatalf("held actor wait finished with %d", code)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("held actor wait never finished")
		}
	}
	waitForSlots(t, server, 0, 5*time.Second)
	if code := probeWait(t, httpServer.URL, "/v0/actor-wait", WaitRequest{Cursor: cursor, TimeoutMS: 50, Session: credential}); code != http.StatusOK {
		t.Fatalf("actor wait after recovery = %d, want %d", code, http.StatusOK)
	}
}

// A client that abandons its long poll gives its slot back: capacity must
// follow the request's lifetime, not its happy path.
func TestCancelledWaitReturnsItsSlot(t *testing.T) {
	t.Parallel()
	server, httpServer := newWaitBoundServer(t)
	server.waitSlots = make(chan struct{}, 1)
	cursor := waitBoundCursor(t, httpServer.URL)
	cancel, _ := holdWait(t, httpServer.URL, "/v0/wait", WaitRequest{Cursor: cursor, TimeoutMS: 20000})
	waitForSlots(t, server, 1, 5*time.Second)
	if code := probeWait(t, httpServer.URL, "/v0/wait", WaitRequest{Cursor: cursor, TimeoutMS: 50}); code != http.StatusTooManyRequests {
		t.Fatalf("second wait over a budget of one = %d, want %d", code, http.StatusTooManyRequests)
	}
	cancel()
	waitForSlots(t, server, 0, 5*time.Second)
	if code := probeWait(t, httpServer.URL, "/v0/wait", WaitRequest{Cursor: cursor, TimeoutMS: 50}); code != http.StatusOK {
		t.Fatalf("wait after cancellation freed the slot = %d, want %d", code, http.StatusOK)
	}
}
