package app
import (
 "context"
 "reflect"
 "strings"
 "testing"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)
func priorWorktreeLandingInputsForReview(p workroom.Projection, budget *worktreeInspectionBudget) ([]worktreeLandingInput, bool) {
	if len(p.Commitments) > worktreeCommitmentLimit {
		return nil, false
	}
	rows := make([]worktreeLandingInput, len(p.Commitments))
	byEvent := map[string][]int{}
	objects := map[string]bool{}
	complete := true
	addHead := func(i int, head string) {
		if head == "" {
			return
		}
		if !exactObjectID(head) {
			rows[i].unknownHead = true
			return
		}
		if !objects[head] && len(objects) == landingObjectLimit {
			complete = false
			return
		}
		objects[head] = true
		rows[i].heads[head] = true
	}
	for i, c := range p.Commitments {
		if !budget.take(1) {
			return nil, false
		}
		rows[i] = worktreeLandingInput{row: WorktreeRow{Request: c.Request, Promise: c.Promise, Status: c.Status, LandingDetails: LandingDetailsFor(c)}, heads: map[string]bool{}, branches: map[string]bool{}}
		addHead(i, c.Candidate)
		for _, event := range []string{c.Request, c.Promise, c.Report} {
			if event != "" {
				byEvent[event] = append(byEvent[event], i)
			}
		}
	}
	// Consume each association before visiting it; never allocate the Cartesian
	// request/commitment expansion, including repeated direct provenance edges.
	for _, s := range p.Statements {
		if !budget.take(1) {
			return nil, false
		}
		associate := func(event string) bool {
			for _, i := range byEvent[event] {
				if !budget.take(1) {
					return false
				}
				addHead(i, s.Body["head"])
				addHead(i, s.Body["commit"])
				if branch := s.Body["branch"]; branch != "" {
					rows[i].branches[strings.TrimPrefix(branch, "refs/heads/")] = true
				}
			}
			return true
		}
		if !associate(s.Event) {
			return nil, false
		}
		if s.Kind == workroom.KindArtifact {
			for _, basis := range p.Provenance[s.Event] {
				if !budget.take(1) || !associate(basis) {
					return nil, false
				}
			}
		}
	}
	var details []*LandingDetails
	for i := range rows {
		details = append(details, &rows[i].row.LandingDetails)
	}
	// The witness join is linear in statements plus selected rows. Charge it
	// before the shared helper, which does not perform provenance expansion.
	if !budget.take(len(p.Statements) + 2*len(rows)) {
		return nil, false
	}
	FillLandingEvidence(p, details)
	return rows, complete && budget.take(0)
}


func TestPlannerPriorInputIndexMatchesCaptured(t *testing.T) {
 f:=readWorktreeCapturedFixture(t)
 prior,ok:=priorWorktreeLandingInputsForReview(f.Projection,&worktreeInspectionBudget{ctx:context.Background(),remaining:1000000})
 if !ok {t.Fatal("prior input index refused generous review budget")}
 next,ok:=worktreeLandingInputs(f.Projection,&worktreeInspectionBudget{ctx:context.Background(),remaining:worktreeInspectionLimit})
 if !ok {t.Fatal("new input index refused actual production budget")}
 if !reflect.DeepEqual(prior,next.rows) {for i:=range prior {if !reflect.DeepEqual(prior[i],next.rows[i]) {t.Fatalf("row %d changed: prior=%+v next=%+v",i,prior[i],next.rows[i])}};t.Fatal("row count differs")}
 t.Logf("all %d input rows, heads, branches, selected receipts and distinct promises match actual prior source",len(prior))
}
