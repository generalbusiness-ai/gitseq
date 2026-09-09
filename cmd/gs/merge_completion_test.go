package main

import (
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
)

func TestCompletedMergeRetryKeepsItsDurableSuffix(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	approval := f.review(t)
	f.ratify(t, approval)
	args := []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", f.candidate, "--approval", approval, "--text", "Land the approved feature."}
	if err := mergeCommand(f.ctx, args); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot(t)
	head := testGit(t, f.repo, "rev-parse", "HEAD")
	actor, private, err := f.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	plan := mergeplan.Build(f.ctx, f.workspace, f.repo, f.candidate, approval,
		actor.Fingerprint, mergeplan.Signer{Name: "operator", Private: private})
	if !plan.Allowed || plan.Mode != "complete" {
		t.Errorf("completed merge plan still claims remaining work: %+v", plan)
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbSupersede,
		Target: f.ground, Text: "The historical basis moved after delivery.",
		RestsOn: []string{f.ground}, IdempotencyKey: "after-delivery-ground"}); err != nil {
		t.Fatal(err)
	}
	before = f.snapshot(t)
	if err := mergeCommand(f.ctx, args); err != nil {
		t.Fatalf("completed retry refused: %v", err)
	}
	after := f.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth || testGit(t, f.repo, "rev-parse", "HEAD") != head {
		t.Fatal("completed retry changed Git or the durable suffix")
	}
}
