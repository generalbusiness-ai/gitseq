package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Request authoring at the common boundary every surface reaches. What is
// proved here is the ordering: an act already accepted under this caller's
// idempotency key is recovered from the log before any ref is read, and
// nothing else about the caller's intent is recovered with it.

type authoringWorkspace struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	workspace *Workspace
	seed      string
}

func newAuthoringWorkspace(t *testing.T) authoringWorkspace {
	t.Helper()
	ctx := context.Background()
	repo := testRepoOnMain(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	return authoringWorkspace{t: t, ctx: ctx, repo: repo, workspace: workspace, seed: seed.ID}
}

func (f authoringWorkspace) git(arguments ...string) string {
	f.t.Helper()
	full := append([]string{"-C", f.repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, arguments...)
	output, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// filing is one caller's whole stated intent, so a control can change exactly
// one part of it and leave the rest alone.
type filing struct {
	key         string
	text        string
	body        map[string]string
	bases       []string
	attachments map[string][]byte
}

func (f authoringWorkspace) file(stated filing) (string, error) {
	f.t.Helper()
	bases := stated.bases
	if len(bases) == 0 {
		bases = []string{f.seed}
	}
	submission, err := f.workspace.Act(f.ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: stated.text,
		Body: stated.body, RestsOn: bases, Attachments: stated.attachments,
		IdempotencyKey: stated.key,
	})
	if err != nil {
		return "", err
	}
	return submission.Record.ID, nil
}

func (f authoringWorkspace) frontier() string {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot.Head
}

func (f authoringWorkspace) body(event string) map[string]string {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == event {
			return statement.Body
		}
	}
	f.t.Fatalf("no statement for %s", event)
	return nil
}

// An accepted request is recovered from the log before the repository is
// touched, so the branch it named may be gone by the time the caller retries.
// A fresh filing against that same absent ref still has nothing to measure and
// is still refused.
func TestAcceptedRequestIsRecoveredBeforeAnyRefIsRead(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringWorkspace(t)
	fixture.git("branch", "side")
	stated := filing{key: "vanishing-branch", text: "land it on the side",
		body: map[string]string{"to": "agent", "conditions": "it lands", "target_ref": "refs/heads/side"}}
	first, err := fixture.file(stated)
	if err != nil {
		t.Fatalf("filing against refs/heads/side: %v", err)
	}
	measured := fixture.body(first)["target_head"]
	if measured == "" {
		t.Fatalf("the accepted request measured nothing: %+v", fixture.body(first))
	}

	fixture.git("branch", "-D", "side")
	frontier := fixture.frontier()
	replay, err := fixture.file(stated)
	if err != nil {
		t.Fatalf("identical retry after the branch was deleted: %v", err)
	}
	if replay != first {
		t.Fatalf("the retry returned %s, want the original %s", replay, first)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("the retry appended: frontier %s to %s", frontier, after)
	}
	if got := fixture.body(first)["target_head"]; got != measured {
		t.Fatalf("the replayed request now measures %q, want %q", got, measured)
	}

	fresh := stated
	fresh.key = "vanishing-branch-fresh"
	fresh.text = "land it on the side again"
	event, err := fixture.file(fresh)
	if err == nil {
		t.Fatalf("a fresh filing against an absent ref was accepted as %s", event)
	}
	if !strings.Contains(err.Error(), "does not resolve in") {
		t.Fatalf("refusal %q does not name the unresolvable ref", err)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused fresh filing appended: frontier %s to %s", frontier, after)
	}
}

// The destination is the caller's own intent, not a measurement, so a reused
// key that names a different branch is a different act and is refused. The old
// request is not an answer to it and must not be handed back.
func TestReusedKeyWithADifferentDestinationIsRefused(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringWorkspace(t)
	stated := filing{key: "retarget", text: "land it",
		body: map[string]string{"to": "agent", "conditions": "it lands", "target_ref": "refs/heads/main"}}
	first, err := fixture.file(stated)
	if err != nil {
		t.Fatal(err)
	}
	fixture.git("branch", "other")
	fixture.git("commit", "--allow-empty", "-qm", "other moves")
	fixture.git("branch", "-f", "other", "HEAD")

	frontier := fixture.frontier()
	retargeted := stated
	retargeted.body = map[string]string{"to": "agent", "conditions": "it lands", "target_ref": "refs/heads/other"}
	event, err := fixture.file(retargeted)
	if err == nil {
		t.Fatalf("a reused key naming refs/heads/other was accepted as %s", event)
	}
	if !errors.Is(err, kernel.ErrIdempotencyConflict) {
		t.Fatalf("refusal %v, want %v", err, kernel.ErrIdempotencyConflict)
	}
	if strings.Contains(err.Error(), first) {
		t.Fatalf("the refusal names the old request %s: %v", first, err)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused retarget appended: frontier %s to %s", frontier, after)
	}
	if got := fixture.body(first)["target_ref"]; got != "refs/heads/main" {
		t.Fatalf("the accepted request now names %q", got)
	}

	// A reused key that names a branch nobody has is refused for that reason,
	// which is how the caller can tell the fresh measurement was actually
	// taken rather than the accepted act's head quietly reused.
	absent := stated
	absent.body = map[string]string{"to": "agent", "conditions": "it lands", "target_ref": "refs/heads/nowhere"}
	event, err = fixture.file(absent)
	if err == nil {
		t.Fatalf("a reused key naming an absent branch was accepted as %s", event)
	}
	if !strings.Contains(err.Error(), "does not resolve in") {
		t.Fatalf("refusal %q does not name the unresolvable ref", err)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused retarget appended: frontier %s to %s", frontier, after)
	}
}

// Everything else the caller states is their intent too. A reused key that
// changes any of it is refused for the same reason.
func TestReusedKeyWithChangedIntentIsRefused(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringWorkspace(t)
	basis := actRecord(t, fixture.ctx, fixture.workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "a live basis",
		RestsOn: []string{fixture.seed}, IdempotencyKey: "changed-intent-basis",
	})
	stated := filing{key: "changed-intent", text: "land it",
		body:        map[string]string{"to": "agent", "conditions": "it lands", "target_ref": "refs/heads/main"},
		attachments: map[string][]byte{"note.txt": []byte("as filed")},
	}
	first, err := fixture.file(stated)
	if err != nil {
		t.Fatal(err)
	}

	changedText := stated
	changedText.text = "land it, differently"
	changedBody := stated
	changedBody.body = map[string]string{"to": "agent", "conditions": "it lands twice", "target_ref": "refs/heads/main"}
	changedBases := stated
	changedBases.bases = []string{fixture.seed, basis.ID}
	changedAttachments := stated
	changedAttachments.attachments = map[string][]byte{"note.txt": []byte("as retried")}

	for _, testCase := range []struct {
		name    string
		changed filing
	}{
		{"text", changedText},
		{"body", changedBody},
		{"bases", changedBases},
		{"attachments", changedAttachments},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			frontier := fixture.frontier()
			event, err := fixture.file(testCase.changed)
			if err == nil {
				t.Fatalf("a changed %s under the accepted key was filed as %s", testCase.name, event)
			}
			if !errors.Is(err, kernel.ErrIdempotencyConflict) {
				t.Fatalf("refusal %v, want %v", err, kernel.ErrIdempotencyConflict)
			}
			if strings.Contains(err.Error(), first) {
				t.Fatalf("the refusal names the old request %s: %v", first, err)
			}
			if after := fixture.frontier(); after != frontier {
				t.Fatalf("a refused retry appended: frontier %s to %s", frontier, after)
			}
		})
	}
}
