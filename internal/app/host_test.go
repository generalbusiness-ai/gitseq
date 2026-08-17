package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	testApplication = "gitseq-host-test"
	testFoldVersion = "gitseq-host-test-fold@0"
)

// withTestHost adds a second interpreter to the registry for the duration of
// one test. The registry is process state, so the test restores it rather than
// leaving a host behind for whatever runs next.
func withTestHost(t *testing.T, foldVersion string) host {
	t.Helper()
	if _, exists := hosts[testApplication]; exists {
		t.Fatalf("%q is already registered", testApplication)
	}
	registered := host{application: testApplication, foldVersion: foldVersion, newFolder: workroom.NewFolder}
	hosts[testApplication] = registered
	t.Cleanup(func() { delete(hosts, testApplication) })
	return registered
}

func TestWorkroomRepositoryRecordsNoBindingAndSelectsWorkroom(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Absence is the workroom declaration, permanently. Recording one would put
	// a record in the opening of every workroom log to repeat what the reader
	// already knows.
	if snapshot.Depth != 1 {
		t.Fatalf("depth after init = %d, want 1: the default application records no binding", snapshot.Depth)
	}
	recorded, err := workspace.readBinding(ctx)
	if err != nil || recorded != nil {
		t.Fatalf("binding = %+v err=%v, want none", recorded, err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	selected := reopened.selected.Load()
	if selected == nil || selected.err != nil || selected.host.application != defaultApplication {
		t.Fatalf("open selected %+v, want the workroom host", selected)
	}
	if selected.host.foldVersion != workroom.ProfileVersion || reopened.foldProfile() != workroom.ProfileVersion {
		t.Fatalf("selected fold = %q, checkpoint profile = %q, want %q", selected.host.foldVersion, reopened.foldProfile(), workroom.ProfileVersion)
	}
}

func TestInitRecordsTheBindingOfAnApplicationAbsenceDoesNotName(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	withTestHost(t, testFoldVersion)
	workspace, _, err := initHosted(ctx, repo, "human", 1<<20, testApplication)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := workspace.readBinding(ctx)
	if err != nil || recorded == nil {
		t.Fatalf("binding = %+v err=%v, want the recorded binding", recorded, err)
	}
	if recorded.Application != testApplication || recorded.FoldVersion != testFoldVersion {
		t.Fatalf("binding = %+v, want application %q at fold %q", recorded, testApplication, testFoldVersion)
	}
	if recorded.SourceCommit != "" && !strings.HasPrefix(recorded.SourceCommit, "git:") {
		t.Fatalf("binding source commit %q is not format-qualified", recorded.SourceCommit)
	}
	opening, err := kernel.ReadOpening(ctx, workspace.Store, workspace.Config.Genesis, openingRecords)
	if err != nil {
		t.Fatal(err)
	}
	if len(opening) != 2 || opening[1].Intent.Schema != bindingSchema {
		t.Fatalf("opening records = %d, second schema = %q, want the binding in the opening", len(opening), opening[len(opening)-1].Intent.Schema)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	selected := reopened.selected.Load()
	if selected == nil || selected.err != nil || selected.host.application != testApplication {
		t.Fatalf("open selected %+v, want the bound host", selected)
	}
	if reopened.foldProfile() != testFoldVersion {
		t.Fatalf("checkpoint profile = %q, want the bound fold %q", reopened.foldProfile(), testFoldVersion)
	}
}

func TestBoundRepositoryIsVerifiableWithoutItsInterpreter(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	release := withTestHost(t, testFoldVersion)
	if _, _, err := initHosted(ctx, repo, "human", 1<<20, testApplication); err != nil {
		t.Fatal(err)
	}
	// A build that does not hold the application: the log is unchanged, only
	// the reader is different.
	delete(hosts, release.application)
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatalf("open refused a repository it can still verify: %v", err)
	}
	verification, err := reopened.Verify(ctx)
	if err != nil || verification.Depth != 2 {
		t.Fatalf("verify = %+v err=%v, want the kernel facts to stand", verification, err)
	}
	_, err = reopened.Snapshot(ctx)
	if err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("snapshot error = %v, want a refusal to interpret", err)
	}
	if _, err := reopened.Act(ctx, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "under the wrong interpreter", IdempotencyKey: "wrong"}); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("act error = %v, want appends refused while the interpreter is unavailable", err)
	}
}

func TestBoundRepositoryRefusesAnotherFoldVersion(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	withTestHost(t, testFoldVersion)
	if _, _, err := initHosted(ctx, repo, "human", 1<<20, testApplication); err != nil {
		t.Fatal(err)
	}
	// The same application, a different meaning. A fold-version change
	// invalidates every reader by construction.
	hosts[testApplication] = host{application: testApplication, foldVersion: testFoldVersion + "1", newFolder: workroom.NewFolder}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "uninterpretable") {
		t.Fatalf("snapshot error = %v, want a refusal naming the fold mismatch", err)
	}
}

func TestBindingAfterTheOpeningRecordsHasNoForce(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	withTestHost(t, testFoldVersion)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "ordinary work first",
		RestsOn: []string{seed.ID}, IdempotencyKey: "first",
	})
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.buildBindingRequest(ctx, private, "human", binding{Application: testApplication, FoldVersion: testFoldVersion})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := reopened.readBinding(ctx)
	if err != nil || recorded != nil {
		t.Fatalf("binding = %+v err=%v: a binding past the opening records has no force", recorded, err)
	}
	selected := reopened.selected.Load()
	if selected == nil || selected.err != nil || selected.host.application != defaultApplication {
		t.Fatalf("open selected %+v, want the workroom host", selected)
	}
	snapshot, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range snapshot.Projection.Decisions {
		if decision.Event == submission.Record.ID && decision.Verdict == workroom.Effective {
			t.Fatalf("workroom fold gave a binding-shaped record force: %+v", decision)
		}
	}
}

func TestBindingFromAnotherKeyHasNoForce(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	withTestHost(t, testFoldVersion)
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// A key with custody and admission, but not the one that initialized the
	// repository, recording a binding in the opening position.
	private, fingerprint, keyFile, err := generateActor(filepath.Join(workspace.MetaDir, "actors"), "intruder")
	if err != nil {
		t.Fatal(err)
	}
	workspace.Config.Actors["intruder"] = Actor{Name: "intruder", Fingerprint: fingerprint, KeyFile: keyFile}
	request, err := workspace.buildBindingRequest(ctx, private, "intruder", binding{Application: testApplication, FoldVersion: testFoldVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.AcceptSubmission(ctx, request); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := reopened.readBinding(ctx)
	if err != nil || recorded != nil {
		t.Fatalf("binding = %+v err=%v: only the initializing key binds a repository", recorded, err)
	}
	if _, err := reopened.Snapshot(ctx); err != nil {
		t.Fatalf("workroom repository stopped reading after an unauthorized binding: %v", err)
	}
}

func TestBindingPayloadsAreCanonicalAndComplete(t *testing.T) {
	for name, payload := range map[string]string{
		"no application":       `{"fold_version":"workroom-fold@3"}`,
		"no fold version":      `{"application":"workroom"}`,
		"bare source commit":   `{"application":"workroom","source_commit":"deadbeef","fold_version":"workroom-fold@3"}`,
		"unknown field":        `{"application":"workroom","fold_version":"workroom-fold@3","interpreter":"anything"}`,
		"not canonical":        `{"application":"workroom", "fold_version":"workroom-fold@3"}`,
		"reordered fields":     `{"fold_version":"workroom-fold@3","application":"workroom"}`,
		"trailing json":        `{"application":"workroom","fold_version":"workroom-fold@3"}{}`,
		"empty":                ``,
		"empty source commit":  `{"application":"workroom","source_commit":"","fold_version":"workroom-fold@3"}`,
		"unqualified url form": `{"application":"workroom","source_commit":"sha1:deadbeef","fold_version":"workroom-fold@3"}`,
	} {
		if _, err := decodeBinding([]byte(payload)); err == nil {
			t.Fatalf("%s: decoded %q, want a refusal", name, payload)
		}
	}
	accepted := `{"application":"workroom","source_commit":"git:sha1:0123456789012345678901234567890123456789","source_url":"https://example.invalid/workroom","fold_version":"workroom-fold@3"}`
	decoded, err := decodeBinding([]byte(accepted))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Application != "workroom" || decoded.FoldVersion != "workroom-fold@3" || decoded.SourceURL != "https://example.invalid/workroom" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
