package kernel
import (
 "strings"
 "testing"
 "github.com/generalbusiness-ai/gitseq/internal/intent"
 "github.com/generalbusiness-ai/gitseq/internal/gitstore"
)
func TestPlannerEmptyCausalTrailer(t *testing.T) {
 for _,extra:=range []bool{false,true} {t.Run(map[bool]string{false:"baseline",true:"empty-trailer"}[extra],func(t *testing.T){
 f:=newFixture(t,"sha1")
 r:=f.request(t,actor(t),"empty-ref-proof",[]byte("claim"),nil)
 tree,err:=f.store.WritePayloadTree(f.ctx,r.Payload,r.Attachments);if err!=nil{t.Fatal(err)}
 message:=intent.Envelope(r.Signed,nil)
 if extra {message=strings.Replace(message,"gitseq-event-v0\n","gitseq-event-v0\nRests-On: \n",1)}
 signed,refs,err:=intent.ParseEnvelope(message,1<<20);if err!=nil{t.Fatal(err)}
 decoded,err:=intent.Verify(signed);if err!=nil{t.Fatal(err)}
 t.Logf("signed refs=%#v trailers=%#v EqualRefs=%v",decoded.RestsOn,refs,intent.EqualRefs(decoded.RestsOn,refs))
 parent,err:=f.store.Head(f.ctx,Ref(f.genesis));if err!=nil{t.Fatal(err)}
 commit,err:=f.store.SignedCommit(f.ctx,tree,parent,message,f.signingKey,gitstore.CommitIdentity{AuthorName:"probe",AuthorEmail:"probe@example.invalid",CommitterName:"sequencer",CommitterEmail:"sequencer@example.invalid"});if err!=nil{t.Fatal(err)}
 stored,err:=f.store.CommitMessage(f.ctx,commit);if err!=nil{t.Fatal(err)}
 _,storedRefs,err:=intent.ParseEnvelope(stored,1<<20);if err!=nil{t.Fatal(err)}
 t.Logf("actual stored trailers=%#v",storedRefs)
 if err:=f.store.UpdateRef(f.ctx,Ref(f.genesis),commit,parent);err!=nil{t.Fatal(err)}
 result,err:=Verify(f.ctx,f.store,f.genesis)
 t.Logf("actual Verify depth=%d error=%v",result.Depth,err)
 if err==nil {
 first,loadErr:=NewReader(f.store,CheckpointOptions{Enabled:true,SigningKey:f.signingKey}).Load(f.ctx,f.genesis)
 t.Logf("checkpoint writer full=%v checkpoint=%v error=%v",first.Full,first.Checkpoint,loadErr)
 if loadErr!=nil{t.Fatal(loadErr)}
 cached,cacheErr:=NewReader(f.store,CheckpointOptions{Enabled:true}).Load(f.ctx,f.genesis)
 t.Logf("checkpoint reader full=%v checkpoint=%v error=%v",cached.Full,cached.Checkpoint,cacheErr)
 if cacheErr!=nil{t.Fatal(cacheErr)}
 if !cached.Checkpoint {t.Fatal("probe did not exercise checkpoint restore")}
 }
 if extra && err==nil {t.Fatal("verifier and checkpoint restore accepted an unsigned empty causal trailer")}
 if !extra && err!=nil {t.Fatal(err)}
 })}
}

func TestPlannerCheckpointEmptyCausalTrailer(t *testing.T) {
 f,_,_,stored:=checkpointState(t,1)
 sequence,err:=f.store.RevListMetadata(f.ctx,stored.Head);if err!=nil{t.Fatal(err)}
 desc,err:=Descriptor(f.ctx,f.store,f.genesis);if err!=nil{t.Fatal(err)}
 if _,err:=validateCheckpoint(stored,desc,sequence);err!=nil{t.Fatal(err)}
 sequence[1].Message=strings.Replace(sequence[1].Message,"gitseq-event-v0\n","gitseq-event-v0\nRests-On: \n",1)
 _,err=validateCheckpoint(stored,desc,sequence)
 t.Logf("checkpoint metadata empty-trailer error=%v",err)
 if err==nil{t.Fatal("checkpoint metadata accepted an empty trailer absent from signed intent")}
 if !strings.Contains(err.Error(),"causal trailers differ"){t.Fatal(err)}
}
