package service

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type planner144Counter struct {
	refs atomic.Int64
	all  atomic.Int64
}

func (o *planner144Counter) Record(_ context.Context, m observe.Measurement) {
	if m.Operation == observe.OperationGit {
		o.all.Add(1)
		if m.Path == observe.PathRef {
			o.refs.Add(1)
		}
	}
}

// Measurement only: the assertions validate the fixture and unchanged frontier,
// not a scheduler-sensitive performance threshold. All records and keys are synthetic.
func TestPlanner144WaitCost(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	w, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	count := &planner144Counter{}
	s, err := NewObserved(w, count)
	if err != nil {
		t.Fatal(err)
	}
	for _, records := range []int{0, 30} {
		if records > 0 {
			for i := 0; i < records; i++ {
				_, err = w.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: fmt.Sprintf("synthetic %d", i), IdempotencyKey: fmt.Sprintf("planner144-%d", i)})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		initial, err := s.status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, scenario := range []struct {
			name    string
			n       int
			stagger time.Duration
		}{{"one", 1, 0}, {"eight-aligned", 8, 0}, {"eight-staggered", 8, 23 * time.Millisecond}} {
			for repeat := 0; repeat < 3; repeat++ {
				count.refs.Store(0)
				count.all.Store(0)
				start := make(chan struct{})
				var wg sync.WaitGroup
				elapsed := make([]time.Duration, scenario.n)
				errs := make([]error, scenario.n)
				for i := 0; i < scenario.n; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						<-start
						time.Sleep(time.Duration(i) * scenario.stagger)
						began := time.Now()
						result, _, changed, err := s.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 1100})
						elapsed[i] = time.Since(began)
						if err != nil {
							errs[i] = err
							return
						}
						if changed || result.Status.Durable.Head != initial.Durable.Head {
							errs[i] = fmt.Errorf("fixture unexpectedly changed")
						}
					}(i)
				}
				close(start)
				wg.Wait()
				var total time.Duration
				for i, e := range errs {
					if e != nil {
						t.Fatal(e)
					}
					total += elapsed[i]
				}
				t.Logf("depth=%d scenario=%s repeat=%d git_processes=%d ref_processes=%d mean_wait_ms=%.3f", initial.Durable.Depth, scenario.name, repeat+1, count.all.Load(), count.refs.Load(), float64(total.Microseconds())/float64(scenario.n)/1000)
			}
		}
	}
}
