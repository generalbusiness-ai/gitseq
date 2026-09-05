//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || illumos

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type mergeProcess struct {
	command *exec.Cmd
	output  bytes.Buffer
	done    chan error
}

type mergeCandidate struct {
	commit   string
	approval string
}

func TestMergeTransactionsSerializeAcrossProcessesAndRemeasure(t *testing.T) {
	binary := buildGS(t)
	for _, test := range []struct {
		name      string
		firstFail bool
		interrupt bool
	}{
		{name: "success seals before the second remeasures"},
		{name: "failure rolls back before the second enters", firstFail: true},
		{name: "interrupt rolls back before the second enters", firstFail: true, interrupt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(t)
			first := mergeCandidate{commit: fixture.candidate, approval: fixture.review(t)}
			fixture.ratify(t, first.approval)
			second := addMergeCandidate(t, fixture)
			initialHead := testGit(t, fixture.repo, "rev-parse", "HEAD")

			entered := filepath.Join(t.TempDir(), "entered")
			release := filepath.Join(filepath.Dir(entered), "release")
			installMergeTestHook(t, fixture.repo)
			firstEnv := []string{
				"GITSEQ_TEST_MERGE_ENTERED=" + entered,
				"GITSEQ_TEST_MERGE_RELEASE=" + release,
			}
			if test.firstFail {
				firstEnv = append(firstEnv, "GITSEQ_TEST_MERGE_FAIL=1")
			}
			firstRun := startMergeProcess(t, binary, fixture.repo, first, firstEnv)
			awaitPath(t, entered)

			secondRun := startMergeProcess(t, binary, fixture.repo, second, nil)
			// This is the mutation-sensitive boundary proof. Without the outer
			// merge lock, or with a lock acquired after the incumbent's first
			// mutating step, the second process runs its own reservation,
			// tentative merge and landing alongside the incumbent's instead of
			// waiting for it.
			assertProcessBlocked(t, secondRun)
			if _, err := gitCommand(fixture.repo, "rev-parse", "--verify", mergeReceiptRef(second.approval)); err == nil {
				t.Fatal("the waiting merge reserved its approval before the incumbent transaction finished")
			}

			if test.interrupt {
				if err := firstRun.command.Process.Signal(os.Interrupt); err != nil {
					t.Fatalf("interrupt first merge: %v", err)
				}
			}
			if err := os.WriteFile(release, []byte("continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			firstErr := awaitMergeProcess(t, firstRun)
			secondErr := awaitMergeProcess(t, secondRun)

			if test.firstFail {
				if firstErr == nil {
					t.Fatalf("first merge unexpectedly succeeded: %s", firstRun.output.String())
				}
				if secondErr != nil {
					t.Fatalf("second merge did not proceed after rollback: %v\n%s", secondErr, secondRun.output.String())
				}
				secondReceipt := receiptAtHead(t, fixture.repo)
				if secondReceipt.Candidate != second.commit || secondReceipt.TargetPreHead != initialHead {
					t.Fatalf("second receipt after rollback = %+v, want candidate %s on %s", secondReceipt, second.commit, initialHead)
				}
				if _, err := gitCommand(fixture.repo, "rev-parse", "--verify", mergeReceiptRef(first.approval)); err == nil {
					t.Fatal("failed merge left its approval reservation behind")
				}
			} else {
				if firstErr != nil {
					t.Fatalf("first merge failed: %v\n%s", firstErr, firstRun.output.String())
				}
				if secondErr != nil {
					t.Fatalf("second merge failed after the first sealed: %v\n%s", secondErr, secondRun.output.String())
				}
				secondReceipt := receiptAtHead(t, fixture.repo)
				firstHead := secondReceipt.TargetPreHead
				firstReceipt, ok, err := readMergeReceipt(context.Background(), fixture.repo, firstHead)
				if err != nil || !ok {
					t.Fatalf("read first receipt at second target pre-head: ok=%v err=%v", ok, err)
				}
				if firstReceipt.Candidate != first.commit || firstReceipt.TargetPreHead != initialHead || secondReceipt.Candidate != second.commit {
					t.Fatalf("serialized receipts: first=%+v second=%+v", firstReceipt, secondReceipt)
				}
			}

			landed := []string{second.commit}
			if !test.firstFail {
				landed = append(landed, first.commit)
			}
			for _, candidate := range landed {
				if _, err := gitCommand(fixture.repo, "merge-base", "--is-ancestor", candidate, "HEAD"); err != nil {
					t.Fatalf("landed candidate %s is not in HEAD: %v", candidate, err)
				}
			}
			if status := testGit(t, fixture.repo, "status", "--porcelain"); status != "" {
				t.Fatalf("serialized merges left the checkout dirty: %q", status)
			}
		})
	}
}

// This helper is run as a child test process. The parent kills it while it
// owns the exact merge lock, proving the kernel releases that lock without a
// cleanup handler or a stale-file protocol.
func TestMergeTransactionCrashLockHelper(t *testing.T) {
	if os.Getenv("GITSEQ_TEST_CRASH_LOCK_HELPER") != "1" {
		return
	}
	metaDir := os.Getenv("GITSEQ_TEST_CRASH_LOCK_META")
	entered := os.Getenv("GITSEQ_TEST_CRASH_LOCK_ENTERED")
	_, err := apphost.WithMetaLock(metaDir, mergeLockFile, func() (struct{}, error) {
		if err := os.WriteFile(entered, []byte("locked\n"), 0o600); err != nil {
			return struct{}{}, err
		}
		for {
			time.Sleep(time.Hour)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMergeTransactionLockIsReleasedWhenHolderCrashes(t *testing.T) {
	metaDir := t.TempDir()
	entered := filepath.Join(metaDir, "entered")
	holder := exec.Command(os.Args[0], "-test.run=^TestMergeTransactionCrashLockHelper$")
	holder.Env = append(os.Environ(),
		"GITSEQ_TEST_CRASH_LOCK_HELPER=1",
		"GITSEQ_TEST_CRASH_LOCK_META="+metaDir,
		"GITSEQ_TEST_CRASH_LOCK_ENTERED="+entered,
	)
	var output bytes.Buffer
	holder.Stdout = &output
	holder.Stderr = &output
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if holder.ProcessState == nil {
			_ = holder.Process.Kill()
		}
	})
	awaitPath(t, entered)

	acquired := make(chan error, 1)
	go func() {
		_, err := apphost.WithMetaLock(metaDir, mergeLockFile, func() (struct{}, error) {
			return struct{}{}, nil
		})
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second holder acquired a live process's merge lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatalf("killed lock helper exited successfully: %s", output.String())
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("acquire after holder crash: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("merge lock remained held after its owning process died")
	}
}

func addMergeCandidate(t *testing.T, fixture workflowFixture) mergeCandidate {
	t.Helper()
	operator := fixture.workspace.View().Actors["operator"].Fingerprint
	reviewer := fixture.workspace.View().Actors["reviewer"].Fingerprint
	request, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "implement independent second feature",
		Body:    map[string]string{"to": operator, "conditions": "publish the exact independent head"},
		RestsOn: []string{fixture.ground}, IdempotencyKey: "second-implementation-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement independent second feature",
		RestsOn: []string{request.Record.ID}, IdempotencyKey: "second-implementation-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(filepath.Dir(fixture.repo), "feature-two")
	testGit(t, fixture.repo, "worktree", "add", "-qb", "feature-two", checkout)
	if err := os.WriteFile(filepath.Join(checkout, "feature-two.txt"), []byte("feature two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, checkout, "add", "feature-two.txt")
	testGit(t, checkout, "commit", "-qm", "feature two")
	commit := testGit(t, checkout, "rev-parse", "HEAD")
	artifact, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "independent second feature artifact",
		Body:    map[string]string{"path": "feature-two.txt", "commit": commit},
		RestsOn: []string{promise.Record.ID, fixture.ground}, IdempotencyKey: "second-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewRequest, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review independent second feature",
		Body:    map[string]string{"to": reviewer, "conditions": "exact head"},
		RestsOn: []string{artifact.Record.ID}, IdempotencyKey: "second-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review independent second feature",
		RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "second-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", checkout,
		"--artifact", artifact.Record.ID, "--promise", reviewPromise.Record.ID,
		"--verdict", "approved", "--text", "APPROVED independent second head",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	approval := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event
	fixture.ratify(t, approval)
	return mergeCandidate{commit: commit, approval: approval}
}

func installMergeTestHook(t *testing.T, repo string) {
	t.Helper()
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	// A merge no longer commits through HEAD, so no commit hook runs inside
	// the transaction. The approval reservation is the first mutating step it
	// takes while holding the merge lock, and pausing or refusing there is the
	// same mid-transaction block the earlier pre-commit hook produced. It is
	// deliberately not the landing ref: a process killed inside that
	// transaction leaves the target branch locked, which is an artefact of the
	// test rather than anything the merge boundary does.
	//
	// The hook fires once. Rollback and receipt publication update refs too,
	// and a block or a refusal on those would be this test interfering with
	// the recovery it is trying to observe.
	hook := `#!/bin/sh
test "$1" = prepared || exit 0
grep -q ' refs/gitseq/merge-receipts/' || exit 0
test -n "$GITSEQ_TEST_MERGE_ENTERED" || exit 0
test -e "$GITSEQ_TEST_MERGE_ENTERED" && exit 0
: > "$GITSEQ_TEST_MERGE_ENTERED"
while test ! -e "$GITSEQ_TEST_MERGE_RELEASE"; do
  sleep 0.01
done
test "$GITSEQ_TEST_MERGE_FAIL" = "1" && exit 1
exit 0
`
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
}

func startMergeProcess(t *testing.T, binary, repo string, candidate mergeCandidate, environment []string) *mergeProcess {
	t.Helper()
	run := &mergeProcess{done: make(chan error, 1)}
	run.command = exec.Command(binary, "merge",
		"--repo", repo, "--as", "operator", "--checkout", repo,
		"--candidate", candidate.commit, "--approval", candidate.approval,
		"--text", "Exercise one complete cross-process merge transaction.",
	)
	run.command.Env = append(os.Environ(), environment...)
	run.command.Stdout = &run.output
	run.command.Stderr = &run.output
	if err := run.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if run.command.ProcessState == nil {
			_ = run.command.Process.Kill()
		}
	})
	go func() { run.done <- run.command.Wait() }()
	return run
}

func assertProcessBlocked(t *testing.T, process *mergeProcess) {
	t.Helper()
	select {
	case err := <-process.done:
		t.Fatalf("second merge exited inside the first transaction: %v\n%s", err, process.output.String())
	case <-time.After(250 * time.Millisecond):
	}
}

func awaitMergeProcess(t *testing.T, process *mergeProcess) error {
	t.Helper()
	select {
	case err := <-process.done:
		return err
	case <-time.After(15 * time.Second):
		_ = process.command.Process.Kill()
		t.Fatalf("merge process did not finish: %s", process.output.String())
		return fmt.Errorf("unreachable")
	}
}

func awaitPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func receiptAtHead(t *testing.T, repo string) mergeReceipt {
	t.Helper()
	head := testGit(t, repo, "rev-parse", "HEAD")
	receipt, ok, err := readMergeReceipt(context.Background(), repo, head)
	if err != nil || !ok {
		t.Fatalf("read merge receipt at %s: ok=%v err=%v", head, ok, err)
	}
	return receipt
}
