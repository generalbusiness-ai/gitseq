package main
import("encoding/json";"os";"fmt";"github.com/generalbusiness-ai/gitseq/internal/app";"github.com/generalbusiness-ai/gitseq/internal/statusview")
func main(){ var s app.Snapshot; f,e:=os.Open(os.Args[1]);if e!=nil{panic(e)};defer f.Close();if e=json.NewDecoder(f).Decode(&s);e!=nil{panic(e)}
 actor:="a5d35aa7e4799472a208663d7f024917442bd513929f9eccf450628ccf70095d"
 var out []statusview.WorkPage
 for _,lanes:=range [][]statusview.WorkLane{{statusview.LaneAvailable,statusview.LaneWaitingOnYou,statusview.LaneNotActionable},{statusview.LaneWaitingOnYou}} {p,e:=statusview.BuildWorkPage(s,statusview.WorkQuery{Actor:actor,Lanes:lanes,Statuses:[]string{"stale","promised"},Stale:statusview.StaleInclude,Limit:50},false);if e!=nil{panic(e)};out=append(out,p)}
 want:="git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:76390a8672d6b8781391883224e8d96fc5ed0e9a"
 found:=false;for _,r:=range out[0].Items {if r.Request==want {found=r.Status=="stale"&&r.Stale&&r.Lane==statusview.LaneNotActionable&&r.WaitingOn.Fingerprint==actor&&r.Promise!=""}}
 if !found {panic("historical promised/stale case not reproduced")};for _,r:=range out[1].Items{if r.Request==want{panic("waiting_on_you unexpectedly includes historical stale promise")}}
 if e:=json.NewEncoder(os.Stdout).Encode(out);e!=nil{panic(e)};fmt.Fprintln(os.Stderr,"PASS: frozen verified19682 promise19653 retains Claude waiting party, but is not_actionable and omitted from waiting_on_you")
}
