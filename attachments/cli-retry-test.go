package main
import (
 "testing"
 "strings"
)
func TestPlannerAuthorizationExactRetryAfterTargetMoves(t *testing.T) {
 f := newWorkflowFixture(t)
 base := testGit(t, f.repo, "rev-parse", "HEAD")
 req := f.ownRequest(t, "planner-retry-request", "measure authorization")
 body := map[string]string{"authorizes_request":f.implementationRequest,"target_ref":"refs/heads/main","target_pre_head":base}
 if err := fileAuthorizationReport(t,f,req,"planner-exact-key",body); err != nil { t.Fatalf("first submission: %v",err) }
 before := f.snapshot(t)
 testGit(t,f.repo,"commit","--allow-empty","-qm","target advances after accepted report")
 if err := fileAuthorizationReport(t,f,req,"planner-new-key",body); err == nil || !strings.Contains(err.Error(),"target_pre_head") {t.Fatalf("fresh stale report must refuse: %v",err)}
 if after:=f.snapshot(t); after.Depth!=before.Depth || after.Head!=before.Head {t.Fatal("fresh refusal appended")}
 if err := fileAuthorizationReport(t,f,req,"planner-exact-key",body); err != nil {t.Fatalf("exact accepted retry rejudged against moved target: %v",err)}
 if after:=f.snapshot(t); after.Depth!=before.Depth || after.Head!=before.Head {t.Fatal("exact retry appended instead of replaying")}
}
