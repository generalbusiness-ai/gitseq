// Command flaredemo replays the real workroom log and appends one synthetic
// merge, twice: once recorded the old way (a single artifact at ".") and once
// recorded the new way (an artifact at each path the merge changed). It prints
// which live work flares in each case. Nothing is signed or written; the
// records exist only in memory for the duration of the run.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/intent"
	"gitseq/spike/internal/kernel"
	"gitseq/spike/internal/workroom"
)

const mergeHead = "1111111111111111111111111111111111111111"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "flaredemo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	repo := "/Users/hughpyle/play/gitseq"
	if len(os.Args) > 1 {
		repo = os.Args[1]
	}
	workspace, err := app.Open(ctx, repo)
	if err != nil {
		return err
	}
	reader := kernel.NewReader(workspace.Store, kernel.CheckpointOptions{
		Profile: workroom.ProfileVersion, SigningKey: workspace.Config.SequencerKey,
	})
	loaded, err := reader.Load(ctx, workspace.Config.Genesis)
	if err != nil {
		return err
	}
	records := make([]workroom.Record, 0, len(loaded.Events))
	for _, event := range loaded.Events {
		records = append(records, workroom.Record{
			ID: workspace.EventID(event.Commit), Timestamp: event.Timestamp,
			Actor:  intent.ActorFingerprint(event.Signed.ActorKey),
			Schema: event.Intent.Schema, RestsOn: event.Intent.RestsOn,
			Payload: event.Payload, Attachments: event.Attachments,
		})
	}
	base := workroom.Fold(records)
	fmt.Printf("real log: head %s, %d records, %d artifacts\n", loaded.Verification.Head, len(records), len(base.Artifacts))

	dot := lastLive(base, ".")
	area := lastLive(base, "ui")
	if dot == nil || area == nil {
		return fmt.Errorf("expected a live artifact at %q and at %q", ".", "ui")
	}
	fmt.Printf("anchor A: live merge artifact at %q, %s@%s\n", dot.Path, short(dot.Event), dot.Commit[:12])
	fmt.Printf("anchor B: live artifact at %q, %s@%s\n", area.Path, short(area.Event), area.Commit[:12])

	restingOnDot := dependents(base, records, dot.Event)
	restingOnArea := dependents(base, records, area.Event)
	fmt.Printf("work anchored on A: %d acts; on B: %d acts\n\n", len(restingOnDot), len(restingOnArea))

	// Both synthetic merge artifacts rest on the same live, unstale basis, so
	// the only difference between the two runs is the path each one claims.
	basis := firstLive(base)
	fmt.Printf("both synthetic merge artifacts rest on %s\n\n", short(basis))

	// The synthetic merge changes only ui/. Under the old rule it is recorded
	// as one artifact at ".", superseding the previous "." artifact. Under the
	// new rule it is recorded at the path it changed, superseding the live
	// artifact there.
	oldWay := workroom.Fold(append(clone(records),
		supersede(dot.Event, byID(records, dot.Event).Actor, "merged ui change"),
		artifact(".", byID(records, dot.Event).Actor, "main after a ui-only merge", basis)))
	newWay := workroom.Fold(append(clone(records),
		supersede(area.Event, byID(records, area.Event).Actor, "merged ui change"),
		artifact("ui", byID(records, area.Event).Actor, "ui after a ui-only merge", basis)))

	report("OLD RULE, merge recorded at \".\"", base, oldWay, restingOnDot, restingOnArea)
	report("NEW RULE, merge recorded at \"ui\"", base, newWay, restingOnDot, restingOnArea)

	// The other direction: a merge that changes a described area must still
	// flare the pages and work that describe it.
	described := lastLive(base, "spike/internal/workroom")
	if described == nil {
		return fmt.Errorf("expected a live artifact at spike/internal/workroom")
	}
	restingOnDescribed := dependents(base, records, described.Event)
	fmt.Printf("anchor C: live artifact at %q, %s@%s, %d acts anchored\n",
		described.Path, short(described.Event), described.Commit[:12], len(restingOnDescribed))
	describedWay := workroom.Fold(append(clone(records),
		supersede(described.Event, byID(records, described.Event).Actor, "merged workroom change"),
		artifact(described.Path, byID(records, described.Event).Actor, "workroom after a fold change", basis)))
	report("NEW RULE, merge recorded at \"spike/internal/workroom\"", base, describedWay, restingOnDot, restingOnDescribed)
	return nil
}

func report(title string, base, after workroom.Projection, onDot, onArea []string) {
	fmt.Println(title)
	fmt.Printf("  newly stale, anchored on the \".\" merge artifact: %d of %d\n", newlyStale(base, after, onDot), len(onDot))
	fmt.Printf("  newly stale, anchored on the changed path artifact: %d of %d\n", newlyStale(base, after, onArea), len(onArea))
	fmt.Printf("  newly stale anywhere in the log:                  %d\n", totalNewlyStale(base, after))
	for _, event := range allNewlyStale(base, after) {
		fmt.Printf("    %s %s\n", short(event), describe(after, event))
	}
	fmt.Println()
}

func staleMap(projection workroom.Projection) map[string]bool {
	stale := make(map[string]bool)
	for _, statement := range projection.Statements {
		stale[statement.Event] = statement.Stale
	}
	for _, artifact := range projection.Artifacts {
		stale[artifact.Event] = artifact.Stale
	}
	return stale
}

func newlyStale(base, after workroom.Projection, events []string) int {
	before, now := staleMap(base), staleMap(after)
	count := 0
	for _, event := range events {
		if !before[event] && now[event] {
			count++
		}
	}
	return count
}

func totalNewlyStale(base, after workroom.Projection) int {
	before, now := staleMap(base), staleMap(after)
	count := 0
	for event, value := range now {
		if value && !before[event] {
			count++
		}
	}
	return count
}

func allNewlyStale(base, after workroom.Projection) []string {
	before, now := staleMap(base), staleMap(after)
	var events []string
	for event, value := range now {
		if value && !before[event] {
			events = append(events, event)
		}
	}
	sort.Strings(events)
	return events
}

func describe(projection workroom.Projection, event string) string {
	for _, statement := range projection.Statements {
		if statement.Event == event {
			text := statement.Text
			if len(text) > 64 {
				text = text[:64]
			}
			return string(statement.Kind) + " " + text
		}
	}
	for _, candidate := range projection.Artifacts {
		if candidate.Event == event {
			return "artifact at " + candidate.Path
		}
	}
	return "?"
}

// dependents returns every act that reaches the target through rests_on, which
// is the set that flares when the target is retired.
func dependents(projection workroom.Projection, records []workroom.Record, target string) []string {
	reach := map[string]bool{target: true}
	var found []string
	for _, record := range records {
		for _, basis := range record.RestsOn {
			if reach[basis] {
				reach[record.ID] = true
				found = append(found, record.ID)
				break
			}
		}
	}
	sort.Strings(found)
	return found
}

func firstLive(projection workroom.Projection) string {
	for _, statement := range projection.Statements {
		if !statement.Stale {
			return statement.Event
		}
	}
	return ""
}

func lastLive(projection workroom.Projection, path string) *workroom.Artifact {
	var found *workroom.Artifact
	for index := range projection.Artifacts {
		candidate := &projection.Artifacts[index]
		if candidate.Path == path && !candidate.Stale {
			found = candidate
		}
	}
	return found
}

func short(event string) string {
	if len(event) < 8 {
		return event
	}
	return event[len(event)-8:]
}

func byID(records []workroom.Record, id string) workroom.Record {
	for _, record := range records {
		if record.ID == id {
			return record
		}
	}
	return workroom.Record{}
}

func clone(records []workroom.Record) []workroom.Record {
	out := make([]workroom.Record, len(records), len(records)+2)
	copy(out, records)
	return out
}

func supersede(target, actor, text string) workroom.Record {
	payload, err := workroom.Encode(workroom.Supersede{Target: target, Text: text})
	if err != nil {
		panic(err)
	}
	return workroom.Record{ID: "synthetic:supersede", Actor: actor, Schema: workroom.SchemaSupersede, RestsOn: []string{target}, Payload: payload}
}

func artifact(path, actor, text, rests string) workroom.Record {
	payload, err := workroom.Encode(workroom.State{
		Kind: workroom.KindArtifact, Text: text,
		Body: map[string]string{"path": path, "commit": mergeHead},
	})
	if err != nil {
		panic(err)
	}
	return workroom.Record{ID: "synthetic:artifact", Actor: actor, Schema: workroom.SchemaState, RestsOn: []string{rests}, Payload: payload}
}
