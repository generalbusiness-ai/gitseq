package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/workroom"
)

func TestValidateLoopbackListen(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:7777": true,
		"[::1]:7777":     true,
		"localhost:7777": true,
		":7777":          false,
		"0.0.0.0:7777":   false,
		"192.0.2.1:7777": false,
		"not-an-address": false,
	}
	for address, want := range tests {
		t.Run(address, func(t *testing.T) {
			if got := validateLoopbackListen(address) == nil; got != want {
				t.Fatalf("validateLoopbackListen(%q) success = %v, want %v", address, got, want)
			}
		})
	}
}

func TestAttachAdvancesButRejectsRemoteRewind(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "remote.git")
	auditor := filepath.Join(root, "auditor")

	testGit(t, "", "init", source)
	workspace, _, err := app.Init(ctx, source, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/seq/" + workspace.Config.Genesis
	first := testGit(t, source, "rev-parse", ref)
	testGit(t, "", "init", "--bare", remote)
	testGit(t, source, "remote", "add", "origin", remote)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")

	testGit(t, "", "clone", remote, auditor)
	testGit(t, auditor, "config", "--add", "remote.origin.fetch", forcedSequenceFetchRefspec)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("initial attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != first {
		t.Fatalf("initial sequence head = %s, want %s", got, first)
	}
	fetchRules := strings.Fields(testGit(t, auditor, "config", "--get-all", "remote.origin.fetch"))
	if contains(fetchRules, forcedSequenceFetchRefspec) || !contains(fetchRules, sequenceFetchRefspec) {
		t.Fatalf("sequence fetch rules = %#v, want only non-forcing sequence rule", fetchRules)
	}

	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "advance",
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "advance",
	}); err != nil {
		t.Fatal(err)
	}
	second := testGit(t, source, "rev-parse", ref)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("forward attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("forward sequence head = %s, want %s", got, second)
	}

	testGit(t, "", "--git-dir", remote, "update-ref", ref, first, second)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err == nil {
		t.Fatal("attach to rewound remote sequence succeeded")
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("rewind changed local sequence head to %s, want %s", got, second)
	}
	if output, err := exec.Command("git", "-C", auditor, "fetch", "origin").CombinedOutput(); err == nil {
		t.Fatalf("ordinary fetch accepted rewound sequence: %s", output)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("ordinary fetch rewound local sequence head to %s, want %s", got, second)
	}
}

func testGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	if repo != "" {
		arguments = append([]string{"-C", repo}, arguments...)
	}
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
