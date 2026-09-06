package reviewguard

import (
 "encoding/json"
 "testing"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPlannerExactPromiseSelectorSurvivesVerdictRoundTrip(t *testing.T) {
 old := assignedCommitment("old-report")
 old.Promise = "old-promise"
 old.Status = "reneged"
 current := assignedCommitment("primary")
 projection := bindingWorld(t, []workroom.Commitment{old, current}, assignedProvenance(), assignedStatements()...)
 binding, err := Resolve(projection, Scope{Candidate:head, Examined:[]string{"primary"}, Implementations:[]string{"work"}})
 if err != nil { t.Fatalf("exact promise selector rejected before serialization: %v",err) }
 if len(binding.Implementations)!=1 || binding.Implementations[0].Promise!="work" {t.Fatalf("wrong lifecycle: %+v",binding)}
 var encoded []string
 if err=json.Unmarshal([]byte(binding.BodyFields()[BodyImplementations]),&encoded);err!=nil{t.Fatal(err)}
 again,err:=Resolve(projection,Scope{Candidate:head,Examined:[]string{"primary"},Implementations:encoded})
 if err!=nil || !SameBinding(binding,again){t.Fatalf("exact promise was lost in verdict serialization (%v): %v; got %+v",encoded,err,again)}
}
