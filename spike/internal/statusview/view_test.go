package statusview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"gitseq/spike/internal/workroom"
)

func growthProjection(size int) workroom.Projection {
	projection := workroom.Projection{Actors: map[string]workroom.ActorState{
		"requester": {Name: "Ada"}, "performer": {Name: "Grace"},
	}}
	for index := range size {
		request := fmt.Sprintf("request:%05d", index)
		projection.Statements = append(projection.Statements, workroom.Statement{
			Event: request, Actor: "requester", Kind: workroom.KindRequest,
			Text: strings.Repeat("bounded request text ", 40) + fmt.Sprint(index),
		})
		projection.Commitments = append(projection.Commitments,
			workroom.Commitment{Request: request, Requester: "requester", Performer: "performer", WaitingOn: "performer", Status: "requested"},
			workroom.Commitment{Request: request + ":stale", Requester: "requester", Performer: "performer", Status: "stale"},
			workroom.Commitment{Request: request + ":done", Requester: "requester", Performer: "performer", Status: "satisfied"},
		)
		projection.Artifacts = append(projection.Artifacts,
			workroom.Artifact{Event: fmt.Sprintf("artifact:current:%05d", index), Path: strings.Repeat("path/", 80) + fmt.Sprint(index), Commit: fmt.Sprintf("commit-current-%05d", index)},
			workroom.Artifact{Event: fmt.Sprintf("artifact:stale:%05d", index), Path: "stale/path", Commit: fmt.Sprintf("commit-stale-%05d", index), Stale: true},
		)
		projection.Decisions = append(projection.Decisions,
			workroom.Decision{Event: fmt.Sprintf("attempt:ineffective:%05d", index), Verdict: workroom.Ineffective, Reason: strings.Repeat("reason ", 80)},
			workroom.Decision{Event: fmt.Sprintf("attempt:disputed:%05d", index), Verdict: workroom.Disputed, Reason: "disputed"},
		)
	}
	return projection
}

func TestBuildBoundsEveryGrowthDimensionAndCountsExactly(t *testing.T) {
	if ListCap != 20 || TextCap != 240 || DeltaCap != 50 {
		t.Fatalf("view caps moved: lists=%d text=%d delta=%d", ListCap, TextCap, DeltaCap)
	}
	const size = 10000
	projection := growthProjection(size)
	summary := Build("genesis", "head", size*2, projection)
	for name, got := range map[string]int{
		"actionable": len(summary.Actionable), "attention": len(summary.Attention),
		"current artifacts": len(summary.CurrentArtifacts), "stale artifacts": len(summary.StaleArtifacts),
		"attempts": len(summary.Attempts),
	} {
		if got != ListCap {
			t.Fatalf("%s listed %d, want %d", name, got, ListCap)
		}
	}
	if summary.ActionableOmitted != size-ListCap || summary.AttentionOmitted != size-ListCap ||
		summary.CurrentOmitted != size-ListCap || summary.StaleOmitted != size-ListCap ||
		summary.AttemptsOmitted != 2*size-ListCap {
		t.Fatalf("omitted counts are not exact: %+v", summary)
	}
	if summary.Totals.Commitments["requested"] != size || summary.Totals.Commitments["stale"] != size || summary.Totals.Commitments["satisfied"] != size {
		t.Fatalf("commitment totals = %#v", summary.Totals.Commitments)
	}
	if summary.Actionable[0].Requester != "Ada" || summary.Actionable[0].Performer != "Grace" {
		t.Fatalf("actor names were not resolved: %+v", summary.Actionable[0])
	}
	if len(summary.Actionable[0].Text) > TextCap || len(summary.CurrentArtifacts[0].Path) > TextCap {
		t.Fatal("user-controlled summary fields exceed their cap")
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 32<<10 {
		t.Fatalf("bounded summary is %d bytes, want < 32 KiB", len(encoded))
	}
	rendered := Render(summary, "test")
	if len(rendered) >= 32<<10 {
		t.Fatalf("bounded text is %d bytes, want < 32 KiB", len(rendered))
	}
	t.Logf("10k-dimension bounded sizes: %d bytes JSON, %d bytes text", len(encoded), len(rendered))
	if !bytes.Contains(rendered, []byte("Showing 20 of 10000; 9980 older omitted")) ||
		!bytes.Contains(rendered, []byte("Showing 20 of 20000; 19980 older omitted")) {
		t.Fatalf("rendered view hides exact omission counts:\n%s", rendered)
	}
	if summary.Actionable[0].Request != "request:09999" || summary.Actionable[len(summary.Actionable)-1].Request != "request:09980" {
		t.Fatalf("summary is not newest-first: %#v", summary.Actionable)
	}
	fullJSON, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fullJSON, []byte("request:00000")) || !bytes.Contains(fullJSON, []byte("request:09999")) {
		t.Fatal("full JSON omitted an entry outside the bounded window")
	}
	fullText := workroom.RenderStatus(projection)
	if rows := bytes.Count(fullText, []byte("| requested |")); rows != size {
		t.Fatalf("full text rendered %d requested rows, want %d", rows, size)
	}
}

func TestDoublingHistoryChangesOnlyBoundedRowsAndCounters(t *testing.T) {
	one, _ := json.Marshal(Build("genesis", "head", 10000, growthProjection(5000)))
	two, _ := json.Marshal(Build("genesis", "head", 20000, growthProjection(10000)))
	if growth := len(two) - len(one); growth > 64 {
		t.Fatalf("doubling history grew the bounded response by %d bytes: %d then %d", growth, len(one), len(two))
	}
	fullOne, _ := json.Marshal(growthProjection(5000))
	fullTwo, _ := json.Marshal(growthProjection(10000))
	if len(fullTwo) < len(fullOne)*19/10 {
		t.Fatalf("fixture did not actually double full history: %d then %d", len(fullOne), len(fullTwo))
	}
}

func TestTextCapIsAValidUTF8ByteCeiling(t *testing.T) {
	got := Text(strings.Repeat("界", TextCap))
	if len(got) > TextCap || !json.Valid([]byte(`{"text":`+fmt.Sprintf("%q", got)+`}`)) {
		t.Fatalf("capped text is %d bytes or invalid JSON: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("capped text does not disclose truncation: %q", got)
	}
}

func TestOpenUnclaimedRequestRemainsActionableWithoutInventedAssignment(t *testing.T) {
	projection := workroom.Projection{
		Actors:      map[string]workroom.ActorState{"requester": {Name: "Ada"}, "addressee": {Name: "Grace"}},
		Statements:  []workroom.Statement{{Event: "request", Actor: "requester", Kind: workroom.KindRequest, Text: "available work"}},
		Commitments: []workroom.Commitment{{Request: "request", Requester: "requester", AddressedTo: "addressee", Status: "open"}},
	}
	summary := Build("genesis", "head", 1, projection)
	if len(summary.Actionable) != 1 || summary.Actionable[0].Status != "open" || summary.Actionable[0].Performer != "" || summary.Actionable[0].WaitingOn != "" {
		t.Fatalf("open request view invented or hid assignment: %+v", summary)
	}
	if len(summary.Attention) != 0 {
		t.Fatalf("open request was classified as terminal attention: %+v", summary.Attention)
	}
}

func TestWarmDepth20000SummaryLatencyAndSize(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation is not a production latency measurement")
	}
	projection := growthProjection(20000)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(Build("genesis", "head", 20000, projection))
	}))
	defer server.Close()
	durations := make([]time.Duration, 0, 100)
	for range 100 {
		started := time.Now()
		response, err := http.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(body) >= 64<<10 {
			t.Fatalf("warm summary response = %d bytes, want < 64 KiB", len(body))
		}
		durations = append(durations, time.Since(started))
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	p99 := durations[98]
	t.Logf("warm depth-20k bounded summary p99: %s", p99)
	if p99 >= 500*time.Millisecond {
		t.Fatalf("warm depth-20k summary p99 = %s, want < 500ms", p99)
	}
}
