// Exact source 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 internal/mergeplan/mergeplan.go
// Lines 610-662, followed by 898-982.
		}
		for _, change := range changes {
			if artifactCoversPath(artifact.Path, change.Old) || artifactCoversPath(artifact.Path, change.New) {
				covered = append(covered, artifact)
				break
			}
		}
	}
	return covered
}

func Classify(ctx context.Context, checkout string, projection workroom.Projection, changes []Change, targetPreHead, candidate string, reviewed map[string]bool) (map[string]Candidate, error) {
	classified := make(map[string]Candidate)
	covered := CoveredArtifacts(projection, changes)
	protected := protectionIndex(projection, covered)
	byCommit := make(map[string]bool)
	for _, artifact := range covered {
		inTarget := artifact.Commit == candidate
		if !inTarget && artifact.Commit != "" {
			var checked bool
			inTarget, checked = byCommit[artifact.Commit]
			if !checked {
				_, err := git(ctx, checkout, "merge-base", "--is-ancestor", artifact.Commit, targetPreHead)
				if err != nil {
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 1 {
						return nil, fmt.Errorf("classify artifact %s at %s against target %s: %w", artifact.Event, artifact.Commit, targetPreHead, err)
					}
				}
				inTarget = err == nil
				byCommit[artifact.Commit] = inTarget
			}
		}
		if inTarget {
			if !artifactRetiresForChanges(artifact.Path, changes) {
				classified[artifact.Event] = Candidate{Class: ClassCarried, LeftLive: LeftLive{Class: LeftLiveCarried}}
				continue
			}
			class := ClassInTargetPredecessor
			if artifact.Commit == candidate && reviewed[artifact.Event] {
				class = ClassReviewedCandidate
			}
			classified[artifact.Event] = Candidate{Class: class}
			continue
		}
		if commitment := protected[artifact.Event]; commitment != "" {
			classified[artifact.Event] = Candidate{Class: ClassProtectedSibling, LeftLive: LeftLive{Class: LeftLiveSibling, Commitment: commitment}}
		} else {
			classified[artifact.Event] = Candidate{Class: ClassAbandoned, LeftLive: LeftLive{Class: LeftLiveAbandoned}}
		}
	}
	return classified, nil
}

func protectionIndex(projection workroom.Projection, artifacts []workroom.Artifact) map[string]string {
	statements := make(map[string]workroom.Statement, len(projection.Statements))
	for _, statement := range projection.Statements {
		statements[statement.Event] = statement
	}
	effective := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	active := make(map[string]bool)
	for _, commitment := range projection.Commitments {
		if !unsettledCommitment(commitment.Status) {
			continue
		}
		for _, event := range []string{commitment.Request, commitment.Promise, commitment.Report} {
			statement, found := statements[event]
			if !found || (statement.Lifecycle != workroom.LifecycleRequest && statement.Lifecycle != workroom.LifecyclePromise && statement.Lifecycle != workroom.LifecycleReport) {
				continue
			}
			active[event] = true
		}
	}
	artifactByEvent := make(map[string]workroom.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		artifactByEvent[artifact.Event] = artifact
	}
	protected := make(map[string]string)
	consider := func(artifact, commitment string) {
		if current := protected[artifact]; current == "" || commitment < current {
			protected[artifact] = commitment
		}
	}
	byCommit := make(map[string]string)
	for event := range active {
		statement := statements[event]
		for _, commit := range []string{statement.Body["head"], statement.Body["commit"]} {
			if commit != "" && (byCommit[commit] == "" || event < byCommit[commit]) {
				byCommit[commit] = event
			}
		}
		for reached := range provenanceClosure(projection.Provenance, effective, event) {
			if _, ok := artifactByEvent[reached]; ok {
				consider(reached, event)
			}
		}
	}
	for _, artifact := range artifacts {
		if event := byCommit[artifact.Commit]; artifact.Commit != "" && event != "" {
			consider(artifact.Event, event)
		}
		for reached := range provenanceClosure(projection.Provenance, effective, artifact.Event) {
			if active[reached] {
				consider(artifact.Event, reached)
			}
		}
	}
	return protected
}

func unsettledCommitment(status string) bool {
	switch status {
	case "open", "promised", "reported", "awaiting-review", "awaiting-authorization", "awaiting-landing", "stale":
		return true
	default:
		return false
	}
}

func provenanceClosure(provenance map[string][]string, effective map[string]bool, from string) map[string]bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		event := queue[0]
		queue = queue[1:]
		if decision, projected := effective[event]; projected && !decision {
			continue
		}
		for _, basis := range provenance[event] {
			if !seen[basis] {
				seen[basis] = true
				queue = append(queue, basis)
			}
		}
	}
	return seen
