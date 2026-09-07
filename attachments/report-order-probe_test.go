package reviewguard
import ("testing";"fmt";"strings"; w "github.com/generalbusiness-ai/gitseq/internal/workroom")
func TestPlannerBehaviorFirstReportBinding(t *testing.T) {
 const room="git:sha1:1111111111111111111111111111111111111111"
 id:=func(n int)string{return room+fmt.Sprintf("#git:sha1:%040x",n)}
 candidate:=strings.Repeat("a",40)
 for _,directPromise:=range []bool{false,true}{t.Run(fmt.Sprint(directPromise),func(t *testing.T){
 var records []w.Record
 add:=func(n int, actor,schema string,payload any,bases ...string){ b,e:=w.Encode(payload);if e!=nil{t.Fatal(e)};records=append(records,w.Record{ID:id(n),Actor:actor,Schema:schema,Payload:b,RestsOn:bases}) }
 add(1,"owner",w.SchemaState,w.State{Kind:w.KindRoster,Text:"owner",Body:map[string]string{"actor":"owner","name":"Owner","role":"operator"}})
 add(2,"owner",w.SchemaState,w.State{Kind:w.KindRoster,Text:"worker",Body:map[string]string{"actor":"worker","name":"Worker","role":"agent"}},id(1))
 add(3,"owner",w.SchemaRatify,w.Ratify{Target:id(2)},id(2))
 add(4,"owner",w.SchemaStateV3,w.State{Kind:w.KindRequest,Text:"deliver code and docs",Body:map[string]string{"to":"worker","conditions":"reviewed delivery","target_repo":room,"target_ref":"refs/heads/main","target_head":candidate}},id(1))
 add(5,"worker",w.SchemaState,w.State{Kind:w.KindPromise,Text:"I will deliver"},id(4))
 add(6,"worker",w.SchemaState,w.State{Kind:w.KindArtifact,Text:"behavior and complete delivery account",Body:map[string]string{"path":"engine.go","commit":candidate}},id(5))
 bases:=[]string{id(6)};if directPromise{bases=append(bases,id(5))}
 add(7,"worker",w.SchemaState,w.State{Kind:w.KindArtifact,Text:"description of engine behavior",Body:map[string]string{"path":"docs/engine.md","commit":candidate}},bases...)
 p:=w.Fold(records)
 for _,d:=range p.Decisions{if d.Verdict!=w.Effective{t.Fatalf("ineffective setup: %+v",d)}}
 if len(p.Commitments)!=1{t.Fatalf("commitments=%+v",p.Commitments)}
 got:=p.Commitments[0].Report;want:=id(6);if directPromise{want=id(7)};if got!=want{t.Fatalf("Report=%s want %s",got,want)}
 b,e:=Resolve(p,Scope{Candidate:candidate,Examined:[]string{id(6),id(7)}})
 if directPromise{if e==nil{t.Fatal("superseded primary unexpectedly bound")}}else{if e!=nil{t.Fatal(e)};if len(b.Implementations)!=1||b.Implementations[0].Report!=id(6){t.Fatalf("binding=%+v",b)}}
 t.Logf("doc_direct_promise=%v selected_report=%s primary_binding_error=%v document_behavior_basis=%s",directPromise,got,e,p.Provenance[id(7)][0])
 })}
}
