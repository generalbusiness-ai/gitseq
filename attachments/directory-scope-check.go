package main
import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "os/exec"
 "strings"
 "github.com/generalbusiness-ai/gitseq/internal/app"
 "github.com/generalbusiness-ai/gitseq/internal/mergeplan"
)
func must(err error) {if err != nil {panic(err)}}
func git(repo string,args ...string) string { c:=exec.Command("git",args...);c.Dir=repo;b,e:=c.Output();must(e);return strings.TrimSpace(string(b)) }
func main(){
 ctx:=context.Background();repo:="/Users/hughpyle/play/gitseq";head:="463ec7b9d48f32835dcbc5034892171b27ccd9a4";prefix:="git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:"
 previous:=prefix+"2794376a061440f65a2b5bbf753180d3f8ee5a74";directory:=prefix+"91f66c9fffedf32889ad2ce20d1ff9b787b4fcf6"
 w,e:=app.Open(ctx,repo);must(e);s,e:=w.ReadOnlySnapshot(ctx);must(e)
 pre:=git(repo,"rev-parse","main");tree:=git(repo,"merge-tree","--write-tree",pre,head)
 c:=exec.Command("git","diff","--name-status","-z","--find-renames",pre,tree);c.Dir=repo;raw,e:=c.Output();must(e);changes,e:=mergeplan.ParseChanges(string(raw));must(e)
 reviewed,_:=mergeplan.ReviewedScope(s.Projection,previous);classes,e:=mergeplan.Classify(ctx,repo,s.Projection,changes,pre,head,reviewed);must(e);plan:=mergeplan.PlanSuccession(s.Projection,changes,classes)
 review,ok:=s.Projection.Review(previous);if !ok {panic("review missing")}
 before:=mergeplan.ValidateReach(s.Projection,plan,previous,review.Implementer);if before==nil {panic("control: previous scope unexpectedly suffices")}
 // This changes only a disposable in-memory scope projection, never the log,
 // a signature, an actual verdict, or merge authorization.
 s.Projection.Provenance[previous]=append(append([]string(nil),s.Projection.Provenance[previous]...),directory)
 reviewed,paths:=mergeplan.ReviewedScope(s.Projection,previous);classes,e=mergeplan.Classify(ctx,repo,s.Projection,changes,pre,head,reviewed);must(e);expanded:=mergeplan.PlanSuccession(s.Projection,changes,classes);must(mergeplan.ValidateReach(s.Projection,expanded,previous,review.Implementer))
 out:=map[string]any{"mode":"read-only hypothetical coverage check; no approval or merge", "frontier":s.Head,"target":pre,"candidate":head,"merge_tree":tree,"previous_scope_refusal":before.Error(),"expanded_scope_paths":paths,"publish":expanded.Publish,"retire":expanded.Retire,"expanded_scope_reach":"passes; fresh independent durable approval still required"}
 must(json.NewEncoder(os.Stdout).Encode(out));fmt.Fprintln(os.Stderr,"read-only check complete")
}
