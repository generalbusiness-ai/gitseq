package app

import (
	"context"
	"encoding/json"
	"os/exec"
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
		// Credentials in the URL are declined outright, never stripped.
		"https://x-access-token:s3cr3t@github.com/org/repo.git",
		"https://token@github.com/org/repo.git",
		"https://:s3cr3t@github.com/org/repo.git",
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
