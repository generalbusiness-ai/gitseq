package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsolateIgnoresGlobalAndSystemGitConfig(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	if err := os.Mkdir(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config")
	contents := "[commit]\n\tgpgsign = true\n\ttemplate = " + filepath.Join(root, "missing-template") +
		"\n[core]\n\tautocrlf = true\n\thooksPath = " + hooks +
		"\n[gpg]\n\tformat = ssh\n[init]\n\tdefaultBranch = trunk\n"
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_SYSTEM", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
	if err := Isolate(); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(root, "repo")
	git(t, "init", "-q", repo)
	git(t, "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "candidate")
	branch := git(t, "-C", repo, "branch", "--show-current")
	if branch == "trunk\n" {
		t.Fatal("ambient init.defaultBranch reached the isolated test repository")
	}
}

func TestIsolateClearsCommandScopedConfigInjection(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	if err := Isolate(); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	git(t, "init", "-q", repo)
	git(t, "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "candidate")
}

func git(t *testing.T, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}
