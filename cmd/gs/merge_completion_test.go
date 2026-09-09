package main

import (
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
)

// Exercise the read-only recovery entry itself: mergeCommand has separate
// destination checks and cannot prove that a preview refuses the same receipt.
func TestMergePlanRecoveryRefusesAnInvalidDestination(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"wrong repository", "wrong ref", "missing repository", "missing ref", "detached checkout"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newWorkflowFixture(t)
			approval := f.review(t)
			f.ratify(t, approval)
			target := mergeplan.Target{Repo: mergeplan.WorkroomRepo(f.workspace), Ref: "refs/heads/main",
				PreHead: testGit(t, f.repo, "rev-parse", "HEAD")}
			head := sealReceipt(t, f, approval, target, false)
			message := testGit(t, f.repo, "show", "-s", "--format=%B", head)
			want := "checkout destination does not match the sealed merge receipt"
			switch name {
			case "wrong repository":
				message = strings.Replace(message, mergeplan.TargetRepoTrailer+target.Repo,
					mergeplan.TargetRepoTrailer+"git:sha1:"+strings.Repeat("b", 40), 1)
			case "wrong ref":
				message = strings.Replace(message, mergeplan.TargetRefTrailer+target.Ref,
					mergeplan.TargetRefTrailer+"refs/heads/release-2", 1)
			case "missing repository":
				message = strings.Replace(message, "\n"+mergeplan.TargetRepoTrailer+target.Repo, "", 1)
				want = "merge receipt must carry target repository and ref together"
			case "missing ref":
				message = strings.Replace(message, "\n"+mergeplan.TargetRefTrailer+target.Ref, "", 1)
				want = "merge receipt must carry target repository and ref together"
			case "detached checkout":
				testGit(t, f.repo, "checkout", "--detach", head)
				want = "detached"
			}
			if name != "detached checkout" {
				testGit(t, f.repo, "commit", "--amend", "-m", message)
				amended := testGit(t, f.repo, "rev-parse", "HEAD")
				testGit(t, f.repo, "update-ref", mergeReceiptRef(approval), amended, head)
				head = amended
			}
			before := f.snapshot(t)
			actor, private, err := f.workspace.Actor("operator")
			if err != nil {
				t.Fatal(err)
			}
			plan := mergeplan.Build(f.ctx, f.workspace, f.repo, f.candidate, approval,
				actor.Fingerprint, mergeplan.Signer{Name: "operator", Private: private})
			if plan.Allowed {
				t.Fatalf("invalid destination preview allowed: %+v", plan)
			}
			found := false
			for _, reason := range plan.Reasons {
				if !reason.Allowed && reason.Check == "target" && strings.Contains(reason.Reason, want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("preview did not refuse at target boundary %q: %+v", want, plan)
			}
			if after := f.snapshot(t); after.Head != before.Head || after.Depth != before.Depth || testGit(t, f.repo, "rev-parse", "HEAD") != head {
				t.Fatal("refused recovery preview changed Git or the durable log")
			}
		})
	}
}

func TestMergeRecoveryRefusesUnrepresentableSuccession(t *testing.T) {
	t.Parallel()
	for _, trailer := range []string{mergeplan.AuthorizationTrailer, mergeplan.AuthorizationRatificationTrailer} {
		t.Run(strings.TrimSpace(trailer), func(t *testing.T) {
			t.Parallel()
			f := newWorkflowFixture(t)
			approval := f.review(t)
			f.ratify(t, approval)
			target := mergeplan.Target{Repo: mergeplan.WorkroomRepo(f.workspace), Ref: "refs/heads/main",
				PreHead: testGit(t, f.repo, "rev-parse", "HEAD")}
			head := sealReceipt(t, f, approval, target, false)
			message := testGit(t, f.repo, "show", "-s", "--format=%B", head)
			// A half authorization pair cannot be represented as durable acts.
			testGit(t, f.repo, "commit", "--amend", "-m", message+"\n"+trailer+approval)
			amended := testGit(t, f.repo, "rev-parse", "HEAD")
			testGit(t, f.repo, "update-ref", mergeReceiptRef(approval), amended, head)
			receipt, found, err := readMergeReceipt(f.ctx, f.repo, amended)
			if err != nil || !found {
				t.Fatalf("read sealed fixture: found=%v err=%v", found, err)
			}
			before := f.snapshot(t)
			actor, private, err := f.workspace.Actor("operator")
			if err != nil {
				t.Fatal(err)
			}
			const want = "sealed merge succession is not representable"
			plan := mergeplan.Build(f.ctx, f.workspace, f.repo, f.candidate, approval,
				actor.Fingerprint, mergeplan.Signer{Name: "operator", Private: private})
			refused := false
			for _, reason := range plan.Reasons {
				if !reason.Allowed && reason.Check == "succession" && reason.Reason == want {
					refused = true
				}
			}
			if plan.Allowed || !refused {
				t.Errorf("unrepresentable recovery preview: %+v", plan)
			}
			if err := recordMergeSuccession(f.ctx, f.workspace, f.repo, "", "operator", private, receipt); err == nil || err.Error() != want {
				t.Errorf("unrepresentable succession recording: %v, want %q", err, want)
			}
			if after := f.snapshot(t); after.Head != before.Head || after.Depth != before.Depth || testGit(t, f.repo, "rev-parse", "HEAD") != amended {
				t.Fatal("refused recovery changed Git or the durable log")
			}
		})
	}
}

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
