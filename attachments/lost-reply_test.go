package main
import("net/http";"net/http/httptest";"testing";"github.com/generalbusiness-ai/gitseq/internal/service")
func TestCodexLostReplyCanFollowAnAppend(t *testing.T) {
 repo:=newObservationRepo(t)
 resident,err:=service.NewObserved(repo.workspace,nopObserver{});if err!=nil{t.Fatal(err)}
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  recorded:=httptest.NewRecorder();resident.Handler().ServeHTTP(recorded,r)
  connection,_,err:=w.(http.Hijacker).Hijack();if err==nil{connection.Close()}
 }));defer server.Close()
 advertise(t,repo.workspace,server.URL);before:=snapshotOf(t,repo.workspace)
 event,err:=observeThrough(t,repo,"","lost-reply");after:=snapshotOf(t,repo.workspace)
 if err==nil || event!="" || after.Depth!=before.Depth+1 || after.Head==before.Head {t.Fatalf("event%q err%v depth%d -> %d",event,err,before.Depth,after.Depth)}
 t.Logf("resident appended one event before the reply was lost; client returned %v",err)
}
