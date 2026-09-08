package main
import ("fmt"; "testing")
func TestPlannerBatchResumesExistingPrefix(t *testing.T) {
 f := newBatchFixture(t)
 first := fmt.Sprintf(`[{"label":"note","verb":"state","kind":"assert","text":"probe durable prefix","rests_on":[%q],"idempotency_key":"planner-prefix"}]`, f.genesis)
 a,err:=f.run("operator",first); if err!=nil {t.Fatal(err)}
 before:=f.snapshot(); if a.Landed!=1 || a.Replayed!=0 {t.Fatalf("seed: %#v",a)}
 full:=fmt.Sprintf(`[{"label":"note","verb":"state","kind":"assert","text":"probe durable prefix","rests_on":[%q],"idempotency_key":"planner-prefix"},{"verb":"state","kind":"assert","text":"probe suffix","rests_on":["$note"],"idempotency_key":"planner-suffix"}]`,f.genesis)
 b,err:=f.run("operator",full);if err!=nil {t.Fatal(err)}
 after:=f.snapshot()
 if b.Replayed!=1 || b.Landed!=1 || b.Acts[0].Outcome!="replayed" || b.Acts[1].Outcome!="landed" || b.Acts[0].Event!=a.Acts[0].Event {t.Fatalf("mixed resume: %#v",b)}
 if after.Depth!=before.Depth+1 {t.Fatalf("depth %d -> %d",before.Depth,after.Depth)}
 if !contains(after.Projection.Provenance[b.Acts[1].Event],a.Acts[0].Event){t.Fatal("suffix lost exact prefix provenance")}
 c,err:=f.run("operator",full);if err!=nil {t.Fatal(err)}
 if c.Landed!=0 || c.Replayed!=2 || f.snapshot().Head!=after.Head {t.Fatalf("final replay: %#v",c)}
 t.Logf("seed landed=%d; resume replayed=%d landed=%d, depth %d -> %d; final replayed=%d no head move",a.Landed,b.Replayed,b.Landed,before.Depth,after.Depth,c.Replayed)
}
