package app
import (
 "context"
 "strings"
 "testing"
)
func TestCodexUnavailableInterpreterReportsEmptyProfile(t *testing.T) {
 ctx:=context.Background();repo:=testRepo(t)
 original,_,err:=initHosted(ctx,repo,"human",1<<20,testHost());if err!=nil {t.Fatal(err)}
 old,err:=original.Snapshot(ctx);if err!=nil {t.Fatal(err)}
 if original.Profile()=="" {t.Fatal("initial interpreter missing")}
 reopened,err:=Open(ctx,repo);if err!=nil {t.Fatal(err)}
 verified,err:=reopened.Verify(ctx);if err!=nil {t.Fatal(err)}
 if verified.Head!=old.Head {t.Fatal("fixture changed the log")}
 if _,err:=reopened.Snapshot(ctx);err==nil || !strings.Contains(err.Error(),"uninterpretable") {t.Fatalf("expected interpretation refusal: %v",err)}
 _,running:=reopened.RebuildProgress()
 t.Logf("actual reopened workspace: previous profile=%q current profile=%q running=%v same verified head=%s",original.Profile(),reopened.Profile(),running,verified.Head)
 if reopened.Profile()!="" || running {t.Fatal("expected empty profile and no running cold audit")}
}
