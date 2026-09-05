package app

import (
	"context"
	"time"
)

// LandingInput contains already projected receipt facts, never an assertion
// discovered by searching body text. Git measurements cannot close a promise.
type LandingInput struct {
	TargetRepo string
	TargetRef  string
	MergeHead  string
}

// LandingMeasurement is a local observation, separate from durable delivery.
// Nullable booleans deliberately distinguish unknown from a measured false.
// RemoteHead names a local tracking observation; no network request is made.
type LandingMeasurement struct {
	MeasuredAt      int64  `json:"measured_at"`
	State           string `json:"state"`
	TargetHead      string `json:"target_head,omitempty"`
	RefIncorporated *bool  `json:"ref_incorporated"`
	Remote          string `json:"remote,omitempty"`
	RemoteRef       string `json:"remote_ref,omitempty"`
	RemoteHead      string `json:"remote_head,omitempty"`
	RemoteContains  *bool  `json:"remote_contains"`
	Reason          string `json:"reason,omitempty"`
}

const LandingMeasurementLimit = 128

// MeasureLandings uses one ref inventory, one object batch and one ancestry
// graph for the bounded rows selected by the caller. It does not cache mutable
// ref facts in a durable snapshot, so a force-move at unchanged seq is visible.
func (w *Workspace) MeasureLandings(ctx context.Context, inputs []LandingInput) []LandingMeasurement {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := make([]LandingMeasurement, len(inputs))
	if len(inputs) == 0 {
		return result
	}
	g := readLandingRefs(ctx, w.Repo)
	remote := landingRemote(ctx, w.Repo)
	var targets, tips, objects []string
	for i, input := range inputs {
		if i == LandingMeasurementLimit {
			break
		}
		if input.TargetRepo != "git:"+w.config.ObjectFormat+":"+w.config.Genesis {
			continue
		}
		targets = append(targets, input.TargetRef)
		tips = append(tips, g.refs[input.TargetRef])
		objects = append(objects, input.MergeHead)
	}
	tracking := remoteTrackingRefs(ctx, w.Repo, remote, targets)
	for _, ref := range tracking {
		tips = append(tips, g.refs[ref])
	}
	g.load(ctx, w.Repo, tips, objects)
	measuredAt := time.Now().Unix()
	for i, input := range inputs {
		if i >= LandingMeasurementLimit {
			result[i] = LandingMeasurement{MeasuredAt: measuredAt, State: "unknown", Reason: "landing row limit exceeded"}
			continue
		}
		result[i] = measureLanding(g, input, "git:"+w.config.ObjectFormat+":"+w.config.Genesis, remote, tracking, measuredAt)
	}
	return result
}

func measureLanding(g *landingGraph, input LandingInput, repository, remote string, tracking map[string]string, measuredAt int64) LandingMeasurement {
	measurement := LandingMeasurement{MeasuredAt: measuredAt, State: "unknown"}
	switch {
	case input.TargetRepo != repository:
		measurement.Reason = "target belongs to another repository"
	case !g.refsKnown:
		measurement.Reason = "target refs unavailable"
	default:
		measurement.TargetHead = g.refs[input.TargetRef]
		measurement.Remote, measurement.RemoteRef = remote, tracking[input.TargetRef]
		measurement.RemoteHead = g.refs[measurement.RemoteRef]
		measurement.RemoteContains = g.contains(measurement.RemoteHead, input.MergeHead)
		switch {
		case measurement.TargetHead == "":
			measurement.State = "target_gone"
		case input.MergeHead == "":
			measurement.State = "no_receipt"
		default:
			measurement.RefIncorporated = g.contains(measurement.TargetHead, input.MergeHead)
			if measurement.RefIncorporated == nil {
				measurement.Reason = "ancestry unavailable or inspection bound reached"
			} else if *measurement.RefIncorporated {
				measurement.State = "incorporated"
			} else {
				measurement.State = "landed-then-removed"
			}
		}
	}
	return measurement
}
