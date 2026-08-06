package gitstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGraphParsesRootMergeRefsAndTrailers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "root")
	run("checkout", "-qb", "side")
	run("commit", "-qm", "side work\n\nRests-On: git:sha1:g#git:sha1:e", "--allow-empty")
	run("checkout", "-q", "main")
	run("merge", "-q", "--no-ff", "side", "-m", "merge side")

	gitDir := filepath.Join(root, ".git")
	commits, err := Store{Repo: gitDir}.Graph(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	merge, side, first := commits[0], commits[1], commits[2]
	if len(merge.Parents) != 2 || merge.Subject != "merge side" {
		t.Fatalf("merge row = %#v", merge)
	}
	if len(side.RestsOn) != 1 || side.RestsOn[0] != "git:sha1:g#git:sha1:e" {
		t.Fatalf("trailer row = %#v", side)
	}
	if len(first.Parents) != 0 {
		t.Fatalf("root commit has parents: %#v", first)
	}
	found := false
	for _, ref := range merge.Refs {
		if ref == "main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge refs = %#v", merge.Refs)
	}
}
