package main
import("testing";"strings";"path/filepath";"os";"github.com/generalbusiness-ai/gitseq/internal/app";"github.com/generalbusiness-ai/gitseq/internal/workroom";"github.com/generalbusiness-ai/gitseq/internal/mergeplan")
func TestPlannerSelectorCannotOmitHeldCompanion(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	body := map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")}
	first := f.stateV3(t, "reviewer", workroom.KindRequest, "implement first", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	second := f.stateV3(t, "reviewer", workroom.KindRequest, "implement second", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it too", "landing": "held", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	promises := map[string]string{}
	for name, request := range map[string]string{"first": first, "second": second} {
		promise, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name, RestsOn: []string{request}, IdempotencyKey: name + "-promise"})
		if err != nil {
			t.Fatal(err)
		}
		promises[name] = promise.Record.ID
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "combined")
	testGit(t, f.repo, "worktree", "add", "-qb", "combined", checkout)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", "combined")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifacts := map[string]string{}
	for _, name := range []string{"first", "second"} {
		artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact", Body: map[string]string{"path": name + ".txt", "commit": candidate}, RestsOn: []string{promises[name]}, IdempotencyKey: name + "-artifact"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact.Record.ID
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review combined", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifacts["first"]}, IdempotencyKey: "combined-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review combined", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "combined-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--promise", reviewPromise.Record.ID}
	if err := reviewCommand(f.ctx, append(base, "--artifact", artifacts["first"], "--artifact", artifacts["second"], "--implementation", first, "--verdict", "approved", "--text", "APPROVED first only")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-combined"}); err != nil {
		t.Fatal(err)
	}
	err = mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", candidate, "--approval", approval, "--text", "land first without held second release"})
 if err == nil { t.Fatal("SECURITY: selecting only first allowed a candidate with an examined held implementation to land without its release") }
 t.Logf("safe refusal: %v", err)
}

func TestPlannerAllExaminedHeldCompanionRefuses(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	body := map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")}
	first := f.stateV3(t, "reviewer", workroom.KindRequest, "implement first", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	second := f.stateV3(t, "reviewer", workroom.KindRequest, "implement second", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it too", "landing": "held", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	promises := map[string]string{}
	for name, request := range map[string]string{"first": first, "second": second} {
		promise, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name, RestsOn: []string{request}, IdempotencyKey: name + "-promise"})
		if err != nil {
			t.Fatal(err)
		}
		promises[name] = promise.Record.ID
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "combined")
	testGit(t, f.repo, "worktree", "add", "-qb", "combined", checkout)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", "combined")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifacts := map[string]string{}
	for _, name := range []string{"first", "second"} {
		artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact", Body: map[string]string{"path": name + ".txt", "commit": candidate}, RestsOn: []string{promises[name]}, IdempotencyKey: name + "-artifact"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact.Record.ID
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review combined", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifacts["first"]}, IdempotencyKey: "combined-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review combined", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "combined-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--promise", reviewPromise.Record.ID}
	if err := reviewCommand(f.ctx, append(base, "--artifact", artifacts["first"], "--artifact", artifacts["second"], "--verdict", "approved", "--text", "APPROVED first only")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-combined"}); err != nil {
		t.Fatal(err)
	}
	err = mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", candidate, "--approval", approval, "--text", "land first without held second release"})
 if err == nil { t.Fatal("SECURITY: selecting only first allowed a candidate with an examined held implementation to land without its release") }
 t.Logf("safe refusal: %v", err)
}

func TestPlannerSelectorCannotOmitWrongTargetCompanion(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	body := map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")}
	first := f.stateV3(t, "reviewer", workroom.KindRequest, "implement first", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	second := f.stateV3(t, "reviewer", workroom.KindRequest, "implement second", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it too", "target_repo": body["target_repo"], "target_ref": "refs/heads/other", "target_head": body["target_head"]}, f.ground)
	promises := map[string]string{}
	for name, request := range map[string]string{"first": first, "second": second} {
		promise, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name, RestsOn: []string{request}, IdempotencyKey: name + "-promise"})
		if err != nil {
			t.Fatal(err)
		}
		promises[name] = promise.Record.ID
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "combined")
	testGit(t, f.repo, "worktree", "add", "-qb", "combined", checkout)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", "combined")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifacts := map[string]string{}
	for _, name := range []string{"first", "second"} {
		artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact", Body: map[string]string{"path": name + ".txt", "commit": candidate}, RestsOn: []string{promises[name]}, IdempotencyKey: name + "-artifact"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact.Record.ID
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review combined", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifacts["first"]}, IdempotencyKey: "combined-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review combined", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "combined-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--promise", reviewPromise.Record.ID}
	if err := reviewCommand(f.ctx, append(base, "--artifact", artifacts["first"], "--artifact", artifacts["second"], "--implementation", first, "--verdict", "approved", "--text", "APPROVED first only")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-combined"}); err != nil {
		t.Fatal(err)
	}
	err = mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", candidate, "--approval", approval, "--text", "land first without held second release"})
 if err == nil { t.Fatal("SECURITY: selecting only first allowed a candidate with an examined held implementation to land without its release") }
 t.Logf("safe refusal: %v", err)
}

func TestPlannerAllExaminedWrongTargetCompanionRefuses(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	body := map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")}
	first := f.stateV3(t, "reviewer", workroom.KindRequest, "implement first", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	second := f.stateV3(t, "reviewer", workroom.KindRequest, "implement second", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it too", "target_repo": body["target_repo"], "target_ref": "refs/heads/other", "target_head": body["target_head"]}, f.ground)
	promises := map[string]string{}
	for name, request := range map[string]string{"first": first, "second": second} {
		promise, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name, RestsOn: []string{request}, IdempotencyKey: name + "-promise"})
		if err != nil {
			t.Fatal(err)
		}
		promises[name] = promise.Record.ID
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "combined")
	testGit(t, f.repo, "worktree", "add", "-qb", "combined", checkout)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", "combined")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifacts := map[string]string{}
	for _, name := range []string{"first", "second"} {
		artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact", Body: map[string]string{"path": name + ".txt", "commit": candidate}, RestsOn: []string{promises[name]}, IdempotencyKey: name + "-artifact"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact.Record.ID
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review combined", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifacts["first"]}, IdempotencyKey: "combined-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review combined", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "combined-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--promise", reviewPromise.Record.ID}
 err = reviewCommand(f.ctx, append(base, "--artifact", artifacts["first"], "--artifact", artifacts["second"], "--verdict", "approved", "--text", "APPROVED both"))
 if err == nil || !strings.Contains(err.Error(), "different targets") { t.Fatalf("expected different-target refusal, got %v", err) }
}
