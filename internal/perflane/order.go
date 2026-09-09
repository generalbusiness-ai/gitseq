package perflane

import (
	"fmt"
	"slices"
)

type BenchmarkCase struct {
	Scenario       string
	Depth          int
	CheckpointTail *int
	Concurrency    int
	ActorCount     int
	Fanout         int
}

func (c BenchmarkCase) Name() string {
	name := fmt.Sprintf("%s/depth-%06d", c.Scenario, c.Depth)
	if c.CheckpointTail != nil {
		name += fmt.Sprintf("/tail-%04d", *c.CheckpointTail)
	}
	if c.Concurrency > 0 {
		name += fmt.Sprintf("/concurrency-%02d", c.Concurrency)
	}
	if c.ActorCount > 1 {
		name += fmt.Sprintf("/actors-%03d", c.ActorCount)
	}
	if c.Fanout > 0 {
		name += fmt.Sprintf("/fanout-%03d", c.Fanout)
	}
	return name
}

// BenchmarkCases expands the contract in stable scenario, depth, then tail
// order. Checkpoint tails apply only to checkpoint_restart.
func BenchmarkCases(contract Contract) ([]BenchmarkCase, error) {
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	envelope := envelopeBenchmarkCases(contract)
	count := (len(contract.Scenarios)-2)*len(contract.Depths) - 1 + len(contract.CheckpointCases) + len(contract.Depths)*len(contract.Concurrency) + len(contract.ActorCounts) - 1 + len(envelope) + len(contract.DependencyFanout.Widths)
	cases := make([]BenchmarkCase, 0, count)
	for _, scenario := range contract.Scenarios {
		if scenario == "checkpoint_restart" {
			for _, checkpoint := range contract.CheckpointCases {
				tail := checkpoint.Tail
				cases = append(cases, BenchmarkCase{Scenario: scenario, Depth: checkpoint.Depth, CheckpointTail: &tail})
			}
			continue
		}
		if scenario == "concurrent_read_write" {
			for _, depth := range contract.Depths {
				for _, concurrency := range contract.Concurrency {
					cases = append(cases, BenchmarkCase{Scenario: scenario, Depth: depth, Concurrency: concurrency})
				}
			}
			continue
		}
		for _, depth := range contract.Depths {
			if scenario == "submit_ack" && depth == contract.DependencyFanout.Depth {
				continue
			}
			cases = append(cases, BenchmarkCase{Scenario: scenario, Depth: depth})
		}
	}
	// Keep scale axes independent at the smallest required depth rather than
	// multiplying every depth, scenario, actor count, and fan-out together.
	// The contract's envelope_cases are the explicit exception it names,
	// because separate axes do not prove a joint envelope.
	axisDepth := contract.Depths[0]
	for _, actorCount := range contract.ActorCounts[1:] {
		cases = append(cases, BenchmarkCase{Scenario: "cold_status", Depth: axisDepth, ActorCount: actorCount})
	}
	cases = append(cases, envelope...)
	for _, fanout := range contract.DependencyFanout.Widths {
		cases = append(cases, BenchmarkCase{Scenario: "submit_ack", Depth: contract.DependencyFanout.Depth, Fanout: fanout})
	}
	return cases, nil
}

// envelopeBenchmarkCases expands the contract's joint depth-and-actor cells.
// Each cell contributes the actor-count cost at its own envelope depth. A cell
// whose depth is off the ordinary depth axis also contributes what the depth
// loop never reached there: the one-actor cold read that is the joint cell's
// own denominator, emitted immediately before it so the two comparable cases
// run adjacent, and the cold verify and warm read a joint claim needs.
func envelopeBenchmarkCases(contract Contract) []BenchmarkCase {
	var cases []BenchmarkCase
	for _, envelope := range contract.EnvelopeCases {
		onAxis := slices.Contains(contract.Depths, envelope.Depth)
		if !onAxis {
			cases = append(cases, BenchmarkCase{Scenario: "cold_status", Depth: envelope.Depth})
		}
		cases = append(cases, BenchmarkCase{Scenario: "cold_status", Depth: envelope.Depth, ActorCount: envelope.Actors})
		if !onAxis {
			cases = append(cases,
				BenchmarkCase{Scenario: "warm_status", Depth: envelope.Depth},
				BenchmarkCase{Scenario: "honest_fallback", Depth: envelope.Depth})
		}
	}
	return cases
}

type Revision string

const (
	BaseRevision      Revision = "base"
	CandidateRevision Revision = "candidate"
)

type SampleRun struct {
	Round    int      `json:"round"`
	Position int      `json:"position"`
	Revision Revision `json:"revision"`
}

// AlternatingSampleOrder balances first-run effects by reversing the base and
// candidate order on every other round.
func AlternatingSampleOrder(rounds int) ([]SampleRun, error) {
	if rounds < 1 || rounds > maxRepetitions {
		return nil, fmt.Errorf("rounds must be between 1 and %d", maxRepetitions)
	}
	runs := make([]SampleRun, 0, rounds*2)
	for round := 0; round < rounds; round++ {
		order := [2]Revision{BaseRevision, CandidateRevision}
		if round%2 == 1 {
			order = [2]Revision{CandidateRevision, BaseRevision}
		}
		for position, revision := range order {
			runs = append(runs, SampleRun{Round: round + 1, Position: position + 1, Revision: revision})
		}
	}
	return runs, nil
}
