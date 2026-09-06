package main

import (
 "testing"
 "github.com/generalbusiness-ai/gitseq/internal/app"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPlannerExactPromiseAfterWithdrawalCanFileReview(t *testing.T) {
 f:=newWorkflowFixture(t)
 lane:=buildCombinedDelivery(t,f,nil)
 var oldPromise string
 for _, c:=range f.snapshot(t).Projection.Commitments {if c.Request==lane.first {oldPromise=c.Promise}}
 if oldPromise=="" {t.Fatal("missing original promise")}
 if _,err:=f.workspace.Act(f.ctx,"operator",app.Act{Verb:app.VerbSupersede,Target:oldPromise,Text:"withdraw first attempt",RestsOn:[]string{oldPromise},IdempotencyKey:"withdraw-first"});err!=nil{t.Fatal(err)}
 current,err:=f.workspace.Act(f.ctx,"operator",app.Act{Verb:app.VerbState,Kind:workroom.KindPromise,Text:"resume same request",RestsOn:[]string{lane.first},IdempotencyKey:"resume-first"});if err!=nil{t.Fatal(err)}
 artifact,err:=f.workspace.Act(f.ctx,"operator",app.Act{Verb:app.VerbState,Kind:workroom.KindArtifact,Text:"current first report",Body:map[string]string{"path":"first.txt","commit":lane.candidate},RestsOn:[]string{current.Record.ID},IdempotencyKey:"current-first-report"});if err!=nil{t.Fatal(err)}
 request,err:=f.workspace.Act(f.ctx,"operator",app.Act{Verb:app.VerbState,Kind:workroom.KindRequest,Text:"review resumed delivery",Body:map[string]string{"to":f.fingerprint(t,"reviewer"),"conditions":"exact head","no_git_artifact":"true"},RestsOn:[]string{artifact.Record.ID},IdempotencyKey:"review-resumed"});if err!=nil{t.Fatal(err)}
 promise,err:=f.workspace.Act(f.ctx,"reviewer",app.Act{Verb:app.VerbState,Kind:workroom.KindPromise,Text:"review resumed",RestsOn:[]string{request.Record.ID},IdempotencyKey:"review-resumed-promise"});if err!=nil{t.Fatal(err)}
 n:=0
 for _, c:=range f.snapshot(t).Projection.Commitments {if c.Request==lane.first {n++;t.Logf("actual lifecycle: promise=%s report=%s status=%s",c.Promise,c.Report,c.Status)}}
 if n!=2{t.Fatalf("need two actual lifecycles, got%d",n)}
 args:=append([]string{},lane.base...)
 // Replace the original review promise with the new review's promise.
 args[len(args)-1]=promise.Record.ID
 args=append(args,"--artifact",artifact.Record.ID,"--implementation",current.Record.ID)
 if err:=reviewCommand(f.ctx,append(append([]string{},args...),"--prepare"));err!=nil{t.Fatalf("exact promise prepare refused: %v",err)}
 before:=f.snapshot(t).Depth
 if err:=reviewCommand(f.ctx,append(args,"--verdict","approved","--text","APPROVED resumed delivery"));err!=nil{t.Fatalf("prepare passed but filing lost exact promise (depth %d -> %d): %v",before,f.snapshot(t).Depth,err)}
}
