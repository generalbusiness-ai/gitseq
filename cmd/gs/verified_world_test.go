package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// publishCheckpointAtHead has a fresh reader publish a signed checkpoint at
// the head. A signing command leaves the checkpoint where its one read found
// it, because it does not read back the acts it appended itself.
func (f workflowFixture) publishCheckpointAtHead(t *testing.T) {
	t.Helper()
	reader, err := app.Open(f.ctx, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Snapshot(f.ctx); err != nil {
		t.Fatal(err)
	}
}

// commandReads runs one command and counts the complete verified reads of the
// log it made, and the cold scans among them.
func commandReads(t *testing.T, run func() error) (complete, cold int64) {
	t.Helper()
	completeBefore, coldBefore := kernel.VerifiedReads()
	if _, err := captureStdout(t, run); err != nil {
		t.Fatal(err)
	}
	completeAfter, coldAfter := kernel.VerifiedReads()
	return completeAfter - completeBefore, coldAfter - coldBefore
}

// With a signed checkpoint at the head, each signing command verifies the log
// once and scans it cold never: the session snapshot is the one read, and the
// retry lookups, the replay checks, both halves of admission and the local
// submitter all judge that same verified world. A second whole-log read in any
// of them turns this red.
//
// Not parallel: it counts process-wide verified reads and reads process-wide
// standard output.
func TestSigningCommandsVerifyTheLogOnce(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "once", "reviewer", "operator", "once.txt")
	commands := []struct {
		name string
		run  func() error
	}{
		{"promise", func() error {
			return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--branch", lane.branch, lane.request})
		}},
		{"state", func() error {
			return stateCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--kind", "assert",
				"--text", "one verified world", "--rests-on", f.ground, "--idempotency-key", "once-state"})
		}},
		{"artifact", func() error {
			return artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
				"--head", lane.head, "--promise", f.latestPromise(t, "operator"), "once.txt"})
		}},
		{"review-request", func() error {
			return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", lane.head, "--to", "reviewer"})
		}},
	}
	for _, command := range commands {
		f.publishCheckpointAtHead(t)
		before := f.snapshot(t).Depth
		complete, cold := commandReads(t, command.run)
		if after := f.snapshot(t).Depth; after != before+1 {
			t.Fatalf("gs %s appended %d acts, want 1", command.name, after-before)
		}
		if complete != 1 || cold != 0 {
			t.Fatalf("gs %s made %d complete verified reads of the log, %d of them cold; want one, from the checkpoint", command.name, complete, cold)
		}
	}

	// A ratification the fold refuses stops before signing, after the same
	// one read.
	f.publishCheckpointAtHead(t)
	artifact := f.latestOfKind(t, workroom.KindArtifact)
	completeBefore, coldBefore := kernel.VerifiedReads()
	_, err := captureStdout(t, func() error {
		return ratifyCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", artifact})
	})
	if err == nil {
		t.Fatal("ratifying an artifact was accepted")
	}
	completeAfter, coldAfter := kernel.VerifiedReads()
	if completeAfter-completeBefore != 1 || coldAfter-coldBefore != 0 {
		t.Fatalf("a refused ratify made %d complete reads, %d cold; want one, from the checkpoint",
			completeAfter-completeBefore, coldAfter-coldBefore)
	}
}

// A checkpoint signed by anyone but the sequencer is refused on the signing
// path too: the command falls back to one cold scan and still files its act.
//
// Not parallel: it counts process-wide verified reads.
func TestSigningCommandRefusesATamperedCheckpoint(t *testing.T) {
	f := newWorkflowFixture(t)
	f.publishCheckpointAtHead(t)
	// Read before forging: any later read would fall back cold and publish a
	// valid checkpoint of its own.
	before := f.snapshot(t).Depth
	genesis := f.workspace.View().Genesis
	ref := kernel.CheckpointRef(genesis)
	valid := testGit(t, f.repo, "rev-parse", ref)
	tree := testGit(t, f.repo, "rev-parse", valid+"^{tree}")
	message := testGit(t, f.repo, "log", "-1", "--format=%B", valid)
	forger := filepath.Join(t.TempDir(), "forger")
	if _, err := gitstore.GenerateSSHKey(f.ctx, forger); err != nil {
		t.Fatal(err)
	}
	forged, err := f.workspace.Store.SignedCommit(f.ctx, tree, "", strings.TrimSpace(message)+"\n", forger, gitstore.CommitIdentity{
		AuthorName: "gitseq checkpoint", AuthorEmail: "checkpoint@gitseq.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only the forged checkpoint remains selectable.
	if err := f.workspace.InvalidateCheckpoint(f.ctx); err != nil {
		t.Fatal(err)
	}
	testGit(t, f.repo, "update-ref", ref, forged)

	complete, cold := commandReads(t, func() error {
		return stateCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--kind", "assert",
			"--text", "filed past a forged checkpoint", "--rests-on", f.ground, "--idempotency-key", "past-forgery"})
	})
	if cold != 1 || complete != 2 {
		t.Fatalf("with a forged checkpoint the command made %d complete reads, %d cold; want the refused checkpoint and one cold scan", complete, cold)
	}
	if after := f.snapshot(t).Depth; after != before+1 {
		t.Fatalf("depth = %d after the command, want %d", after, before+1)
	}
}

func (f workflowFixture) latestOfKind(t *testing.T, kind workroom.Kind) string {
	t.Helper()
	statements := f.snapshot(t).Projection.Statements
	for index := len(statements) - 1; index >= 0; index-- {
		if statements[index].Kind == kind {
			return statements[index].Event
		}
	}
	t.Fatalf("the workroom holds no %s", kind)
	return ""
}

func (f workflowFixture) latestPromise(t *testing.T, actor string) string {
	t.Helper()
	fingerprint := f.fingerprint(t, actor)
	statements := f.snapshot(t).Projection.Statements
	for index := len(statements) - 1; index >= 0; index-- {
		if statements[index].Kind == workroom.KindPromise && statements[index].Actor == fingerprint {
			return statements[index].Event
		}
	}
	t.Fatalf("%s holds no promise", actor)
	return ""
}
