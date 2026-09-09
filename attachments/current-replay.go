package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
	"os"
	"time"
)

func main() {
	const genesis = "5d2622748872b7e2dec3fe5c59e4be73a35e0bc8"
	const frozen = "24b6353f5b41ef95a7d8737bc7e138fbaeda2520"
	const actor = "a5d35aa7e4799472a208663d7f024917442bd513929f9eccf450628ccf70095d"
	const request = "git:sha1:" + genesis + "#git:sha1:76390a8672d6b8781391883224e8d96fc5ed0e9a"
	ctx := context.Background()
	started := time.Now()
	folder := workroom.NewFolder(nil)
	loaded, err := kernel.NewReader(gitstore.Store{Repo: os.Args[1]}).LoadWithProgressStream(ctx, genesis, nil, func(e kernel.Event) error {
		folder.Append(workroom.Record{ID: "git:sha1:" + genesis + "#git:sha1:" + e.Commit, Timestamp: e.Timestamp, Actor: intent.ActorFingerprint(e.Signed.ActorKey), Schema: e.Intent.Schema, RestsOn: e.Intent.RestsOn, Payload: e.Payload, Attachments: e.Attachments})
		return nil
	})
	if err != nil {
		panic(err)
	}
	if loaded.Verification.Head != frozen || loaded.Verification.Depth != 19682 || len(loaded.Events) != 0 {
		panic("unexpected frozen verification or cold streaming result")
	}
	snapshot := app.Snapshot{Genesis: genesis, Head: frozen, Depth: 19682, Projection: folder.Projection(), Vocabulary: folder.Vocabulary()}
	var selected *workroom.Commitment
	for i := range snapshot.Projection.Commitments {
		c := &snapshot.Projection.Commitments[i]
		if c.Request == request {
			selected = c
			break
		}
	}
	if selected == nil || selected.Status != "stale" || !selected.Stale || selected.Promise == "" || selected.Report != "" || selected.WaitingOn != actor {
		panic("current fold does not reproduce original stale live promise")
	}
	found := false
	pages := map[string]statusview.WorkPage{}
	for _, lane := range []statusview.WorkLane{statusview.LaneNotActionable, statusview.LaneWaitingOnYou} {
		p, err := statusview.BuildWorkPage(snapshot, statusview.WorkQuery{Actor: actor, Lanes: []statusview.WorkLane{lane}, Statuses: []string{"stale", "promised"}, Stale: statusview.StaleInclude, Limit: 50}, false)
		if err != nil {
			panic(err)
		}
		for _, row := range p.Items {
			if row.Request == request {
				if lane == statusview.LaneWaitingOnYou {
					panic("target unexpectedly visible in waiting lane")
				}
				found = true
			}
		}
		pages[string(lane)] = p
	}
	if !found {
		panic("target missing from not_actionable page")
	}
	out := map[string]any{"verified": loaded.Verification, "profile_version": workroom.ProfileVersion, "selected": selected, "query_proof": "PASS: current fold keeps live promise stale; current query returns it in not_actionable and omits it from waiting_on_you", "elapsed_seconds": time.Since(started).Seconds(), "query_pages": pages}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, "PASS: current source replayed verified frozen19682 and reproduced promised-work omission; no live refs, witnesses or keys touched")
}
