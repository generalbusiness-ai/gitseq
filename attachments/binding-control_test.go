package main

import (
 "strings"
 "testing"
 "github.com/generalbusiness-ai/gitseq/internal/mergeplan"
)

func TestBindingNoArtifactMergeControl(t *testing.T) {
 for _, evidenceOnly := range []bool{true,false} {
  name:="assigned"
  if evidenceOnly {name="evidence-only"}
  t.Run(name,func(t *testing.T){
   f:=newWorkflowFixture(t)
   body:=map[string]string{"target_repo":mergeplan.WorkroomRepo(f.workspace),"target_ref":"refs/heads/main","target_head":testGit(t,f.repo,"rev-parse","HEAD")}
   if evidenceOnly {body=map[string]string{"no_git_artifact":"true"}}
   lane:=f.buildLandingLane(t,"binding-control",body)
   matches:=0
   snapshot:=f.snapshot(t)
   for _, c:=range snapshot.Projection.Commitments {if c.Request==lane.request&&c.Report!=""{matches++}}
   if evidenceOnly&&matches!=0||!evidenceOnly&&matches!=1{t.Fatalf("unexpected report matches %d",matches)}
   testGit(t,f.repo,"checkout","-qb","wrong-destination")
   before:=testGit(t,f.repo,"rev-parse","HEAD")
   depth:=f.snapshot(t).Depth
   err:=f.mergeLane(t,lane)
   after:=testGit(t,f.repo,"rev-parse","HEAD")
   if evidenceOnly {
    if err!=nil {t.Fatalf("expected observed escape, got %v",err)}
    if after==before||f.snapshot(t).Depth<=depth {t.Fatal("escape did not move Git and workroom")}
    testGit(t,f.repo,"merge-base","--is-ancestor",lane.candidate,after)
    t.Logf("REPRODUCED: no_git_artifact artifact has zero report matches; guarded approval and ratification allowed merge without authorization into wrong-destination, Git and sequence advanced")
   } else {
    if err==nil||!strings.Contains(err.Error(),"refs/heads/main"){t.Fatalf("ordinary assigned wrong-target control: %v",err)}
    if after!=before||f.snapshot(t).Depth!=depth{t.Fatal("valid refusal mutated Git/workroom")}
    t.Log("CONTROL: actual assigned report refused wrong destination before Git/workroom mutation")
   }
  })
 }
}
