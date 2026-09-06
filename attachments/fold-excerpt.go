// internal/workroom/fold.go @ 3b71806061c90fe462ba857f788dc1ec7e2d2580; original lines 3168-3186
		// A report carrying a verdict is a review. Which artifact it judges is
		// settled after the loop, because a resolution by reviewed commit may
		// depend on an artifact that has not been read yet.
		if state.Kind == KindReport && state.Body["verdict"] != "" && record.decision.Verdict == Effective {
			head := state.Body["head"]
			if head == "" {
				head = state.Body["commit"]
			}
			projection.Reviews = append(projection.Reviews, Review{
				Report: record.record.ID, Timestamp: record.record.Timestamp, Reviewer: record.record.Actor,
				Verdict: state.Body["verdict"], Head: head, Independence: IndependenceUnresolved,
				Ratified: f.ratified(record.record.ID),
				Retired:  f.retired(record.record.ID), Stale: result.stale[record.record.ID],
			})
			reviewBases = append(reviewBases, reviewBasis{named: state.Body["artifact"], restsOn: record.record.RestsOn})
		}
	}
	resolveReviews(projection.Reviews, reviewBases, implementers, artifactCommits, artifactsByCommit)
	// A live artifact owes its retirement only while a live artifact later at
