package docset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// noWorkroomSkipReason is the opening of anchor_test.go's skip for a checkout
// that holds no workroom. Matching it distinguishes that skip from the
// genesis-mismatch skip in the same gate.
const noWorkroomSkipReason = "no workroom in this checkout"

// withoutGitEnvironment drops the variables by which an outer Git environment
// could redirect the subprocess at a real repository.
func withoutGitEnvironment(environment []string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && (name == "GIT_DIR" || name == "GIT_WORK_TREE" || name == "GIT_COMMON_DIR") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func TestGateWithoutWorkroomIsAnExplicitSkip(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DocsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Test checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	page := `---
title: Test page
summary: A page with one durable citation.
rests_on:
  - git:sha1:1111111111111111111111111111111111111111#git:sha1:2222222222222222222222222222222222222222
---

# Test page
`
	if err := os.WriteFile(filepath.Join(root, DocsDir, "test.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestGateEveryNamedActResolvesToALiveRecord$", "-test.v")
	command.Dir = root
	// app.Open resolves through git, which honours GIT_DIR over the walk-up, so
	// an inherited Git environment points the subprocess at a real workroom and
	// the gate skips for a genesis mismatch instead. The test would still see a
	// skip and still pass, proving the wrong branch. Strip those three so the
	// checkout under test is the temporary directory by construction.
	command.Env = withoutGitEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("citation gate hard-failed without a workroom: %v\n%s", err, output)
	}
	result := string(output)
	// The reason, not just the prefix: this gate also skips on a genesis
	// mismatch, and only the no-workroom branch is under test here.
	if !strings.Contains(result, noWorkroomSkipReason) {
		t.Fatalf("citation gate did not skip for the absent workroom; wanted %q in:\n%s", noWorkroomSkipReason, result)
	}
	if !strings.Contains(result, "--- SKIP: TestGateEveryNamedActResolvesToALiveRecord") {
		t.Fatalf("citation gate did not report an explicit skip without a workroom:\n%s", result)
	}
}
