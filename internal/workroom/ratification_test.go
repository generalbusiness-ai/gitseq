package workroom

import "testing"

// Which ratification is in force cannot be worked out from the projected acts:
// they carry no retirement, so the first effective ratification of a target may
// be one that was withdrawn, and so may the last. The two histories below are
// the pair that tells the rules apart. Any single one of them is passed by two
// of the three candidate rules, which is how a UI shipped first-effective and
// how last-effective would have looked like the fix.
func TestProjectedRatificationIsTheSurvivingOne(t *testing.T) {
	seed := []Record{
		event(t, "seed", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "proposal", operator, SchemaState, State{Kind: KindPropose, Text: "Adopt something"}, "seed"),
	}
	tests := []struct {
		name    string
		records []Record
		want    string
	}{
		{
			// The case the thread view got wrong: it named the withdrawn one.
			name: "a later ratification replaces a withdrawn earlier one",
			records: append(append([]Record(nil), seed...),
				event(t, "ratify-a", operator, SchemaRatify, Ratify{Target: "proposal"}, "proposal"),
				event(t, "withdraw-a", operator, SchemaSupersede, Supersede{Target: "ratify-a", Text: "withdrawn"}, "ratify-a"),
				event(t, "ratify-b", operator, SchemaRatify, Ratify{Target: "proposal"}, "proposal"),
			),
			want: "ratify-b",
		},
		{
			// The case last-effective gets wrong: the newest ratification is
			// the withdrawn one, and the earlier one still stands.
			name: "withdrawing the later ratification falls back to the earlier",
			records: append(append([]Record(nil), seed...),
				event(t, "ratify-a", operator, SchemaRatify, Ratify{Target: "proposal"}, "proposal"),
				event(t, "ratify-b", operator, SchemaRatify, Ratify{Target: "proposal"}, "proposal"),
				event(t, "withdraw-b", operator, SchemaSupersede, Supersede{Target: "ratify-b", Text: "withdrawn"}, "ratify-b"),
			),
			want: "ratify-a",
		},
		{
			name: "withdrawing every ratification leaves none",
			records: append(append([]Record(nil), seed...),
				event(t, "ratify-a", operator, SchemaRatify, Ratify{Target: "proposal"}, "proposal"),
				event(t, "withdraw-a", operator, SchemaSupersede, Supersede{Target: "ratify-a", Text: "withdrawn"}, "ratify-a"),
			),
			want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := Fold(test.records)
			var proposal *Statement
			for i := range projection.Statements {
				if projection.Statements[i].Event == "proposal" {
					proposal = &projection.Statements[i]
				}
			}
			if proposal == nil {
				t.Fatal("the proposal is not in the projection")
			}
			if proposal.RatifiedBy != test.want {
				t.Errorf("RatifiedBy = %q, want %q", proposal.RatifiedBy, test.want)
			}
			// Ratified is this field being non-empty, and the point of reading
			// both from one call is that they cannot disagree.
			if proposal.Ratified != (test.want != "") {
				t.Errorf("Ratified = %v but RatifiedBy = %q", proposal.Ratified, proposal.RatifiedBy)
			}
		})
	}
}
