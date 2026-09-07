package main
import("context";"os";"os/exec";"path/filepath";"testing";"github.com/generalbusiness-ai/gitseq/internal/app";"github.com/generalbusiness-ai/gitseq/internal/workroom")
func TestCodexProposalDryRunIgnoresObservationRoute(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	state := func(act app.Act) workroom.Record {
		t.Helper()
		submission, err := workspace.Act(ctx, "human", act)
		if err != nil {
			t.Fatal(err)
		}
		return submission.Record
	}
	charter := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindPropose, Text: "connector charter",
		Body: map[string]string{
			"connector": "github", "owner": "generalbusiness-ai", "repo": "gitseq",
			"actor": "github-connector", "operations": "propose",
		},
		RestsOn: []string{seed.ID}, IdempotencyKey: "charter",
	})
	state(app.Act{Verb: app.VerbRatify, Target: charter.ID, IdempotencyKey: "ratify-charter"})
	request := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "fix issue",
		Body: map[string]string{
			"to": workspace.View().Actors["human"].Fingerprint, "conditions": "exact head", "no_git_artifact": "true",
		},
		RestsOn: []string{seed.ID}, IdempotencyKey: "request",
	})
	const commit = "1111111111111111111111111111111111111111"
	artifact := state(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "candidate",
		Body:    map[string]string{"path": "internal/connector", "commit": commit},
		RestsOn: []string{request.ID}, IdempotencyKey: "artifact",
	})

	if err := os.WriteFile(filepath.Join(workspace.MetaDir, "resident.json"), []byte("not json"), 0o644); err != nil { t.Fatal(err) }
	err = run(ctx, []string{
		"--repo", repo, "--as", "github-connector", "--charter", charter.ID,
		"--owner", "generalbusiness-ai", "--repo-name", "gitseq",
		"--propose", "7", "--branch", "request/fix", "--base", "main",
		"--commit", commit, "--request", request.ID, "--artifact", artifact.ID,
		"--title", "Fix issue", "--dry-run",
	})
	if err != nil {
		t.Fatalf("dry-run proposal required local connector custody: %v", err)
	}
}

