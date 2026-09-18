package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
)

// The connector takes no deadline flag, so the environment is the only place an
// operator can raise it — and it is read before any observation, so a value
// nobody can read stops the run rather than failing at the append, where it
// would read as the resident's fault rather than the operator's typing.
func TestConnectorRefusesAnUnreadableDeadlineBeforeObserving(t *testing.T) {
	t.Setenv(residentclient.SubmitDeadlineEnvironment, "eventually")
	err := run(context.Background(), []string{"--repo", t.TempDir(), "--as", "connector",
		"--charter", "git:sha1:0#git:sha1:0", "--owner", "owner", "--repo-name", "name"})
	if err == nil || !strings.Contains(err.Error(), residentclient.SubmitDeadlineEnvironment) {
		t.Fatalf("run with an unreadable deadline = %v, want a refusal naming the variable", err)
	}
}

// The deadline this run submits under is the one it was given, and a run that
// was given none submits under the value every caller had before.
func TestConnectorSubmitsUnderThisRunsDeadline(t *testing.T) {
	if deadline := submitDeadline(context.Background()); deadline != residentclient.DefaultSubmitDeadline {
		t.Fatalf("deadline without a value = %s, want %s", deadline, residentclient.DefaultSubmitDeadline)
	}
	ctx := withSubmitDeadline(context.Background(), 90*time.Second)
	if deadline := submitDeadline(ctx); deadline != 90*time.Second {
		t.Fatalf("deadline on the run's context = %s, want 90s", deadline)
	}
}

// And it reaches the dial, not merely the context: a resident that never answers
// is the only way to see which deadline the submission was actually made under.
func TestConnectorAppendWaitsForThisRunsDeadline(t *testing.T) {
	repo := newObservationRepo(t)
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); stalled.Close() })

	connector, err := loadObservationIdentity(repo.workspace, "connector")
	if err != nil {
		t.Fatal(err)
	}
	waited := func(t *testing.T, deadline time.Duration, id string) time.Duration {
		t.Helper()
		ctx := withSubmitDeadline(context.Background(), deadline)
		started := time.Now()
		_, err := appendObservation(ctx, repo.workspace, connector, stalled.URL, repo.charter.ID, observation(id))
		if err == nil {
			t.Fatal("a resident that never answered was read as a successful append")
		}
		if !residentclient.TimedOut(err) {
			t.Fatalf("stalled resident error = %v, want an expired deadline", err)
		}
		return time.Since(started)
	}
	if quick := waited(t, 200*time.Millisecond, "1"); quick > 3*time.Second {
		t.Fatalf("waited %s under a 200ms deadline: the deadline did not reach the dial", quick)
	}
	if patient := waited(t, 1500*time.Millisecond, "2"); patient < time.Second {
		t.Fatalf("waited only %s under a 1500ms deadline: the deadline did not reach the dial", patient)
	}
}
