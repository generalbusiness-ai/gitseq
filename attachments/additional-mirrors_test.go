package wireparity
import (
 "testing"
 "reflect"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
 "github.com/generalbusiness-ai/gitseq/internal/app"
 "github.com/generalbusiness-ai/gitseq/internal/statusview"
 "github.com/generalbusiness-ai/gitseq/internal/gitstore"
 live "github.com/generalbusiness-ai/gitseq/host/live"
)
func TestPlannerExistingAdditionalSimpleMirrors(t *testing.T) {
 pairs:=map[string]any{
  "FieldConstraint":workroom.FieldConstraint{}, "BasisConstraint":workroom.BasisConstraint{},
  "KindDefinition":workroom.KindDefinition{}, "FoldTransition":workroom.FoldTransition{},
  "Vocabulary":workroom.Vocabulary{}, "ActorState":workroom.ActorState{},
  "DurableSnapshot":app.Snapshot{}, "LiveCursor":live.Cursor{}, "Activity":live.Activity{},
  "LiveSnapshot":live.Snapshot{}, "Cursor":statusview.Cursor{}, "GraphCommit":gitstore.GraphCommit{},
 }
 source:=readAPI(t)
 for name,value:=range pairs {t.Run(name,func(t *testing.T){
  want:=jsonFields(t,value);got,found:=interfaceFields(source,name)
  if !found || !reflect.DeepEqual(want,got){t.Errorf("%s names: Go=%v TS=%v",name,want,got)}
 })}
}
