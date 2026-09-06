package main
import "testing"
func TestPlannerChangedRequestDestinationIsDifferentIntent(t *testing.T) {
 f:=newAuthoringFixture(t)
 b:=map[string]string{"to":"@agent","conditions":"land it","target_ref":"refs/heads/main"}
 first,err:=f.file("planner-change-ref","same request",b);if err!=nil {t.Fatal(err)}
 before:=f.frontier()
 testGit(t,f.repo,"update-ref","refs/heads/other",f.head())
 b["target_ref"]="refs/heads/other"
 got,err:=f.file("planner-change-ref","same request",b)
 if err==nil {t.Errorf("changed destination silently replayed: first=%s returned=%s wanted refs/heads/other, accepted body=%v",first,got,f.body(first))}
 if f.frontier()!=before {t.Fatal("different-intent refusal appended")}
}
func TestPlannerAcceptedRequestRetrySurvivesDeletedRef(t *testing.T) {
 f:=newAuthoringFixture(t)
 b:=map[string]string{"to":"@agent","conditions":"land it","target_ref":"refs/heads/main"}
 first,err:=f.file("planner-delete-ref","accepted request",b);if err!=nil {t.Fatal(err)}
 before:=f.frontier()
 testGit(t,f.repo,"update-ref","-d","refs/heads/main")
 got,err:=f.file("planner-delete-ref","accepted request",b)
 if err!=nil || got!=first {t.Errorf("exact accepted retry refused after ref deletion: got=%s want=%s err=%v",got,first,err)}
 if f.frontier()!=before {t.Fatal("accepted retry appended")}
 if _,err=f.file("planner-fresh-missing-ref","fresh request",b);err==nil {t.Error("fresh missing-ref request accepted")}
 if f.frontier()!=before {t.Fatal("fresh refusal appended")}
}
func TestPlannerExplicitEmptyHoldOwnerRefuses(t *testing.T) {
 f:=newAuthoringFixture(t)
 before:=f.frontier()
 b:=map[string]string{"to":"@agent","conditions":"land it","target_ref":"refs/heads/main","landing":"held","hold_owner":""}
 got,err:=f.file("planner-empty-owner","empty owner",b)
 if err==nil {t.Errorf("explicit empty hold owner accepted with fallback authority: event=%s row=%+v",got,f.commitment(got))}
 if f.frontier()!=before {t.Error("empty-owner refusal appended")}
}
