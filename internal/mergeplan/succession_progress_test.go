package mergeplan

import (
	"maps"
	"slices"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPendingSuccessionRequiresTheSignedContentAndKeepsHistoricalTestimony(t *testing.T) {
	for _, change := range []string{"none", "staleness", "words", "body", "citation order", "signer", "ineffective", "retired receipt", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			acts := SuccessionActs("approval", "", "", "candidate", Target{Repo: "repo", Ref: "refs/heads/main", PreHead: "base"},
				"merged", "", false, Succession{Publish: []string{"a.go"}, Retire: map[string]string{"old": "a.go"}})
			projection := workroom.Projection{Provenance: map[string][]string{}}
			ids := []string{"receipt", "successor", "retirement"}
			known := map[string]string{}
			for i, entry := range acts {
				act := entry.Act
				rests := make([]string, len(act.RestsOn))
				for j, ref := range act.RestsOn {
					rests[j] = resolveProspective(ref, known)
				}
				projection.Decisions = append(projection.Decisions, workroom.Decision{Event: ids[i], Verdict: workroom.Effective})
				if act.Verb == app.VerbState {
					projection.Statements = append(projection.Statements, workroom.Statement{Event: ids[i], Actor: "builder", Kind: act.Kind, Text: act.Text, Body: maps.Clone(act.Body)})
				} else {
					rests = append([]string{act.Target}, rests...)
					projection.Acts = append(projection.Acts, workroom.Act{Event: ids[i], Actor: "builder", Type: "supersede", Text: act.Text, Target: act.Target})
				}
				projection.Provenance[ids[i]] = rests
				if entry.Label != "" {
					known[entry.Label] = ids[i]
				}
			}
			switch change {
			case "staleness":
				projection.Statements[0].Body[app.StaleBasesField] = "the historical note"
				projection.Statements[1].Body["dead_basis_override"] = "true"
				projection.Statements[1].Retired = true // a later delivery must not recreate it
			case "words":
				projection.Statements[1].Text = "different publication"
			case "body":
				projection.Statements[1].Body["commit"] = "another-head"
			case "citation order":
				slices.Reverse(projection.Provenance["retirement"])
			case "signer":
				projection.Statements[1].Actor = "stranger"
			case "ineffective":
				projection.Decisions[1].Verdict = workroom.Ineffective
			case "retired receipt":
				projection.Statements[0].Retired = true
			case "duplicate":
				copy := projection.Statements[1]
				copy.Event = "duplicate"
				projection.Statements = append(projection.Statements, copy)
				projection.Decisions = append(projection.Decisions, workroom.Decision{Event: copy.Event, Verdict: workroom.Effective})
				projection.Provenance[copy.Event] = projection.Provenance["successor"]
			}
			pending, err := PendingSuccession(projection, "builder", acts)
			switch change {
			case "retired receipt", "duplicate":
				if err == nil {
					t.Fatal("unusable historical suffix was accepted")
				}
			case "none", "staleness":
				if err != nil || len(pending) != 0 {
					t.Fatalf("complete historical suffix = %v, %v", pending, err)
				}
			default:
				if err != nil || len(pending) == 0 {
					t.Fatalf("different or ineffective act counted as completion: %v, %v", pending, err)
				}
			}
		})
	}
}
