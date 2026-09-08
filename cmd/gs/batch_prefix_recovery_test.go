package main

import (
	"fmt"
	"slices"
	"testing"
)

func TestBatchResumesADurablePrefixAndReplaysTheCompletedChain(t *testing.T) {
	fixture := newBatchFixture(t)
	prefix := fmt.Sprintf(`{"label":"note","verb":"state","kind":"assert","text":"recovery prefix",
	  "rests_on":[%q],"idempotency_key":"recovery-prefix"}`, fixture.genesis)
	suffix := `{"verb":"state","kind":"assert","text":"recovery suffix",
	  "rests_on":["$note"],"idempotency_key":"recovery-suffix"}`
	chain := "[" + prefix + "," + suffix + "]"
	// Seed the exact first act through the real signed append path. Reusing its
	// bytes and idempotency key makes the later input an unchanged full chain.
	initial := fixture.snapshot()
	seed, err := fixture.run("operator", "["+prefix+"]")
	if err != nil {
		t.Fatal(err)
	}
	if seed.Error != nil || seed.Landed != 1 || seed.Replayed != 0 || len(seed.Acts) != 1 || seed.Acts[0].Event == "" {
		t.Fatalf("prefix seed report = %#v", seed)
	}
	prefixEvent := seed.Acts[0].Event
	if seed.Acts[0] != (batchOutcome{Position: 0, Label: "note", Event: prefixEvent, Outcome: "landed"}) {
		t.Fatalf("prefix seed outcome = %#v", seed.Acts[0])
	}
	before := fixture.snapshot()
	if before.Head == initial.Head || before.Depth != initial.Depth+1 {
		t.Fatalf("seed did not append exactly one event: %s/%d to %s/%d", initial.Head, initial.Depth, before.Head, before.Depth)
	}
	resumed, err := fixture.run("operator", chain)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Error != nil || resumed.Landed != 1 || resumed.Replayed != 1 || len(resumed.Acts) != 2 {
		t.Fatalf("mixed replay/new suffix report = %#v", resumed)
	}
	suffixEvent := resumed.Acts[1].Event
	if suffixEvent == "" || suffixEvent == prefixEvent {
		t.Fatalf("suffix event = %q, want a new event distinct from %s", suffixEvent, prefixEvent)
	}
	want := []batchOutcome{
		{Position: 0, Label: "note", Event: prefixEvent, Outcome: "replayed"},
		{Position: 1, Event: suffixEvent, Outcome: "landed"},
	}
	if !slices.Equal(resumed.Acts, want) {
		t.Fatalf("mixed outcomes = %#v, want %#v", resumed.Acts, want)
	}
	landed := fixture.snapshot()
	if landed.Head == before.Head || landed.Depth != before.Depth+1 {
		t.Fatalf("resume did not append exactly the missing suffix: %s/%d to %s/%d", before.Head, before.Depth, landed.Head, landed.Depth)
	}
	if got := landed.Projection.Provenance[suffixEvent]; !slices.Equal(got, []string{prefixEvent}) {
		t.Fatalf("suffix provenance = %v, want only the original prefix %s", got, prefixEvent)
	}
	prefixes := 0
	for _, statement := range landed.Projection.Statements {
		if statement.Text == "recovery prefix" {
			prefixes++
			if statement.Event != prefixEvent {
				t.Fatalf("prefix was minted again as %s", statement.Event)
			}
		}
	}
	if prefixes != 1 {
		t.Fatalf("durable prefix count = %d, want one", prefixes)
	}
	replayed, err := fixture.run("operator", chain)
	if err != nil {
		t.Fatal(err)
	}
	want[1].Outcome = "replayed"
	if replayed.Error != nil || replayed.Landed != 0 || replayed.Replayed != 2 || !slices.Equal(replayed.Acts, want) {
		t.Fatalf("complete replay = %#v, want both original events replayed", replayed)
	}
	after := fixture.snapshot()
	if after.Head != landed.Head || after.Depth != landed.Depth {
		t.Fatalf("complete replay moved head/depth: %s/%d to %s/%d", landed.Head, landed.Depth, after.Head, after.Depth)
	}
	t.Logf("seed landed=1; resume replayed=1 landed=1, depth %d -> %d; complete replay retained both event IDs and unchanged head/depth", before.Depth, landed.Depth)
}
