package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/connector/github"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Where an observation goes. The connector shares one routing rule with gs:
// the repository's advertised resident by default, an explicit loopback URL,
// or "-" for the local fold; anything else that is advertised refuses before
// the connector key is read. These tests drive the connector's own
// observation path (resolve the route, then sign and submit a real
// observation) against a real resident on a loopback listener, and the
// refusal cases through the command itself. Each is written so that removing
// exactly one guard fails it by name.

type nopObserver struct{}

func (nopObserver) Record(context.Context, observe.Measurement) {}

type observationRepo struct {
	path      string
	workspace *app.Workspace
	charter   workroom.Record
}

// newObservationRepo initialises a workroom whose operator is the connector
// itself, so the connector's signing key is in local custody.
func newObservationRepo(t *testing.T) observationRepo {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(context.Background(), repo, "connector", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return observationRepo{path: repo, workspace: workspace, charter: seed}
}

func countingServer(t *testing.T, hits *atomic.Int64, inner http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		inner.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	return server
}

// serveResident serves this workspace's own resident on a loopback listener,
// exactly as `gs serve` does, and returns its URL and request counter.
func serveResident(t *testing.T, workspace *app.Workspace) (string, *atomic.Int64) {
	t.Helper()
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	listener := countingServer(t, &hits, server.Handler())
	return listener.URL, &hits
}

func advertise(t *testing.T, workspace *app.Workspace, url string) {
	t.Helper()
	if _, err := workspace.PublishResident(url); err != nil {
		t.Fatal(err)
	}
}

func snapshotOf(t *testing.T, workspace *app.Workspace) app.Snapshot {
	t.Helper()
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func observation(id string) github.Observation {
	return github.Observation{
		ExternalID: "github:issue:" + id, IdempotencyKey: "observe-" + id,
		Text: "issue " + id, Body: map[string]string{"external_id": "github:issue:" + id},
	}
}

// observeThrough is the connector's write path as the command runs it: the
// route is resolved first, then the key is read and one real observation is
// signed and submitted. It returns the routing or submission error.
func observeThrough(t *testing.T, repo observationRepo, explicit string, id string) (string, error) {
	t.Helper()
	ctx := context.Background()
	serverURL, err := residentclient.ResolveServerURL(repo.workspace, explicit)
	if err != nil {
		return "", err
	}
	connector, err := loadObservationIdentity(repo.workspace, "connector")
	if err != nil {
		t.Fatal(err)
	}
	return appendObservation(ctx, repo.workspace, connector, serverURL, repo.charter.ID, observation(id))
}

func mustNotAppend(t *testing.T, workspace *app.Workspace, before app.Snapshot) {
	t.Helper()
	after := snapshotOf(t, workspace)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("a refused observation landed anyway: head %s depth %d, want %s/%d", after.Head, after.Depth, before.Head, before.Depth)
	}
}

func mustAppendOne(t *testing.T, workspace *app.Workspace, before app.Snapshot, event string) {
	t.Helper()
	after := snapshotOf(t, workspace)
	if after.Depth != before.Depth+1 {
		t.Fatalf("depth %d after the observation, want %d", after.Depth, before.Depth+1)
	}
	if !strings.HasSuffix(event, after.Head) {
		t.Fatalf("observation %s is not the new head %s", event, after.Head)
	}
}

// Advertised, no flag: the observation crosses the advertised resident's
// socket and the event its kernel appended is the new head.
func TestObservationDefaultsToTheAdvertisedResident(t *testing.T) {
	repo := newObservationRepo(t)
	url, hits := serveResident(t, repo.workspace)
	advertise(t, repo.workspace, url)
	before := snapshotOf(t, repo.workspace)
	event, err := observeThrough(t, repo, "", "1")
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() == 0 {
		t.Fatal("the advertised resident was never dialled; the observation folded locally")
	}
	mustAppendOne(t, repo.workspace, before, event)
}

// No advertisement: the connector acts locally exactly as before.
func TestObservationStaysLocalWithoutAnAdvertisement(t *testing.T) {
	repo := newObservationRepo(t)
	before := snapshotOf(t, repo.workspace)
	event, err := observeThrough(t, repo, "", "1")
	if err != nil {
		t.Fatal(err)
	}
	mustAppendOne(t, repo.workspace, before, event)
}

// An explicit loopback URL selects that resident even with no advertisement.
func TestObservationHonoursAnExplicitResidentURL(t *testing.T) {
	repo := newObservationRepo(t)
	url, hits := serveResident(t, repo.workspace)
	before := snapshotOf(t, repo.workspace)
	event, err := observeThrough(t, repo, url, "1")
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() == 0 {
		t.Fatal("the explicit resident was never dialled")
	}
	mustAppendOne(t, repo.workspace, before, event)
}

// "-" forces the local fold and never dials the advertised resident.
func TestLocalSentinelDoesNotDialTheAdvertisedResident(t *testing.T) {
	repo := newObservationRepo(t)
	url, hits := serveResident(t, repo.workspace)
	advertise(t, repo.workspace, url)
	before := snapshotOf(t, repo.workspace)
	event, err := observeThrough(t, repo, residentclient.LocalRoute, "1")
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("--server - dialled the advertised resident %d times", hits.Load())
	}
	mustAppendOne(t, repo.workspace, before, event)
}

// An advertisement that cannot be trusted or used refuses; nothing is
// appended locally.
func TestUntrustedOrUnusableAdvertisementsRefuse(t *testing.T) {
	cases := []struct {
		name, record, want string
	}{
		{"garbage record", "not json", "cannot be trusted"},
		{"addressless record", `{"genesis":"x","pid":1}`, "cannot be trusted"},
		{"foreign workroom", `{"url":"http://127.0.0.1:1","genesis":"0000000000000000000000000000000000000000","pid":1}`, "cannot be trusted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newObservationRepo(t)
			if err := os.WriteFile(filepath.Join(repo.workspace.MetaDir, "resident.json"), []byte(tc.record), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshotOf(t, repo.workspace)
			_, err := observeThrough(t, repo, "", "1")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			mustNotAppend(t, repo.workspace, before)
		})
	}
	for _, url := range []string{"not a url", "http://10.0.0.1:9", "https://127.0.0.1:1", "http://user:pw@127.0.0.1:1"} {
		t.Run("advertised "+url, func(t *testing.T) {
			repo := newObservationRepo(t)
			advertise(t, repo.workspace, url)
			before := snapshotOf(t, repo.workspace)
			_, err := observeThrough(t, repo, "", "1")
			if err == nil || !strings.Contains(err.Error(), "not usable") {
				t.Fatalf("error = %v, want an unusable-advertisement refusal", err)
			}
			mustNotAppend(t, repo.workspace, before)
		})
	}
}

// An advertised resident that is not listening refuses and names the way
// out; the local log does not move.
func TestUnreachableAdvertisedResidentRefuses(t *testing.T) {
	repo := newObservationRepo(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	listener.Close()
	advertise(t, repo.workspace, url)
	before := snapshotOf(t, repo.workspace)
	_, err = observeThrough(t, repo, "", "1")
	if err == nil || !strings.Contains(err.Error(), "no resident is listening") || !strings.Contains(err.Error(), "--server -") {
		t.Fatalf("error = %v, want the refused-dial sentence naming --server -", err)
	}
	mustNotAppend(t, repo.workspace, before)
}

// A resident that answers with a refusal is a refusal, not a reason to fold
// locally.
func TestRefusingResidentDoesNotFoldLocally(t *testing.T) {
	repo := newObservationRepo(t)
	var hits atomic.Int64
	server := countingServer(t, &hits, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "resident unavailable", http.StatusServiceUnavailable)
	}))
	advertise(t, repo.workspace, server.URL)
	before := snapshotOf(t, repo.workspace)
	_, err := observeThrough(t, repo, "", "1")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want the resident's refusal", err)
	}
	if hits.Load() == 0 {
		t.Fatal("the advertised resident was never asked")
	}
	mustNotAppend(t, repo.workspace, before)
}

// The command resolves the route before it reads the connector key: with an
// untrusted advertisement and no connector key in custody, the refusal is
// about the advertisement, and no observation is attempted.
func TestTheCommandResolvesTheRouteBeforeReadingTheKey(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.MetaDir, "resident.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotOf(t, workspace)
	err = run(ctx, []string{"--repo", repo, "--as", "github-connector", "--charter", seed.ID, "--owner", "o", "--repo-name", "r"})
	if err == nil || !strings.Contains(err.Error(), "cannot be trusted") {
		t.Fatalf("error = %v, want the advertisement refusal before any key or charter check", err)
	}
	mustNotAppend(t, workspace, before)
}

// The command itself, end to end: a chartered clause, a stubbed tracker
// returning one issue, and a real advertised resident. The observation must
// cross the resident's socket and become the new head. This is the control
// for the production call site that hands the resolved route to the
// observation: routing straight from the flag there passes every other test
// and fails this one.
func TestTheCommandRoutesObservationsToTheAdvertisedResident(t *testing.T) {
	ctx := context.Background()
	repo := newObservationRepo(t)
	state := func(act app.Act) workroom.Record {
		t.Helper()
		submission, err := repo.workspace.Act(ctx, "connector", act)
		if err != nil {
			t.Fatal(err)
		}
		return submission.Record
	}
	charter := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindPropose, Text: "connector charter",
		Body:    map[string]string{"connector": "github", "owner": "o", "repo": "r", "actor": "connector", "operations": "observe"},
		RestsOn: []string{repo.charter.ID}, IdempotencyKey: "charter",
	})
	state(app.Act{Verb: app.VerbRatify, Target: charter.ID, IdempotencyKey: "ratify-charter"})
	state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "observe issue 7",
		Body:    map[string]string{"connector": "github", "issues": "7"},
		RestsOn: []string{charter.ID}, IdempotencyKey: "clause",
	})

	tracker := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/o/r/issues/7" {
			http.NotFound(writer, request)
			return
		}
		json.NewEncoder(writer).Encode(map[string]any{
			"number": 7, "title": "Seven", "body": "", "html_url": "https://example.invalid/o/r/issues/7",
			"state": "open", "user": map[string]string{"login": "someone"},
		})
	}))
	t.Cleanup(tracker.Close)
	previous := newGitHubClient
	newGitHubClient = func(token string) *github.Client {
		return &github.Client{BaseURL: tracker.URL, HTTP: tracker.Client(), Token: token, Logger: log.New(io.Discard, "", 0)}
	}
	t.Cleanup(func() { newGitHubClient = previous })

	url, hits := serveResident(t, repo.workspace)
	advertise(t, repo.workspace, url)
	before := snapshotOf(t, repo.workspace)
	err := run(ctx, []string{"--repo", repo.path, "--as", "connector", "--charter", charter.ID, "--owner", "o", "--repo-name", "r"})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() == 0 {
		t.Fatal("the command appended its observation without dialling the advertised resident")
	}
	after := snapshotOf(t, repo.workspace)
	if after.Depth != before.Depth+1 {
		t.Fatalf("depth %d after the run, want %d", after.Depth, before.Depth+1)
	}

	// The sentinel through the same command: the resident is still advertised
	// and still not dialled.
	hits.Store(0)
	err = run(ctx, []string{"--repo", repo.path, "--as", "connector", "--charter", charter.ID, "--owner", "o", "--repo-name", "r", "--server", residentclient.LocalRoute})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("--server - dialled the advertised resident %d times", hits.Load())
	}
}

// A proposal publishes to the forge and submits nothing to a resident, so
// the observation route is not its business: an untrusted advertisement must
// not stop an otherwise valid dry-run proposal. Neither execution contacts
// GitHub.
func TestAProposalDoesNotResolveTheObservationRoute(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	state := func(act app.Act) workroom.Record {
		t.Helper()
		submission, err := workspace.Act(ctx, "human", act)
		if err != nil {
			t.Fatal(err)
		}
		return submission.Record
	}
	charter := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindPropose, Text: "connector charter",
		Body:    map[string]string{"connector": "github", "owner": "o", "repo": "r", "actor": "github-connector", "operations": "propose"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "charter",
	})
	state(app.Act{Verb: app.VerbRatify, Target: charter.ID, IdempotencyKey: "ratify-charter"})
	request := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "fix issue",
		Body:    map[string]string{"to": workspace.View().Actors["human"].Fingerprint, "conditions": "exact head", "no_git_artifact": "true"},
		RestsOn: []string{seed.ID}, IdempotencyKey: "request",
	})
	const commit = "1111111111111111111111111111111111111111"
	artifact := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "candidate",
		Body:    map[string]string{"path": "internal/connector", "commit": commit},
		RestsOn: []string{request.ID}, IdempotencyKey: "artifact",
	})
	if err := os.WriteFile(filepath.Join(workspace.MetaDir, "resident.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run(ctx, []string{
		"--repo", repo, "--as", "github-connector", "--charter", charter.ID, "--owner", "o", "--repo-name", "r",
		"--propose", "7", "--branch", "request/fix", "--base", "main", "--commit", commit,
		"--request", request.ID, "--artifact", artifact.ID, "--title", "Fix issue", "--dry-run",
	})
	if err != nil {
		t.Fatalf("a dry-run proposal was stopped by the observation route: %v", err)
	}
}
