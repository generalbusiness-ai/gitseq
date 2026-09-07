package service
import("context";"sync/atomic";"sync";"github.com/generalbusiness-ai/gitseq/internal/app";"testing";"time")
func TestCodexHeadWatchCancelledJoin(t *testing.T){
 entered:=make(chan struct{});unblock:=make(chan struct{});firstRelease:=make(chan func(),1);h:=newHeadWatch(func(context.Context)(string,error){close(entered);<-unblock;return "head",nil})
 go func(){firstRelease<-h.acquire(context.Background())}();<-entered
 ctx,cancel:=context.WithCancel(context.Background());cancel();secondDone:=make(chan struct{})
 go func(){release:=h.acquire(ctx);release();close(secondDone)}()
 select{case <-secondDone:case <-time.After(100*time.Millisecond):t.Error("cancelled joining waiter remains blocked on another waiter's baseline read")}
 close(unblock);release:=<-firstRelease;<-secondDone;release()
}
func TestCodexHeadWatchReadOutlivesLastReleaseAndOverlapsRestart(t *testing.T){
 var calls,active,maximum atomic.Int64;entered:=make(chan context.Context,1);unblock:=make(chan struct{})
 h:=newHeadWatch(func(ctx context.Context)(string,error){n:=calls.Add(1);a:=active.Add(1);defer active.Add(-1);for{old:=maximum.Load();if a<=old||maximum.CompareAndSwap(old,a){break}};if n==2{entered<-ctx;<-unblock;return "old",nil};return "new",nil});h.interval=time.Millisecond
 release:=h.acquire(context.Background());readctx:=<-entered;h.mu.Lock();stopped:=h.stopped;h.mu.Unlock();release()
 if readctx.Done()==nil||readctx.Err()==nil{t.Error("last release does not cancel in-flight background head read")}
 again:=h.acquire(context.Background());again();if maximum.Load()>1{t.Errorf("restart overlaps retired clock read: %d concurrent ref reads",maximum.Load())}
 close(unblock);<-stopped
}

func TestCodexColdSnapshotsSpendGitProcesses(t *testing.T){
 _,_,repo,_:=newHeadWatchFixture(t,1);counter:=&refCounter{};ctx:=context.Background();fresh,err:=app.OpenObserved(ctx,repo,counter);if err!=nil{t.Fatal(err)};counter.refs.Store(0);counter.all.Store(0);var wg sync.WaitGroup;start:=make(chan struct{});for i:=0;i<8;i++{wg.Add(1);go func(){defer wg.Done();<-start;if _,err:=fresh.Snapshot(ctx);err!=nil{t.Error(err)}}()};close(start);wg.Wait();t.Logf("actual cold snapshot processes: refs=%d all=%d",counter.refs.Load(),counter.all.Load());if counter.refs.Load()==0{t.Fatal("observer failed to measure")}
}
