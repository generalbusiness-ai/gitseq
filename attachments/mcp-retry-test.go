package main
import (
 "testing"
 "context"
 "strings"
 "github.com/generalbusiness-ai/gitseq/internal/app"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)
func TestPlannerMCPAuthorizationExactRetryAfterTargetMoves(t *testing.T) {
 ctx:=context.Background()
 w,genesis:=signedWorkspace(t,1)
 server:=newServer("human",w.Repo)
 commit:=func()string{mergePlanTestGit(t,w.Repo,"-c","user.name=Test","-c","user.email=test@example.invalid","commit","--allow-empty","-qm","source");return mergePlanTestGit(t,w.Repo,"rev-parse","HEAD")}
 base:=commit();ref:=mergePlanTestGit(t,w.Repo,"symbolic-ref","HEAD")
 req,err:=w.Act(ctx,"human",app.Act{Verb:app.VerbState,Kind:workroom.KindRequest,Text:"report authorization",Body:map[string]string{"to":"human","conditions":"report authorization"},RestsOn:[]string{genesis.ID},IdempotencyKey:"mcp-retry-request"})
 if err!=nil {t.Fatal(err)}
 call:=func(key string)error{_,_,err:=server.call(ctx,toolCall{Name:"state",Arguments:map[string]any{"kind":"report","text":"release the hold","body":map[string]any{"authorizes_request":req.Record.ID,"target_ref":ref,"target_pre_head":base},"rests_on":[]any{req.Record.ID},"idempotency_key":key}});return err}
 if err:=call("accepted-key");err!=nil{t.Fatalf("first submission: %v",err)}
 before,err:=w.Snapshot(ctx);if err!=nil{t.Fatal(err)}
 commit()
 if err:=call("fresh-key");err==nil||!strings.Contains(err.Error(),"target_pre_head"){t.Fatalf("fresh stale report must refuse: %v",err)}
 if err:=call("accepted-key");err!=nil{t.Fatalf("exact accepted MCP retry rejudged against moved target: %v",err)}
 after,err:=w.Snapshot(ctx);if err!=nil{t.Fatal(err)}
 if after.Depth!=before.Depth||after.Head!=before.Head{t.Fatal("retry appended")}
}
