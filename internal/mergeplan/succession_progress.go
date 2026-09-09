package mergeplan

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// PendingSuccession removes the already recorded part of the canonical sealed
// suffix. Matching includes the signer, words, body and ordered citations;
// merely finding an artifact at a path is not evidence that this merge filed it.
// Admission's historical staleness testimony is not recomputed for an act that
// already landed. Missing acts still pass through ordinary admission.
func PendingSuccession(projection workroom.Projection, merger string, acts []ProspectiveAct) ([]ProspectiveAct, error) {
	type recorded struct {
		event   string
		retired bool
	}
	indexed := make(map[[32]byte][]recorded)
	effective := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	for _, statement := range projection.Statements {
		if statement.Actor != merger || !effective[statement.Event] ||
			(statement.Kind != workroom.KindAssert && statement.Kind != workroom.KindArtifact) {
			continue
		}
		body := maps.Clone(statement.Body)
		delete(body, app.StaleBasesField)
		delete(body, "dead_basis_override")
		key := successionContentKey(app.Act{Verb: app.VerbState, Kind: statement.Kind,
			Text: statement.Text, Body: body, RestsOn: projection.Provenance[statement.Event]})
		indexed[key] = append(indexed[key], recorded{statement.Event, statement.Retired})
	}
	for _, act := range projection.Acts {
		if act.Actor != merger || act.Type != "supersede" || !effective[act.Event] {
			continue
		}
		key := successionContentKey(app.Act{Verb: app.VerbSupersede, Text: act.Text,
			Target: act.Target, RestsOn: projection.Provenance[act.Event]})
		indexed[key] = append(indexed[key], recorded{event: act.Event})
	}
	known := make(map[string]string)
	pending := make([]ProspectiveAct, 0, len(acts))
	for _, entry := range acts {
		act := entry.Act
		act.RestsOn = append([]string(nil), act.RestsOn...)
		for i, reference := range act.RestsOn {
			if label, ok := strings.CutPrefix(reference, "$"); ok && known[label] != "" {
				act.RestsOn[i] = known[label]
			}
		}
		intent := act
		if act.Verb == app.VerbSupersede {
			intent.RestsOn = append([]string{act.Target}, act.RestsOn...)
		}
		matches := indexed[successionContentKey(intent)]
		if len(matches) > 1 {
			return nil, fmt.Errorf("merge succession has multiple matching acts for %s", act.IdempotencyKey)
		}
		if len(matches) == 1 {
			if entry.Label == "merge" && matches[0].retired {
				return nil, fmt.Errorf("merge receipt %s is retired", matches[0].event)
			}
			if entry.Label != "" {
				known[entry.Label] = matches[0].event
			}
			continue
		}
		entry.Act = act
		pending = append(pending, entry)
	}
	return pending, nil
}

func successionContentKey(act app.Act) [32]byte {
	// Only the signed content of a canonical merge act participates. Signing
	// options and local batch labels are not durable statement fields.
	content := struct {
		Verb    app.Verb
		Kind    workroom.Kind
		Text    string
		Body    map[string]string
		Target  string
		RestsOn []string
	}{act.Verb, act.Kind, act.Text, act.Body, act.Target, act.RestsOn}
	encoded, _ := json.Marshal(content) // strings, slices and maps cannot fail
	return sha256.Sum256(encoded)
}
