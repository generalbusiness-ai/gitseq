// Package custody is deliberately above the sequencing kernel. It interprets
// opaque events as an acyclic offer -> accept -> settle saga.
package custody

import (
	"errors"
	"fmt"
)

const (
	Offer       = "offer"
	Accept      = "accept"
	Settle      = "settle"
	Resolved    = "resolved"
	Disputed    = "disputed"
	Ineffective = "ineffective"
	Settled     = "settled"
)

type Body struct {
	Type  string `json:"type"`
	Asset string `json:"asset"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type Record struct {
	ID      string
	Log     string
	RestsOn []string
	Body    Body
}

type Decision struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type State struct {
	Asset     string     `json:"asset"`
	Owner     string     `json:"owner"`
	Status    string     `json:"status"`
	Decisions []Decision `json:"decisions"`
}

func oneReference(record Record) (string, bool) {
	return first(record.RestsOn), len(record.RestsOn) == 1
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Fold keeps policy out of Git: logs establish identity, ordering and causal
// references; this projection decides whether those facts transfer custody.
// Ambiguity is data: competing completed settlements produce a disputed state
// and a decision for every event, never a partial projection or fold error.
func Fold(asset, initialOwner string, records []Record) (State, error) {
	if asset == "" || initialOwner == "" {
		return State{}, errors.New("asset and initial owner are required")
	}
	byID := make(map[string]Record, len(records))
	for _, record := range records {
		if record.ID == "" || record.Log == "" || record.Body.Asset != asset {
			return State{}, fmt.Errorf("invalid record %q", record.ID)
		}
		if _, exists := byID[record.ID]; exists {
			return State{}, fmt.Errorf("duplicate record %q", record.ID)
		}
		byID[record.ID] = record
	}

	decisions := make(map[string]Decision, len(records))
	type completed struct {
		settlement Record
		acceptance Record
		offer      Record
	}
	var completedSagas []completed
	for _, settlement := range records {
		if settlement.Body.Type != Settle {
			continue
		}
		acceptID, ok := oneReference(settlement)
		acceptance, found := byID[acceptID]
		if !ok || !found || acceptance.Body.Type != Accept {
			decisions[settlement.ID] = Decision{ID: settlement.ID, Status: Ineffective, Reason: "settlement does not reference one acceptance"}
			continue
		}
		offerID, ok := oneReference(acceptance)
		offer, found := byID[offerID]
		if !ok || !found || offer.Body.Type != Offer {
			decisions[settlement.ID] = Decision{ID: settlement.ID, Status: Ineffective, Reason: "acceptance does not reference one offer"}
			continue
		}
		matching := offer.Body.From == settlement.Body.From && offer.Body.To == settlement.Body.To &&
			acceptance.Body.From == offer.Body.From && acceptance.Body.To == offer.Body.To &&
			offer.Log == offer.Body.From && acceptance.Log == offer.Body.To && settlement.Log == offer.Body.From
		if !matching {
			decisions[settlement.ID] = Decision{ID: settlement.ID, Status: Ineffective, Reason: "party or authority log mismatch"}
			continue
		}
		completedSagas = append(completedSagas, completed{settlement: settlement, acceptance: acceptance, offer: offer})
	}
	// Custody orders itself. A transfer is authorized by the settlement that
	// gave its offeror custody, and the offer names that settlement, so the
	// chain is written down rather than inferred from the order a caller
	// happened to supply. An offer naming nothing claims the initial owner's
	// custody, of which there is exactly one; an offer naming something this
	// log does not carry as a settlement is refused rather than silently
	// stranded, so the projection says why. Two sagas naming the same basis
	// are a genuine double spend and project disputed; a saga naming a basis
	// that granted custody to somebody else, or no basis while somebody else
	// holds, is ineffective on its own evidence and disputes nothing.
	byBasis := make(map[string][]completed, len(completedSagas))
	for _, saga := range completedSagas {
		basis, ok := custodyBasis(saga.offer)
		if !ok {
			decisions[saga.settlement.ID] = Decision{ID: saga.settlement.ID, Status: Ineffective, Reason: "offer does not name one custody basis"}
			continue
		}
		if basis != "" {
			named, found := byID[basis]
			if !found || named.Body.Type != Settle {
				decisions[saga.settlement.ID] = Decision{ID: saga.settlement.ID, Status: Ineffective, Reason: "offer names an unknown custody basis"}
				continue
			}
		}
		byBasis[basis] = append(byBasis[basis], saga)
	}

	owner := initialOwner
	basis := ""
	granted := map[string]bool{}
	for {
		// Only the holder can transfer. A saga naming this basis while
		// somebody else holds is ineffective on its own evidence, and is
		// therefore not a competitor: it cannot make a legitimate transfer
		// disputed.
		var candidates []completed
		for _, saga := range byBasis[basis] {
			if saga.offer.Body.From != owner {
				decisions[saga.settlement.ID] = Decision{ID: saga.settlement.ID, Status: Ineffective, Reason: "offeror did not hold custody"}
				continue
			}
			candidates = append(candidates, saga)
		}
		if len(candidates) == 0 {
			break
		}
		if len(candidates) > 1 {
			for _, disputed := range candidates {
				for _, record := range []Record{disputed.offer, disputed.acceptance, disputed.settlement} {
					decisions[record.ID] = Decision{ID: record.ID, Status: Disputed, Reason: "part of competing completed custody sagas"}
				}
			}
			return projectedState(asset, owner, Disputed, records, decisions), nil
		}
		saga := candidates[0]
		if granted[saga.settlement.ID] {
			return State{}, fmt.Errorf("custody basis cycle at %q", saga.settlement.ID)
		}
		granted[saga.settlement.ID] = true
		owner = saga.offer.Body.To
		basis = saga.settlement.ID
		decisions[saga.offer.ID] = Decision{ID: saga.offer.ID, Status: Settled}
		decisions[saga.acceptance.ID] = Decision{ID: saga.acceptance.ID, Status: Settled}
		decisions[saga.settlement.ID] = Decision{ID: saga.settlement.ID, Status: Settled}
	}
	for _, saga := range completedSagas {
		if _, decided := decisions[saga.settlement.ID]; !decided {
			decisions[saga.settlement.ID] = Decision{ID: saga.settlement.ID, Status: Ineffective, Reason: "offeror did not hold custody"}
		}
	}
	return projectedState(asset, owner, Resolved, records, decisions), nil
}

// custodyBasis reports the settlement an offer names as the source of its
// offeror's custody. No reference claims the initial owner's custody; more
// than one names nothing in particular and is refused rather than guessed.
func custodyBasis(offer Record) (string, bool) {
	switch len(offer.RestsOn) {
	case 0:
		return "", true
	case 1:
		return offer.RestsOn[0], true
	default:
		return "", false
	}
}

func projectedState(asset, owner, status string, records []Record, decisions map[string]Decision) State {
	ordered := make([]Decision, 0, len(records))
	for _, record := range records {
		decision, exists := decisions[record.ID]
		if !exists {
			decision = Decision{ID: record.ID, Status: Ineffective, Reason: "not part of a completed custody saga"}
		}
		ordered = append(ordered, decision)
	}
	return State{Asset: asset, Owner: owner, Status: status, Decisions: ordered}
}
