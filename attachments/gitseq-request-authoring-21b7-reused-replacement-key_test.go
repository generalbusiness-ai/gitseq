package main
import (
 "testing"
 "strings"
 "github.com/generalbusiness-ai/gitseq/internal/app"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)
func TestPlannerReusedReplacementKeyDoesNotBypassPreflight(t *testing.T) {
 f := newAuthoringFixture(t)
 if _,_,err:=f.workspace.AddActor(f.ctx,"operator","second","agent");err!=nil {t.Fatal(err)}
 prior,err:=f.file("earlier-original","earlier task",map[string]string{"to":"@agent","conditions":"finish earlier","no_git_artifact":"true"});if err!=nil {t.Fatal(err)}
 retired,err:=f.workspace.Act(f.ctx,"operator",app.Act{Verb:app.VerbRetireIfUnclaimed,Target:prior,Text:"earlier guarded retirement",IdempotencyKey:"earlier-retirement"});if err!=nil {t.Fatal(err)}
 replacement,err:=f.workspace.Act(f.ctx,"operator",app.Act{Verb:app.VerbReassignIfUnclaimed,Target:prior,Retirement:retired.Record.ID,Text:"earlier replacement",Body:map[string]string{"to":"@second","conditions":"finish earlier","target_ref":"refs/heads/main"},IdempotencyKey:"collision/request"});if err!=nil {t.Fatal(err)}
 snapshot,err:=f.workspace.Snapshot(f.ctx);if err!=nil {t.Fatal(err)}
 decision,ok:=snapshot.Projection.Decision(replacement.Record.ID);if !ok||decision.Verdict!=workroom.Effective {t.Fatalf("seed replacement is not effective: %+v",decision)}
 old,err:=f.file("new-original","new task that must survive",map[string]string{"to":"@agent","conditions":"finish current","no_git_artifact":"true"});if err!=nil {t.Fatal(err)}
 before:=f.frontier()
 err=reassignIfUnclaimedCommand(f.ctx,[]string{"--repo",f.repo,"--as","operator","--to","@second","--text","new replacement","--conditions","finish current","--body","target_ref=refs/heads/absent","--idempotency-key","collision",old})
 if err==nil {t.Fatal("invalid new destination and reused replacement key accepted")}
 if !strings.Contains(err.Error(),"does not resolve")&&!strings.Contains(err.Error(),"idempotency") {t.Fatalf("unexpected refusal: %v",err)}
 t.Logf("refused: %v; original status=%s, retired=%v",err,f.commitment(old).Status,f.statement(old).Retired)
 if after:=f.frontier();after!=before||f.statement(old).Retired {t.Fatalf("replacement-key collision bypassed preflight and dropped original: frontier %s -> %s, retired=%v",before,after,f.statement(old).Retired)}
}
