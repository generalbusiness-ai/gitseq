package main
import "testing"
func TestPlannerRetiredReplacementAddresseeRefusesBeforeRetirement(t *testing.T) {
 f:=newAuthoringFixture(t)
 if _,_,err:=f.workspace.AddActor(f.ctx,"operator","second","agent");err!=nil {t.Fatal(err)}
 if _,err:=f.workspace.RetireActor(f.ctx,"operator","@second");err!=nil {t.Fatal(err)}
 old,err:=f.file("live-task","task must survive",map[string]string{"to":"@agent","conditions":"finish","no_git_artifact":"true"});if err!=nil {t.Fatal(err)}
 before:=f.frontier()
 err=reassignIfUnclaimedCommand(f.ctx,[]string{"--repo",f.repo,"--as","operator","--to","@second","--text","replacement","--conditions","finish","--body","no_git_artifact=true","--idempotency-key","retired-address",old})
 snapshot,e:=f.workspace.Snapshot(f.ctx);if e!=nil {t.Fatal(e)}; for _,d:=range snapshot.Projection.Decisions {if d.Sequence>4 {t.Logf("decision=%+v",d)}}; for _,a:=range snapshot.Projection.Actors {t.Logf("actor name=%s retired=%v",a.Name,a.Retired)}; for _,c:=range snapshot.Projection.Commitments {t.Logf("commitment=%+v",c)}
 t.Logf("refusal=%v, original=%s retired=%v",err,f.commitment(old).Status,f.statement(old).Retired)
 if f.frontier()!=before||f.statement(old).Retired {t.Fatal("known retired addressee bypassed preflight and retired the original with no successor")}
}
