package workroom

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The landing obligation, tested against the fold alone. Every identifier here
// is a canonical <workroom>#<event> name, because target_repo is checked
// against the workroom half of the request's own id and a bare test name has
// no workroom half to check against.

const landingWorkroom = "git:sha1:1111111111111111111111111111111111111111"

// planner is the actor the current merge-authorization list names by name. A
// held landing must refuse it like any other non-owner, so it needs a seat.
const planner = "actor:planner"

var (
	approvedHead = strings.Repeat("a", 40)
	repairHead   = strings.Repeat("b", 40)
	filingHead   = strings.Repeat("c", 40)
)

func lid(name string) string { return landingWorkroom + "#" + name }

func landingWorld(t testing.TB, extra ...Record) []Record {
	t.Helper()
	base := []Record{
		event(t, lid("w0"), operator, SchemaState, State{Kind: KindRoster, Text: "Operator begins the workroom", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
	}
	join := func(index int, actor, name, role string) {
		grant, ratification := lid(fmt.Sprintf("w%d", index)), lid(fmt.Sprintf("w%dr", index))
		base = append(base,
			event(t, grant, operator, SchemaState, State{Kind: KindRoster, Text: name + " joins", Body: map[string]string{"actor": actor, "name": name, "role": role}}, lid("w0")),
			event(t, ratification, operator, SchemaRatify, Ratify{Target: grant}, grant),
		)
	}
	join(1, agent, "Agent", "agent")
	join(2, other, "Reviewer", "agent")
	join(3, planner, "planner", "agent")
	join(4, bystander, "Ratifier", "ratifier")
	return append(base, extra...)
}

// landingBody is the section-1 value triple: a request that owes a Git artifact
// landed into refs/heads/main of this workroom.
func landingBody(to string, fields ...string) map[string]string {
	body := map[string]string{
		"to": to, "conditions": "the approved head lands",
		"target_repo": landingWorkroom, "target_ref": "refs/heads/main", "target_head": filingHead,
	}
	for index := 0; index+1 < len(fields); index += 2 {
		body[fields[index]] = fields[index+1]
	}
	return body
}

func noArtifactBody(to string) map[string]string {
	return map[string]string{"to": to, "conditions": "answer in the log", "no_git_artifact": "true"}
}

func commitmentForPromise(t testing.TB, projection Projection, promise string) Commitment {
	t.Helper()
	for _, commitment := range projection.Commitments {
		if commitment.Promise == promise {
			return commitment
		}
	}
	t.Fatalf("no commitment for promise %s", promise)
	return Commitment{}
}

// landingFixture is one implementation round on a request that owes a landing:
// the request, a promise, a reporting artifact, and an independent review whose
// approval the performer ratified.
type landingFixture struct {
	records  []Record
	request  string
	promise  string
	artifact string
	approval string
}

func landingRound(t testing.TB, requestBody map[string]string) landingFixture {
	t.Helper()
	return landingRoundFor(t, nil,
		event(t, lid("request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Implement it", Body: requestBody}, lid("w0")))
}

// landingRoundFor is landingRound with the request supplied, so a test can put
// ancestry above it and still get the implementation round below it.
func landingRoundFor(t testing.TB, ancestry []Record, request Record) landingFixture {
	t.Helper()
	fixture := landingFixture{
		request: request.ID, promise: lid("promise"),
		artifact: lid("artifact"), approval: lid("approval"),
	}
	records := append(append([]Record(nil), ancestry...), request)
	fixture.records = landingWorld(t, append(records,
		event(t, fixture.promise, agent, SchemaState, State{Kind: KindPromise, Text: "I will implement it"}, fixture.request),
		event(t, fixture.artifact, agent, SchemaState, State{Kind: KindArtifact, Text: "Exact implementation head", Body: map[string]string{"path": "internal/workroom", "commit": approvedHead}}, fixture.promise),
		event(t, lid("review-request"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Review the exact head", Body: noArtifactBody(other)}, fixture.artifact),
		event(t, lid("review-promise"), other, SchemaState, State{Kind: KindPromise, Text: "I will review it"}, lid("review-request")),
		event(t, fixture.approval, other, SchemaState, State{Kind: KindReport, Text: "Approved", Body: map[string]string{"verdict": "approved", "head": approvedHead, "artifact": fixture.artifact}}, lid("review-promise"), fixture.artifact),
	)...)
	return fixture
}

func (f landingFixture) ratifiedApproval(t testing.TB) landingFixture {
	t.Helper()
	f.records = append(f.records,
		event(t, lid("approval-ratified"), agent, SchemaRatify, Ratify{Target: f.approval}, f.approval))
	return f
}

// release is the structured authorization report that lifts a hold, plus the
// performer's ratification of it.
func (f landingFixture) release(t testing.TB, signer string) landingFixture {
	t.Helper()
	return f.releaseFiledBy(t, signer, agent)
}

// releaseFiledBy names who opened the authorization commitment. It is the
// performer's to file; any other actor is the malformed shape.
func (f landingFixture) releaseFiledBy(t testing.TB, signer, requester string) landingFixture {
	t.Helper()
	f.records = append(f.records,
		event(t, lid("authorization-request"), requester, SchemaStateV3, State{Kind: KindRequest, Text: "Release the hold", Body: noArtifactBody(signer)}, f.request),
		event(t, lid("authorization-promise"), signer, SchemaState, State{Kind: KindPromise, Text: "I will decide"}, lid("authorization-request")),
		event(t, lid("release"), signer, SchemaState, State{Kind: KindReport, Text: "Released", Body: map[string]string{
			"authorizes_request": f.request, "authorizes_candidate": approvedHead,
			"authorizes_approval": f.approval, "target_pre_head": filingHead,
		}}, lid("authorization-promise")),
		event(t, lid("release-ratified"), requester, SchemaRatify, Ratify{Target: lid("release")}, lid("release")),
	)
	return f
}

// receipt seals a merge of the approved head. ref empty means a legacy receipt,
// which reads as refs/heads/main of this workroom's own repository.
func (f landingFixture) receipt(t testing.TB, ref string) landingFixture {
	t.Helper()
	body := map[string]string{
		"merge_approval": f.approval, "merge_candidate": approvedHead,
		"merge_target_pre_head": filingHead, "merge_head": strings.Repeat("d", 40),
		"merge_retirements": fmt.Sprintf("{%q:%q}", f.artifact, "internal/workroom"),
		"merge_successors":  `["internal/workroom"]`,
	}
	if ref != "" {
		body["merge_target_repo"] = landingWorkroom
		body["merge_target_ref"] = ref
	}
	f.records = append(f.records,
		event(t, lid("merge"), agent, SchemaState, State{Kind: KindAssert, Text: "Approved candidate merged", Body: body}, f.approval))
	return f
}

// Section 1. Exactly one of the three encodings, and a triple the fold can
// check without a repository.
func TestStateV3RequestMustStateExactlyOneCheckableResult(t *testing.T) {
	cases := []struct {
		name    string
		body    map[string]string
		verdict Verdict
		reason  string
	}{
		{
			name: "neither", verdict: Ineffective,
			body:   map[string]string{"to": agent, "conditions": "do it"},
			reason: "request states no result: name a target, inherit one, or state no_git_artifact",
		},
		{
			name: "triple and no artifact", verdict: Ineffective,
			body:   landingBody(agent, "no_git_artifact", "true"),
			reason: "request states more than one result",
		},
		{
			name: "inherit and no artifact", verdict: Ineffective,
			body:   map[string]string{"to": agent, "conditions": "do it", "target": "inherit", "no_git_artifact": "true"},
			reason: "request states more than one result",
		},
		{
			name: "partial triple", verdict: Ineffective,
			body:   map[string]string{"to": agent, "conditions": "do it", "target_repo": landingWorkroom, "target_ref": "refs/heads/main"},
			reason: "target triple is incomplete",
		},
		{
			name: "ref outside refs/heads", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/tags/v1"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "bare branch name", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "main"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "ref carrying a traversal and a control byte", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/heads/../../etc\npasswd"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "ref with a control byte alone", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/heads/ma\nin"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "ref longer than the bound", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/heads/"+strings.Repeat("x", 300)),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "ref with a path traversal alone", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/heads/feature/../main"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "ref with an empty component", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/heads/team//main"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "ref with a trailing slash", verdict: Ineffective,
			body:   landingBody(agent, "target_ref", "refs/heads/main/"),
			reason: "target_ref must name a branch under refs/heads/",
		},
		{
			name: "another repository", verdict: Ineffective,
			body:   landingBody(agent, "target_repo", "git:sha1:"+strings.Repeat("9", 40)),
			reason: "target_repo must be this workroom's genesis id",
		},
		{
			name: "abbreviated head", verdict: Ineffective,
			body:   landingBody(agent, "target_head", filingHead[:12]),
			reason: "target_head must be a full lowercase object id",
		},
		{
			name: "full triple", verdict: Effective,
			body: landingBody(agent),
		},
		{
			name: "no git artifact", verdict: Effective,
			body: noArtifactBody(agent),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			records := landingWorld(t,
				event(t, lid("request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Do it", Body: test.body}, lid("w0")))
			decision := decisionFor(t, Fold(records), lid("request"))
			if decision.Verdict != test.verdict || (test.reason != "" && decision.Reason != test.reason) {
				t.Fatalf("state@3 request decision = %+v, want %s %q", decision, test.verdict, test.reason)
			}
		})
	}
}

// Section 9. The same names on a state@2 record are opaque body text: an older
// request carrying none of them is admitted exactly as it always was.
func TestStateV2RequestNeedsNoResultChoice(t *testing.T) {
	records := landingWorld(t,
		event(t, lid("request"), operator, SchemaState, State{Kind: KindRequest, Text: "Do it", Body: map[string]string{"to": agent, "conditions": "do it"}}, lid("w0")))
	if decision := decisionFor(t, Fold(records), lid("request")); decision.Verdict != Effective {
		t.Fatalf("state@2 request decision = %+v", decision)
	}
}

// Section 2, all five cases plus the exact depth bound.
func TestTargetInheritanceWalk(t *testing.T) {
	inherit := map[string]string{"to": agent, "conditions": "do it", "target": "inherit"}
	release := "refs/heads/release-2"
	spacers := func(count int, first string) ([]Record, string) {
		t.Helper()
		var records []Record
		basis := first
		for index := 0; index < count; index++ {
			id := lid(fmt.Sprintf("spacer-%d", index))
			records = append(records, event(t, id, operator, SchemaState, State{Kind: KindRequest, Text: "Spacer", Body: map[string]string{"to": agent, "conditions": "spacer"}}, basis))
			basis = id
		}
		return records, basis
	}
	t.Run("one nearest triple is inherited", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("parent"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child", Body: inherit}, lid("parent")),
			event(t, lid("child-promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("child")),
			event(t, lid("child-artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "head", Body: map[string]string{"path": "internal", "commit": approvedHead}}, lid("child-promise")),
		)
		projection := Fold(records)
		if decision := decisionFor(t, projection, lid("child")); decision.Verdict != Effective {
			t.Fatalf("inheriting child = %+v", decision)
		}
		row := commitmentForPromise(t, projection, lid("child-promise"))
		if row.TargetRef != "refs/heads/main" || row.TargetRepo != landingWorkroom || row.Legacy {
			t.Fatalf("inherited row = %+v", row)
		}
	})
	t.Run("two nearest requests agreeing inherit", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("parent-a"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent A", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("parent-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent B", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child", Body: inherit}, lid("parent-a"), lid("parent-b")),
		)
		if decision := decisionFor(t, Fold(records), lid("child")); decision.Verdict != Effective {
			t.Fatalf("agreeing ancestry = %+v", decision)
		}
	})
	t.Run("two nearest requests disagreeing refuse", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("parent-a"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent A", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("parent-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent B", Body: landingBody(agent, "target_ref", release)}, lid("w0")),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child", Body: inherit}, lid("parent-a"), lid("parent-b")),
		)
		decision := decisionFor(t, Fold(records), lid("child"))
		if decision.Verdict != Ineffective || decision.Reason != "conflicting target ancestry; restate all three target fields" {
			t.Fatalf("conflicting ancestry = %+v", decision)
		}
	})
	t.Run("a no-artifact request blocks the walk", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("parent"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("review"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Review", Body: noArtifactBody(other)}, lid("parent")),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child", Body: inherit}, lid("review")),
		)
		decision := decisionFor(t, Fold(records), lid("child"))
		if decision.Verdict != Ineffective || decision.Reason != "no target to inherit" {
			t.Fatalf("walk passed through a no-artifact request = %+v", decision)
		}
	})
	t.Run("no triple within depth eight", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			spacers int
			verdict Verdict
		}{
			{name: "triple at depth eight", spacers: 7, verdict: Effective},
			{name: "triple at depth nine", spacers: 8, verdict: Ineffective},
		} {
			t.Run(test.name, func(t *testing.T) {
				chain, nearest := spacers(test.spacers, lid("parent"))
				records := landingWorld(t, append([]Record{
					event(t, lid("parent"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent", Body: landingBody(agent)}, lid("w0")),
				}, append(chain,
					event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child", Body: inherit}, nearest),
				)...)...)
				decision := decisionFor(t, Fold(records), lid("child"))
				if decision.Verdict != test.verdict {
					t.Fatalf("depth bound decision = %+v, want %s", decision, test.verdict)
				}
				if test.verdict == Ineffective && decision.Reason != "no target to inherit" {
					t.Fatalf("depth bound reason = %q", decision.Reason)
				}
			})
		}
	})
}

// Section 3 and 4. The three artifact-completion states and who each waits on.
func TestArtifactCompletionStatesAndWaitingParty(t *testing.T) {
	t.Run("no approval waits on the performer for review", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "awaiting-review" || row.WaitingOn != agent || row.Approval != "" || row.ApprovedNotLanded {
			t.Fatalf("unapproved artifact row = %+v", row)
		}
	})
	t.Run("a ratified approval on an unheld request waits on the performer to land", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "awaiting-landing" || row.WaitingOn != agent || row.Approval != fixture.approval ||
			row.Candidate != approvedHead || !row.ApprovedNotLanded {
			t.Fatalf("approved unheld row = %+v", row)
		}
	})
	t.Run("a held request waits on its hold owner", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent, "landing", "held", "hold_owner", other)).ratifiedApproval(t)
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "awaiting-authorization" || row.WaitingOn != other || row.HoldOwner != other || row.Release != "" {
			t.Fatalf("held row = %+v", row)
		}
	})
	t.Run("the hold owner defaults to the requester", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent, "landing", "held")).ratifiedApproval(t)
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "awaiting-authorization" || row.WaitingOn != operator || row.HoldOwner != operator {
			t.Fatalf("defaulted hold row = %+v", row)
		}
	})
	t.Run("a release moves a held request to awaiting-landing", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent, "landing", "held", "hold_owner", other)).ratifiedApproval(t).release(t, other)
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "awaiting-landing" || row.WaitingOn != agent || row.Release != lid("release") {
			t.Fatalf("released row = %+v", row)
		}
	})
}

// Section 10, blocker 1: the five release-authority cases.
func TestReleaseIsAdmittedOnlyFromTheHoldOwner(t *testing.T) {
	cases := []struct {
		name    string
		signer  string
		admits  bool
		wantRow string
	}{
		{name: "requester who is not the owner", signer: operator, wantRow: "awaiting-authorization"},
		{name: "owner who holds no ratifier role", signer: other, admits: true, wantRow: "awaiting-landing"},
		{name: "planner who does not own the hold", signer: planner, wantRow: "awaiting-authorization"},
		{name: "ratifier who does not own the hold", signer: bystander, wantRow: "awaiting-authorization"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := landingRound(t, landingBody(agent, "landing", "held", "hold_owner", other)).
				ratifiedApproval(t).release(t, test.signer)
			projection := Fold(fixture.records)
			decision := decisionFor(t, projection, lid("release"))
			want := Ineffective
			if test.admits {
				want = Effective
			}
			if decision.Verdict != want {
				t.Fatalf("release by %s = %+v", test.name, decision)
			}
			if !test.admits && decision.Reason != "only the hold owner may release the landing hold on "+fixture.request {
				t.Fatalf("refusal reason = %q", decision.Reason)
			}
			if row := commitmentForPromise(t, projection, fixture.promise); row.Status != test.wantRow {
				t.Fatalf("row after release attempt = %+v, want %s", row, test.wantRow)
			}
		})
	}
	t.Run("hold_owner off the roster refuses the request itself", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Implement it",
				Body: landingBody(agent, "landing", "held", "hold_owner", "actor:stranger")}, lid("w0")))
		decision := decisionFor(t, Fold(records), lid("request"))
		if decision.Verdict != Ineffective || decision.Reason != "hold_owner is not in the live roster" {
			t.Fatalf("unknown hold owner = %+v", decision)
		}
	})
}

// Section 3: what a report may and may not do on a request that owes a landing.
func TestReportsOnALandingRequest(t *testing.T) {
	t.Run("a plain report is ineffective", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		fixture.records = append(fixture.records,
			event(t, lid("report"), agent, SchemaState, State{Kind: KindReport, Text: "Done"}, fixture.promise))
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("report"))
		if decision.Verdict != Ineffective || decision.Reason != `request owes a landing to "refs/heads/main"; land it or supersede it` {
			t.Fatalf("plain report on a landing request = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-review" || row.Report != fixture.artifact {
			t.Fatalf("refused report changed the row = %+v", row)
		}
	})
	t.Run("a verdict-carrying report is ineffective", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		fixture.records = append(fixture.records,
			event(t, lid("report"), agent, SchemaState, State{Kind: KindReport, Text: "Approved",
				Body: map[string]string{"verdict": "approved", "head": approvedHead, "artifact": fixture.artifact}}, fixture.promise))
		decision := decisionFor(t, Fold(fixture.records), lid("report"))
		if decision.Verdict != Ineffective ||
			decision.Reason != `request owes a landing to "refs/heads/main"; a review verdict belongs to its own review commitment` {
			t.Fatalf("verdict report on a landing request = %+v", decision)
		}
	})
	t.Run("a resolution report is evidence and closes nothing", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("resolution"), agent, SchemaState, State{Kind: KindReport, Text: "Blocked on a dependency",
				Body: map[string]string{"resolution": "parked"}}, fixture.promise),
			event(t, lid("resolution-ratified"), operator, SchemaRatify, Ratify{Target: lid("resolution")}, lid("resolution")),
		)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("resolution")); decision.Verdict != Effective {
			t.Fatalf("resolution report = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.LatestResolution != lid("resolution") {
			t.Fatalf("resolution is not projected as evidence = %+v", row)
		}
		if row.Status != "awaiting-landing" || row.WaitingOn != agent || row.Report != fixture.artifact {
			t.Fatalf("ratified resolution changed the commitment = %+v", row)
		}
	})
	t.Run("a plain report closes a no-artifact request", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Decide it", Body: noArtifactBody(agent)}, lid("w0")),
			event(t, lid("promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("request")),
			event(t, lid("report"), agent, SchemaState, State{Kind: KindReport, Text: "Decided"}, lid("promise")),
		)
		row := commitmentForPromise(t, Fold(records), lid("promise"))
		if row.Status != "reported" || row.WaitingOn != operator || row.TargetRef != "" {
			t.Fatalf("no-artifact row = %+v", row)
		}
	})
}

// Section 5, one case per adjacent pair of the precedence list.
func TestCompletionPrecedenceOnALandingRequest(t *testing.T) {
	t.Run("a receipt outranks the approved artifact", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).receipt(t, "refs/heads/main")
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "satisfied" || row.Terminal != "landed" || row.ApprovedNotLanded {
			t.Fatalf("sealed receipt row = %+v", row)
		}
	})
	t.Run("the approved artifact outranks a newer unapproved one", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("later-artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "Later head",
				Body: map[string]string{"path": "internal/workroom", "commit": repairHead}}, fixture.promise))
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Report != fixture.artifact || row.Status != "awaiting-landing" {
			t.Fatalf("a newer unapproved head displaced the approved one = %+v", row)
		}
	})
	t.Run("with no approval the newest artifact reports", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		fixture.records = append(fixture.records,
			event(t, lid("later-artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "Later head",
				Body: map[string]string{"path": "internal/workroom", "commit": repairHead}}, fixture.promise))
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Report != lid("later-artifact") || row.Status != "awaiting-review" {
			t.Fatalf("unapproved precedence = %+v", row)
		}
	})
}

// Section 7: carried, abandoned, or refused.
func TestSupersedingARequestThatHoldsAnApprovedHead(t *testing.T) {
	t.Run("without carry or abandonment it is ineffective", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target: fixture.request, Text: "Refile it"}, fixture.request))
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("retire"))
		if decision.Verdict != Ineffective ||
			decision.Reason != `request holds approved head "`+approvedHead+`"; carry it in the successor or declare abandoned` {
			t.Fatalf("bare supersession = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-landing" {
			t.Fatalf("refused supersession changed the row = %+v", row)
		}
	})
	t.Run("a successor resting on the artifact carries it", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("successor"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Carry it forward",
				Body: landingBody(agent)}, fixture.request, fixture.artifact),
			event(t, lid("carry"), operator, SchemaSupersede, Supersede{Target: fixture.request, Text: "Carried into the successor"}, fixture.request, lid("successor")),
		)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("carry")); decision.Verdict != Effective {
			t.Fatalf("carrying supersession = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.Status != "superseded" || row.SuccessorRequest != lid("successor") {
			t.Fatalf("carried row = %+v", row)
		}
	})
	t.Run("a stated disposition abandons it", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("abandon"), operator, SchemaSupersedeV1, SupersedeV1{
				Target: fixture.request, Text: "The approach was wrong; the head is dropped",
				Body: map[string]string{"disposition": "abandoned"},
			}, fixture.request))
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("abandon")); decision.Verdict != Effective {
			t.Fatalf("abandoning supersession = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.Status != "abandoned" || row.Terminal != "abandoned" || row.WaitingOn != "" || row.ApprovedNotLanded {
			t.Fatalf("abandoned row = %+v", row)
		}
	})
	t.Run("a request with no approved head is superseded as before", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		fixture.records = append(fixture.records,
			event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target: fixture.request, Text: "Refile it"}, fixture.request))
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("retire")); decision.Verdict != Effective {
			t.Fatalf("unapproved supersession = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "cancelled" {
			t.Fatalf("unapproved cancelled row = %+v", row)
		}
	})
}

// Section 8: the audit fact, measured against the destination.
func TestApprovedNotLandedIsMeasuredAgainstTheTarget(t *testing.T) {
	t.Run("a legacy commitment closed by report still owes its landing", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("request"), operator, SchemaState, State{Kind: KindRequest, Text: "Implement it", Body: map[string]string{"to": agent, "conditions": "approved head lands"}}, lid("w0")),
			event(t, lid("promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("request")),
			event(t, lid("artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "head", Body: map[string]string{"path": "internal/workroom", "commit": approvedHead}}, lid("promise")),
			event(t, lid("review-request"), agent, SchemaState, State{Kind: KindRequest, Text: "Review", Body: map[string]string{"to": other, "conditions": "exact head"}}, lid("artifact")),
			event(t, lid("review-promise"), other, SchemaState, State{Kind: KindPromise, Text: "I will review"}, lid("review-request")),
			event(t, lid("approval"), other, SchemaState, State{Kind: KindReport, Text: "Approved", Body: map[string]string{"verdict": "approved", "head": approvedHead, "artifact": lid("artifact")}}, lid("review-promise"), lid("artifact")),
			event(t, lid("approval-ratified"), agent, SchemaRatify, Ratify{Target: lid("approval")}, lid("approval")),
			event(t, lid("closing-report"), agent, SchemaState, State{Kind: KindReport, Text: "Done"}, lid("promise")),
			event(t, lid("closing-ratified"), operator, SchemaRatify, Ratify{Target: lid("closing-report")}, lid("closing-report")),
		)
		row := commitmentForPromise(t, Fold(records), lid("promise"))
		if row.Status != "satisfied" || row.Terminal != "reported" {
			t.Fatalf("the audit's closure changed = %+v", row)
		}
		if !row.ApprovedNotLanded || !row.Legacy || row.TargetRef != "refs/heads/main" {
			t.Fatalf("legacy audit row = %+v", row)
		}
	})
	t.Run("a legacy receipt into the named ref discharges it", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).receipt(t, "")
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "satisfied" || row.ApprovedNotLanded {
			t.Fatalf("legacy receipt row = %+v", row)
		}
	})
	t.Run("a receipt into another ref discharges nothing", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).receipt(t, "refs/heads/release-2")
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if !row.ApprovedNotLanded {
			t.Fatalf("a receipt into another ref discharged the obligation = %+v", row)
		}
	})
	t.Run("a commitment with no approval owes nothing yet", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		if row := commitmentForPromise(t, Fold(fixture.records), fixture.promise); row.ApprovedNotLanded {
			t.Fatalf("unapproved row claimed an unlanded approval = %+v", row)
		}
	})
}

// Section 7's transfer-created staleness, all four cases.
func TestRejectedRoundTransferDoesNotStaleItsSuccessor(t *testing.T) {
	t.Run("the transfer leaves the successor fresh", func(t *testing.T) {
		fixture := rejectedTransferRecords(t, "request")
		child := commitmentForRequest(t, Fold(fixture.records), fixture.child)
		if child.Stale {
			t.Fatalf("the transfer staled its own successor = %+v", child)
		}
	})
	t.Run("retiring another basis of the successor still stales it", func(t *testing.T) {
		fixture := rejectedTransferRecords(t, "request")
		records := append(fixture.records,
			event(t, "extra-basis", operator, SchemaState, State{Kind: KindAssert, Text: "A separate ground"}, "w0"),
			event(t, "child-2", operator, SchemaState, State{Kind: KindRequest, Text: "Repair it", Body: map[string]string{"to": agent, "conditions": "repair"}}, fixture.request, "extra-basis"),
			event(t, "extra-retired", operator, SchemaSupersede, Supersede{Target: "extra-basis", Text: "That ground is gone"}, "extra-basis"),
		)
		if child := commitmentForRequest(t, Fold(records), "child-2"); !child.Stale {
			t.Fatalf("the exception suppressed staleness from another edge = %+v", child)
		}
	})
	t.Run("resting on a retired request without being its successor still stales", func(t *testing.T) {
		fixture := rejectedTransferRecords(t, "request")
		records := append(fixture.records,
			event(t, "bystander-request", operator, SchemaState, State{Kind: KindRequest, Text: "Something else", Body: map[string]string{"to": agent, "conditions": "elsewhere"}}, fixture.request),
		)
		if row := commitmentForRequest(t, Fold(records), "bystander-request"); !row.Stale {
			t.Fatalf("the exception reached a request that is not the transfer successor = %+v", row)
		}
	})
	t.Run("an ordinary retirement of the same request stales its children", func(t *testing.T) {
		fixture := rejectedTransferRecords(t, "request")
		records := withoutFixtureRecord(t, fixture.records, fixture.transfer)
		records = append(records,
			event(t, "ordinary-retirement", operator, SchemaSupersede, Supersede{Target: fixture.request, Text: "Stop it"}, fixture.request),
		)
		if child := commitmentForRequest(t, Fold(records), fixture.child); !child.Stale {
			t.Fatalf("a retirement that named no successor left the child fresh = %+v", child)
		}
	})
}

// Section 10, blocker 4. The one word these three states replaced is not kept
// as an alias, so a site that still says it is a site that disagrees with the
// fold. The literal is assembled here rather than written out, because this
// test would otherwise find itself.
//
// The population is what git tracks, not what the working tree holds. Walking
// the filesystem would have failed on any checkout carrying untracked scratch
// that mentions the old word, which says nothing about whether this repository
// still ships it.
func TestNoSiteStillNamesTheReplacedArtifactCompletionStatus(t *testing.T) {
	retired := []byte("awaiting-" + "merge")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	listing := exec.Command("git", "ls-files", "-z")
	listing.Dir = root
	tracked, err := listing.Output()
	if err != nil {
		t.Fatalf("git ls-files in %s: %v", root, err)
	}
	// notes/ is design history and says what the word was. The UI embed under
	// internal/service/uidist/assets is generated from ui/src, which this
	// listing reads directly. Everything else tracked here is a live site.
	var offenders []string
	for _, name := range strings.Split(strings.TrimSuffix(string(tracked), "\x00"), "\x00") {
		if name == "" || strings.HasPrefix(name, "notes/") || strings.HasPrefix(name, "internal/service/uidist/assets/") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || bytes.IndexByte(content, 0) >= 0 {
			continue
		}
		if bytes.Contains(content, retired) {
			offenders = append(offenders, name)
		}
	}
	if len(offenders) != 0 {
		t.Fatalf("%q survives at %v; every enumeration site must move in the same head", retired, offenders)
	}
}

// Blocker 1. A merge retires the predecessors at the paths it publishes, and
// the discipline requires it, so an approved head that has been retired is the
// ordinary case. Whether the commitment obtained an approval is a fact about
// its history; reading liveness there lost the approved head from the audit
// set and let it be superseded without being carried or abandoned.
func TestARetiredApprovedHeadIsStillAnApprovedHead(t *testing.T) {
	retire := func(t *testing.T, f landingFixture) landingFixture {
		t.Helper()
		f.records = append(f.records,
			event(t, lid("artifact-retired"), agent, SchemaSupersede,
				Supersede{Target: f.artifact, Text: "Another merge published over this path"}, f.artifact))
		// The premise of every case below, asserted rather than assumed: if the
		// retirement itself were refused, these would pass for the wrong reason.
		if !artifactByEvent(t, Fold(f.records), f.artifact).Retired {
			t.Fatal("the fixture did not retire the approved artifact")
		}
		return f
	}
	t.Run("a bare supersession is still refused", func(t *testing.T) {
		fixture := retire(t, landingRound(t, landingBody(agent)).ratifiedApproval(t))
		fixture.records = append(fixture.records,
			event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target: fixture.request, Text: "Refile it"}, fixture.request))
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("retire"))
		if decision.Verdict != Ineffective ||
			decision.Reason != `request holds approved head "`+approvedHead+`"; carry it in the successor or declare abandoned` {
			t.Fatalf("retirement of the artifact let the request be dropped = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status == "cancelled" {
			t.Fatalf("the request was cancelled with an approved head outstanding = %+v", row)
		}
	})
	t.Run("the obligation stays outstanding", func(t *testing.T) {
		fixture := retire(t, landingRound(t, landingBody(agent)).ratifiedApproval(t))
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if !row.ApprovedNotLanded || row.Approval != fixture.approval || row.Candidate != approvedHead {
			t.Fatalf("retiring the artifact discharged the landing = %+v", row)
		}
	})
	t.Run("a landed row names its approval and candidate", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).receipt(t, "refs/heads/main")
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "satisfied" || row.Terminal != "landed" {
			t.Fatalf("landed row = %+v", row)
		}
		if row.Approval != fixture.approval || row.Candidate != approvedHead {
			t.Fatalf("the merge that retired the artifact emptied the row's approval = %+v", row)
		}
	})
}

// Should-fix 4. The delegation belongs to the request that made it.
func TestHoldOwnerMayBeNamedOnlyWhereTheHoldIsStated(t *testing.T) {
	parent := func(t testing.TB) Record {
		return event(t, lid("parent"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent",
			Body: landingBody(agent, "landing", "held", "hold_owner", other)}, lid("w0"))
	}
	t.Run("a child inherits the hold and its owner", func(t *testing.T) {
		records := landingWorld(t, parent(t),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child",
				Body: map[string]string{"to": agent, "conditions": "do it", "target": "inherit"}}, lid("parent")),
			event(t, lid("child-promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("child")),
		)
		projection := Fold(records)
		if decision := decisionFor(t, projection, lid("child")); decision.Verdict != Effective {
			t.Fatalf("inheriting child = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, lid("child-promise")); row.HoldOwner != other {
			t.Fatalf("inherited hold owner = %+v", row)
		}
	})
	t.Run("a child may not rename the owner it inherited", func(t *testing.T) {
		records := landingWorld(t, parent(t),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child",
				Body: map[string]string{"to": agent, "conditions": "do it", "target": "inherit", "hold_owner": agent}}, lid("parent")),
		)
		decision := decisionFor(t, Fold(records), lid("child"))
		if decision.Verdict != Ineffective || decision.Reason != "hold_owner may be named only by the request that states the hold" {
			t.Fatalf("child renamed an inherited hold owner = %+v", decision)
		}
	})
	t.Run("a child may name the owner of a hold it states itself", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("parent"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Parent", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Child",
				Body: map[string]string{"to": agent, "conditions": "do it", "target": "inherit", "landing": "held", "hold_owner": other}}, lid("parent")),
			event(t, lid("child-promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("child")),
		)
		projection := Fold(records)
		if decision := decisionFor(t, projection, lid("child")); decision.Verdict != Effective {
			t.Fatalf("child stating its own hold = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, lid("child-promise")); row.HoldOwner != other {
			t.Fatalf("stated hold owner = %+v", row)
		}
	})
}

// Nit 9. A disposition the fold would drop is refused, not ignored: an author
// who wrote one believes they have disposed of an approved head.
func TestADispositionIsNeverSilentlyDiscarded(t *testing.T) {
	t.Run("on a request holding no approved head", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent))
		fixture.records = append(fixture.records,
			event(t, lid("abandon"), operator, SchemaSupersedeV1, SupersedeV1{
				Target: fixture.request, Text: "Dropped",
				Body: map[string]string{"disposition": "abandoned"},
			}, fixture.request))
		decision := decisionFor(t, Fold(fixture.records), lid("abandon"))
		if decision.Verdict != Ineffective || decision.Reason != "disposition applies only to a request that holds an approved head" {
			t.Fatalf("dropped disposition = %+v", decision)
		}
	})
	t.Run("on a record that is not a landing request", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("note"), operator, SchemaState, State{Kind: KindAssert, Text: "A note"}, lid("w0")),
			event(t, lid("abandon"), operator, SchemaSupersedeV1, SupersedeV1{
				Target: lid("note"), Text: "Dropped",
				Body: map[string]string{"disposition": "abandoned"},
			}, lid("note")))
		decision := decisionFor(t, Fold(records), lid("abandon"))
		if decision.Verdict != Ineffective || decision.Reason != "disposition applies only to a request that holds an approved head" {
			t.Fatalf("dropped disposition on a note = %+v", decision)
		}
	})
	t.Run("an unknown disposition word", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("abandon"), operator, SchemaSupersedeV1, SupersedeV1{
				Target: fixture.request, Text: "Dropped",
				Body: map[string]string{"disposition": "parked"},
			}, fixture.request))
		decision := decisionFor(t, Fold(fixture.records), lid("abandon"))
		if decision.Verdict != Ineffective || decision.Reason != `disposition must be "abandoned" when stated` {
			t.Fatalf("unknown disposition = %+v", decision)
		}
	})
}

// Nit 11. One supersession can both qualify as a rejected-round transfer and
// declare an approved head dropped. The declaration is the explicit one.
func TestAnExplicitAbandonmentOutranksAQualifyingTransfer(t *testing.T) {
	rounds := func(t testing.TB, supersession Record) []Record {
		t.Helper()
		return landingWorld(t,
			event(t, lid("request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Implement it", Body: landingBody(agent)}, lid("w0")),
			event(t, lid("promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("request")),
			event(t, lid("rejected"), agent, SchemaState, State{Kind: KindArtifact, Text: "First head", Body: map[string]string{"path": "internal/workroom", "commit": repairHead}}, lid("promise")),
			event(t, lid("review-request"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Review", Body: noArtifactBody(other)}, lid("rejected")),
			event(t, lid("review-promise"), other, SchemaState, State{Kind: KindPromise, Text: "I will review"}, lid("review-request")),
			event(t, lid("changes"), other, SchemaState, State{Kind: KindReport, Text: "Changes requested", Body: map[string]string{"verdict": "changes-requested", "head": repairHead, "artifact": lid("rejected")}}, lid("review-promise"), lid("rejected")),
			event(t, lid("changes-ratified"), agent, SchemaRatify, Ratify{Target: lid("changes")}, lid("changes")),
			event(t, lid("artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "Second head", Body: map[string]string{"path": "internal/workroom", "commit": approvedHead}}, lid("promise")),
			event(t, lid("second-review"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Review again", Body: noArtifactBody(other)}, lid("artifact")),
			event(t, lid("second-promise"), other, SchemaState, State{Kind: KindPromise, Text: "I will review"}, lid("second-review")),
			event(t, lid("approval"), other, SchemaState, State{Kind: KindReport, Text: "Approved", Body: map[string]string{"verdict": "approved", "head": approvedHead, "artifact": lid("artifact")}}, lid("second-promise"), lid("artifact")),
			event(t, lid("approval-ratified"), agent, SchemaRatify, Ratify{Target: lid("approval")}, lid("approval")),
			// The child rests on both, so without a declaration this
			// supersession qualifies twice over: as the rejected round's
			// transfer, and as carrying the approved head forward.
			event(t, lid("child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Repair it", Body: landingBody(agent)}, lid("request"), lid("artifact")),
			supersession,
		)
	}
	// The control. Without the declaration this exact supersession is a
	// qualifying rejected-round transfer, so the case below is a contest
	// between two readings rather than a test of one.
	t.Run("without a disposition it is a transfer", func(t *testing.T) {
		records := rounds(t, event(t, lid("both"), operator, SchemaSupersede,
			Supersede{Target: lid("request"), Text: "Repair moved to the child"}, lid("request"), lid("child")))
		row := commitmentForPromise(t, Fold(records), lid("promise"))
		if row.Status != "superseded" || row.SuccessorRequest != lid("child") {
			t.Fatalf("the fixture is not a qualifying transfer = %+v", row)
		}
	})
	t.Run("with a disposition it is abandoned", func(t *testing.T) {
		records := rounds(t, event(t, lid("both"), operator, SchemaSupersedeV1, SupersedeV1{
			Target: lid("request"), Text: "The head is dropped, not carried",
			Body: map[string]string{"disposition": "abandoned"},
		}, lid("request"), lid("child")))
		projection := Fold(records)
		if decision := decisionFor(t, projection, lid("both")); decision.Verdict != Effective {
			t.Fatalf("supersession that both qualifies and abandons = %+v", decision)
		}
		row := commitmentForPromise(t, projection, lid("promise"))
		if row.Status != "abandoned" || row.SuccessorRequest != "" {
			t.Fatalf("an abandoned head was projected as carried into a successor = %+v", row)
		}
	})
}

// A hold is stated by a request that may itself have inherited its triple, so
// it sits between a grandchild and the value triple the grandchild inherits.
// Reading the hold from the triple's own request walked straight past it, and
// every generation after the first lost the hold silently — the landing became
// unheld without anyone deciding it should be.
func TestAHoldSurvivesEveryInheritedGeneration(t *testing.T) {
	inherit := func(fields ...string) map[string]string {
		body := map[string]string{"to": agent, "conditions": "do it", "target": "inherit"}
		for index := 0; index+1 < len(fields); index += 2 {
			body[fields[index]] = fields[index+1]
		}
		return body
	}
	root := func(t testing.TB) Record {
		return event(t, lid("root"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Root", Body: landingBody(agent)}, lid("w0"))
	}
	heldChild := func(t testing.TB, owner string) Record {
		return event(t, lid("held-child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Held child",
			Body: inherit("landing", "held", "hold_owner", owner)}, lid("root"))
	}
	grandchild := func(t testing.TB, bases ...string) Record {
		return event(t, lid("grandchild"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Grandchild", Body: inherit()}, bases...)
	}
	round := func(t testing.TB, ancestry ...Record) landingFixture {
		return landingRoundFor(t, ancestry, grandchild(t, lid("held-child")))
	}

	t.Run("the grandchild waits on the intervening owner", func(t *testing.T) {
		fixture := round(t, root(t), heldChild(t, other)).ratifiedApproval(t)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, fixture.request); decision.Verdict != Effective {
			t.Fatalf("grandchild = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.HoldOwner != other || row.Status != "awaiting-authorization" || row.WaitingOn != other {
			t.Fatalf("the inherited hold disappeared a generation down = %+v", row)
		}
		if row.TargetRef != "refs/heads/main" || row.TargetRepo != landingWorkroom {
			t.Fatalf("the triple did not come from the nearest value triple = %+v", row)
		}
	})
	t.Run("the root requester cannot release it", func(t *testing.T) {
		fixture := round(t, root(t), heldChild(t, other)).ratifiedApproval(t).release(t, operator)
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("release"))
		if decision.Verdict != Ineffective ||
			decision.Reason != "only the hold owner may release the landing hold on "+fixture.request {
			t.Fatalf("the root requester released an inherited hold = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-authorization" {
			t.Fatalf("row after the refused release = %+v", row)
		}
	})
	t.Run("the inherited owner can release it", func(t *testing.T) {
		fixture := round(t, root(t), heldChild(t, other)).ratifiedApproval(t).release(t, other)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("release")); decision.Verdict != Effective {
			t.Fatalf("release by the inherited owner = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.Status != "awaiting-landing" || row.Release != lid("release") {
			t.Fatalf("released row = %+v", row)
		}
	})
	t.Run("two equally near holds naming one owner inherit", func(t *testing.T) {
		second := event(t, lid("second-child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Second child",
			Body: inherit("landing", "held", "hold_owner", other)}, lid("root"))
		fixture := landingRoundFor(t, []Record{root(t), heldChild(t, other), second},
			grandchild(t, lid("held-child"), lid("second-child")))
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, fixture.request); decision.Verdict != Effective {
			t.Fatalf("agreeing hold ancestry = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.HoldOwner != other {
			t.Fatalf("agreeing hold ancestry = %+v", row)
		}
	})
	t.Run("two equally near holds naming different owners refuse", func(t *testing.T) {
		second := event(t, lid("second-child"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Second child",
			Body: inherit("landing", "held", "hold_owner", bystander)}, lid("root"))
		fixture := landingRoundFor(t, []Record{root(t), heldChild(t, other), second},
			grandchild(t, lid("held-child"), lid("second-child")))
		decision := decisionFor(t, Fold(fixture.records), fixture.request)
		if decision.Verdict != Ineffective ||
			decision.Reason != "conflicting hold ownership in target ancestry; restate the hold" {
			t.Fatalf("edge order decided who may release a landing = %+v", decision)
		}
	})
}

// A body field is free text, so a record of any kind can carry the four names a
// release is written with. Recognising a release by those names alone let a
// ratified proposal that merely mentioned them lift a hold. A release is one
// durable shape: the hold owner's report answering an authorization commitment
// the performer opened for exactly that purpose.
func TestOnlyAnAuthorizationReportReleasesAHold(t *testing.T) {
	held := func(t testing.TB) landingFixture {
		return landingRound(t, landingBody(agent, "landing", "held", "hold_owner", other)).ratifiedApproval(t)
	}
	bindings := func(f landingFixture, fields ...string) map[string]string {
		body := map[string]string{
			"authorizes_request": f.request, "authorizes_candidate": approvedHead,
			"authorizes_approval": f.approval, "target_pre_head": filingHead,
		}
		for index := 0; index+1 < len(fields); index += 2 {
			body[fields[index]] = fields[index+1]
		}
		return body
	}

	// The positive control. Everything below is this shape with one thing wrong.
	t.Run("the hold owner's authorization report releases it", func(t *testing.T) {
		fixture := held(t).release(t, other)
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.Status != "awaiting-landing" || row.Release != lid("release") {
			t.Fatalf("the authorization report did not release the hold = %+v", row)
		}
	})

	for _, test := range []struct {
		name string
		kind Kind
	}{
		{name: "a proposal", kind: KindPropose},
		{name: "an assertion", kind: KindAssert},
	} {
		t.Run(test.name+" naming the same fields releases nothing", func(t *testing.T) {
			fixture := held(t)
			// It rests exactly where the release report rests, and its author
			// is exactly the hold owner, so the only thing between it and a
			// release is that it is not a report.
			fixture.records = append(fixture.records,
				event(t, lid("authorization-request"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Release the hold", Body: noArtifactBody(other)}, fixture.request),
				event(t, lid("claimed-release"), other, SchemaStateV3, State{Kind: test.kind,
					Text: "About the release", Body: bindings(fixture)}, lid("authorization-request")),
				event(t, lid("claimed-ratified"), operator, SchemaRatify, Ratify{Target: lid("claimed-release")}, lid("claimed-release")),
			)
			projection := Fold(fixture.records)
			// Talking about the release is a legitimate act, so it is admitted;
			// it simply is not the release.
			if decision := decisionFor(t, projection, lid("claimed-release")); decision.Verdict != Effective {
				t.Fatalf("%s was refused rather than merely powerless = %+v", test.name, decision)
			}
			row := commitmentForPromise(t, projection, fixture.promise)
			if row.Status != "awaiting-authorization" || row.Release != "" {
				t.Fatalf("%s released the hold = %+v", test.name, row)
			}
		})
	}

	// The kind gate carries the case on its own only when the performer also
	// holds ratifier, which in this workroom several actors do: any other
	// performer cannot ratify a proposal at all, and the release would fail for
	// want of their acceptance instead. Here nothing else is wrong with it.
	t.Run("a proposal a ratifier-performer accepted still releases nothing", func(t *testing.T) {
		request, promise, artifact := lid("held-request"), lid("held-promise"), lid("held-artifact")
		approval := lid("held-approval")
		records := landingWorld(t,
			event(t, request, operator, SchemaStateV3, State{Kind: KindRequest, Text: "Implement it",
				Body: landingBody(bystander, "landing", "held", "hold_owner", other)}, lid("w0")),
			event(t, promise, bystander, SchemaState, State{Kind: KindPromise, Text: "I will"}, request),
			event(t, artifact, bystander, SchemaState, State{Kind: KindArtifact, Text: "Exact head", Body: map[string]string{"path": "internal/workroom", "commit": approvedHead}}, promise),
			event(t, lid("held-review"), bystander, SchemaStateV3, State{Kind: KindRequest, Text: "Review", Body: noArtifactBody(other)}, artifact),
			event(t, lid("held-review-promise"), other, SchemaState, State{Kind: KindPromise, Text: "I will review"}, lid("held-review")),
			event(t, approval, other, SchemaState, State{Kind: KindReport, Text: "Approved", Body: map[string]string{"verdict": "approved", "head": approvedHead, "artifact": artifact}}, lid("held-review-promise"), artifact),
			event(t, lid("held-approval-ratified"), bystander, SchemaRatify, Ratify{Target: approval}, approval),
			event(t, lid("authorization-request"), bystander, SchemaStateV3, State{Kind: KindRequest, Text: "Release the hold", Body: noArtifactBody(other)}, request),
			event(t, lid("claimed-release"), other, SchemaStateV3, State{Kind: KindPropose, Text: "About the release",
				Body: map[string]string{"authorizes_request": request, "authorizes_candidate": approvedHead, "authorizes_approval": approval}}, lid("authorization-request")),
			event(t, lid("claimed-ratified"), bystander, SchemaRatify, Ratify{Target: lid("claimed-release")}, lid("claimed-release")),
		)
		projection := Fold(records)
		if decision := decisionFor(t, projection, lid("claimed-ratified")); decision.Verdict != Effective {
			t.Fatalf("the fixture did not ratify the proposal = %+v", decision)
		}
		row := commitmentForPromise(t, projection, promise)
		if row.Status != "awaiting-authorization" || row.Release != "" {
			t.Fatalf("a proposal released the hold = %+v", row)
		}
	})

	t.Run("an authorization request the performer did not file is refused", func(t *testing.T) {
		fixture := held(t).releaseFiledBy(t, other, operator)
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("release"))
		if decision.Verdict != Ineffective ||
			decision.Reason != "the authorization request for a landing hold is the performer's to file" {
			t.Fatalf("release on somebody else's authorization = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Release != "" {
			t.Fatalf("row after the refused release = %+v", row)
		}
	})
	// The hold owner here is the performer, who may report on their own
	// promise, so the report is admissible on its face. What refuses it is the
	// commitment it answers: a landing commitment is not an authorization to
	// release one, and without that distinction a performer who owns the hold
	// could release it by reporting against the very work being held.
	t.Run("a report answering the landing commitment itself is refused", func(t *testing.T) {
		fixture := landingRound(t, landingBody(agent, "landing", "held", "hold_owner", agent)).ratifiedApproval(t)
		fixture.records = append(fixture.records,
			event(t, lid("release"), agent, SchemaState, State{Kind: KindReport, Text: "Released",
				Body: bindings(fixture)}, fixture.promise))
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("release"))
		if decision.Verdict != Ineffective ||
			decision.Reason != "a landing hold is released only on a no_git_artifact authorization request addressed to the hold owner" {
			t.Fatalf("a release resting on the landing promise = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-authorization" || row.Release != "" {
			t.Fatalf("row after the refused release = %+v", row)
		}
	})
	t.Run("a mismatched target binding is refused", func(t *testing.T) {
		fixture := held(t)
		fixture.records = append(fixture.records,
			event(t, lid("authorization-request"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Release the hold", Body: noArtifactBody(other)}, fixture.request),
			event(t, lid("release"), other, SchemaState, State{Kind: KindReport, Text: "Released",
				Body: bindings(fixture, "target_ref", "refs/heads/release-2")}, lid("authorization-request")),
		)
		decision := decisionFor(t, Fold(fixture.records), lid("release"))
		if decision.Verdict != Ineffective || decision.Reason != "release target_ref does not match the request's target" {
			t.Fatalf("mismatched target binding = %+v", decision)
		}
	})
	t.Run("a report naming another candidate releases nothing", func(t *testing.T) {
		fixture := held(t)
		fixture.records = append(fixture.records,
			event(t, lid("authorization-request"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Release the hold", Body: noArtifactBody(other)}, fixture.request),
			event(t, lid("release"), other, SchemaState, State{Kind: KindReport, Text: "Released",
				Body: bindings(fixture, "authorizes_candidate", repairHead)}, lid("authorization-request")),
			event(t, lid("release-ratified"), agent, SchemaRatify, Ratify{Target: lid("release")}, lid("release")),
		)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("release")); decision.Verdict != Effective {
			t.Fatalf("a well-formed release for another candidate = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-authorization" || row.Release != "" {
			t.Fatalf("a release for another candidate lifted this hold = %+v", row)
		}
	})
	t.Run("an unratified release releases nothing", func(t *testing.T) {
		fixture := held(t)
		fixture.records = append(fixture.records,
			event(t, lid("authorization-request"), agent, SchemaStateV3, State{Kind: KindRequest, Text: "Release the hold", Body: noArtifactBody(other)}, fixture.request),
			event(t, lid("release"), other, SchemaState, State{Kind: KindReport, Text: "Released", Body: bindings(fixture)}, lid("authorization-request")),
		)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("release")); decision.Verdict != Effective {
			t.Fatalf("the unratified release = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-authorization" || row.Release != "" {
			t.Fatalf("an unratified release lifted the hold = %+v", row)
		}
	})
}

// An explicit no_git_artifact=true request owes no Git artifact, so an artifact
// resting on its claim answers nothing. It stays admitted and visible — an
// artifact pointing at something is a perfectly good artifact — but it is never
// the completion and never puts the row in one of the awaiting-* states, which
// are the states of a landing that has not happened yet.
func TestAnExplicitNoArtifactRequestHasNoArtifactCompletion(t *testing.T) {
	t.Run("promised", func(t *testing.T) {
		// The round carries a ratified approval of the artifact, so the audit
		// fields would be filled if they were not gated on owing a landing.
		fixture := landingRound(t, noArtifactBody(agent)).ratifiedApproval(t)
		projection := Fold(fixture.records)
		if decisionFor(t, projection, fixture.artifact).Verdict != Effective {
			t.Fatal("the artifact was refused rather than merely powerless")
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.Status != "promised" || row.Report != "" || row.Approval != "" || row.Candidate != "" {
			t.Fatalf("no-artifact commitment gained an artifact completion = %+v", row)
		}

		fixture.records = append(fixture.records,
			event(t, lid("report"), agent, SchemaState, State{Kind: KindReport, Text: "Decided"}, fixture.promise))
		if row := commitmentForPromise(t, Fold(fixture.records), fixture.promise); row.Status != "reported" || row.Report != lid("report") {
			t.Fatalf("the explicit report is the completion = %+v", row)
		}
		fixture.records = append(fixture.records,
			event(t, lid("report-ratified"), operator, SchemaRatify, Ratify{Target: lid("report")}, lid("report")))
		if row := commitmentForPromise(t, Fold(fixture.records), fixture.promise); row.Status != "satisfied" || row.Terminal != "reported" {
			t.Fatalf("ratified report = %+v", row)
		}
	})
	t.Run("direct", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Decide it", Body: noArtifactBody(agent)}, lid("w0")),
			event(t, lid("artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "A pointer", Body: map[string]string{"path": "docs", "commit": approvedHead}}, lid("request")),
		)
		row := commitmentForRequest(t, Fold(records), lid("request"))
		if row.Status != "open" || row.Report != "" {
			t.Fatalf("no-artifact request gained a direct artifact completion = %+v", row)
		}
		records = append(records,
			event(t, lid("report"), agent, SchemaState, State{Kind: KindReport, Text: "Decided"}, lid("request")))
		if row := commitmentForRequest(t, Fold(records), lid("request")); row.Status != "reported" || row.Report != lid("report") {
			t.Fatalf("direct explicit report = %+v", row)
		}
	})
	// The control that keeps the differential witness honest. The same shape
	// filed before state@3 stated nothing, and the fold still reads it the way
	// it always did: the artifact is the completion.
	t.Run("an older request keeps its historical reading", func(t *testing.T) {
		records := landingWorld(t,
			event(t, lid("request"), operator, SchemaState, State{Kind: KindRequest, Text: "Decide it", Body: map[string]string{"to": agent, "conditions": "answer in the log"}}, lid("w0")),
			event(t, lid("promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, lid("request")),
			event(t, lid("artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "A pointer", Body: map[string]string{"path": "docs", "commit": approvedHead}}, lid("promise")),
		)
		row := commitmentForPromise(t, Fold(records), lid("promise"))
		if row.Status != "awaiting-review" || row.Report != lid("artifact") || !row.Legacy {
			t.Fatalf("the historical reading changed = %+v", row)
		}
	})
}

// Choosing the destination and the hold independently splices two branches. A
// nearer held ancestor owing some other ref handed its owner authority over a
// landing they were never given, and that owner's release then moved a
// destination whose own hold nobody had lifted. The hold is bound to the
// destination the request actually inherited.
func TestAHoldComesOnlyFromTheSelectedDestination(t *testing.T) {
	inherit := func(owner string) map[string]string {
		body := map[string]string{"to": agent, "conditions": "do it", "target": "inherit"}
		if owner != "" {
			body["landing"], body["hold_owner"] = "held", owner
		}
		return body
	}
	// Branch A reaches refs/heads/main at depth two, through a request filed
	// before state@3, and its hold belongs to the reviewer. Branch B reaches
	// its own destination at depth three and delegates a hold at depth one.
	ancestry := func(t testing.TB, secondRef string) []Record {
		return []Record{
			event(t, lid("target-a"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Land main under a hold",
				Body: landingBody(agent, "landing", "held", "hold_owner", other)}, lid("w0")),
			event(t, lid("legacy-wrapper-a"), operator, SchemaState, State{Kind: KindRequest, Text: "Legacy child",
				Body: map[string]string{"to": agent, "conditions": "do it"}}, lid("target-a")),
			event(t, lid("target-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Second destination",
				Body: landingBody(agent, "target_ref", secondRef)}, lid("w0")),
			event(t, lid("wrapper-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Inherit it", Body: inherit("")}, lid("target-b")),
			event(t, lid("held-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Delegate that hold",
				Body: inherit(bystander)}, lid("wrapper-b")),
		}
	}
	round := func(t testing.TB, secondRef string, bases ...string) landingFixture {
		return landingRoundFor(t, ancestry(t, secondRef),
			event(t, lid("mixed-child"), operator, SchemaStateV3, State{Kind: KindRequest,
				Text: "Inherit the nearest target", Body: inherit("")}, bases...)).ratifiedApproval(t)
	}
	mixed := func(t testing.TB, bases ...string) landingFixture {
		return round(t, "refs/heads/experiment", bases...)
	}

	t.Run("a hold on another destination does not reach the selected one", func(t *testing.T) {
		fixture := mixed(t, lid("legacy-wrapper-a"), lid("held-b"))
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, fixture.request); decision.Verdict != Effective {
			t.Fatalf("mixed child = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.TargetRef != "refs/heads/main" {
			t.Fatalf("the destination moved = %+v", row)
		}
		if row.HoldOwner != other || row.WaitingOn != other || row.Status != "awaiting-authorization" {
			t.Fatalf("a hold for another ref took the selected destination's authority = %+v", row)
		}
	})
	// Citation order is how the request was typed. It must not reach the answer.
	t.Run("reversed citation order gives the same answer", func(t *testing.T) {
		fixture := mixed(t, lid("held-b"), lid("legacy-wrapper-a"))
		row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
		if row.TargetRef != "refs/heads/main" || row.HoldOwner != other {
			t.Fatalf("citation order changed the answer = %+v", row)
		}
	})
	t.Run("the other branch's owner cannot release it", func(t *testing.T) {
		fixture := mixed(t, lid("legacy-wrapper-a"), lid("held-b")).release(t, bystander)
		projection := Fold(fixture.records)
		decision := decisionFor(t, projection, lid("release"))
		if decision.Verdict != Ineffective ||
			decision.Reason != "only the hold owner may release the landing hold on "+fixture.request {
			t.Fatalf("the other branch's owner released this landing = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-authorization" || row.Release != "" {
			t.Fatalf("row after the refused release = %+v", row)
		}
	})
	t.Run("the selected destination's owner can release it", func(t *testing.T) {
		fixture := mixed(t, lid("legacy-wrapper-a"), lid("held-b")).release(t, other)
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, lid("release")); decision.Verdict != Effective {
			t.Fatalf("release by the selected destination's owner = %+v", decision)
		}
		if row := commitmentForPromise(t, projection, fixture.promise); row.Status != "awaiting-landing" {
			t.Fatalf("released row = %+v", row)
		}
	})
	// The control that keeps the binding from being a blanket refusal: on the
	// same graph with both branches owing one destination, the nearer hold is
	// exactly the ordinary inheritance answer and it governs.
	t.Run("a nearer hold on the selected destination still governs", func(t *testing.T) {
		fixture := round(t, "refs/heads/main", lid("legacy-wrapper-a"), lid("held-b"))
		projection := Fold(fixture.records)
		if decision := decisionFor(t, projection, fixture.request); decision.Verdict != Effective {
			t.Fatalf("same-destination child = %+v", decision)
		}
		row := commitmentForPromise(t, projection, fixture.promise)
		if row.TargetRef != "refs/heads/main" || row.HoldOwner != bystander || row.WaitingOn != bystander {
			t.Fatalf("a hold on the selected destination was dropped = %+v", row)
		}
	})
}
