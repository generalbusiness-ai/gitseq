package workroom

// DeadBasis names the one way a citation was already dead when an act rested
// on it. The distinction matters to whoever filed the act: retired means the
// cited record was itself superseded and nothing stands there any more, stale
// means the record still stands but a basis under it was withdrawn, and
// supersede means the citation names an effective supersession — resting on it
// after the fact reads as approving a retirement that already happened.
type DeadBasis string

const (
	DeadBasisRetired   DeadBasis = "retired"
	DeadBasisStale     DeadBasis = "stale"
	DeadBasisSupersede DeadBasis = "supersede"
)

// DeadBases classifies each rest-on citation that this projection already
// shows to be dead, so the surfaces that file an act can say so while the
// author is still looking at the result instead of leaving them to meet the
// retirement in a later review or merge. It answers only what was dead before
// the act landed; whether that should have stopped the act is not for a note
// to decide.
//
// Citations that name nothing in this workroom are absent on purpose: that is
// a different mistake, and the callers that show these notes already report
// unresolved citations separately. A live statement, a live artifact, an
// ineffective supersession, and an unknown identifier all stay out — flagging
// any of them would teach readers to skip the note.
//
// When one identifier could be read more than one way, the strongest fact
// about the event itself wins: retirement over staleness over supersession.
// Staleness is deliberately read only where the row is not retired, because a
// retired statement can carry a stale flag left over from its own life and
// reporting both would bury the news under the history. The same identifier
// twice in restsOn collapses to one entry, for the same reason.
func DeadBases(p Projection, restsOn []string) map[string]DeadBasis {
	statements := make(map[string]Statement, len(p.Statements))
	for _, statement := range p.Statements {
		statements[statement.Event] = statement
	}
	artifacts := make(map[string]Artifact, len(p.Artifacts))
	for _, artifact := range p.Artifacts {
		artifacts[artifact.Event] = artifact
	}
	supersedes := make(map[string]bool)
	for _, act := range p.Acts {
		if act.Type == "supersede" && act.Verdict == Effective {
			supersedes[act.Event] = true
		}
	}
	classify := func(id string) (DeadBasis, bool) {
		switch {
		case statements[id].Retired || artifacts[id].Retired:
			return DeadBasisRetired, true
		case statements[id].Stale || artifacts[id].Stale:
			return DeadBasisStale, true
		case supersedes[id]:
			return DeadBasisSupersede, true
		default:
			return "", false
		}
	}
	dead := make(map[string]DeadBasis)
	for _, id := range restsOn {
		if basis, ok := classify(id); ok {
			dead[id] = basis
		}
	}
	return dead
}
