package perflane

import "fmt"

type BenchmarkCase struct {
	Scenario       string
	Depth          int
	CheckpointTail *int
	Concurrency    int
}

func (c BenchmarkCase) Name() string {
	name := fmt.Sprintf("%s/depth-%06d", c.Scenario, c.Depth)
	if c.CheckpointTail != nil {
		name += fmt.Sprintf("/tail-%04d", *c.CheckpointTail)
	}
	if c.Concurrency > 0 {
		name += fmt.Sprintf("/concurrency-%02d", c.Concurrency)
	}
	return name
}

// BenchmarkCases expands the contract in stable scenario, depth, then tail
// order. Checkpoint tails apply only to checkpoint_restart.
func BenchmarkCases(contract Contract) ([]BenchmarkCase, error) {
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	count := (len(contract.Scenarios)-2)*len(contract.Depths) + len(contract.CheckpointCases) + len(contract.Depths)*len(contract.Concurrency)
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
			cases = append(cases, BenchmarkCase{Scenario: scenario, Depth: depth})
		}
	}
	return cases, nil
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
