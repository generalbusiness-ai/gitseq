package workroom

import "testing"

// Characterize the currently shipped @1 meaning before considering the older
// general-retirement proposal. All records are synthetic; no log is written.
func TestPlannerRetirementScope(t *testing.T) {
	for _, approved := range []bool{false, true} {
		for _, abandoned := range []bool{false, true} {
			name := "unapproved"
			if approved {
				name = "approved"
			}
			if abandoned {
				name += "-explicit-abandon"
			} else {
				name += "-bare"
			}
			t.Run(name, func(t *testing.T) {
				req := lid("request")
				records := landingWorld(t, event(t, req, operator, SchemaStateV3, State{Kind: KindRequest, Text: "unfinished work", Body: landingBody(agent)}, lid("w0")))
				if approved {
					records = landingRound(t, landingBody(agent)).ratifiedApproval(t).records
				}
				body := map[string]string{}
				if abandoned {
					body["disposition"] = "abandoned"
				}
				records = append(records, event(t, lid("retire"), operator, SchemaSupersedeV1, SupersedeV1{Target: req, Text: "disposition probe", Body: body}, req))
				p := Fold(records)
				d, ok := p.Decision(req)
				if !ok || d.Verdict != Effective {
					t.Fatalf("invalid fixture request: %+v", d)
				}
				d, ok = p.Decision(lid("retire"))
				if !ok {
					t.Fatal("no retirement decision")
				}
				expected := Effective
				if approved != abandoned {
					expected = Ineffective
				}
				if d.Verdict != expected {
					t.Fatalf("current-contract expectation changed: %+v", d)
				}
				for _, c := range p.Commitments {
					if c.Request == req {
						t.Logf("verdict=%s reason=%q status=%s successor=%q terminal=%q", d.Verdict, d.Reason, c.Status, c.SuccessorRequest, c.Terminal)
					}
				}
			})
		}
	}
}
