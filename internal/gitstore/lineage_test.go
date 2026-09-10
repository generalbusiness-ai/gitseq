package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func capturedGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

// Lineage answers a different question from Graph and has to follow a
// different traversal to answer it. Graph draws what happened lately across
// every branch and tag; Lineage asks what one branch claims, which is its own
// line of descent and not whatever was committed beside it. Three things are
// pinned here at once: the first-parent restriction, the boundary that stops
// the walk where the mainline begins, and the per-tip limit.
func TestLineageFollowsFirstParentToTheBoundary(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	root := filepath.Dir(gitDir)
	run("commit", "-qm", "base")
	base := capturedGit(t, root, "rev-parse", "HEAD")
	run("checkout", "-qb", "side")
	run("commit", "--allow-empty", "-qm", "side work")
	side := capturedGit(t, root, "rev-parse", "HEAD")
	run("checkout", "-q", "main")
	run("commit", "--allow-empty", "-qm", "main moves on")
	mainline := capturedGit(t, root, "rev-parse", "HEAD")
	run("merge", "-q", "--no-ff", "-m", "merge side", "side")
	merged := capturedGit(t, root, "rev-parse", "HEAD")

	store := Store{Repo: gitDir}
	read, err := store.Lineage(ctx, []string{merged}, "", LineageCommitLimit)
	if err != nil {
		t.Fatal(err)
	}
	if read.Incomplete() {
		t.Fatalf("a lineage well inside every bound reported incomplete: %+v", read)
	}
	commits := read.Commits
	var seen []string
	for _, commit := range commits {
		seen = append(seen, commit.Hash)
	}
	want := []string{merged, mainline, base}
	if len(seen) != len(want) {
		t.Fatalf("first-parent lineage of the merge = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("first-parent lineage of the merge = %v, want %v", seen, want)
		}
	}
	for _, commit := range commits {
		if commit.Hash == side {
			t.Fatal("the merged-in branch tip is on the first-parent line; the walk followed a second parent")
		}
	}

	// The boundary is where the lineage stops. Reading past the mainline
	// re-reads the shared history of every branch for no new answer.
	boundedRead, err := store.Lineage(ctx, []string{side}, mainline, LineageCommitLimit)
	if err != nil {
		t.Fatal(err)
	}
	bounded := boundedRead.Commits
	if len(bounded) != 1 || bounded[0].Hash != side || boundedRead.Incomplete() {
		t.Fatalf("bounded lineage = %+v, want only %s and complete", boundedRead, side)
	}

	// A boundary equal to the tip would exclude the tip itself, and the
	// question is what this tip claims.
	selfRead, err := store.Lineage(ctx, []string{side}, side, LineageCommitLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(selfRead.Commits) == 0 || selfRead.Commits[0].Hash != side {
		t.Fatalf("a tip that is its own boundary read as %+v, want its own commit", selfRead)
	}

	// The per-tip limit is per tip, so one long branch cannot spend the read
	// and leave a later tip unexamined.
	limited, err := store.Lineage(ctx, []string{merged}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Commits) != 1 || limited.Commits[0].Hash != merged {
		t.Fatalf("per-tip limit of one returned %d commits", len(limited.Commits))
	}
	if !limited.Incomplete() || len(limited.Truncated) != 1 || limited.Truncated[0] != merged {
		t.Fatalf("a lineage cut off by the per-tip limit reported no truncation: %+v", limited)
	}

	// A tip this repository does not hold contributes nothing and refuses
	// nothing: a stale checkout listing naming a pruned head must not cost
	// every other checkout its answer.
	absent := strings.Repeat("a", 40)
	mixed, err := store.Lineage(ctx, []string{absent, side}, mainline, LineageCommitLimit)
	if err != nil {
		t.Fatalf("a missing tip refused the whole pass: %v", err)
	}
	if len(mixed.Commits) != 1 || mixed.Commits[0].Hash != side {
		t.Fatalf("mixed tips = %+v, want only %s", mixed, side)
	}
	if len(mixed.Unavailable) != 1 || mixed.Unavailable[0] != absent {
		t.Fatalf("a tip this repository does not hold was not reported: %+v", mixed)
	}
}

// The trailer this reads is what the whole reverse association is derived
// from, so the object it is read from has to be the object its hash names.
// Lineage shares Graph's re-verification rather than copying it; this proves
// the sharing is real and not a comment.
func TestLineageRefusesACommitObjectGitFsckCallsMalformed(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	root := filepath.Dir(gitDir)
	run("commit", "-qm", "base")
	tree := capturedGit(t, root, "rev-parse", "HEAD^{tree}")
	raw := "tree " + tree + "\n" +
		"author t <t@t> 1700000000 +0000\n" +
		"committer t <t@t> 1700000000 +0000\n\n" +
		"visible subject\n\nRests-On: git:sha1:x#git:sha1:y\x00TRUNCATED\n"
	rawPath := filepath.Join(t.TempDir(), "raw")
	if err := os.WriteFile(rawPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	oid := capturedGit(t, root, "hash-object", "--literally", "-t", "commit", "-w", rawPath)
	run("update-ref", "refs/heads/evil", oid)

	if _, err := (Store{Repo: gitDir}).Lineage(ctx, []string{oid}, "", LineageCommitLimit); err == nil {
		t.Fatal("Lineage read trailers from a commit object git fsck calls nulInCommit")
	} else if !strings.Contains(err.Error(), "NUL byte") {
		t.Errorf("refusal does not name the cause: %v", err)
	}
}

// Duplicate tips are ordinary: two checkouts of one branch, and two branches
// sharing history, both arrive here. Reading the shared commits twice would
// pay twice for one answer and report the same claim twice.
func TestLineageCollapsesDuplicateTipsAndCommits(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	root := filepath.Dir(gitDir)
	run("commit", "-qm", "base")
	run("commit", "--allow-empty", "-qm", "shared")
	shared := capturedGit(t, root, "rev-parse", "HEAD")
	run("branch", "second")

	read, err := (Store{Repo: gitDir}).Lineage(ctx, []string{shared, shared, shared}, "", LineageCommitLimit)
	if err != nil {
		t.Fatal(err)
	}
	commits := read.Commits
	seen := map[string]int{}
	for _, commit := range commits {
		seen[commit.Hash]++
	}
	for hash, count := range seen {
		if count != 1 {
			t.Fatalf("commit %s appeared %d times", hash, count)
		}
	}
	if len(commits) != 2 {
		t.Fatalf("lineage read %d commits from three copies of one tip", len(commits))
	}

	tips := make([]string, LineageTipLimit+1)
	for i := range tips {
		tips[i] = shared
	}
	if _, err := (Store{Repo: gitDir}).Lineage(ctx, tips, "", LineageCommitLimit); err != nil {
		t.Fatalf("one tip repeated past the tip limit was refused: %v", err)
	}
}
