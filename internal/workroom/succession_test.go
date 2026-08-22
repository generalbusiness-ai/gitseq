package workroom

import (
	"fmt"
	"math"
	"testing"
)

// successionSeed is the smallest roster that lets an agent's artifacts be
// effective: the operator seeds itself, admits the agent, and ratifies it.
func successionSeed(t testing.TB) []Record {
	t.Helper()
	return []Record{
		event(t, "seed", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "agent-joins", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "seed"),
		event(t, "agent-ratified", operator, SchemaRatify, Ratify{Target: "agent-joins"}, "agent-joins"),
	}
}

// samePathArtifacts builds a history of artifacts all standing at one path,
// which is the adversarial shape for succession accounting: every artifact is
// a predecessor of every later one. retireEvery > 0 retires every nth
// artifact, so the live count grows more slowly than the artifact count and a
// naive "count everything seen" shortcut would diverge from the truth.
func samePathArtifacts(t testing.TB, count, retireEvery int) []Record {
	t.Helper()
	records := successionSeed(t)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("artifact-%d", i)
		records = append(records, event(t, id, agent, SchemaState, State{
			Kind: KindArtifact,
			Text: "implementation",
			Body: map[string]string{"path": "internal/workroom", "commit": fmt.Sprintf("head-%d", i)},
		}, "seed"))
		if retireEvery > 0 && i%retireEvery == 0 {
			records = append(records, event(t, "retire-"+id, agent, SchemaSupersede,
				Supersede{Target: id, Text: "retired"}, id))
		}
	}
	return records
}

// The counts a reader acts on. LivePredecessors answers "how many live
// artifacts does this one stand in front of", counted independently per
// artifact, so a shared ancestor is counted once for each successor.
// OmittedSupersessions answers the different question of how many live
// artifacts are owed a retirement, counting each owing artifact once.
// Conflating the two is the mistake these cases exist to catch.
func TestSamePathSuccessionCounts(t *testing.T) {
	tests := []struct {
		name                 string
		records              func(t testing.TB) []Record
		livePredecessors     []int
		omittedSupersessions int
	}{
		{
			name:                 "nothing retired",
			records:              func(t testing.TB) []Record { return samePathArtifacts(t, 4, 0) },
			livePredecessors:     []int{0, 1, 2, 3},
			omittedSupersessions: 3,
		},
		{
			name:                 "every artifact retired",
			records:              func(t testing.TB) []Record { return samePathArtifacts(t, 4, 1) },
			livePredecessors:     []int{0, 0, 0, 0},
			omittedSupersessions: 0,
		},
		{
			name:                 "every other artifact retired",
			records:              func(t testing.TB) []Record { return samePathArtifacts(t, 4, 2) },
			livePredecessors:     []int{0, 0, 1, 1},
			omittedSupersessions: 1,
		},
		{
			// A retirement in the middle must not hide the live ancestor in
			// front of it: with A live, B retired and C live, C still stands
			// in front of A. Tracking only the immediate predecessor gets
			// this wrong, and a running count must not reintroduce that.
			name: "a retirement between two live artifacts hides neither",
			records: func(t testing.TB) []Record {
				records := samePathArtifacts(t, 3, 0)
				return append(records, event(t, "retire-middle", agent, SchemaSupersede,
					Supersede{Target: "artifact-1", Text: "retired"}, "artifact-1"))
			},
			livePredecessors:     []int{0, 1, 1},
			omittedSupersessions: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := Fold(test.records(t))
			if len(projection.Artifacts) != len(test.livePredecessors) {
				t.Fatalf("artifacts = %d, want %d", len(projection.Artifacts), len(test.livePredecessors))
			}
			for i, artifact := range projection.Artifacts {
				want := test.livePredecessors[i]
				if artifact.LivePredecessors != want {
					t.Errorf("artifact %d LivePredecessors = %d, want %d", i, artifact.LivePredecessors, want)
				}
				if artifact.SuccessionUnrecorded != (want > 0) {
					t.Errorf("artifact %d SuccessionUnrecorded = %v, want %v", i, artifact.SuccessionUnrecorded, want > 0)
				}
			}
			if projection.OmittedSupersessions != test.omittedSupersessions {
				t.Errorf("OmittedSupersessions = %d, want %d", projection.OmittedSupersessions, test.omittedSupersessions)
			}
		})
	}
}

// A retirement recorded after every artifact still applies to the artifact it
// names, because the projection runs over a fully folded log. Any accounting
// that decides an artifact's live count from the records seen so far, rather
// than from final retirement, gets a different answer here than in the
// interleaved case above — and the same answer is the whole point.
func TestSamePathSuccessionIgnoresRetirementOrder(t *testing.T) {
	interleaved := Fold(samePathArtifacts(t, 6, 2))

	trailing := samePathArtifacts(t, 6, 0)
	for i := 0; i < 6; i += 2 {
		id := fmt.Sprintf("artifact-%d", i)
		trailing = append(trailing, event(t, "retire-"+id, agent, SchemaSupersede,
			Supersede{Target: id, Text: "retired"}, id))
	}
	trailingProjection := Fold(trailing)

	if len(interleaved.Artifacts) != len(trailingProjection.Artifacts) {
		t.Fatalf("artifact counts differ: %d and %d", len(interleaved.Artifacts), len(trailingProjection.Artifacts))
	}
	for i := range interleaved.Artifacts {
		if interleaved.Artifacts[i].LivePredecessors != trailingProjection.Artifacts[i].LivePredecessors {
			t.Errorf("artifact %d LivePredecessors = %d interleaved, %d trailing",
				i, interleaved.Artifacts[i].LivePredecessors, trailingProjection.Artifacts[i].LivePredecessors)
		}
	}
	if interleaved.OmittedSupersessions != trailingProjection.OmittedSupersessions {
		t.Errorf("OmittedSupersessions = %d interleaved, %d trailing",
			interleaved.OmittedSupersessions, trailingProjection.OmittedSupersessions)
	}
}

// Artifacts at different paths are not each other's predecessors, so a
// per-path count must stay per path.
func TestSuccessionCountsDoNotCrossPaths(t *testing.T) {
	records := append(successionSeed(t),
		event(t, "a1", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "alpha", "commit": "h1"}}, "seed"),
		event(t, "b1", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "beta", "commit": "h2"}}, "seed"),
		event(t, "a2", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "alpha", "commit": "h3"}}, "seed"),
	)
	projection := Fold(records)
	want := []int{0, 0, 1}
	for i, artifact := range projection.Artifacts {
		if artifact.LivePredecessors != want[i] {
			t.Errorf("artifact %d (%s) LivePredecessors = %d, want %d", i, artifact.Path, artifact.LivePredecessors, want[i])
		}
	}
	if projection.OmittedSupersessions != 1 {
		t.Errorf("OmittedSupersessions = %d, want 1", projection.OmittedSupersessions)
	}
}

// spreadPathArtifacts is the control for the gate below: the same number of
// artifacts and the same record count, each at its own path, so no artifact is
// any other's predecessor. Every linear cost in the fold is the same as in the
// same-path history; the only difference is succession accounting.
func spreadPathArtifacts(t testing.TB, count int) []Record {
	t.Helper()
	records := successionSeed(t)
	for i := 0; i < count; i++ {
		records = append(records, event(t, fmt.Sprintf("artifact-%d", i), agent, SchemaState, State{
			Kind: KindArtifact,
			Text: "implementation",
			Body: map[string]string{"path": fmt.Sprintf("path-%d", i), "commit": fmt.Sprintf("head-%d", i)},
		}, "seed"))
	}
	return records
}

// The gate. A ratio against a control is used rather than an absolute time or
// a size-to-size ratio, because both of those measure the whole fold, whose
// other work is linear and large enough to mask the term under test: before
// this was fixed, quadrupling the same-path history multiplied the total by
// under 7, which no honest threshold separates from linear. Comparing
// same-path against spread-path cancels that shared work. Two histories of
// equal size fold in comparable time when succession accounting is linear, and
// diverge without bound when it is quadratic.
//
// Measured on this package: before the fix, 748ms against 89ms, a ratio of
// 8.4. After it, 87ms against 92ms, a ratio of 0.94. The threshold sits an
// order of magnitude below the defect and well above the noise, and the
// minimum of several runs is used because contention only ever adds time.
func TestSamePathSuccessionAccountingStaysLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("the gate folds two 32000-artifact histories")
	}
	const (
		artifacts = 32000
		runs      = 3
		// Linear accounting measured 0.94. Quadratic measured 8.4.
		maxRatio = 3.0
	)
	best := func(records []Record) float64 {
		lowest := math.Inf(1)
		for range runs {
			result := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					Fold(records)
				}
			})
			if seconds := result.T.Seconds() / float64(result.N); seconds < lowest {
				lowest = seconds
			}
		}
		return lowest
	}
	same := best(samePathArtifacts(t, artifacts, 0))
	spread := best(spreadPathArtifacts(t, artifacts))
	if ratio := same / spread; ratio > maxRatio {
		t.Errorf("folding %d artifacts at one path took %.3fs against %.3fs for the same artifacts at distinct paths, a ratio of %.2f, want at most %.1f: succession accounting is not linear in the artifacts at a path",
			artifacts, same, spread, ratio, maxRatio)
	}
}

func BenchmarkFoldSamePathArtifacts(b *testing.B) {
	for _, count := range []int{500, 2000, 8000} {
		records := samePathArtifacts(b, count, 0)
		b.Run(fmt.Sprintf("artifacts-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Fold(records)
			}
		})
	}
}
