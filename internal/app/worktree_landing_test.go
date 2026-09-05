package app

import (
	"context"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestWorktreeClassificationProtectsEveryNamedHead(t *testing.T) {
	repo := testRepo(t)
	commit := func() string {
		landingTestGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "source")
		return landingTestGit(t, repo, "rev-parse", "HEAD")
	}
	seed := commit()
	landingTestGit(t, repo, "checkout", "-qb", "request/feature")
	candidate := commit()
	landingTestGit(t, repo, "update-ref", "refs/heads/main", candidate)
	w, _, err := Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	repository := "git:" + w.config.ObjectFormat + ":" + w.config.Genesis
	p := workroom.Projection{Commitments: []workroom.Commitment{{Request: "request", Promise: "promise", Performer: "performer", Status: "satisfied", TargetRepo: repository, TargetRef: "refs/heads/main", Candidate: candidate, Approval: "approval", Terminal: "landed", LandingReceipt: "receipt"}}, Statements: []workroom.Statement{{Event: "receipt", Body: map[string]string{"merge_head": candidate, "merge_target_repo": repository, "merge_target_ref": "refs/heads/main"}}}}
	head := candidate
	classify := func() ([]string, WorktreeView) {
		views := []WorktreeView{{Checkout: "feature", Branch: "request/feature", Head: head, State: "clean"}}
		deletable := w.ClassifyWorktrees(context.Background(), p, views)
		return deletable, views[0]
	}
	if deletable, view := classify(); len(deletable) != 1 || view.Approved != "approval" || view.LandedInto != "refs/heads/main" {
		t.Fatalf("landed clean branch not classified: %v %+v", deletable, view)
	}
	p.Commitments[0].ApprovedNotLanded = true
	if deletable, view := classify(); len(deletable) != 0 || view.Classification != "protected" {
		t.Fatalf("legacy satisfied approval debt lost: %v %+v", deletable, view)
	}
	p.Commitments[0].ApprovedNotLanded = false
	// A different unfinished commitment names an ancestor, not the branch tip.
	p.Commitments = append(p.Commitments, workroom.Commitment{Request: "older-flight", Status: "awaiting-review", Candidate: seed})
	if deletable, view := classify(); len(deletable) != 0 || view.Classification != "protected" || view.Row != "older-flight" {
		t.Fatalf("ancestor's unsettled obligation lost: %v %+v", deletable, view)
	}
	p.Commitments = p.Commitments[:1]
	head = commit()
	// The endpoint may still have a cached worktree listing at the old tip.
	newHead := head
	head = candidate
	if deletable, view := classify(); len(deletable) != 0 || view.ClassificationReason != "current tip is not proved landed or explicitly abandoned" {
		t.Fatalf("unreported new tip deletable: %v %+v", deletable, view)
	} else if view.Head != newHead {
		t.Fatalf("cached worktree tip survived current ref observation: %+v", view)
	}
	head = candidate
	p.Commitments = append(p.Commitments, workroom.Commitment{Request: "missing", Status: "promised", Candidate: strings.Repeat("a", 40)})
	if deletable, view := classify(); len(deletable) != 0 || view.Classification != "unknown" {
		t.Fatalf("missing object shrank protection: %v %+v", deletable, view)
	}
	p.Commitments = p.Commitments[:1]
	landingTestGit(t, repo, "update-ref", "refs/heads/request/feature", candidate)
	p.Commitments[0].Status = "abandoned"
	p.Commitments[0].LandingReceipt = ""
	p.Commitments[0].Terminal = "abandoned"
	if deletable, view := classify(); len(deletable) != 1 {
		t.Fatalf("explicitly abandoned exact tip not deletable: %+v", view)
	}
	for _, view := range []WorktreeView{{Checkout: "current", Branch: "request/feature", Head: candidate, State: "clean", Current: true}, {Checkout: "dirty", Branch: "request/feature", Head: candidate, State: "dirty"}, {Checkout: "target", Branch: "main", Head: candidate, State: "clean"}} {
		views := []WorktreeView{view}
		if deletable := w.ClassifyWorktrees(context.Background(), p, views); len(deletable) != 0 {
			t.Fatalf("unsafe checkout listed: %+v", views)
		}
	}
}

func TestLandingEvidenceRequiresProjectionWitness(t *testing.T) {
	p := workroom.Projection{Statements: []workroom.Statement{{Event: "claimed", Body: map[string]string{"merge_head": strings.Repeat("a", 40), "merge_hold_warning": "true"}}, {Event: "released", Body: map[string]string{"merge_head": strings.Repeat("b", 40), "merge_target_repo": "repo", "merge_target_ref": "refs/heads/main", "merge_authorization": "release"}}, {Event: "legacy", Body: map[string]string{"merge_head": strings.Repeat("c", 40)}}}}
	rows := []LandingDetails{{}, {LandingReceipt: "claimed"}, {LandingReceipt: "released"}, {LandingReceipt: "legacy"}}
	var pointers []*LandingDetails
	for i := range rows {
		pointers = append(pointers, &rows[i])
	}
	FillLandingEvidence(p, pointers)
	if rows[0].MergeHead != "" || rows[0].MergeHoldWarning {
		t.Fatal("arbitrary assertion was discovered as a receipt")
	}
	if !rows[1].MergeHoldWarning || rows[1].LandingReceipt != "claimed" {
		t.Fatal("validated witness warning lost")
	}
	if rows[2].MergeHoldWarning || rows[2].ReceiptLegacy {
		t.Fatal("released receipt became compatibility warning")
	}
	if !rows[3].ReceiptLegacy || rows[3].MergeHoldWarning {
		t.Fatal("legacy absence became compatibility warning")
	}
}
