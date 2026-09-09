package workroom

// DeadBasis names the one way a citation was already dead when an act rested
// on it. The distinction matters to whoever filed the act: retired means the
// cited record was itself superseded and nothing stands there any more, stale
// means the record still stands but a basis under it was withdrawn, and
// supersede means the citation names an effective supersession — resting on it
// after the fact reads as approving a retirement that already happened, and
// ineffective means the cited record never took force, so nothing it rested
// on reaches through it: staleness stops at an ineffective record, and a
// basis retired underneath one stays out of sight unless the citation is
// named for what it is.
type DeadBasis string

const (
	DeadBasisRetired     DeadBasis = "retired"
	DeadBasisStale       DeadBasis = "stale"
	DeadBasisSupersede   DeadBasis = "supersede"
	DeadBasisIneffective DeadBasis = "ineffective"
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
// unresolved citations separately. A live statement, a live artifact, and an
// unknown identifier all stay out — flagging any of them would teach readers
// to skip the note. A record the fold refused is in: it carries no authority
// and no staleness, so an act resting on it stands on nothing the projection
// will ever flare, and the only moment to say so is when the citation is made.
//
// When one identifier could be read more than one way, the strongest fact
// about the event itself wins: retirement over staleness over ineffectiveness
// over supersession. The last two cannot in fact meet: the fold retires and
// stales only effective records, so an ineffective one is never either.
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
	// Decisions are the one-per-record source of verdicts; a refused
	// statement keeps its row, so the row alone cannot say it never took force.
	ineffective := make(map[string]bool)
	for _, decision := range p.Decisions {
		if decision.Verdict != Effective {
			ineffective[decision.Event] = true
		}
	}
	classify := func(id string) (DeadBasis, bool) {
		switch {
		case statements[id].Retired || artifacts[id].Retired:
			return DeadBasisRetired, true
		case statements[id].Stale || artifacts[id].Stale:
			return DeadBasisStale, true
		case ineffective[id]:
			return DeadBasisIneffective, true
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
