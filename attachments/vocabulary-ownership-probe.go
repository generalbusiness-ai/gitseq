package app
import("bytes";"context";"encoding/json";"testing";"github.com/generalbusiness-ai/gitseq/internal/workroom")
func TestReviewAdmissionDoesNotInheritCallerVocabularyEdits(t *testing.T){
 ctx:=context.Background();fixture,_:=admissionWorkspace(t,ctx);w,err:=Open(ctx,fixture.Repo);if err!=nil{t.Fatal(err)}
 snapshot,err:=w.Snapshot(ctx);if err!=nil{t.Fatal(err)}
 changed:=false
 for _,definition:=range snapshot.Vocabulary.Definitions{for _,basis:=range definition.Basis{if len(basis.Kinds)>0{basis.Kinds[0]=workroom.Kind("caller-injected-kind");changed=true;break}};if changed{break}}
 if !changed{t.Fatal("inert control: no nested basis kind")}
 world,err:=w.admissionWorld(ctx,snapshot.Head);if err!=nil{t.Fatal(err)}
 cold,err:=w.ReadOnlySnapshot(ctx);if err!=nil{t.Fatal(err)}
 got,_:=json.Marshal(world.Vocabulary);want,_:=json.Marshal(cold.Vocabulary)
 if !bytes.Equal(got,want){t.Fatal("admission's supposedly private verified world inherited a caller's vocabulary edit")}
}
