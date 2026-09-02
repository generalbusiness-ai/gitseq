package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Where a command acts, when nobody passed --server. The six cases are the
// whole decision: an advertised resident answers, "-" forces the local fold,
// no advertisement acts locally, and a malformed, non-loopback or unreachable
// advertisement refuses instead of quietly folding a durable act locally.
// Each test is written so that removing exactly one guard fails it by name.

type nopObserver struct{}

func (nopObserver) Record(context.Context, observe.Measurement) {}

// countingServer wraps a handler and counts every request it serves, so a
// test can tell a resident answer from a local fold: the local fold never
// dials the listener at all.
func countingServer(t *testing.T, hits *atomic.Int64, inner http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		inner.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	return server
}

// publishResident serves this workspace's own resident on a loopback listener
// and advertises it in the repository, exactly as `gs serve` does.
func publishResident(t *testing.T, workspace *app.Workspace) *atomic.Int64 {
	t.Helper()
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	listener := countingServer(t, &hits, server.Handler())
	if _, err := workspace.PublishResident(listener.URL); err != nil {
		t.Fatal(err)
	}
	return &hits
}

// runPiped runs one command with standard output and standard error captured,
// and returns the command error together with both captures.
func runPiped(t *testing.T, run func() error) (error, string, string) {
	t.Helper()
	stdoutOld, stderrOld := os.Stdout, os.Stderr
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outWriter, errWriter
	commandErr := run()
	os.Stdout, os.Stderr = stdoutOld, stderrOld
	outWriter.Close()
	errWriter.Close()
	outBytes, _ := io.ReadAll(outReader)
	errBytes, _ := io.ReadAll(errReader)
	outReader.Close()
	errReader.Close()
	return commandErr, string(outBytes), string(errBytes)
}

// writeAssert files one durable act through whatever the flags resolve to.
func writeAssert(t *testing.T, repo, text string, arguments ...string) (error, string) {
	t.Helper()
	commandErr, _, stderr := runPiped(t, func() error {
		return stateCommand(context.Background(), append([]string{
			"--repo", repo, "--as", "operator", "--kind", "assert", "--text", text}, arguments...))
	})
	return commandErr, stderr
}

// mustNotAppend proves a refused command left the log exactly where it was.
func mustNotAppend(t *testing.T, workspace *app.Workspace, before app.Snapshot) {
	t.Helper()
	after, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("a refused act landed anyway: head %s depth %d, want %s/%d", after.Head, after.Depth, before.Head, before.Depth)
	}
}

// Advertised. A durable write with no --server flag crosses the advertised
// resident's socket and the event its kernel appended is in the log.
// Dropping the advertisement lookup from resolveServerURL leaves no dial and
// fails this by name.
func TestAdvertisedResidentCarriesWriteWithoutServerFlag(t *testing.T) {
	const text = "carried by the advertised resident"
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	hits := publishResident(t, workspace)
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	commandErr, stderr := writeAssert(t, workspace.Repo, text)
	if commandErr != nil {
		t.Fatalf("durable write with an advertised resident: %v", commandErr)
	}
	if hits.Load() == 0 {
		t.Fatalf("the advertised resident was never dialed; the write folded locally (stderr=%q)", stderr)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head == before.Head || after.Depth <= before.Depth {
		t.Fatalf("the resident answered but nothing landed: head %s depth %d, want an advance past %s/%d", after.Head, after.Depth, before.Head, before.Depth)
	}
	for _, statement := range after.Projection.Statements {
		if statement.Kind == workroom.KindAssert && statement.Text == text {
			return
		}
	}
	t.Fatal("the act did not land in the resident's log as an assert")
}

// Advertised, on a read. The bounded status read is answered by the resident
// and not by a local fold, with no flag at all.
func TestAdvertisedResidentAnswersReadWithoutServerFlag(t *testing.T) {
	workspace, _ := statusSummaryFixture(t)
	hits := publishResident(t, workspace)

	commandErr, out, stderr := runPiped(t, func() error {
		return statusCommand(context.Background(), []string{"--repo", workspace.Repo, "--json"})
	})
	if commandErr != nil {
		t.Fatalf("status with an advertised resident: %v", commandErr)
	}
	if hits.Load() == 0 {
		t.Fatalf("the advertised resident was never dialed; the read fell back locally (stderr=%q)", stderr)
	}
	if strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("the resident answer was refused and fell back locally: %q", stderr)
	}
	var durable app.Snapshot
	if err := json.Unmarshal([]byte(out), &durable); err != nil {
		t.Fatalf("resident status output was not the durable JSON: %v", err)
	}
}

// Overridden local. "--server -" folds locally even while a healthy resident
// is advertised, so a resident defect can be reproduced without editing
// anything. Removing the sentinel dials the resident and fails this by name.
func TestServerDashForcesTheLocalFold(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	hits := publishResident(t, workspace)
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	commandErr, stderr := writeAssert(t, workspace.Repo, "folded locally on purpose", "--server", "-")
	if commandErr != nil {
		t.Fatalf("forced-local durable write: %v", commandErr)
	}
	if hits.Load() != 0 {
		t.Fatalf("a forced-local write dialed the advertised resident %d times", hits.Load())
	}
	if strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("a deliberate local fold must not announce a fallback: %q", stderr)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Depth <= before.Depth {
		t.Fatalf("the forced-local write landed nothing: depth %d, want an advance past %d", after.Depth, before.Depth)
	}
}

// The authority commands are multi-act operations, and two of them also
// change local key custody, so they cannot be forwarded as one resident
// submission. Each must refuse before its first local mutation while a
// resident is advertised. Removing the requireLocalAuthorityWrite call from
// any one handler makes that command's named case append and fail here. An
// unusable advertisement and an explicit URL exercise the other two refusal
// inputs, while the explicit local sentinel remains the deliberate escape
// hatch.
func TestAuthorityCommandsRefuseAnAdvertisedResidentBeforeLocalMutation(t *testing.T) {
	t.Parallel()
	binary := buildGS(t)
	commands := []struct {
		name  string
		setup func(t *testing.T, workspace *app.Workspace)
		args  []string
	}{
		{
			name: "actor-add",
			args: []string{"--as", "human", "--name", "new-agent", "--kind", "agent"},
		},
		{
			name: "role-grant",
			setup: func(t *testing.T, workspace *app.Workspace) {
				t.Helper()
				if _, _, err := workspace.AddActor(context.Background(), "human", "agent", "agent"); err != nil {
					t.Fatal(err)
				}
			},
			args: []string{"--as", "human", "--actor", "agent", "--role", "ratifier"},
		},
		{
			name: "role-revoke",
			setup: func(t *testing.T, workspace *app.Workspace) {
				t.Helper()
				if _, _, err := workspace.AddActor(context.Background(), "human", "agent", "agent"); err != nil {
					t.Fatal(err)
				}
				if _, err := workspace.GrantRole(context.Background(), "human", "agent", "ratifier"); err != nil {
					t.Fatal(err)
				}
			},
			args: []string{"--as", "human", "--actor", "agent", "--role", "ratifier"},
		},
		{
			name: "actor-retire",
			setup: func(t *testing.T, workspace *app.Workspace) {
				t.Helper()
				if _, _, err := workspace.AddActor(context.Background(), "human", "agent", "agent"); err != nil {
					t.Fatal(err)
				}
			},
			args: []string{"--as", "human", "--actor", "agent"},
		},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			repo, workspace := servableRepository(t)
			if command.setup != nil {
				command.setup(t, workspace)
			}
			hits := publishResident(t, workspace)
			before, err := workspace.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			arguments := append([]string{command.name, "--repo", repo}, command.args...)

			output, commandErr := exec.Command(binary, arguments...).CombinedOutput()
			if commandErr == nil {
				t.Fatalf("gs %s appended locally past the advertised resident: %s", command.name, output)
			}
			for _, want := range []string{"has no resident write path", "--server -"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("gs %s refusal does not name %q: %s", command.name, want, output)
				}
			}
			if hits.Load() != 0 {
				t.Fatalf("gs %s dialed the resident %d times instead of refusing before mutation", command.name, hits.Load())
			}
			mustNotAppend(t, workspace, before)

			var explicitHits atomic.Int64
			explicitResident := countingServer(t, &explicitHits, http.NotFoundHandler())
			output, commandErr = exec.Command(binary, append(arguments, "--server", explicitResident.URL)...).CombinedOutput()
			if commandErr == nil {
				t.Fatalf("gs %s accepted an explicit resident URL: %s", command.name, output)
			}
			for _, want := range []string{"has no resident write path", "--server -"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("gs %s explicit-URL refusal does not name %q: %s", command.name, want, output)
				}
			}
			if explicitHits.Load() != 0 {
				t.Fatalf("gs %s dialed the explicit resident %d times instead of refusing", command.name, explicitHits.Load())
			}
			mustNotAppend(t, workspace, before)

			if _, err := workspace.PublishResident("not-a-url"); err != nil {
				t.Fatal(err)
			}
			output, commandErr = exec.Command(binary, arguments...).CombinedOutput()
			if commandErr == nil {
				t.Fatalf("gs %s accepted an unusable advertised resident: %s", command.name, output)
			}
			for _, want := range []string{"advertises", "not-a-url", "--server -"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("gs %s unusable-advertisement refusal does not name %q: %s", command.name, want, output)
				}
			}
			if hits.Load() != 0 {
				t.Fatalf("gs %s dialed the prior resident %d times after its advertisement changed", command.name, hits.Load())
			}
			mustNotAppend(t, workspace, before)

			output, commandErr = exec.Command(binary, append(arguments, "--server", "-")...).CombinedOutput()
			if commandErr != nil {
				t.Fatalf("gs %s --server -: %v: %s", command.name, commandErr, output)
			}
			after, err := workspace.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if after.Depth <= before.Depth {
				t.Fatalf("gs %s --server - appended nothing: depth %d, want an advance past %d", command.name, after.Depth, before.Depth)
			}
			if hits.Load() != 0 {
				t.Fatalf("gs %s --server - dialed the resident %d times", command.name, hits.Load())
			}
		})
	}
}

// No advertisement. A repository that publishes nothing acts locally exactly
// as before: the command succeeds and says nothing about a fallback. Turning
// a missing advertisement into a refusal fails this by name.
func TestNoAdvertisementActsLocally(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	commandErr, stderr := writeAssert(t, workspace.Repo, "folded locally, nothing advertised")
	if commandErr != nil {
		t.Fatalf("durable write with no advertisement: %v", commandErr)
	}
	if strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("a purely local act must not announce a fallback: %q", stderr)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Depth <= before.Depth {
		t.Fatalf("the local write landed nothing: depth %d, want an advance past %d", after.Depth, before.Depth)
	}
}

// Malformed advertisement. Every published value that is not the bare
// loopback origin the resident publishes is refused before any dial, and the
// refusal names both the advertisement and the way out. The listener here is
// live and counting, so accepting any of these shapes shows up as a dial that
// should never have happened: dropping the URL-shape check from ValidateURL
// smuggles a path, a scheme or a credential past the boundary and fails this
// by name.
func TestMalformedAdvertisementRefusesWrite(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	var hits atomic.Int64
	listener := countingServer(t, &hits, http.NotFoundHandler())
	for _, advertised := range []string{
		listener.URL + "/v0/submit",
		"https" + strings.TrimPrefix(listener.URL, "http"),
		strings.Replace(listener.URL, "http://", "http://smuggled@", 1),
		"not-a-url",
	} {
		t.Run(advertised, func(t *testing.T) {
			if _, err := workspace.PublishResident(advertised); err != nil {
				t.Fatal(err)
			}
			before, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}

			commandErr, stderr := writeAssert(t, workspace.Repo, "must not land")
			if commandErr == nil {
				t.Fatal("a malformed advertisement must be refused, not silently folded locally")
			}
			for _, want := range []string{"advertises", "--server -"} {
				if !strings.Contains(commandErr.Error(), want) {
					t.Fatalf("the refusal does not name %q: %v", want, commandErr)
				}
			}
			if hits.Load() != 0 {
				t.Fatalf("an unusable advertisement was dialed %d times", hits.Load())
			}
			if strings.Contains(stderr, "verified local fallback") {
				t.Fatalf("the refusal must not masquerade as a fallback notice: %q", stderr)
			}
			mustNotAppend(t, workspace, before)
		})
	}
}

// Non-loopback advertisement. The record is an ordinary file any local
// process can write, so an address off the loopback interface is refused
// rather than dialed. Removing the validation of the advertised address
// opens that socket and fails this by name.
func TestNonLoopbackAdvertisementRefusesWrite(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	if _, err := workspace.PublishResident("http://192.0.2.1:7777"); err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	commandErr, stderr := writeAssert(t, workspace.Repo, "must not land")
	if commandErr == nil {
		t.Fatal("a non-loopback advertisement must be refused, not dialed or ignored")
	}
	if !strings.Contains(commandErr.Error(), "loopback") {
		t.Fatalf("the refusal does not say the address is not loopback: %v", commandErr)
	}
	if strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("the refusal must happen before any resident attempt: %q", stderr)
	}
	mustNotAppend(t, workspace, before)
}

// Unreachable advertisement. A record left behind by a service that is gone
// refuses the act and names the two ways out; it must never become an
// invisible multi-minute local fold. Making the submission boundary fall back
// to the local fold on a refused dial fails this by name.
func TestUnreachableAdvertisementRefusesWriteAndNamesRecovery(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	// Bind, take the address, then release it: nothing is listening there, so
	// the dial is refused rather than left hanging.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.PublishResident(address); err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	commandErr, stderr := writeAssert(t, workspace.Repo, "must not land")
	if commandErr == nil {
		t.Fatal("an unreachable advertised resident must refuse the act, not fold it locally")
	}
	message := commandErr.Error()
	for _, want := range []string{"nothing was appended", "gs serve", "--server -"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the refusal does not name %q: %v", want, commandErr)
		}
	}
	if strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("the refusal must not masquerade as a fallback notice: %q", stderr)
	}
	mustNotAppend(t, workspace, before)
}

// An explicit --server value is honoured only after the same loopback
// validation the advertisement gets, so one rule governs a hand-typed URL and
// a published one.
func TestResolveServerURLValidatesExplicitServer(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"http://127.0.0.1:7777":   true,
		"http://[::1]:7777":       true,
		"http://localhost:7777":   true,
		"https://127.0.0.1:7777":  false,
		"http://user@127.0.0.1:7": false,
		"http://192.0.2.1:7777":   false,
		"http://0.0.0.0:7777":     false,
		"http://127.0.0.1/x":      false,
		"http://127.0.0.1/?x=y":   false,
		"not-a-url":               false,
	}
	workspace, _ := statusSummaryFixture(t)
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			serverURL, err := resolveServerURL(workspace, raw)
			if got := err == nil; got != want {
				t.Fatalf("resolveServerURL(%q) success = %v, want %v", raw, got, want)
			}
			if want && serverURL != raw {
				t.Fatalf("resolveServerURL(%q) = %q, want the value as given", raw, serverURL)
			}
		})
	}
}

// The frontier check still refuses an advertised resident whose durable head
// is not this checkout's, and says so before answering from the local fold.
// The stub serves the resident's own status with exactly one lie, so only
// that check can refuse it; neutering validateRemoteFrontier admits the
// divergent answer and fails this by name.
func TestAdvertisedResidentWithDivergentFrontierIsRefused(t *testing.T) {
	workspace, summary := statusSummaryFixture(t)
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	listener := countingServer(t, &hits, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/status" {
			server.Handler().ServeHTTP(writer, request)
			return
		}
		recorded := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorded, request)
		var body map[string]any
		if err := json.Unmarshal(recorded.Body.Bytes(), &body); err != nil {
			t.Errorf("resident status was not JSON: %v", err)
			return
		}
		if durable, ok := body["durable"].(map[string]any); ok {
			durable["head"] = strings.Repeat("0", 40)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(body)
	}))
	if _, err := workspace.PublishResident(listener.URL); err != nil {
		t.Fatal(err)
	}

	commandErr, out, stderr := runPiped(t, func() error {
		return statusCommand(context.Background(), []string{"--repo", workspace.Repo, "--json"})
	})
	if commandErr != nil {
		t.Fatalf("a divergent resident must degrade to the local read, not fail: %v", commandErr)
	}
	if hits.Load() == 0 {
		t.Fatal("the divergent resident was never dialed")
	}
	if !strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("the divergent frontier was not refused on standard error: %q", stderr)
	}
	var durable app.Snapshot
	if err := json.Unmarshal([]byte(out), &durable); err != nil {
		t.Fatalf("the local fallback output was not the durable JSON: %v", err)
	}
	if durable.Head != summary.Durable.Head {
		t.Fatalf("the fallback answered head %s, want the checkout head %s", durable.Head, summary.Durable.Head)
	}
}

// The same exact-frontier guard inside askResident is what makes the default
// safe on the bounded query paths. The stub tells one lie, the frontier head,
// so deleting the guard alone fails this by name.
func TestAskResidentRefusesDivergentQueryFrontier(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	listener := countingServer(t, &hits, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/work-query" {
			server.Handler().ServeHTTP(writer, request)
			return
		}
		recorded := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorded, request)
		var body map[string]any
		if err := json.Unmarshal(recorded.Body.Bytes(), &body); err != nil {
			t.Errorf("the work-query answer was not JSON: %v", err)
			return
		}
		if frontier, ok := body["frontier"].(map[string]any); ok {
			frontier["head"] = strings.Repeat("0", 40)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(body)
	}))

	query := statusview.WorkQuery{Actor: workspace.View().Actors["operator"].Fingerprint}
	var page statusview.WorkPage
	answered := false
	_, _, stderr := runPiped(t, func() error {
		answered = askResident(ctx, workspace, listener.URL, "/v0/work-query", query, &page,
			func() statusview.Frontier { return page.Frontier })
		return nil
	})
	if answered {
		t.Fatal("askResident accepted an answer whose frontier head is not this checkout's")
	}
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	if !strings.Contains(stderr, "verified local fallback") {
		t.Fatalf("the refusal was not named on standard error: %q", stderr)
	}
}
