package statusview

import (
	"errors"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// StalenessWave is the bounded whole-log summary used to measure the effect of
// retiring artifacts at one exact path. It is intentionally not a work gate:
// ordinary stale history stays honest history and cannot be driven to zero.
type StalenessWave struct {
	Frontier      Frontier `json:"frontier"`
	Path          string   `json:"path"`
	Records       int      `json:"records"`
	Reached       int      `json:"reached"`
	LiveArtifacts int      `json:"live_artifacts"`
	Reaching      int      `json:"reaching"`
	Degraded      bool     `json:"degraded,omitempty"`
}

// BuildStalenessWave follows the same causal edges as the fold's ordinary
// staleness propagation: every basis of every effective record, except the
// supersession record's edge to the target it retires. Retired artifacts at
// the selected path remain seeds because history is not erased by retirement.
func BuildStalenessWave(durable app.Snapshot, path string, degraded bool) (StalenessWave, error) {
	if path == "" {
		return StalenessWave{}, errors.New("staleness wave requires one exact artifact path")
	}
	projection := durable.Projection
	effective := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		if decision.Verdict == workroom.Effective {
			effective[decision.Event] = true
		}
	}
	retiredTarget := make(map[string]string)
	for _, act := range projection.Acts {
		if act.Type == string(app.VerbSupersede) && effective[act.Event] {
			retiredTarget[act.Event] = act.Target
		}
	}
	children := make(map[string][]string)
	for event, bases := range projection.Provenance {
		if !effective[event] {
			continue
		}
		for _, basis := range bases {
			if basis != retiredTarget[event] {
				children[basis] = append(children[basis], event)
			}
		}
	}
	reached := make(map[string]bool)
	frontier := make([]string, 0)
	for _, artifact := range projection.Artifacts {
		if artifact.Path == path && !reached[artifact.Event] {
			reached[artifact.Event] = true
			frontier = append(frontier, artifact.Event)
		}
	}
	for len(frontier) > 0 {
		last := len(frontier) - 1
		event := frontier[last]
		frontier = frontier[:last]
		for _, child := range children[event] {
			if !reached[child] {
				reached[child] = true
				frontier = append(frontier, child)
			}
		}
	}
	wave := StalenessWave{
		Frontier: Frontier{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth},
		Path:     Text(path), Records: len(projection.Decisions), Reached: len(reached), Degraded: degraded,
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path == path {
			continue
		}
		wave.LiveArtifacts++
		if reached[artifact.Event] {
			wave.Reaching++
		}
	}
	return wave, nil
}
