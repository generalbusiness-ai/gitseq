package docset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The cleanup block in the cloned-repository how-to protects a deletion with
// two exact-path comparisons. Gate 2 runs every block under a harness that
// prepends `set -euo pipefail`, so under `make test` those comparisons abort
// the script whether or not the page asks them to. A reader pasting the block
// into an ordinary shell gets no such preamble, and there a failed comparison
// is only a non-zero exit nobody reads: execution continues to the deletion.
//
// This gate runs the block the reader's way — bare, with no preamble — and
// checks that a deliberately failed guard leaves the target intact. It runs
// the same extracted block a second time with its `set -e` line removed, and
// requires that the target is destroyed then. That second run is what stops
// the first from passing vacuously: it proves the fixture really is a
// deletion the guard is preventing, rather than a scenario in which nothing
// would have been deleted anyway.

const cleanupPage = DocsDir + "/how-to/use-in-a-cloned-repo.md"

// cleanupMarker identifies the removal block without pinning its exact text.
const cleanupMarker = `GITSEQ_DIR="$COMMON_DIR/gitseq"`

func TestGateClonedRepoCleanupGuardBindsWithoutHarnessPreamble(t *testing.T) {
	requireTool(t, "bash")
	block := cleanupBlock(t)

	// A line of its own, not a mention in the comment: the comment names
	// set -e to explain it, and matching that would let the explanation
	// stand in for the thing explained.
	errexit := -1
	guard := -1
	for i, line := range strings.Split(block, "\n") {
		switch {
		case errexit < 0 && strings.TrimSpace(line) == "set -e":
			errexit = i
		case guard < 0 && strings.HasPrefix(strings.TrimSpace(line), `test "$COMMON_DIR"`):
			guard = i
		}
	}
	if errexit < 0 {
		t.Fatalf("%s: the removal block has no `set -e` line, so its exact-path guards do nothing in a reader's shell", cleanupPage)
	}
	if guard < 0 {
		t.Fatalf("%s: the removal block no longer compares $COMMON_DIR; this gate is pointed at the wrong lines", cleanupPage)
	}
	if errexit > guard {
		t.Errorf("%s: set -e comes after the first guard; a guard that fails before errexit is armed still falls through", cleanupPage)
	}
	for _, forbidden := range []string{"rm -rf", "rm -f", "rm "} {
		if strings.Contains(block, forbidden) {
			t.Errorf("%s: removal block uses %q; this page removes with unlink and rmdir so a wrong path cannot take a tree with it", cleanupPage, forbidden)
		}
	}

	survived := runCleanupAgainstWrongTarget(t, block)
	if !survived {
		t.Errorf("%s: a failed path guard did not stop the block — the decoy file was deleted", cleanupPage)
	}

	// Same block, errexit removed: the deletion must now happen. If it does
	// not, the fixture proves nothing and the check above is decoration.
	unguarded := strings.Replace(block, "set -e\n", "", 1)
	if unguarded == block {
		t.Fatalf("%s: could not remove the set -e line to prove the fixture has teeth", cleanupPage)
	}
	if runCleanupAgainstWrongTarget(t, unguarded) {
		t.Errorf("%s: with set -e removed the decoy file still survived, so this fixture does not exercise the guard at all", cleanupPage)
	}
}

func cleanupBlock(t *testing.T) string {
	t.Helper()
	for _, page := range mustPages(t, mustRoot(t)) {
		if page.Path != cleanupPage {
			continue
		}
		for _, b := range page.Blocks() {
			if b.Lang == "sh" && strings.Contains(b.Code, cleanupMarker) {
				return b.Code
			}
		}
		t.Fatalf("%s: no runnable block contains %s", cleanupPage, cleanupMarker)
	}
	t.Fatalf("%s: page not found", cleanupPage)
	return ""
}

// runCleanupAgainstWrongTarget executes the block with a git that reports a
// common directory somewhere other than the copy, which is the substitution
// failure the guards exist to catch. It reports whether the decoy file that
// git pointed at survived.
func runCleanupAgainstWrongTarget(t *testing.T, block string) bool {
	t.Helper()
	scratch := t.TempDir()

	work := filepath.Join(scratch, "work")
	repo := filepath.Join(scratch, "repo")
	decoy := filepath.Join(scratch, "decoy")
	sentinel := filepath.Join(decoy, ".git", "gitseq", "keep-me")
	for _, dir := range []string{work, repo, filepath.Dir(sentinel)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sentinel, []byte("not yours to delete\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(scratch, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\ncase \"$*\" in\n*--git-common-dir*) echo " + shellQuote(filepath.Join(decoy, ".git")) + " ;;\n*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(scratch, "block.sh")
	if err := os.WriteFile(script, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", script)
	command.Dir = scratch
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WORK="+work,
		"REPO="+repo,
	)
	output, err := command.CombinedOutput()

	_, statErr := os.Stat(sentinel)
	survived := statErr == nil

	if survived && err == nil {
		t.Errorf("%s: the block reported success even though its guard could not have passed:\n%s", cleanupPage, output)
	}
	return survived
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
