package workroom

import "testing"

// One projection literal carries every shape at once, so each classification
// below is asserted against the same world and a branch that starts classifying
// its neighbours' cases fails here rather than in production.
func TestDeadBasesClassifiesOnlyAlreadyDeadCitations(t *testing.T) {
	const (
		retiredStatement = "git:sha1:g#git:sha1:retired"
		staleStatement   = "git:sha1:g#git:sha1:stale"
		liveStatement    = "git:sha1:g#git:sha1:live"
		retiredArtifact  = "git:sha1:g#git:sha1:retired-artifact"
		staleArtifact    = "git:sha1:g#git:sha1:stale-artifact"
		effectiveAct     = "git:sha1:g#git:sha1:supersede-act"
	)
	projection := Projection{
		Statements: []Statement{
			{Event: retiredStatement, Kind: KindAssert, Retired: true},
			{Event: staleStatement, Kind: KindAssert, Stale: true},
			{Event: liveStatement, Kind: KindAssert},
		},
		Artifacts: []Artifact{
			{Event: retiredArtifact, Path: "docs/a.md", Retired: true},
			{Event: staleArtifact, Path: "docs/b.md", Stale: true},
		},
		Acts: []Act{
			{Event: effectiveAct, Type: "supersede", Target: retiredStatement, Verdict: Effective},
			{Event: "git:sha1:g#git:sha1:failed-supersede", Type: "supersede", Target: liveStatement, Verdict: Ineffective},
			{Event: "git:sha1:g#git:sha1:ratify-act", Type: "ratify", Target: liveStatement, Verdict: Effective},
		},
	}

	dead := DeadBases(projection, []string{
		retiredStatement, staleStatement, retiredArtifact, staleArtifact,
		effectiveAct, liveStatement, "git:sha1:g#git:sha1:nothing",
		// Cited although expected absent, so a classification that starts
		// firing on them fails here instead of passing silently.
		"git:sha1:g#git:sha1:failed-supersede", "git:sha1:g#git:sha1:ratify-act",
	})
	want := map[string]DeadBasis{
		retiredStatement: DeadBasisRetired,
		staleStatement:   DeadBasisStale,
		retiredArtifact:  DeadBasisRetired,
		staleArtifact:    DeadBasisStale,
		effectiveAct:     DeadBasisSupersede,
	}
	if len(dead) != len(want) {
		t.Fatalf("DeadBases = %v, want exactly %v", dead, want)
	}
	for id, basis := range want {
		if dead[id] != basis {
			t.Errorf("%s classified %q, want %q", id, dead[id], basis)
		}
	}
	for _, absent := range []string{liveStatement, "git:sha1:g#git:sha1:nothing",
		"git:sha1:g#git:sha1:failed-supersede", "git:sha1:g#git:sha1:ratify-act"} {
		if basis, reported := dead[absent]; reported {
			t.Errorf("%s was reported dead as %q", absent, basis)
		}
	}
}

// Precedence decides which single name an identifier earns when the projection
// would justify more than one. Retirement is the strongest fact about the event
// itself, and it wins even over an effective supersession, because superseding
// something does not stop it from having been withdrawn in turn. The duplicate
// citation proves the collapse too: one identifier, one entry, whatever the
// author's citation style.
func TestDeadBasesPrefersTheStrongestFactAboutAnEvent(t *testing.T) {
	const bothFlags = "git:sha1:g#git:sha1:both-flags"
	const retiredSupersede = "git:sha1:g#git:sha1:retired-supersede"
	projection := Projection{
		Statements: []Statement{
			{Event: bothFlags, Kind: KindAssert, Retired: true, Stale: true},
			// One identifier cannot really be both a statement and an act; the
			// literal forces the case so the precedence order itself is what
			// the assertion judges.
			{Event: retiredSupersede, Kind: KindAssert, Retired: true},
		},
		Acts: []Act{{Event: retiredSupersede, Type: "supersede", Target: "git:sha1:g#git:sha1:target", Verdict: Effective}},
	}

	if dead := DeadBases(projection, []string{bothFlags}); dead[bothFlags] != DeadBasisRetired {
		t.Errorf("a statement that is both retired and stale was classified %q, want retired", dead[bothFlags])
	}
	dead := DeadBases(projection, []string{retiredSupersede, retiredSupersede})
	if len(dead) != 1 || dead[retiredSupersede] != DeadBasisRetired {
		t.Fatalf("a retired supersession cited twice = %v, want one entry classified retired", dead)
	}
}
