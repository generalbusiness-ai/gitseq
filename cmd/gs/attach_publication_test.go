package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestAttachMigratesBothLegacyRulesAndPreservesCustomConfiguration(t *testing.T) {
	t.Parallel()
	for _, rules := range [][]string{{sequenceFetchRefspec}, {forcedSequenceFetchRefspec}, {sequenceFetchRefspec, forcedSequenceFetchRefspec}} {
		t.Run(strings.Join(rules, ","), func(t *testing.T) {
			ctx := context.Background()
			source := filepath.Join(t.TempDir(), "source")
			testGit(t, "", "init", source)
			workspace, _, err := app.Init(ctx, source, "operator", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			repo := filepath.Join(t.TempDir(), "auditor")
			testGit(t, "", "clone", source, repo)
			custom := "+refs/heads/release/*:refs/remotes/origin/releases/*"
			testGit(t, repo, "config", "--replace-all", "remote.origin.fetch", custom)
			testGit(t, repo, "config", "custom.keep", "keep this exact value")
			for _, rule := range rules {
				testGit(t, repo, "config", "--add", "remote.origin.fetch", rule)
			}
			args := []string{"--repo", repo, "--genesis", workspace.View().Genesis}
			if err := attachCommand(ctx, args); err != nil {
				t.Fatal(err)
			}
			got := strings.Split(testGit(t, repo, "config", "--get-all", "remote.origin.fetch"), "\n")
			if len(got) != 2 || !contains(got, custom) || !contains(got, sequenceTrackingRefspec("origin")) {
				t.Fatalf("migration changed custom mappings or retained unsafe rules: %q", got)
			}
			if got := testGit(t, repo, "config", "custom.keep"); got != "keep this exact value" {
				t.Fatalf("migration changed unrelated configuration: %q", got)
			}
			_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(apphost.MetaDir(commonDir), apphost.ConfigFile)
			before, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := attachCommand(ctx, args); err != nil {
				t.Fatalf("repeated unchanged attach: %v", err)
			}
			after, err := os.ReadFile(config)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("repeated attachment changed saved custody/checkpoint: %v", err)
			}
		})
	}
}

// The receiving hook pins the append inside the sender's successful push.
// Checking its exit status is essential: a hook that silently failed to move
// the sender would make the legacy mapping appear to demonstrate this race.
func TestSequencePublicationPreservesConcurrentAppend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the deterministic receiving hook requires a POSIX shell")
	}
	for _, legacy := range []bool{true, false} {
		name := "supported attachment mapping"
		if legacy {
			name = "legacy mapping loses the append"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			sender := filepath.Join(root, "sender")
			remote := filepath.Join(root, "remote.git")
			testGit(t, "", "init", sender)
			workspace, seed, err := app.Init(ctx, sender, "operator", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			ref := "refs/seq/" + workspace.View().Genesis
			testGit(t, "", "init", "--bare", remote)
			testGit(t, sender, "remote", "add", "origin", remote)
			testGit(t, sender, "push", "origin", ref+":"+ref)
			if legacy {
				testGit(t, sender, "config", "--add", "remote.origin.fetch", "refs/seq/*:refs/seq/*")
			} else if err := fetchSequenceRefs(ctx, sender, "origin", workspace.View().Genesis); err != nil {
				t.Fatal(err)
			}
			var heads []string
			for _, text := range []string{"advertised position", "concurrent append"} {
				if _, err := workspace.Act(ctx, "operator", app.Act{
					Verb: app.VerbState, Kind: workroom.KindAssert, Text: text,
					RestsOn: []string{seed.ID}, IdempotencyKey: text,
				}); err != nil {
					t.Fatal(err)
				}
				heads = append(heads, testGit(t, sender, "rev-parse", ref))
			}
			// Both signed objects already exist in this isolated fixture. The
			// hook publishes the second ref advance at the exact race window.
			testGit(t, sender, "update-ref", ref, heads[0], heads[1])
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
			hook := "#!/bin/sh\nset -eu\n" +
				"unset GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_QUARANTINE_PATH\n" +
				"git --git-dir=" + quote(filepath.Join(sender, ".git")) + " update-ref " + quote(ref) + " " + heads[1] + " " + heads[0] + "\n" +
				"git --git-dir=" + quote(filepath.Join(sender, ".git")) + " rev-parse " + quote(ref) + " > " + quote(filepath.Join(root, "hook-head")) + "\n" +
				"cat >/dev/null\n"
			if err := os.MkdirAll(filepath.Join(remote, "hooks"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o700); err != nil {
				t.Fatal(err)
			}
			testGit(t, sender, "push", "origin", ref+":"+ref)
			observed, err := os.ReadFile(filepath.Join(root, "hook-head"))
			if err != nil || strings.TrimSpace(string(observed)) != heads[1] {
				t.Fatalf("hook did not prove the concurrent advance: %q, %v", observed, err)
			}
			if got := testGit(t, remote, "rev-parse", ref); got != heads[0] {
				t.Fatalf("remote received %s, want advertised head %s", got, heads[0])
			}
			want := heads[1]
			if legacy {
				want = heads[0]
			}
			if got := testGit(t, sender, "rev-parse", ref); got != want {
				t.Fatalf("local sequence after successful push = %s, want %s (concurrent append %s)", got, want, heads[1])
			}
		})
	}
}
