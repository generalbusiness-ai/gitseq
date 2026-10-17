package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// readsDuring counts the complete verified reads of a log, and the cold scans
// among them, that run makes. The counters are process-wide, so a test using
// this must not run in parallel.
func readsDuring(t *testing.T, run func()) (complete, cold int64) {
	t.Helper()
	completeBefore, coldBefore := kernel.VerifiedReads()
	run()
	completeAfter, coldAfter := kernel.VerifiedReads()
	return completeAfter - completeBefore, coldAfter - coldBefore
}

func holdsEvent(projection workroom.Projection, event string) bool {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return true
		}
	}
	return false
}

// Admission judges the session snapshot's verified fold at exactly the head
// that fold stands at, without reading the log again, and renders the same
// projection the session holds. At any other head it judges its private
// world, which holds the events the session has not read.
//
// Not parallel: it counts process-wide verified reads.
func TestAdmissionJudgesTheSessionFoldOnlyAtItsExactHead(t *testing.T) {
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	session := workspace.mustSnapshot(t, ctx)
	var world Snapshot
	complete, _ := readsDuring(t, func() {
		var err error
		if world, err = workspace.admissionWorld(ctx, session.Head); err != nil {
			t.Fatal(err)
		}
	})
	if complete != 0 {
		t.Fatalf("admission at the session's own head read the log %d times, want 0", complete)
	}
	if world.Head != session.Head || !bytes.Equal(encodedSnapshot(t, world), encodedSnapshot(t, session)) {
		t.Fatal("admission's world at the session head differs from the session's own projection")
	}

	other, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	landed := actRecord(t, ctx, other, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "the session has not read this",
		RestsOn: []string{seed.ID}, IdempotencyKey: "unread-by-session",
	})
	moved := other.mustSnapshot(t, ctx).Head
	moved2, err := workspace.admissionWorld(ctx, moved)
	if err != nil {
		t.Fatal(err)
	}
	if moved2.Head != moved || !holdsEvent(moved2.Projection, landed.ID) {
		t.Fatalf("admission at %s judged a world without %s, the event that moved the head", moved, landed.ID)
	}
	audited, err := workspace.ReadOnlySnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encodedSnapshot(t, moved2), encodedSnapshot(t, audited)) {
		t.Fatal("admission's private world differs from a read-only audit of the same head")
	}
}

// The private reader starts from a signed checkpoint and changes nothing any
// other reader starts from: it publishes no checkpoint at the head though this
// workspace holds the sequencer key, restores no checkpoint ref the pointer
// still names, and advances no rollback witness.
//
// Not parallel: it counts process-wide verified reads.
func TestAdmissionPrivateReaderConsumesTheCheckpointWithoutChangingIt(t *testing.T) {
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	workspace.mustSnapshot(t, ctx)
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "after the checkpoint",
		RestsOn: []string{seed.ID}, IdempotencyKey: "after-the-checkpoint",
	})
	ref := kernel.CheckpointRef(workspace.config.Genesis)
	checkpoint, err := workspace.Store.Head(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Store.DeleteRef(ctx, ref, checkpoint); err != nil {
		t.Fatal(err)
	}
	pointer, err := os.ReadFile(workspace.checkpointPointerPath())
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(workspace.MetaDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	head, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	judge, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	complete, cold := readsDuring(t, func() {
		if _, err := judge.admissionWorldChecked(ctx, head, nil); err != nil {
			t.Fatal(err)
		}
	})
	if complete != 1 || cold != 0 {
		t.Fatalf("the private reader made %d complete reads, %d cold, want one read from the checkpoint", complete, cold)
	}
	if _, err := workspace.Store.Head(ctx, ref); err == nil {
		t.Fatal("the private reader restored or published the checkpoint ref")
	}
	if after, err := os.ReadFile(workspace.checkpointPointerPath()); err != nil || !bytes.Equal(after, pointer) {
		t.Fatalf("the private reader moved the checkpoint pointer: %s, %v", after, err)
	}
	if after, err := os.ReadFile(filepath.Join(workspace.MetaDir, "config.json")); err != nil || !bytes.Equal(after, config) {
		t.Fatalf("the private reader advanced the rollback witness: %v", err)
	}
}

// A retry lookup and a replay check are answered from the session reader's
// own index at the head it verified, without a second read of the log, and
// with the kernel's identity: an exact retry replays, and a different act
// under a held key is the conflict the kernel refuses. When the log has moved
// past the session, a signing caller takes the session snapshot forward first;
// a read-only caller does not, and its answer is the kernel's own scan.
//
// Not parallel: it counts process-wide verified reads.
func TestRetryLookupsReadTheSessionIndex(t *testing.T) {
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	accepted := Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "accepted", RestsOn: []string{seed.ID}, IdempotencyKey: "held"}
	landed := actRecord(t, ctx, workspace, "human", accepted)
	session, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	session.mustSnapshot(t, ctx)
	actor, private, err := session.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	filer := Filer{Name: "human", Fingerprint: actor.Fingerprint}
	retry, err := session.BuildActRequest(ctx, private, "human", accepted)
	if err != nil {
		t.Fatal(err)
	}
	different := accepted
	different.Text = "a different act under the held key"
	conflict, err := session.BuildActRequest(ctx, private, "human", different)
	if err != nil {
		t.Fatal(err)
	}
	judged := errors.New("precondition judged")
	precondition := func(context.Context) error { return judged }
	complete, _ := readsDuring(t, func() {
		if held := session.AcceptedUnderKeys(ctx, filer, []string{"held", "absent"}); len(held) != 1 || held["held"] != landed.ID {
			t.Fatalf("lookup answered %v, want only %s", held, landed.ID)
		}
		if err := session.refuseNewSubmission(ctx, precondition, retry, false); err != nil {
			t.Fatalf("an exact retry was judged as new: %v", err)
		}
		if err := session.refuseNewSubmission(ctx, precondition, conflict, false); !errors.Is(err, kernel.ErrIdempotencyConflict) {
			t.Fatalf("a different act under a held key = %v, want the kernel's conflict", err)
		}
	})
	if complete != 0 {
		t.Fatalf("lookups at the session head read the log %d times, want 0", complete)
	}

	later := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "after the session read",
		RestsOn: []string{seed.ID}, IdempotencyKey: "later",
	})
	complete, cold := readsDuring(t, func() {
		if held := session.priorActsFor(ctx, filer, []string{"later"}, true); len(held) != 1 || session.EventID(held["later"].Commit) != later.ID {
			t.Fatalf("read-only lookup answered %v, want %s", held, later.ID)
		}
	})
	if complete != 1 || cold != 1 {
		t.Fatalf("a read-only lookup past the session made %d reads, %d cold, want the kernel's one scan", complete, cold)
	}
	complete, _ = readsDuring(t, func() {
		if held := session.AcceptedUnderKeys(ctx, filer, []string{"later"}); held["later"] != later.ID {
			t.Fatalf("lookup past the session answered %v, want %s", held, later.ID)
		}
	})
	if complete != 0 {
		t.Fatalf("a signing lookup past the session made %d complete reads, want only the session's delta", complete)
	}
}

// A fold the session built while an audit was still running is provisional,
// and an audit that then fails publishes nothing admission could reuse: at the
// head the failed audit was reading, admission has no world to judge.
func TestAdmissionNeverReusesAFoldFromAFailedAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "carried by a forged commit",
		RestsOn: []string{seed.ID}, IdempotencyKey: "forged",
	})
	if err != nil {
		t.Fatal(err)
	}
	declared, err := intent.Verify(request.Signed)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := workspace.Store.WritePayloadTree(ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	head, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	forged, err := workspace.Store.SignedCommit(ctx, tree, head, intent.Envelope(request.Signed, declared.RestsOn), wrongKey, gitstore.CommitIdentity{
		AuthorName: "hostile", AuthorEmail: "hostile@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.config.Genesis), forged, head); err != nil {
		t.Fatal(err)
	}
	// A fresh process, so the session's read is a cold, streamed audit that
	// folds every verified event before it reaches the forged one.
	reader, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Snapshot(ctx); err == nil {
		t.Fatal("the session audit accepted a commit the sequencer did not sign")
	}
	if world, err := reader.admissionWorld(ctx, forged); err == nil {
		t.Fatalf("admission judged a world at %s, the head whose audit failed: depth %d", forged, world.Depth)
	}
}
