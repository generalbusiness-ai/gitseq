package main
import "testing"
func TestPlannerInvalidReplacementLeavesOldAssignmentLive(t *testing.T) {
 f := newAuthoringFixture(t)
 if _, _, err := f.workspace.AddActor(f.ctx, "operator", "second", "agent"); err != nil { t.Fatal(err) }
 old, err := f.file("old-request", "Review the release", map[string]string{"to":"@agent", "conditions":"release is checked", "no_git_artifact":"true"})
 if err != nil { t.Fatal(err) }
 before := f.frontier()
 err = reassignIfUnclaimedCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--to", "@second", "--text", "Review the release", "--conditions", "release is checked", "--idempotency-key", "invalid-new-choice", old})
 if err == nil { t.Fatal("replacement lacking its result was accepted") }
 t.Logf("replacement refused: %v; original commitment: %+v", err, f.commitment(old))
 if after := f.frontier(); after != before { t.Fatalf("invalid replacement retired the original assignment: frontier changed from %s to %s", before, after) }
}
