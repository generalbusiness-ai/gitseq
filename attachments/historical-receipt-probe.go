package main

import ("testing"; "strings"; "github.com/generalbusiness-ai/gitseq/internal/mergeplan")

func TestPlannerHistoricalWiderReceiptResume(t *testing.T) {
	t.Parallel()
	f, candidate, approval, wider, nested := buildNestedCrossAuthorApproval(t)
	targetPreHead := testGit(t, f.repo, "rev-parse", "HEAD")
	changes, err := mergeChangesBetween(f.ctx, f.repo, targetPreHead, candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	_ = changes
	// Explicit historical shape from the originally approved ca5b378f test.
	// It deliberately differs from today's fresh succession plan.
	sealed := mergeplan.Succession{Publish: []string{"docs", "feature.txt"}, Retire: map[string]string{wider: "docs", nested: "docs"}}
	message, err := mergeReceiptMessage("Merge the approved nested guide.", approval, "", "", candidate, mergeTestTarget(f.workspace, targetPreHead), "", false, sealed)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(message, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "Gitseq-Left-Live:") || strings.HasPrefix(line, "Gitseq-Changed-Paths:") { continue }
		kept = append(kept, line)
	}
	message = strings.Join(kept, "\n")
	testGit(t, f.repo, "merge", "--no-ff", "-m", message, candidate)
	mergeHead := testGit(t, f.repo, "rev-parse", "HEAD")
	testGit(t, f.repo, "update-ref", mergeReceiptRef(approval), mergeHead, "")

	// What was sealed really does sit outside today's prospective reach, so
	// only the fold's unchanged authority can carry it.
	if err := mergeplan.ValidateReach(snapshot.Projection, sealed, approval,
		f.workspace.View().Actors["operator"].Fingerprint); err == nil || !strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("historical plan must be refused by fresh reach: %v", err)
	}

	before := f.snapshot(t).Depth
	if err := mergeCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", candidate, "--approval", approval,
		"--text", "Resume the sealed receipt without replanning or re-merging.",
	}); err != nil {
		t.Fatalf("resuming a receipt the unchanged fold authorized was refused: %v", err)
	}
	if got := testGit(t, f.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("resume re-merged or moved HEAD to %s, want %s", got, mergeHead)
	}
	after := f.snapshot(t)
	// One receipt assertion plus one durable act per publish and per
	// retirement in the sealed suffix.
	wantDepth := before + 1 + len(sealed.Publish) + len(sealed.Retire)
	if after.Depth != wantDepth {
		t.Fatalf("resume depth = %d, want %d: the sealed receipt plus its publish and retirement acts", after.Depth, wantDepth)
	}
	for target := range sealed.Retire {
		if !artifactByEvent(t, after.Projection, target).Retired {
			t.Fatalf("resume did not append the sealed retirement of %s", target)
		}
	}
	for _, path := range sealed.Publish {
		live := 0
		for _, artifact := range after.Projection.Artifacts {
			if artifact.Path == path && artifact.Commit == mergeHead && !artifact.Retired {
				live++
			}
		}
		if live != 1 {
			t.Fatalf("resume left %d live successors at %s, want one", live, path)
		}
	}
}
