package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The allowlist is the whole point of the feature, so it is pinned by what it
// must admit and by what it must refuse, not by what today's parser happens to
// return. A scheme nobody has thought of belongs in neither list and must still
// be refused, which is what an allowlist buys and a denylist does not.
func TestWebRemoteURLAdmitsOnlyHTTPAndHTTPS(t *testing.T) {
	admitted := map[string]string{
		"https://github.com/org/repo.git":          "https://github.com/org/repo.git",
		"http://git.example.invalid/org/repo":      "http://git.example.invalid/org/repo",
		"HTTPS://github.com/org/repo.git":          "https://github.com/org/repo.git",
		"  https://github.com/org/repo.git\n":      "https://github.com/org/repo.git",
		"https://git.example.invalid:8443/o/r.git": "https://git.example.invalid:8443/o/r.git",
		// A percent-encoded ? in the path is path, not a query. The query rule
		// reads the parser's serialization, so this stays admitted and a naive
		// substring test over the caller's input would not.
		"https://github.com/org/re%3Fpo.git": "https://github.com/org/re%3Fpo.git",
	}
	for raw, want := range admitted {
		if got := webRemoteURL(raw); got != want {
			t.Errorf("webRemoteURL(%q) = %q, want %q", raw, got, want)
		}
	}

	refused := []string{
		// Git's own non-web transports.
		"git@github.com:org/repo.git",
		"org-alias:org/repo.git",
		"ssh://git@github.com/org/repo.git",
		"git://github.com/org/repo.git",
		"file:///srv/git/repo.git",
		"/srv/git/repo.git",
		"../sibling/repo.git",
		"ext::ssh %S org/repo.git",
		// Script-bearing schemes must never survive to become an href.
		"javascript:alert(document.domain)",
		"JavaScript:alert(1)",
		"jAvAsCrIpT:alert(1)",
		"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
		"vbscript:msgbox(1)",
		"blob:https://github.com/deadbeef",
		// A scheme nobody has enumerated, carrying an authority so that the
		// host check cannot be what refuses it. Only an allowlist refuses
		// this; every denylist of known-bad schemes admits it.
		"future+git://github.com/org/repo.git",
		"x-unheard-of://github.com/org/repo.git",
		// Credentials in the URL are declined outright, never stripped.
		"https://x-access-token:s3cr3t@github.com/org/repo.git",
		"https://token@github.com/org/repo.git",
		"https://:s3cr3t@github.com/org/repo.git",
		// A query or fragment can carry credential material exactly as
		// userinfo can, so it is declined by the same rule.
		"https://github.com/org/repo.git?access_token=s3cr3t",
		"https://github.com/org/repo.git#access_token=s3cr3t",
		"https://github.com/org/repo.git?ref=main#L1",
		// The two empty delimiters. url.URL records an empty query as
		// ForceQuery and puts the ? back, but an empty fragment leaves no
		// trace at all in the serialisation, so only a test on the raw input
		// refuses this pair together. The browser's URL keeps the trailing #,
		// and ui/src/lib/repolink.ts refuses it; admitting it here would emit
		// a link the page then declines to draw.
		"https://github.com/org/repo.git?",
		"https://github.com/org/repo.git#",
		// Nothing to link.
		"https://",
		"http://",
		"",
		"   ",
	}
	for _, raw := range refused {
		if got := webRemoteURL(raw); got != "" {
			t.Errorf("webRemoteURL(%q) = %q, want no link", raw, got)
		}
	}
}

// Which remote, when a repository has several, is a rule the reader has to be
// able to predict. It is name-first: origin, else the lexicographically first
// name. Filtering runs afterwards on that single choice, so an unlinkable
// origin means no link rather than a link to some other remote.
func TestLinkableRemoteSelectsOriginThenTheFirstNameAlphabetically(t *testing.T) {
	cases := []struct {
		name    string
		remotes map[string]string
		want    string
	}{
		{"no remotes at all", nil, ""},
		{"empty remote set", map[string]string{}, ""},
		{
			"a lone remote is used whatever it is called",
			map[string]string{"upstream": "https://upstream.invalid/u.git"},
			"https://upstream.invalid/u.git",
		},
		{
			"origin wins over an alphabetically earlier sibling",
			map[string]string{"alpha": "https://alpha.invalid/a.git", "origin": "https://origin.invalid/o.git"},
			"https://origin.invalid/o.git",
		},
		{
			"without an origin the first name alphabetically is used",
			map[string]string{"upstream": "https://upstream.invalid/u.git", "fork": "https://fork.invalid/f.git"},
			"https://fork.invalid/f.git",
		},
		{
			"an unlinkable origin does not fall through to a linkable sibling",
			map[string]string{"origin": "git@github.com:org/repo.git", "mirror": "https://mirror.invalid/m.git"},
			"",
		},
		{
			"an origin carrying a credential yields no link",
			map[string]string{"origin": "https://token:s3cr3t@github.com/org/repo.git"},
			"",
		},
	}
	for _, item := range cases {
		if got := linkableRemote(item.remotes); got != item.want {
			t.Errorf("%s: linkableRemote = %q, want %q", item.name, got, item.want)
		}
	}
}

// Configuration outside the repository must not be able to say where the
// repository lives. `git config` without --local answers from the merge of
// system, global, command and local scopes, and command scope is set by three
// environment variables: anything that can reach the resident's environment or
// the invoking user's ~/.gitconfig could name a remote.origin.url this
// repository never configured, and git emits the outer value last, so a
// name-keyed map takes it. Only reading --local refuses that.
func TestGitRemotesReadsRepositoryLocalConfigurationOnly(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	setRemoteURL(t, repo, "origin", "https://real.invalid/org/repo.git")

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "remote.origin.url")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://attacker.invalid/evil.git")

	remotes := gitRemotes(ctx, repo)
	if got := remotes["origin"]; got != "https://real.invalid/org/repo.git" {
		t.Fatalf("origin = %q, want the repository's own local origin", got)
	}

	// An outer scope must not be able to introduce a remote either, not just
	// overwrite one: reading --local means the whole outer scope is absent.
	t.Setenv("GIT_CONFIG_KEY_0", "remote.aardvark.url")
	if _, present := gitRemotes(ctx, repo)["aardvark"]; present {
		t.Fatal("an out-of-repository scope introduced a remote")
	}
}

// The same defect, followed all the way to the value that leaves this
// boundary. gitRemotes is where it is fixed; LocalRepo.Remote is where it
// would have been observed.
func TestLocalWorktreesIgnoresOutOfRepositoryRemoteConfiguration(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "seed").CombinedOutput(); err != nil {
		t.Fatalf("seed history: %v: %s", err, output)
	}
	if _, _, err := Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}
	setRemoteURL(t, repo, "origin", "https://real.invalid/org/repo.git")

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "remote.origin.url")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://attacker.invalid/evil.git")

	workspace, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	local, err := workspace.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if local.Remote != "https://real.invalid/org/repo.git" {
		t.Fatalf("LocalRepo.Remote = %q, want the repository's own local origin", local.Remote)
	}
	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "attacker.invalid") {
		t.Fatalf("an out-of-repository remote reached the local repository projection: %s", encoded)
	}
}

// Valid configuration that is merely enormous must fail closed. Neither bound
// is reachable by any real repository; they exist so that the resident reads a
// bounded amount on every uncached worktree read and answers "no link" rather
// than a partial parse when a repository exceeds them.
func TestGitRemotesFailsClosedOnOversizedConfiguration(t *testing.T) {
	ctx := context.Background()

	tooMany := testRepo(t)
	setRemoteURL(t, tooMany, "origin", "https://real.invalid/org/repo.git")
	for index := 0; index <= maxRemoteCount; index++ {
		setRemoteURL(t, tooMany, fmt.Sprintf("filler%03d", index), "https://filler.invalid/f.git")
	}
	if remotes := gitRemotes(ctx, tooMany); remotes != nil {
		t.Fatalf("a repository over the remote-count bound answered %d remotes, want none", len(remotes))
	}
	if got := linkableRemote(gitRemotes(ctx, tooMany)); got != "" {
		t.Fatalf("a repository over the remote-count bound linked %q", got)
	}

	tooBig := testRepo(t)
	setRemoteURL(t, tooBig, "origin", "https://real.invalid/"+strings.Repeat("p", maxRemoteConfigBytes+1)+".git")
	if remotes := gitRemotes(ctx, tooBig); remotes != nil {
		t.Fatalf("a repository over the config-size bound answered %d remotes, want none", len(remotes))
	}
}

// --local answers the question "whose configuration file?" and nothing else.
// It does not answer "which repository?", and the two are separable: GIT_DIR
// and GIT_COMMON_DIR both re-point the repository Git resolves, so
// `git -C victim config --local` reads the attacker's config file while being,
// by its own lights, strictly local. GIT_WORK_TREE re-points the checkout the
// same way. Anything able to set a variable in the resident's environment
// could therefore name a remote this repository never configured — and could
// make the worktree list describe some other repository's checkouts. Only
// removing those variables from the child's environment refuses it.
func TestLocalWorktreesIgnoresInheritedGitRoutingVariables(t *testing.T) {
	ctx := context.Background()
	victim := seededRepoNamed(t, "victim", "https://real.invalid/org/repo.git")
	attacker := seededRepoNamed(t, "attacker", "https://attacker.invalid/evil.git")
	if _, _, err := Init(ctx, victim, "human", 1<<20); err != nil {
		t.Fatal(err)
	}

	for _, variable := range []struct{ name, value string }{
		{"GIT_DIR", filepath.Join(attacker, ".git")},
		{"GIT_COMMON_DIR", filepath.Join(attacker, ".git")},
		{"GIT_WORK_TREE", attacker},
		{"GIT_INDEX_FILE", filepath.Join(attacker, ".git", "index")},
	} {
		t.Run(variable.name, func(t *testing.T) {
			// A workspace per case, because LocalWorktrees caches for eight
			// seconds and a shared one would let a later case pass on an
			// earlier read; and opened before the variable is set, because
			// Open resolves the repository through internal/gitstore, which
			// this test is not about. What is under test is the four
			// read-only Git commands this package runs afterwards.
			workspace, err := Open(ctx, victim)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(variable.name, variable.value)
			local, err := workspace.LocalWorktrees(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if local.Remote != "https://real.invalid/org/repo.git" {
				t.Errorf("LocalRepo.Remote = %q, want the victim's own local origin", local.Remote)
			}
			// Each checkout's state comes from its own `git status`, which is
			// redirected by the same variables: the victim is committed
			// clean, and comparing its working tree against the attacker's
			// index or object store reports changes that are not there.
			var served *WorktreeView
			for index := range local.Worktrees {
				view := &local.Worktrees[index]
				if view.Checkout == "attacker" {
					t.Errorf("the worktree list described the attacker's checkout: %+v", *view)
				}
				if view.Checkout == "victim" {
					served = view
				}
			}
			switch {
			case served == nil:
				t.Errorf("the victim's own checkout is missing from %+v", local.Worktrees)
			case served.State != "clean":
				t.Errorf("the victim's checkout is committed clean but reported %q", served.State)
			}
			encoded, err := json.Marshal(local)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "attacker") {
				t.Errorf("%s redirected the local repository projection: %s", variable.name, encoded)
			}
		})
	}
}

// A Git configuration file is not data. `core.fsmonitor`, `core.pager`,
// `diff.external` and their relatives name programs Git runs, so a variable
// that points Git at a configuration file hands the caller command execution
// inside this process's shoes, whatever the variable is called. Admitting
// `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM` on the reasoning that they only
// ever narrow scope was wrong in kind: allowlisting the *name* of a variable
// that points at a *file* bounds nothing at all about the file's contents.
//
// `git status` is the boundary that has to refuse it, because it is the one
// command here that runs against every linked checkout and the one whose answer
// a reader acts on. Both effects are asserted, and neither implies the other. A
// change that stopped the program running but still let the file reach Git
// would pass an execution-only test while some other execution-free directive
// still moved the answer; a change that fixed the answer while the program
// still ran would leave the worse of the two defects in place.
//
// Both channels the contract closes are exercised, because dropping the
// GIT_CONFIG family alone does not close either one. A caller who merely omits
// GIT_CONFIG_GLOBAL still gets the invoking user's ~/.gitconfig read, and HOME
// is inherited on purpose — Git is not runnable without it — so a caller able
// to set HOME reaches the same file by another name. Only pinning the global
// and system scopes shut refuses the second case, which is why the pins are set
// here rather than passed through from whoever started the process.
//
// The control run is what stops each case passing vacuously. It performs the
// same exploit against a throwaway repository outside the constructor, so a
// green result below means the constructor refused the vector rather than that
// the vector stopped working. It is a hard failure and not a skip: a silently
// skipped security regression is indistinguishable from a passing one, and a
// Git that no longer executes `core.fsmonitor` is a reason to re-derive the
// vector deliberately, not to stop testing the property. The control needs its
// own repository because the exploit leaves junk files behind, which is the
// second effect and would make the guarded checkout dirty for the wrong reason.
func TestInheritedConfigurationCannotReachTheWorktreeStatusRead(t *testing.T) {
	ctx := context.Background()

	for _, vector := range []struct {
		name string
		// variables names what the caller controls, and is used twice: set on
		// the control's own environment, and set on this process for the
		// guarded read. Both runs therefore face the same inherited state.
		variables func(t *testing.T, marker string) map[string]string
	}{
		{
			"GIT_CONFIG_GLOBAL names the file",
			func(t *testing.T, marker string) map[string]string {
				return map[string]string{
					"HOME":              t.TempDir(),
					"GIT_CONFIG_GLOBAL": commandBearingConfiguration(t, filepath.Join(t.TempDir(), "gitconfig"), marker),
				}
			},
		},
		{
			"HOME names the directory holding it",
			func(t *testing.T, marker string) map[string]string {
				home := t.TempDir()
				commandBearingConfiguration(t, filepath.Join(home, ".gitconfig"), marker)
				return map[string]string{"HOME": home}
			},
		},
	} {
		t.Run(vector.name, func(t *testing.T) {
			control := seededRepoNamed(t, "control", "https://real.invalid/org/repo.git")
			controlMarker := filepath.Join(t.TempDir(), "CONTROL-EXECUTED")
			controlRead := exec.Command("git", "--no-optional-locks", "-C", control,
				"status", "--porcelain=v1", "--untracked-files=normal")
			// Built from nothing but PATH and the vector, so that the test
			// process's own isolation — internal/testgit pins the global scope
			// for every test in this package — cannot be what refuses the
			// control, and so cannot make the control report that a vector
			// still working has stopped working.
			controlRead.Env = []string{"PATH=" + os.Getenv("PATH")}
			for name, value := range vector.variables(t, controlMarker) {
				controlRead.Env = append(controlRead.Env, name+"="+value)
			}
			controlStatus, err := controlRead.Output()
			if err != nil {
				t.Fatalf("control status read: %v", err)
			}
			if _, statErr := os.Stat(controlMarker); statErr != nil {
				t.Fatalf("the vector this test defends against no longer executes here (%v); re-derive it rather than deleting the test", statErr)
			}
			if len(controlStatus) == 0 {
				t.Fatal("the vector no longer corrupts the status answer; re-derive it rather than deleting the test")
			}

			// A repository and a workspace per case, because LocalWorktrees
			// caches for eight seconds and a shared one would let a later case
			// pass on an earlier read. Opened before the variables are set,
			// because Open resolves the repository through internal/gitstore,
			// which bounds its own environment and is not what this test is
			// about. What is under test is the read-only Git commands this
			// package runs afterwards.
			victim := seededRepoNamed(t, "victim", "https://real.invalid/org/repo.git")
			if _, _, err := Init(ctx, victim, "human", 1<<20); err != nil {
				t.Fatal(err)
			}
			workspace, err := Open(ctx, victim)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "EXECUTED")
			for name, value := range vector.variables(t, marker) {
				t.Setenv(name, value)
			}

			local, err := workspace.LocalWorktrees(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Errorf("inherited configuration ran a command from a read-only Git command: %s exists", marker)
			}
			var served *WorktreeView
			for index := range local.Worktrees {
				if local.Worktrees[index].Checkout == "victim" {
					served = &local.Worktrees[index]
				}
			}
			switch {
			case served == nil:
				t.Fatalf("the victim's own checkout is missing from %+v", local.Worktrees)
			case served.State != "clean":
				// Git appends its own arguments to whatever the configured
				// command is, so the program it runs leaves files in the
				// checkout it was reading. The answer the reader acts on then
				// says "dirty" about a checkout nobody touched.
				entries, _ := os.ReadDir(victim)
				t.Errorf("inherited configuration corrupted the status answer: state %q, checkout now holds %v", served.State, entries)
			}
		})
	}
}

// commandBearingConfiguration writes a Git configuration file whose contents
// are a command rather than a value, and returns where it put it. It stands for
// every execution-affecting directive a configuration file can carry;
// `core.fsmonitor` is the cheapest to arrange because `git status` runs it
// unprompted on an ordinary read.
func commandBearingConfiguration(t *testing.T, path, marker string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("[core]\n\tfsmonitor = touch "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// citingDocuments is read as a guard, not as a display: a retirement the
// documentation still names is refused on the strength of its answer, so an
// empty answer is a bypass rather than a cosmetic wrong. The pathspec
// variables are the cheap way to force one — GIT_LITERAL_PATHSPECS makes the
// `*.md` pathspec a literal filename that matches nothing — and GIT_DIR
// re-points the grep at a repository where the page does not exist. Neither
// changes a byte of the command line, which is why the environment is what
// has to refuse them.
func TestCitingDocumentsIgnoresInheritedGitRoutingVariables(t *testing.T) {
	ctx := context.Background()
	repo := seededRepoNamed(t, "victim", "https://real.invalid/org/repo.git")
	elsewhere := seededRepoNamed(t, "attacker", "https://attacker.invalid/evil.git")
	const event = "sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	if err := os.WriteFile(filepath.Join(repo, "page.md"), []byte("this page cites "+event+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "add", "page.md").CombinedOutput(); err != nil {
		t.Fatalf("track page: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "page").CombinedOutput(); err != nil {
		t.Fatalf("commit page: %v: %s", err, output)
	}
	if _, _, err := Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}
	workspace, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := workspace.citingDocuments(ctx, repo, event)
	if err != nil {
		t.Fatalf("baseline citation lookup: %v", err)
	}
	if len(pages) != 1 || pages[0] != "page.md" {
		t.Fatalf("baseline citing documents = %v, want [page.md]", pages)
	}

	for _, variable := range []struct{ name, value string }{
		{"GIT_LITERAL_PATHSPECS", "1"},
		{"GIT_NOGLOB_PATHSPECS", "1"},
		{"GIT_DIR", filepath.Join(elsewhere, ".git")},
	} {
		t.Run(variable.name, func(t *testing.T) {
			t.Setenv(variable.name, variable.value)
			pages, err := workspace.citingDocuments(ctx, repo, event)
			if err != nil {
				t.Errorf("%s stopped the citation lookup answering: %v", variable.name, err)
			} else if len(pages) != 1 || pages[0] != "page.md" {
				t.Errorf("%s hid the citing page: got %v, want [page.md]", variable.name, pages)
			}
		})
	}
}

// The citation guard decides a retirement, so the one answer it must never
// give is a confident "nothing cites this" that it did not actually establish.
// `git grep` says "no matches" by exiting 1 and says "I did not run" by exiting
// anything else, and a guard that reads both as an empty page list retires a
// record the documentation still names — the exact breakage of 2026-08-12. The
// environment can no longer reach it, but that is not what makes it safe: a
// repository Git declines to read, an exhausted resource, or a setting in the
// repository's own configuration file stops the lookup just as dead, and none
// of those is anything an environment bound has an opinion about.
//
// The lookup is broken here through the repository's own configuration file,
// and that choice is the whole point of the test. Every scope outside the
// repository is now pinned shut by repositoryLocalGit, so breaking the lookup
// from out there would only re-test that bound; the repository's own file is
// inside the contract by necessity, because remote.origin.url lives in it and
// reading it is what gitRemotes is for. So this is a way of breaking the lookup
// that no environment bound is allowed to close, which is what keeps this a
// test of the exit-code distinction rather than a second test of the
// configuration contract. `grep.threads = -1` is valid configuration syntax
// that `git grep` refuses at startup, so nothing about the command line has to
// be wrong for the lookup to stop answering, and nothing but grep is affected:
// the same act builds cleanly right up until the setting is written.
//
// The target is one nothing cites, so a refusal here cannot be the ordinary
// citation refusal wearing a disguise: before the configuration is broken the
// same act builds cleanly.
func TestRetirementIsRefusedWhenTheCitationLookupCannotRun(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	uncited := seed.ID + "-nothing-cites-this"
	retire := Act{Verb: VerbSupersede, Target: uncited, Text: "retire it"}
	if _, err := workspace.BuildActRequest(ctx, private, "human", retire); err != nil {
		t.Fatalf("baseline: an uncited retirement must build, got %v", err)
	}

	makeGrepUnrunnable(t, repo)
	_, err = workspace.BuildActRequest(ctx, private, "human", retire)
	if err == nil {
		t.Fatal("a retirement was built on a citation lookup that never ran")
	}
	// The message has to say which of the two refusals this is, because the
	// operator's next move differs: a lookup that could not run is retried
	// once the repository is readable again, and a genuine citation is
	// repointed first. Naming the target as well keeps a batch refusal
	// attributable to one act.
	if !strings.Contains(err.Error(), "citation lookup") || !strings.Contains(err.Error(), uncited) {
		t.Errorf("the refusal must say the lookup did not run and name %s, got %v", uncited, err)
	}
}

// The other half of the same distinction, and the reason the fix cannot be
// "refuse every non-zero exit". Exit 1 is how `git grep` says nothing matched,
// which is the ordinary answer on nearly every retirement there will ever be:
// reading it as a failure would refuse the whole verb. The repository here
// tracks a Markdown page that does not name the target, so the grep really
// walks a candidate file and really finds nothing, rather than exiting early on
// a pathspec that matches no tracked file at all.
func TestCitationLookupFindingNothingStillPermitsTheRetirement(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(repo, "docs", "reference", "elsewhere.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("prose that names no event at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "add", "docs/reference/elsewhere.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}

	retire := Act{Verb: VerbSupersede, Target: seed.ID, Text: "retire it"}
	if _, err := workspace.BuildActRequest(ctx, private, "human", retire); err != nil {
		t.Fatalf("no page cites the target, so the retirement must build: %v", err)
	}
}

// The secondary defence, stated as the property it buys rather than as the
// list it is implemented by: nothing a caller puts in the command configuration
// scope may change what the citation guard sees. Command scope is the cheapest
// injection there is — three environment variables, no file, no repository
// access — and `grep.threads = -1` in it stops the lookup dead.
//
// The assertion is on the honest answer, not merely on a refusal. Either
// defence alone produces some refusal here, so a test that only demanded "an
// error" would pass with the injection still reaching Git. Demanding that the
// refusal names the citing page demands that the lookup ran and answered
// truthfully, which only the environment bound gives.
func TestCommandScopeConfigurationCannotReachTheCitationLookup(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(repo, "docs", "reference", "thing.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("---\nrests_on:\n  - "+seed.ID+"\n---\n\nprose\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "add", "docs/reference/thing.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "grep.threads")
	t.Setenv("GIT_CONFIG_VALUE_0", "-1")

	retire := Act{Verb: VerbSupersede, Target: seed.ID, Text: "retire it"}
	_, err = workspace.BuildActRequest(ctx, private, "human", retire)
	if err == nil {
		t.Fatal("command-scope configuration silenced the citation guard")
	}
	if !strings.Contains(err.Error(), "docs/reference/thing.md") {
		t.Errorf("command-scope configuration displaced the honest answer, got %v", err)
	}
}

// makeGrepUnrunnable writes into a repository's own configuration file a
// setting that is valid to parse and impossible to run a grep under. It is a
// stand-in for every reason the lookup might not answer — a broken Git, an
// exhausted file descriptor table, a corrupt index, a repository Git declines
// to read — and it is the cheapest of them to arrange reproducibly. None of
// those causes is reachable by a list of environment variables, which is the
// property the caller depends on.
func makeGrepUnrunnable(t *testing.T, repo string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", repo, "config", "--local", "grep.threads", "-1").CombinedOutput(); err != nil {
		t.Fatalf("break the repository's grep configuration: %v: %s", err, output)
	}
}

// seededRepoNamed builds a clean repository with one origin, in a directory of
// the given name so that the worktree list's display label says which
// repository answered. The one tracked file is named after the repository too,
// so that a checkout compared against the wrong repository's index or object
// store reports changes and a redirected `git status` cannot be mistaken for
// the honest one.
func seededRepoNamed(t *testing.T, name, remote string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", name, err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, name+".md"), []byte("belongs to "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "add", name+".md").CombinedOutput(); err != nil {
		t.Fatalf("track %s file: %v: %s", name, err, output)
	}
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "seed").CombinedOutput(); err != nil {
		t.Fatalf("seed %s: %v: %s", name, err, output)
	}
	setRemoteURL(t, repo, "origin", remote)
	return repo
}

func setRemoteURL(t *testing.T, repo, name, value string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", repo, "config", "remote."+name+".url", value).CombinedOutput(); err != nil {
		t.Fatalf("set remote %s: %v: %s", name, err, output)
	}
}

// The end-to-end carry: git config, through LocalWorktrees, into the value the
// worktrees response serialises. A repository with no remote must serialise
// exactly as it did before this field existed.
func TestLocalWorktreesCarriesOnlyALinkableRemote(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "seed").CombinedOutput(); err != nil {
		t.Fatalf("seed history: %v: %s", err, output)
	}
	if _, _, err := Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}

	// A fresh workspace each read: LocalWorktrees caches for eight seconds, and
	// a stale cache would let a later case pass on an earlier case's answer.
	read := func() LocalRepo {
		t.Helper()
		workspace, err := Open(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		local, err := workspace.LocalWorktrees(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return local
	}

	local := read()
	if local.Remote != "" {
		t.Fatalf("repository with no remote reported remote %q", local.Remote)
	}
	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"remote"`) {
		t.Fatalf("no-remote repository serialised a remote field: %s", encoded)
	}

	setRemoteURL(t, repo, "origin", "https://github.com/generalbusiness-ai/gitseq.git")
	if local := read(); local.Remote != "https://github.com/generalbusiness-ai/gitseq.git" {
		t.Fatalf("https origin remote = %q", local.Remote)
	}

	setRemoteURL(t, repo, "origin", "git@github.com:generalbusiness-ai/gitseq.git")
	if local := read(); local.Remote != "" {
		t.Fatalf("scp-style origin reported remote %q, want none", local.Remote)
	}

	setRemoteURL(t, repo, "origin", "ssh://git@github.com/generalbusiness-ai/gitseq.git")
	if local := read(); local.Remote != "" {
		t.Fatalf("ssh origin reported remote %q, want none", local.Remote)
	}

	// git:// parses cleanly and carries no userinfo, so only the scheme
	// allowlist can refuse it. Without this case a permissive scheme check
	// still passes every other case here.
	setRemoteURL(t, repo, "origin", "git://github.com/generalbusiness-ai/gitseq.git")
	if local := read(); local.Remote != "" {
		t.Fatalf("git:// origin reported remote %q, want none", local.Remote)
	}

	// A credential in the remote URL is a secret. It must not appear anywhere in
	// what leaves this boundary, not even sanitised.
	setRemoteURL(t, repo, "origin", "https://x-access-token:s3cr3t-token@github.com/generalbusiness-ai/gitseq.git")
	local = read()
	if local.Remote != "" {
		t.Fatalf("credential-bearing origin reported remote %q, want none", local.Remote)
	}
	encoded, err = json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "s3cr3t-token") || strings.Contains(string(encoded), "x-access-token") {
		t.Fatalf("credential leaked into the local repository projection: %s", encoded)
	}

	// Several remotes with no origin: the first name alphabetically, read from
	// real git config rather than from a hand-built map.
	if output, err := exec.Command("git", "-C", repo, "config", "--unset", "remote.origin.url").CombinedOutput(); err != nil {
		t.Fatalf("unset origin: %v: %s", err, output)
	}
	setRemoteURL(t, repo, "upstream", "https://upstream.invalid/u.git")
	setRemoteURL(t, repo, "fork", "https://fork.invalid/f.git")
	if local := read(); local.Remote != "https://fork.invalid/f.git" {
		t.Fatalf("originless multi-remote repository = %q, want the first name alphabetically", local.Remote)
	}
}
