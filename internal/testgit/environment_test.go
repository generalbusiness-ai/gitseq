package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Every repository created after Isolate, plain or bare, in either object
// format, starts without fsync and without automatic gc at repository-local
// scope, the scope the store's hermetic Git environment keeps.
func TestIsolateTemplatesTestConfigurationIntoNewRepositories(t *testing.T) {
	if err := Isolate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var repos []string
	for _, format := range []string{"sha1", "sha256"} {
		plain := filepath.Join(root, "plain-"+format)
		git(t, "init", "-q", "--object-format="+format, plain)
		bare := filepath.Join(root, "bare-"+format+".git")
		git(t, "init", "-q", "--bare", "--object-format="+format, bare)
		repos = append(repos, plain, bare)
		if got := git(t, "--git-dir", bare, "rev-parse", "--show-object-format"); got != format+"\n" {
			t.Fatalf("template overwrote init's own keys: object format of %s = %q", bare, got)
		}
	}
	for _, repo := range repos {
		for key, want := range map[string]string{"core.fsync": "local\tnone\n", "gc.auto": "local\t0\n"} {
			if got := git(t, "--git-dir", gitDir(repo), "config", "--show-scope", "--get", key); got != want {
				t.Fatalf("%s in %s = %q, want %q", key, repo, got, want)
			}
		}
	}
}

func gitDir(repo string) string {
	if strings.HasSuffix(repo, ".git") {
		return repo
	}
	return filepath.Join(repo, ".git")
}

func git(t *testing.T, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}
