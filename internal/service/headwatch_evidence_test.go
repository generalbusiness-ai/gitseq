package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// headWaitSample is one measured scenario in the head-wait evidence lane.
type headWaitSample struct {
	Depth        int     `json:"depth"`
	Scenario     string  `json:"scenario"`
	Waiters      int     `json:"waiters"`
	StaggerMS    int     `json:"stagger_ms"`
	Repeat       int     `json:"repeat"`
	RefProcesses int64   `json:"ref_processes"`
	GitProcesses int64   `json:"git_processes"`
	MeanWaitMS   float64 `json:"mean_wait_ms"`
	// WakeMS is the time from the external change to the first waiter's
	// return, for the wake scenarios; zero for timeout scenarios.
	WakeMS float64 `json:"wake_ms,omitempty"`
	Note   string  `json:"note,omitempty"`
}

type headWaitEvidence struct {
	MeasuredAt   string            `json:"measured_at"`
	Head         string            `json:"head"`
	GoVersion    string            `json:"go_version"`
	Platform     string            `json:"platform"`
	TimeoutMS    int               `json:"timeout_ms"`
	Repeats      int               `json:"repeats"`
	ClockMS      int               `json:"head_clock_ms"`
	Samples      []headWaitSample  `json:"samples"`
	Explanations map[string]string `json:"explanations"`
}

// TestHeadWaitEvidence is the opt-in measurement lane for the shared head
// clock. It runs only with GITSEQ_HEAD_WAIT_EVIDENCE set to an output path
// and writes JSON there; it asserts fixture validity, never a timing
// threshold. Depths, waiter counts and starts are stated in the output. The
// same file compiles against the head before the clock existed, which is how
// the before figures in performance/HEAD-WAIT.md were taken.
func TestHeadWaitEvidence(t *testing.T) {
	out := os.Getenv("GITSEQ_HEAD_WAIT_EVIDENCE")
	if out == "" {
		t.Skip("set GITSEQ_HEAD_WAIT_EVIDENCE=<path> to record head-wait evidence")
	}
	ctx := context.Background()
	const timeoutMS, repeats = 1100, 3
	head, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	evidence := headWaitEvidence{
		MeasuredAt: time.Now().UTC().Format(time.RFC3339), Head: string(head[:min(len(head), 40)]),
		GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH,
		TimeoutMS: timeoutMS, Repeats: repeats, ClockMS: 250,
		Explanations: map[string]string{
			"ref_processes":   "git rev-parse processes the resident spent during the measurement window only",
			"mean_wait_ms":    "for timeout scenarios this is the poll timeout, not a latency",
			"wake_ms":         "external change or live announcement to the first waiter's return",
			"cold_read":       "wall time until the first verified snapshot of a freshly opened workspace, with all readers started together",
			"scaling_caution": "small-depth warm fixtures on one machine; not a claim about depth 20k or physical resources",
		},
	}
	for _, depth := range []int{1, 31, 300} {
		server, workspace, repo, counter := newHeadWatchFixture(t, depth-1)
		initial, err := server.status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if initial.Durable.Depth != depth {
			t.Fatalf("fixture depth = %d, want %d", initial.Durable.Depth, depth)
		}
		for _, scenario := range []struct {
			name    string
			n       int
			stagger time.Duration
			timeout int
			once    bool
		}{{"one", 1, 0, timeoutMS, false}, {"eight-aligned", 8, 0, timeoutMS, false}, {"eight-staggered", 8, 23 * time.Millisecond, timeoutMS, false},
			{"thirty-two-staggered", 32, 23 * time.Millisecond, timeoutMS, false},
			// The idle case the clock exists for: waiters that sit for a
			// long poll with nothing happening. One repeat, since it is long.
			{"eight-staggered-idle-5s", 8, 23 * time.Millisecond, 5000, true}} {
			for repeat := 1; repeat <= repeats; repeat++ {
				if scenario.once && repeat > 1 {
					break
				}
				counter.refs.Store(0)
				counter.all.Store(0)
				changed, elapsed := runWaiters(t, server, initial.Cursor, scenario.n, scenario.stagger, scenario.timeout)
				if changed != 0 {
					t.Fatalf("fixture changed during %s", scenario.name)
				}
				var total time.Duration
				for _, d := range elapsed {
					total += d
				}
				evidence.Samples = append(evidence.Samples, headWaitSample{
					Depth: depth, Scenario: "timeout/" + scenario.name, Waiters: scenario.n, StaggerMS: int(scenario.stagger / time.Millisecond), Repeat: repeat,
					RefProcesses: counter.refs.Load(), GitProcesses: counter.all.Load(),
					MeanWaitMS: float64(total.Microseconds()) / float64(scenario.n) / 1000,
				})
			}
		}
		// External durable change: eight open waits, one append from a second
		// workspace on the same repository, wake measured to the first return.
		other, err := app.Open(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		for repeat := 1; repeat <= repeats; repeat++ {
			before, err := server.status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			counter.refs.Store(0)
			counter.all.Store(0)
			first := make(chan time.Duration, 8)
			var wg sync.WaitGroup
			var changedAt time.Time
			started := make(chan struct{})
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-started
					_, _, changed, err := server.wait(ctx, WaitRequest{Cursor: before.Cursor, TimeoutMS: 6000})
					if err != nil || !changed {
						t.Errorf("external wake: changed=%v err=%v", changed, err)
						return
					}
					first <- time.Since(changedAt)
				}()
			}
			close(started)
			time.Sleep(200 * time.Millisecond)
			changedAt = time.Now()
			if _, err := other.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "external", IdempotencyKey: fmt.Sprintf("evidence-external-%d-%d", depth, repeat)}); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			close(first)
			var wake time.Duration
			var sum time.Duration
			n := 0
			for d := range first {
				if wake == 0 || d < wake {
					wake = d
				}
				sum += d
				n++
			}
			evidence.Samples = append(evidence.Samples, headWaitSample{
				Depth: depth, Scenario: "wake/external-durable", Waiters: 8, Repeat: repeat,
				RefProcesses: counter.refs.Load(), GitProcesses: counter.all.Load(),
				MeanWaitMS: float64(sum.Microseconds()) / float64(max(n, 1)) / 1000, WakeMS: float64(wake.Microseconds()) / 1000,
			})
		}
		// Live-only change: eight open waits, one presence announcement.
		for repeat := 1; repeat <= repeats; repeat++ {
			before, err := server.status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			counter.refs.Store(0)
			counter.all.Store(0)
			first := make(chan time.Duration, 8)
			var wg sync.WaitGroup
			var changedAt time.Time
			started := make(chan struct{})
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-started
					response, _, changed, err := server.wait(ctx, WaitRequest{Cursor: before.Cursor, TimeoutMS: 6000})
					if err != nil || !changed || len(response.LiveChanges) == 0 {
						t.Errorf("live wake: changed=%v live=%d err=%v", changed, len(response.LiveChanges), err)
						return
					}
					first <- time.Since(changedAt)
				}()
			}
			close(started)
			time.Sleep(200 * time.Millisecond)
			public, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			changedAt = time.Now()
			if _, _, err := server.hub.OpenTrustedSession("human", public, "available", time.Minute, nexus.ActivityUpdate{}); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			close(first)
			var wake, sum time.Duration
			n := 0
			for d := range first {
				if wake == 0 || d < wake {
					wake = d
				}
				sum += d
				n++
			}
			evidence.Samples = append(evidence.Samples, headWaitSample{
				Depth: depth, Scenario: "wake/live-only", Waiters: 8, Repeat: repeat,
				RefProcesses: counter.refs.Load(), GitProcesses: counter.all.Load(),
				MeanWaitMS: float64(sum.Microseconds()) / float64(max(n, 1)) / 1000, WakeMS: float64(wake.Microseconds()) / 1000,
			})
		}
		// Cold read availability: a fresh workspace on the same repository,
		// eight readers started together, time to the first verified
		// snapshot for each. One audit serves them all.
		for repeat := 1; repeat <= repeats; repeat++ {
			fresh, err := app.Open(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			// The fresh workspace gets its own observer, so the Git processes
			// of the cold window are measured rather than assumed.
			cold := &refCounter{}
			fresh.SetObserver(cold)
			var wg sync.WaitGroup
			elapsed := make([]time.Duration, 8)
			started := make(chan struct{})
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-started
					began := time.Now()
					if _, err := fresh.Snapshot(ctx); err != nil {
						t.Errorf("cold read: %v", err)
					}
					elapsed[i] = time.Since(began)
				}(i)
			}
			close(started)
			wg.Wait()
			var total, slowest time.Duration
			for _, d := range elapsed {
				total += d
				if d > slowest {
					slowest = d
				}
			}
			evidence.Samples = append(evidence.Samples, headWaitSample{
				Depth: depth, Scenario: "cold-read/eight-readers", Waiters: 8, Repeat: repeat,
				RefProcesses: cold.refs.Load(), GitProcesses: cold.all.Load(),
				MeanWaitMS: float64(total.Microseconds()) / 8 / 1000, WakeMS: float64(slowest.Microseconds()) / 1000,
				Note: "wake_ms here is the slowest reader; processes are the fresh workspace's own, measured in the window",
			})
		}
		_ = workspace
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d samples to %s", len(evidence.Samples), out)
}
