package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLandingMeasurementsBatchGitCommands(t *testing.T) {
	repo := testRepo(t)
	landingTestGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "source")
	head := landingTestGit(t, repo, "rev-parse", "HEAD")
	landingTestGit(t, repo, "update-ref", "refs/heads/release", head)
	w, _, err := Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	proxy := "#!/bin/sh\nprintf 'call\\n' >> " + quote(counter) + "\nexec " + quote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(proxy), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	input := LandingInput{TargetRepo: "git:" + w.config.ObjectFormat + ":" + w.config.Genesis, TargetRef: "refs/heads/release", MergeHead: head}
	count := func() int {
		data, err := os.ReadFile(counter)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "call\n")
	}
	if row := w.MeasureLandings(context.Background(), []LandingInput{input})[0]; row.State != "incorporated" {
		t.Fatalf("inert command control: %+v", row)
	}
	one := count()
	inputs := make([]LandingInput, LandingMeasurementLimit+1)
	for i := range inputs {
		inputs[i] = input
	}
	rows := w.MeasureLandings(context.Background(), inputs)
	for i, row := range rows[:LandingMeasurementLimit] {
		if row.State != "incorporated" {
			t.Fatalf("row %d lost: %+v", i, row)
		}
	}
	if last := rows[LandingMeasurementLimit]; last.State != "unknown" || last.RefIncorporated != nil {
		t.Fatalf("row bound became a false answer: %+v", last)
	}
	if many := count() - one; many != one || one > 6 {
		t.Fatalf("Git subprocesses scaled with rows: one=%d many=%d", one, many)
	}
}

func landingTestGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := repositoryLocalGit(context.Background(), repo, args...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLandingMeasurementsFollowTargetAndPreserveUnknown(t *testing.T) {
	repo := testRepo(t)
	commit := func() string {
		landingTestGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "source")
		return landingTestGit(t, repo, "rev-parse", "HEAD")
	}
	seed := commit()
	merged := commit()
	forward := commit()
	landingTestGit(t, repo, "update-ref", "refs/heads/release", forward)
	landingTestGit(t, repo, "remote", "add", "origin", "https://example.invalid/never-contacted.git")
	landingTestGit(t, repo, "update-ref", "refs/remotes/origin/release", seed)
	w, _, err := Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	input := LandingInput{TargetRepo: "git:" + w.config.ObjectFormat + ":" + w.config.Genesis, TargetRef: "refs/heads/release", MergeHead: merged}
	measure := func() LandingMeasurement { return w.MeasureLandings(context.Background(), []LandingInput{input})[0] }
	got := measure()
	if got.State != "incorporated" || got.RefIncorporated == nil || !*got.RefIncorporated || got.RemoteContains == nil || *got.RemoteContains || got.TargetHead != forward || got.RemoteRef != "refs/remotes/origin/release" {
		t.Fatalf("target/remote distinction: %+v", got)
	}
	// Move only the source ref; an unchanged durable frontier must not reuse
	// the previous measurement or reinterpret the receipt as unsatisfied.
	landingTestGit(t, repo, "update-ref", "refs/heads/release", seed)
	landingTestGit(t, repo, "update-ref", "refs/remotes/origin/release", forward)
	got = measure()
	if got.State != "landed-then-removed" || got.RefIncorporated == nil || *got.RefIncorporated || got.RemoteContains == nil || !*got.RemoteContains {
		t.Fatalf("ref move hidden: %+v", got)
	}
	landingTestGit(t, repo, "update-ref", "-d", "refs/heads/release")
	got = measure()
	if got.State != "target_gone" || got.RefIncorporated != nil {
		t.Fatalf("deleted target: %+v", got)
	}
	landingTestGit(t, repo, "update-ref", "refs/heads/release", forward)
	input.MergeHead = strings.Repeat("a", 40)
	got = measure()
	if got.State != "unknown" || got.RefIncorporated != nil || got.RemoteContains != nil {
		t.Fatalf("missing object became absence: %+v", got)
	}
	input.MergeHead = merged
	// Ambient routing must not redirect the advisory read into another repo.
	other := testRepo(t)
	t.Setenv("GIT_DIR", other+"/.git")
	got = measure()
	if got.State != "incorporated" {
		t.Fatalf("ambient repository redirected read: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = w.MeasureLandings(ctx, []LandingInput{input})[0]
	if got.State != "unknown" || got.RefIncorporated != nil {
		t.Fatalf("unavailable refs became target_gone: %+v", got)
	}
	input.TargetRepo = "another repository"
	got = measure()
	if got.State != "unknown" || got.RefIncorporated != nil {
		t.Fatalf("foreign target was measured locally: %+v", got)
	}
}

func TestLandingGraphIncompleteTraversalNeverProvesAbsence(t *testing.T) {
	for _, incomplete := range []string{"missing parent", "shallow", "budget"} {
		t.Run(incomplete, func(t *testing.T) {
			g := &landingGraph{objects: map[string]bool{"tip": true, "root": true, "other": true}, parents: map[string][]string{"tip": {"root"}, "root": {}}, ancestors: map[string]landingAncestors{}, walkRemaining: 100}
			if incomplete == "missing parent" {
				delete(g.parents, "root")
			}
			if incomplete == "shallow" {
				g.shallow = true
			}
			if incomplete == "budget" {
				g.walkRemaining = 0
			}
			if got := g.contains("tip", "other"); got != nil {
				t.Fatalf("%s became negative evidence: %v", incomplete, *got)
			}
			if got := g.contains("tip", "tip"); got == nil || !*got {
				t.Fatal("known identical commit lost")
			}
		})
	}
}

func TestRemoteTrackingUsesFetchMapping(t *testing.T) {
	repo := testRepo(t)
	landingTestGit(t, repo, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/custom/*")
	got := remoteTrackingRefs(context.Background(), repo, "origin", []string{"refs/heads/release"})
	if got["refs/heads/release"] != "refs/remotes/custom/release" {
		t.Fatalf("custom mapping guessed: %v", got)
	}
	landingTestGit(t, repo, "config", "--add", "remote.origin.fetch", "+refs/heads/release:refs/remotes/other/release")
	if got := remoteTrackingRefs(context.Background(), repo, "origin", []string{"refs/heads/release"}); len(got) != 0 {
		t.Fatalf("ambiguous mapping guessed: %v", got)
	}
}
