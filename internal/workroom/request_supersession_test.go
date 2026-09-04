package workroom

import "testing"

// canonicalMigrationEvidence is a checked transcription of the bounded
// projection at frontier 554fe63a1a448655da237262a429eb72f545189f,
// depth 13605. It records the per-row facts the linked migration actually
// needs, rather than substituting several request ids into one invented happy
// path. The compact records built from it below preserve those exact causal
// identities and relationships while the separate synthetic tests exercise
// the fold rule in isolation.
type canonicalMigrationRow struct {
	request, promise                              string
	artifact, head, verdict, ratification         string
	child, childPromise, childArtifact, childHead string
	exclusion                                     string
}

var canonicalMigrationEvidence = []canonicalMigrationRow{
	{
		request: "d14e96fb65a69c43fcfeeb59ab320cebafe72492", promise: "d2575ac11b3de361c66a197c4b80c4dc2cd24975",
		artifact: "bbdf6c6bccc58a199ea3571991cd608d7551444a", head: "f6489c78a4e9c11460ebe95e3312f1acd1d0de2c",
		verdict: "22db9df60efaa6a4187003e4f478f7955d06d06b", ratification: "40b1cac94fb1aaf0dbb980ee24be4db22e07a14d",
		child: "ae0d3d862dfe21d45a53b220f8834baa46812b75",
	},
	{
		request: "ae0d3d862dfe21d45a53b220f8834baa46812b75", promise: "1abf201be605a2765b39aa1b956dc18430e78bf8",
		artifact: "352f2b9694890cf2024eae96b6b6224f7025aace", head: "93f0b2a6fe2431b42dc38fbd2a03cd65132f0b3e",
		verdict: "679891a2adc5e44f11f1a27494e1742f19e2dce3", ratification: "9b17c939fdd49e1b267ebc009cf7847277c90171",
		exclusion: "no direct repair child exists at the evidence frontier",
	},
	{
		request: "dd883da0ad8de785f77d81706c88c2e8848ab690", promise: "29b95948b1a020dac0862e71dc33160d3af41353",
		artifact: "d8c2e96d67308017207d1a9a5806a46d12c698ab", head: "95374ec2b44779fc712de97c0bec4270c94b9fcb",
		verdict: "c8df0126e0fdd87b0aceeccb2fdb878e93f66c42", ratification: "27a246225f65dfb9a4e3bdb30acfedb134bac1ac",
		child: "48ec44db58cca2d4277c221629c16ae58e25fd67",
	},
	{
		request: "b03f55fec82b7f09cb1ab098b0c34ed73a4cc296", promise: "746388e389f952d7d537be4f4417ca0ef2a43a3e",
		artifact: "9b3f21bd2eab413730d58bf3c0220018d8071c37", head: "e1798050b37e8a3c48f985a672afc1628beff9a2",
		verdict: "924b99038afa9d8d14a8421dfce44a7e9084e74b", ratification: "14600cdd14a5d8a642182688ba1e534b3e420255",
		child: "a599f1a32c9e463b9c956c4852f71fc12c4b9e73",
	},
	{
		request: "ede16d2273f4b33bbf9c029ec7e956a90371d42f", promise: "512acfe1428e25f344a40a946f99f0a860c86bba",
		artifact: "935e4594284bc6ce51aee59553d9edfb109e5222", head: "97a749ecd8a27e57eb6c301f822f105bda391249",
		verdict: "54fb43c0f3df245d2221acb62255ae7f8975d8a8", ratification: "baffb6852a4ca41b4151481209ee173f8e419125",
		exclusion: "no direct repair child exists at the evidence frontier",
	},
	{
		request: "703ca5428934f4d0110d2c7afb7c731885ea9c85", promise: "df068611087472db4215f9aaa450b0bbf6fb2a62",
		artifact: "52e3acfdab5c075526e00992e9e3d1975846b570", head: "6bcb4798e07efe3a8c8de4a495e709520cf099e7",
		verdict: "88e86ac3f1c5a97b46849d9b58eca16e99371002", ratification: "2c2887303505d2c98b5881030a38bd4aae38b987",
		exclusion: "no direct repair child exists at the evidence frontier",
	},
	{
		request: "1396b7b653a0c5c39b5ed0fff92011e2a337fba7", promise: "4f705246ffc7633046795a80229d4a119a7df607",
		artifact: "c7f42da46f2345cab26410b12b612e00c028d5a6", head: "6bcb4798e07efe3a8c8de4a495e709520cf099e7",
		exclusion: "no verdict names its reporting artifact and no direct repair child exists",
	},
	{
		request: "7e55339e548bc1ee5944c83205033e90ca63ea18", promise: "85c73730bffbb7037a484cd158fd423a387411b3",
		child: "467ad4832fb29f7462f19789e4b083654b300728", childPromise: "5ee01d7b937fd6a187c40a00678fcdf7cec7525c",
		childArtifact: "9936cbb28db1642a5cdabd2f787fb881fb33dbf2", childHead: "128132f23c3ea13e7b0ed93e33735f6181531c92",
		exclusion: "the parent has no reporting artifact; the child is reported separately",
	},
	{
		request: "89aeec3a117418fc71b86c19f3b5cd863a0b5166", promise: "639a2dbbc3f13305e155d0ffd5af1065979d17f6",
		artifact: "056b534815acca710d0e007697162a353919874a", head: "b1daa56710e5c8df3d2c7d9035682cb9e99f30be",
		verdict: "74fcc72aaab6b7464206bbb82b0f84e54f81c1e6", ratification: "16b2fbe7196b70d5c90f55fe6162990a78b6e63f",
		child: "cdb992a13208f526a11a1b1cfc32d015bae73352",
	},
}

type rejectedTransferFixture struct {
	records      []Record
	request      string
	artifact     string
	verdict      string
	ratification string
	child        string
	transfer     string
}

func rejectedTransferRecords(t testing.TB, request string) rejectedTransferFixture {
	t.Helper()
	id := func(suffix string) string { return request + ":" + suffix }
	artifact, verdict := id("artifact"), id("changes-requested")
	child, transfer := id("repair-child"), id("transfer")
	records := worldRecords(t,
		event(t, id("reviewer-membership"), operator, SchemaState, State{Kind: KindRoster, Text: "Reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "w0"),
		event(t, id("reviewer-ratified"), operator, SchemaRatify, Ratify{Target: id("reviewer-membership")}, id("reviewer-membership")),
		event(t, request, operator, SchemaState, State{Kind: KindRequest, Text: "Implement it", Body: map[string]string{"to": agent, "conditions": "reviewed exact head"}}, "w0"),
		event(t, id("promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will implement it"}, request),
		event(t, artifact, agent, SchemaState, State{Kind: KindArtifact, Text: "Rejected implementation", Body: map[string]string{"path": "internal/workroom", "commit": "head-rejected"}}, id("promise")),
		event(t, id("review-request"), agent, SchemaState, State{Kind: KindRequest, Text: "Review it", Body: map[string]string{"to": other, "conditions": "exact-head verdict"}}, artifact),
		event(t, id("review-promise"), other, SchemaState, State{Kind: KindPromise, Text: "I will review it"}, id("review-request")),
		event(t, verdict, other, SchemaState, State{Kind: KindReport, Text: "Changes requested", Body: map[string]string{"verdict": "changes-requested", "head": "head-rejected", "artifact": artifact}}, id("review-promise"), artifact),
		event(t, id("verdict-ratified"), agent, SchemaRatify, Ratify{Target: verdict}, verdict),
		event(t, child, operator, SchemaState, State{Kind: KindRequest, Text: "Repair it", Body: map[string]string{"to": agent, "conditions": "correct the rejected head"}}, request),
		event(t, transfer, operator, SchemaSupersede, Supersede{Target: request, Text: "Required repair moved to the child"}, request, child),
	)
	return rejectedTransferFixture{
		records: records, request: request, artifact: artifact, verdict: verdict,
		ratification: id("verdict-ratified"), child: child, transfer: transfer,
	}
}

func commitmentForRequest(t testing.TB, projection Projection, request string) Commitment {
	t.Helper()
	for _, commitment := range projection.Commitments {
		if commitment.Request == request {
			return commitment
		}
	}
	t.Fatalf("commitment %s is missing", request)
	return Commitment{}
}

func replaceFixtureRecord(t testing.TB, records []Record, id string, replacement Record) []Record {
	t.Helper()
	result := append([]Record(nil), records...)
	for index := range result {
		if result[index].ID == id {
			result[index] = replacement
			return result
		}
	}
	t.Fatalf("fixture record %s is missing", id)
	return nil
}

func withoutFixtureRecord(t testing.TB, records []Record, id string) []Record {
	t.Helper()
	result := make([]Record, 0, len(records)-1)
	found := false
	for _, record := range records {
		if record.ID == id {
			found = true
			continue
		}
		result = append(result, record)
	}
	if !found {
		t.Fatalf("fixture record %s is missing", id)
	}
	return result
}

func insertBeforeFixtureChild(records []Record, inserted ...Record) []Record {
	result := append([]Record(nil), records[:len(records)-2]...)
	result = append(result, inserted...)
	return append(result, records[len(records)-2:]...)
}

func canonicalMigrationRecords(t testing.TB, row canonicalMigrationRow) []Record {
	t.Helper()
	records := worldRecords(t,
		event(t, row.request+":reviewer-membership", operator, SchemaState, State{Kind: KindRoster, Text: "Reviewer joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Reviewer", "role": "participant"}}, "w0"),
		event(t, row.request+":reviewer-ratified", operator, SchemaRatify, Ratify{Target: row.request + ":reviewer-membership"}, row.request+":reviewer-membership"),
		event(t, row.request, operator, SchemaState, State{Kind: KindRequest, Text: "Canonical parent", Body: map[string]string{"to": agent, "conditions": "reviewed exact head"}}, "w0"),
		event(t, row.promise, agent, SchemaState, State{Kind: KindPromise, Text: "Canonical promise"}, row.request),
	)
	if row.artifact != "" {
		records = append(records,
			event(t, row.artifact, agent, SchemaState, State{Kind: KindArtifact, Text: "Canonical reporting artifact", Body: map[string]string{"path": "canonical/" + row.request, "commit": row.head}}, row.promise),
		)
	}
	if row.verdict != "" {
		records = append(records,
			event(t, row.request+":review-request", agent, SchemaState, State{Kind: KindRequest, Text: "Review canonical artifact", Body: map[string]string{"to": other, "conditions": "exact-head verdict"}}, row.artifact),
			event(t, row.request+":review-promise", other, SchemaState, State{Kind: KindPromise, Text: "Canonical review promise"}, row.request+":review-request"),
			event(t, row.verdict, other, SchemaState, State{Kind: KindReport, Text: "Changes requested", Body: map[string]string{"verdict": "changes-requested", "head": row.head, "artifact": row.artifact}}, row.request+":review-promise", row.artifact),
			event(t, row.ratification, agent, SchemaRatify, Ratify{Target: row.verdict}, row.verdict),
		)
	}
	if row.child != "" {
		records = append(records,
			event(t, row.child, operator, SchemaState, State{Kind: KindRequest, Text: "Canonical repair child", Body: map[string]string{"to": agent, "conditions": "repair the rejected head"}}, row.request),
		)
	}
	if row.childArtifact != "" {
		records = append(records,
			event(t, row.childPromise, agent, SchemaState, State{Kind: KindPromise, Text: "Canonical child promise"}, row.child),
			event(t, row.childArtifact, agent, SchemaState, State{Kind: KindArtifact, Text: "Child-only reporting artifact", Body: map[string]string{"path": "canonical/child/" + row.request, "commit": row.childHead}}, row.childPromise),
		)
	}
	return records
}

func TestCanonicalRejectedRoundMigrationEvidenceAtFrontier554fe63a(t *testing.T) {
	var qualifying []string
	for _, row := range canonicalMigrationEvidence {
		t.Run(row.request[:8], func(t *testing.T) {
			records := canonicalMigrationRecords(t, row)
			before := commitmentForRequest(t, Fold(records), row.request)
			if before.SuccessorRequest != "" {
				t.Fatalf("current canonical evidence invented a successor = %+v", before)
			}

			if row.exclusion != "" {
				if row.request == "7e55339e548bc1ee5944c83205033e90ca63ea18" {
					if before.Status != "promised" || before.Report != "" {
						t.Fatalf("7e55339e parent evidence = %+v", before)
					}
					records = append(records, event(t, row.request+":prospective-transfer", operator, SchemaSupersede,
						Supersede{Target: row.request, Text: "Try to move the separately reported child"}, row.request, row.child))
					after := commitmentForRequest(t, Fold(records), row.request)
					if after.Status != "cancelled" || after.SuccessorRequest != "" {
						t.Fatalf("non-qualifying 7e55339e transfer = %+v", after)
					}
					child := commitmentForRequest(t, Fold(records), row.child)
					if child.Status != "awaiting-review" || child.Report != row.childArtifact {
						t.Fatalf("separately reported 467ad483 child = %+v", child)
					}
					return
				}
				if before.Status != "awaiting-review" {
					t.Fatalf("excluded current parent = %+v (%s)", before, row.exclusion)
				}
				return
			}

			if before.Status != "awaiting-review" || before.Report != row.artifact {
				t.Fatalf("qualifying current parent evidence = %+v", before)
			}
			records = append(records, event(t, row.request+":prospective-transfer", operator, SchemaSupersede,
				Supersede{Target: row.request, Text: "Move the rejected round to its canonical repair child"}, row.request, row.child))
			after := commitmentForRequest(t, Fold(records), row.request)
			if after.Status != "superseded" || after.SuccessorRequest != row.child ||
				after.Report != row.artifact || after.WaitingOn != "" {
				t.Fatalf("canonical linked transfer = %+v", after)
			}
			qualifying = append(qualifying, row.request)
		})
	}
	want := []string{
		"d14e96fb65a69c43fcfeeb59ab320cebafe72492",
		"dd883da0ad8de785f77d81706c88c2e8848ab690",
		"b03f55fec82b7f09cb1ab098b0c34ed73a4cc296",
		"89aeec3a117418fc71b86c19f3b5cd863a0b5166",
	}
	if len(qualifying) != len(want) {
		t.Fatalf("qualifying canonical rows = %v, want %v", qualifying, want)
	}
	for index := range want {
		if qualifying[index] != want[index] {
			t.Fatalf("qualifying canonical rows = %v, want %v", qualifying, want)
		}
	}
}

func TestLinkedRequestSupersessionProjectsAQualifyingRejectedRound(t *testing.T) {
	fixture := rejectedTransferRecords(t, "request")
	commitment := commitmentForRequest(t, Fold(fixture.records), fixture.request)
	if commitment.Status != "superseded" || commitment.SuccessorRequest != fixture.child ||
		commitment.Report != fixture.artifact || commitment.WaitingOn != "" {
		t.Fatalf("linked rejected commitment = %+v", commitment)
	}
}

func TestLinkedRequestSupersessionProjectsADirectArtifactRound(t *testing.T) {
	fixture := rejectedTransferRecords(t, "request")
	records := withoutFixtureRecord(t, fixture.records, "request:promise")
	records = replaceFixtureRecord(t, records, fixture.artifact,
		event(t, fixture.artifact, agent, SchemaState, State{Kind: KindArtifact, Text: "Direct rejected implementation", Body: map[string]string{"path": "internal/workroom", "commit": "head-rejected"}}, fixture.request))
	commitment := commitmentForRequest(t, Fold(records), fixture.request)
	if commitment.Status != "superseded" || commitment.SuccessorRequest != fixture.child ||
		commitment.Report != fixture.artifact || commitment.Performer != agent || commitment.WaitingOn != "" {
		t.Fatalf("direct linked rejected commitment = %+v", commitment)
	}
}

func TestLinkedRequestSupersessionSealsTheHistoricalTransfer(t *testing.T) {
	fixture := rejectedTransferRecords(t, "request")
	records := append(fixture.records,
		event(t, "child-retired", operator, SchemaSupersede, Supersede{Target: fixture.child, Text: "The repair child stopped"}, fixture.child),
	)
	commitment := commitmentForRequest(t, Fold(records), fixture.request)
	if commitment.Status != "superseded" || commitment.SuccessorRequest != fixture.child {
		t.Fatalf("child retirement rewrote the transfer = %+v", commitment)
	}

	records = append(records,
		event(t, "transfer-retired", operator, SchemaSupersede, Supersede{Target: fixture.transfer, Text: "Restore the parent transfer"}, fixture.transfer),
	)
	commitment = commitmentForRequest(t, Fold(records), fixture.request)
	if commitment.Status != "awaiting-review" || commitment.SuccessorRequest != "" || commitment.Report != fixture.artifact {
		t.Fatalf("retired transfer did not restore the rejected parent = %+v", commitment)
	}
}

func TestLinkedRequestSupersessionRequiresEveryRatifiedCondition(t *testing.T) {
	base := rejectedTransferRecords(t, "request")
	promise := "request:promise"
	reviewPromise := "request:review-promise"
	cases := []struct {
		name       string
		records    func(testing.TB) []Record
		wantStatus string
	}{
		{
			name: "verdict is not changes-requested",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.verdict,
					event(t, base.verdict, other, SchemaState, State{Kind: KindReport, Text: "Approved", Body: map[string]string{"verdict": "approved", "head": "head-rejected", "artifact": base.artifact}}, reviewPromise, base.artifact))
			},
		},
		{
			name: "verdict is unratified",
			records: func(t testing.TB) []Record {
				return withoutFixtureRecord(t, base.records, base.ratification)
			},
		},
		{
			name: "verdict names another artifact",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.verdict,
					event(t, base.verdict, other, SchemaState, State{Kind: KindReport, Text: "Changes requested", Body: map[string]string{"verdict": "changes-requested", "head": "head-rejected", "artifact": "another-artifact"}}, reviewPromise, base.artifact))
			},
		},
		{
			name: "verdict names another head",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.verdict,
					event(t, base.verdict, other, SchemaState, State{Kind: KindReport, Text: "Changes requested", Body: map[string]string{"verdict": "changes-requested", "head": "another-head", "artifact": base.artifact}}, reviewPromise, base.artifact))
			},
		},
		{
			name: "target has no reporting artifact",
			records: func(t testing.TB) []Record {
				records := replaceFixtureRecord(t, base.records, base.artifact,
					event(t, base.artifact, agent, SchemaState, State{Kind: KindAssert, Text: "Not an artifact"}, promise))
				return withoutFixtureRecord(t, withoutFixtureRecord(t, records, base.verdict), base.ratification)
			},
		},
		{
			name: "reporting artifact has no exact head",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.artifact,
					event(t, base.artifact, agent, SchemaState, State{Kind: KindArtifact, Text: "No exact head", Body: map[string]string{"path": "internal/workroom"}}, promise))
			},
		},
		{
			name: "reporting artifact was retired before transfer",
			records: func(t testing.TB) []Record {
				return insertBeforeFixtureChild(base.records,
					event(t, "request:artifact-retired", agent, SchemaSupersede, Supersede{Target: base.artifact, Text: "Artifact withdrawn"}, base.artifact))
			},
		},
		{
			name: "changes-requested verdict was retired before transfer",
			records: func(t testing.TB) []Record {
				return insertBeforeFixtureChild(base.records,
					event(t, "request:verdict-retired", other, SchemaSupersede, Supersede{Target: base.verdict, Text: "Verdict withdrawn"}, base.verdict))
			},
		},
		{
			name: "verdict ratification was retired before transfer",
			records: func(t testing.TB) []Record {
				return insertBeforeFixtureChild(base.records,
					event(t, "request:ratification-retired", agent, SchemaSupersede, Supersede{Target: base.ratification, Text: "Ratification withdrawn"}, base.ratification))
			},
		},
		{
			name: "child is not direct",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.child,
					event(t, base.child, operator, SchemaState, State{Kind: KindRequest, Text: "Repair it", Body: map[string]string{"to": agent, "conditions": "repair"}}, "w0"))
			},
		},
		{
			name: "child has another requester",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.child,
					event(t, base.child, agent, SchemaState, State{Kind: KindRequest, Text: "Repair it", Body: map[string]string{"to": agent, "conditions": "repair"}}, base.request))
			},
		},
		{
			name: "child request is ineffective",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.child,
					event(t, base.child, operator, SchemaState, State{Kind: KindRequest, Text: "Repair it", Body: map[string]string{"to": "unknown-actor", "conditions": "repair"}}, base.request))
			},
		},
		{
			name: "target is not the first cited basis",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.transfer,
					event(t, base.transfer, operator, SchemaSupersede, Supersede{Target: base.request, Text: "Out-of-order transfer"}, base.child, base.request))
			},
			wantStatus: "awaiting-review",
		},
		{
			name: "target is cited twice",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.transfer,
					event(t, base.transfer, operator, SchemaSupersede, Supersede{Target: base.request, Text: "Duplicate target"}, base.request, base.child, base.request))
			},
		},
		{
			name: "no successor request is cited",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.transfer,
					event(t, base.transfer, operator, SchemaSupersede, Supersede{Target: base.request, Text: "Ordinary retirement"}, base.request, base.artifact))
			},
		},
		{
			name: "two successor requests are cited",
			records: func(t testing.TB) []Record {
				second := "request:second-child"
				records := append([]Record(nil), base.records[:len(base.records)-1]...)
				records = append(records,
					event(t, second, operator, SchemaState, State{Kind: KindRequest, Text: "Another repair", Body: map[string]string{"to": agent, "conditions": "repair"}}, base.request),
					event(t, base.transfer, operator, SchemaSupersede, Supersede{Target: base.request, Text: "Ambiguous transfer"}, base.request, base.child, second),
				)
				return records
			},
		},
		{
			name: "target was already retired before transfer",
			records: func(t testing.TB) []Record {
				records := append([]Record(nil), base.records[:len(base.records)-1]...)
				return append(records,
					event(t, "request:ordinary-retirement", operator, SchemaSupersede, Supersede{Target: base.request, Text: "Stop the parent"}, base.request),
					base.records[len(base.records)-1],
				)
			},
		},
		{
			name: "unauthorized supersession is ineffective",
			records: func(t testing.TB) []Record {
				return replaceFixtureRecord(t, base.records, base.transfer,
					event(t, base.transfer, agent, SchemaSupersede, Supersede{Target: base.request, Text: "Unauthorized transfer"}, base.request, base.child))
			},
			wantStatus: "awaiting-review",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			commitment := commitmentForRequest(t, Fold(test.records(t)), base.request)
			wantStatus := test.wantStatus
			if wantStatus == "" {
				wantStatus = "cancelled"
			}
			if commitment.Status != wantStatus || commitment.SuccessorRequest != "" {
				t.Fatalf("malformed transfer gained successor force = %+v", commitment)
			}
		})
	}
}

func TestLinkedRequestSupersessionIsDecidedAtItsOwnPosition(t *testing.T) {
	fixture := rejectedTransferRecords(t, "request")
	records := append([]Record(nil), fixture.records[:len(fixture.records)-2]...)
	records = append(records,
		event(t, fixture.transfer, operator, SchemaSupersede, Supersede{Target: fixture.request, Text: "The child does not exist yet"}, fixture.request, fixture.child),
		event(t, fixture.child, operator, SchemaState, State{Kind: KindRequest, Text: "Late repair child", Body: map[string]string{"to": agent, "conditions": "repair"}}, fixture.request),
	)
	commitment := commitmentForRequest(t, Fold(records), fixture.request)
	if commitment.Status != "cancelled" || commitment.SuccessorRequest != "" {
		t.Fatalf("a later child retroactively changed the transfer = %+v", commitment)
	}
}

func TestLinkedRequestSupersessionRejectsSuccessorAtOrBeforeTarget(t *testing.T) {
	fixture := rejectedTransferRecords(t, "request")
	var child Record
	for _, record := range fixture.records {
		if record.ID == fixture.child {
			child = record
			break
		}
	}

	records := make([]Record, 0, len(fixture.records))
	for _, record := range fixture.records {
		if record.ID == fixture.child {
			continue
		}
		if record.ID == fixture.request {
			records = append(records, child)
		}
		records = append(records, record)
	}

	commitment := commitmentForRequest(t, Fold(records), fixture.request)
	if commitment.Status != "cancelled" || commitment.SuccessorRequest != "" {
		t.Fatalf("a forward-citing predecessor gained successor force = %+v", commitment)
	}
}
