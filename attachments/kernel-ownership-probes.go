package kernel
import("errors";"testing")
func TestReviewReaderKeepsVerifiedSignedBytesPrivate(t *testing.T){
 f:=newFixture(t,"sha1");alice:=actor(t);first:=f.request(t,alice,"held",[]byte("first"),nil)
 if _,err:=Submit(f.ctx,f.store,first,Options{SigningKey:f.signingKey});err!=nil{t.Fatal(err)}
 reader:=NewReader(f.store);loaded,err:=reader.Load(f.ctx,f.genesis);if err!=nil{t.Fatal(err)}
 key,err:=first.Signed.DedupKey();if err!=nil{t.Fatal(err)}
 prior,held,err:=reader.PriorActsAt(f.ctx,f.genesis,loaded.Verification.Head,[]string{key});if err!=nil||!held{t.Fatal(held,err)}
 changed:=f.request(t,alice,"held",[]byte("other"),nil);lent:=prior[key]
 if len(changed.Signed.Intent)!=len(lent.Signed.Intent){t.Fatal("inert control: intent lengths differ")}
 copy(lent.Signed.Intent,changed.Signed.Intent);copy(lent.Signed.Signature,changed.Signed.Signature)
 replay,held,err:=reader.ReplayAt(loaded.Verification.Head,changed)
 if replay||!held||!errors.Is(err,ErrIdempotencyConflict){t.Fatalf("editing lookup result changed verified replay history: replay=%v held=%v err=%v; want conflict",replay,held,err)}
}
func TestReviewSeededWorldIgnoresEditsToReturnedLoadEvents(t *testing.T){
 f:=newFixture(t,"sha1");alice:=actor(t);first:=f.request(t,alice,"held",[]byte("first"),nil)
 if _,err:=Submit(f.ctx,f.store,first,Options{SigningKey:f.signingKey});err!=nil{t.Fatal(err)}
 reader:=NewReader(f.store);loaded,err:=reader.Load(f.ctx,f.genesis);if err!=nil{t.Fatal(err)}
 if len(loaded.Events)!=1{t.Fatalf("inert control: got %d events",len(loaded.Events))}
 changed:=f.request(t,alice,"held",[]byte("other"),nil);lent:=loaded.Events[0]
 if len(changed.Signed.Intent)!=len(lent.Signed.Intent){t.Fatal("inert control: intent lengths differ")}
 copy(lent.Signed.Intent,changed.Signed.Intent);copy(lent.Signed.Signature,changed.Signed.Signature)
 submitter:=NewSubmitterFrom(f.store,Options{SigningKey:f.signingKey},reader)
 result,err:=submitter.Submit(f.ctx,changed)
 if err==nil||!errors.Is(err,ErrIdempotencyConflict){t.Fatalf("editing load result changed seeded submission history: result=%+v err=%v; want conflict",result,err)}
}
