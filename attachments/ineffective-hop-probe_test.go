package workroom

import "testing"

// Historical #140 permits propagation or an explicit warning on the ineffective
// intermediary. The effective-hop case proves the retired base is observable.
func TestPlanner140IneffectiveHop(t *testing.T) {
 for _, valid := range []bool{true, false} {
  name := "ineffective-hop"
  if valid { name = "effective-hop-control" }
  t.Run(name, func(t *testing.T) {
   body := map[string]string{"to": agent}
   if valid { body["conditions"] = "deliver the requested result" }
   records := landingWorld(t,
    event(t, lid("base"), operator, SchemaState, State{Kind: KindArtifact, Text: "base", Body: map[string]string{"path":"base.go", "commit":approvedHead}}),
    event(t, lid("middle"), operator, SchemaState, State{Kind: KindRequest, Text: "middle", Body:body}, lid("base")),
    event(t, lid("leaf"), operator, SchemaState, State{Kind: KindArtifact, Text: "leaf", Body: map[string]string{"path":"leaf.go", "commit":approvedHead}}, lid("middle")),
    event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target:lid("base"), Text:"base no longer applies"}, lid("base")),
   )
   p := Fold(records)
   expected := Ineffective
   if valid { expected = Effective }
   if d, ok := p.Decision(lid("middle")); !ok || d.Verdict != expected { t.Fatalf("invalid probe: middle=%+v",d) }
   if d, ok := p.Decision(lid("retire")); !ok || d.Verdict != Effective { t.Fatalf("invalid probe: retirement=%+v",d) }
   found, stale := false, false
   for _, a := range p.Artifacts { if a.Event==lid("leaf") { found=true; stale=a.Stale } }
   if !found { t.Fatal("invalid probe: dependent artifact absent") }
   warning := DeadBases(p, []string{lid("middle")})
   t.Logf("middle=%s leaf.stale=%v direct-basis-warning=%v",expected,stale,warning)
   if !stale && len(warning)==0 { t.Fatal("retired base is hidden by ineffective intermediary: neither propagated staleness nor direct-basis warning") }
  })
 }
}
