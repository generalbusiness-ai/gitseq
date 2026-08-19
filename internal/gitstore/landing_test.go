package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The whole point of this type is that "not landed" and "cannot tell" are
// different answers. A repository with one merged branch, one unmerged branch
// and one commit nobody has ever heard of exercises all three at once.
func TestLandingsSeparateLandedFromAbsentFromUnknown(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "root")

	run("checkout", "-qb", "shipped")
	run("commit", "-qm", "shipped work", "--allow-empty")
	shipped := run("rev-parse", "HEAD")
	run("checkout", "-q", "main")
	run("merge", "-q", "--no-ff", "shipped", "-m", "merge shipped")
	merge := run("rev-parse", "HEAD")

	run("checkout", "-qb", "waiting")
	run("commit", "-qm", "not merged yet", "--allow-empty")
	waiting := run("rev-parse", "HEAD")
	run("checkout", "-q", "main")

	store := Store{Repo: filepath.Join(root, ".git")}
	stranger := "0123456789abcdef0123456789abcdef01234567"
	landings := store.Landings(ctx, "refs/heads/main", []string{shipped, waiting, stranger, "not a commit"})
	if len(landings) != 4 {
		t.Fatalf("expected one answer per commit, got %d", len(landings))
	}

	if landings[0].Status != LandingLanded {
		t.Fatalf("merged head should be landed: %#v", landings[0])
	}
	if landings[0].Merge != merge {
		t.Fatalf("merge station names the wrong commit: got %q want %q", landings[0].Merge, merge)
	}
	if landings[0].Time == 0 {
		t.Fatalf("a landed commit carries the time it landed: %#v", landings[0])
	}
	if landings[1].Status != LandingAbsent {
		t.Fatalf("unmerged head should be absent: %#v", landings[1])
	}
	// A commit git has never seen exits 128, and 128 is not 1. Reading it as
	// "absent" is exactly the defect this type exists to prevent.
	if landings[2].Status != LandingUnknown || landings[2].Reason == "" {
		t.Fatalf("an unknown commit must say so, with a reason: %#v", landings[2])
	}
	if landings[3].Status != LandingUnknown || landings[3].Reason != "not a commit name" {
		t.Fatalf("a malformed name is refused before git runs: %#v", landings[3])
	}
}

// A fast-forwarded commit is on the branch with no merge above it. Reporting
// it as absent because there is no merge to name would be the same lie in a
// different shape.
func TestLandingWithoutAMergeIsStillLanded(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "root")
	run("commit", "-qm", "second", "--allow-empty")
	head := run("rev-parse", "HEAD")

	landings := Store{Repo: filepath.Join(root, ".git")}.Landings(ctx, "refs/heads/main", []string{head})
	if landings[0].Status != LandingLanded {
		t.Fatalf("a commit on the branch is landed: %#v", landings[0])
	}
	if landings[0].Merge != "" {
		t.Fatalf("no merge brought it in, so none is named: %#v", landings[0])
	}
	if landings[0].Time == 0 {
		t.Fatalf("a landed commit still carries its time: %#v", landings[0])
	}
}

// A batch is bounded. A thread asks about the handful of heads on its own
// rail, and an unbounded list would let one page start an unbounded number of
// git processes.
func TestLandingsBoundTheBatch(t *testing.T) {
	commits := make([]string, LandingLimit+5)
	for index := range commits {
		commits[index] = "zzz"
	}
	landings := Store{Repo: "/nonexistent"}.Landings(context.Background(), "refs/heads/main", commits)
	if len(landings) != LandingLimit {
		t.Fatalf("expected %d answers, got %d", LandingLimit, len(landings))
	}
}
