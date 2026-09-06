package mergeplan

import (
 "context"
 "encoding/json"
 "os"
 "testing"

 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPlannerPublishedOtherTargetClassification(t *testing.T) {
 raw, err := os.ReadFile("/tmp/planner-other-target-minimal-input.json")
 if err != nil { t.Fatal(err) }
 var snapshot struct { Durable struct { Head string; Depth int; Projection workroom.Projection } }
 if err := json.Unmarshal(raw, &snapshot); err != nil { t.Fatal(err) }
 p := snapshot.Durable.Projection
 const artifact = "git:sha1:da732b0bdaad4426ed4ad666b892d8a7c68f625f#git:sha1:7bfc28b144d68fefcfe29e24f991d15bdc18025b"
 const main = "7ecffd53013fd6ca45693a1a8e28b7c8d52432e8"
 const recovery = "5d84ac71d625b29d9e7e9dbdf4c7c66c60429434"
 const candidate = "94baf5a979e11f35333e1340c4d42947e65abddd"
 repo := "/Users/hughpyle/play/tailapp"
 changes := []Change{{Status:"M",New:"internal/inbox/inbox.go"}}
 classify := func(projection workroom.Projection, target string, paths []Change) map[string]Candidate {
  t.Helper()
  got, err := Classify(context.Background(),repo,projection,paths,target,candidate,nil)
  if err != nil { t.Fatal(err) }; return got
 }
 other := classify(p,recovery,changes)
 if other[artifact].Class != ClassAbandoned { t.Fatalf("observed class changed: %+v",other[artifact]) }
 same := classify(p,main,changes)
 if same[artifact].Class != ClassInTargetPredecessor { t.Fatalf("same-target control: %+v",same[artifact]) }
 unrelated := classify(p,recovery,[]Change{{Status:"M",New:"not-a-covered-task-file"}})
 if _,ok := unrelated[artifact]; ok { t.Fatal("unrelated path classified") }
 controlled := p
 controlled.Statements = append(append([]workroom.Statement(nil),p.Statements...),workroom.Statement{Event:"probe-request",Lifecycle:workroom.LifecycleRequest})
 controlled.Commitments = append(append([]workroom.Commitment(nil),p.Commitments...),workroom.Commitment{Request:"probe-request",Status:"open"})
 controlled.Provenance = make(map[string][]string,len(p.Provenance)+1)
 for k,v := range p.Provenance { controlled.Provenance[k]=v }
 controlled.Provenance["probe-request"] = []string{artifact}
 protected := classify(controlled,recovery,changes)
 if protected[artifact].Class != ClassProtectedSibling { t.Fatalf("unsettled control: %+v",protected[artifact]) }
 plan := PlanSuccession(p,changes,other)
 out := map[string]any{"snapshot_head":snapshot.Durable.Head,"snapshot_depth":snapshot.Durable.Depth,"artifact":artifact,"artifact_main_head":main,"recovery_target":recovery,"classifier_candidate":candidate,"changed_path":"internal/inbox/inbox.go","recovery_class":other[artifact],"main_control_class":same[artifact],"active_commitment_control_class":protected[artifact],"unrelated_path_absent":true,"sealed_left_live":plan.LeftLive[artifact],"scope":"Read-only Classify/PlanSuccession probe only. Candidate is the historical upstream fix for classification; no backport candidate, admissibility verdict, merge, ref mutation or retirement is made."}
 encoded,err := json.MarshalIndent(out,"","  "); if err != nil {t.Fatal(err)}
 if err := os.WriteFile("/tmp/planner-other-target-minimal-result.json",encoded,0600);err!=nil {t.Fatal(err)}
 t.Log(string(encoded))
}
