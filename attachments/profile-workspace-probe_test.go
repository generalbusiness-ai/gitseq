package app
import (
 "context"
 "testing"
 "sync"
 "time"
 "github.com/generalbusiness-ai/gitseq/internal/apphost"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)
func TestCodexProfileRebuildProgressBoundary(t *testing.T) {
 ctx:=context.Background();w,_,err:=Init(ctx,testRepo(t),"human",1<<20);if err!=nil {t.Fatal(err)}
 before,err:=w.Snapshot(ctx);if err!=nil {t.Fatal(err)}
 w.selected=selection{host:host{application:apphost.DefaultApplication,foldVersion:workroom.ProfileVersion+"-probe-change",newFolder:workroom.NewFolder}}
 entered,release:=make(chan struct{}),make(chan struct{});var once sync.Once; defer once.Do(func(){close(release)})
 w.SetProjectionRebuildTestGate(func(int){close(entered);<-release})
 result:=make(chan error,1);go func(){_,err:=w.Snapshot(ctx);result<-err}()
 select{case <-entered:case <-time.After(5*time.Second):t.Fatal("profile rebuild did not reach publication gate")}
 progress,running:=w.RebuildProgress()
 t.Logf("profile rebuild is paused before publication at original frontier %s; progress=%+v running=%v",before.Head,progress,running)
 select{case err:=<-result:t.Fatalf("snapshot returned before profile publication: %v",err);default:}
 once.Do(func(){close(release)});select{case err:=<-result:if err!=nil {t.Fatal(err)};case <-time.After(5*time.Second):t.Fatal("profile snapshot did not finish after release")}
}
