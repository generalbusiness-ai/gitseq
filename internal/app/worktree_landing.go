package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const worktreeCommitmentLimit = 4096
const worktreeRowLimit = 20
const worktreeInspectionLimit = 65536

// One budget spans association construction and every checkout's row/head
// comparisons. Unique object limits alone do not bound repeated associations.
type worktreeInspectionBudget struct {
	ctx       context.Context
	remaining int
}

func (b *worktreeInspectionBudget) take(n int) bool {
	if b.ctx.Err() != nil || n > b.remaining {
		return false
	}
	b.remaining -= n
	return true
}

type WorktreeRow struct {
	Request string `json:"request"`
	Promise string `json:"promise,omitempty"`
	Status  string `json:"status"`
	LandingDetails
}

type worktreeLandingInput struct {
	row         WorktreeRow
	heads       map[string]bool
	branches    map[string]bool
	unknownHead bool
}

func protectsWorktree(row WorktreeRow) bool {
	if row.ApprovedNotLanded {
		return true
	}
	switch row.Status {
	case "satisfied", "abandoned", "superseded", "withdrawn", "cancelled", "reneged":
		return false
	default:
		return true // including future statuses this client cannot settle
	}
}

// worktreeLandingInputs associates all directly named heads, not just the
// latest reporting artifact. A later unapproved artifact must not hide an
// earlier approved obligation carried by another checkout.
func worktreeLandingInputs(p workroom.Projection, budget *worktreeInspectionBudget) ([]worktreeLandingInput, bool) {
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

// ClassifyWorktrees is advice for W1, never a deletion operation. It uses one
// immutable graph for every checkout and every named commitment head. Missing
// evidence protects the checkout rather than making the deletable set larger.
func (w *Workspace) ClassifyWorktrees(ctx context.Context, p workroom.Projection, views []WorktreeView) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	deletable := []string{}
	budget := &worktreeInspectionBudget{ctx: ctx, remaining: worktreeInspectionLimit}
	unknown := func() []string {
		for i := range views {
			views[i].Classification = "unknown"
			views[i].ClassificationReason = "worktree inspection limit or cancellation"
			views[i].Approved, views[i].LandedInto, views[i].Row = "", "", ""
			views[i].RemoteContains, views[i].Rows = nil, nil
			views[i].RowsOmitted = 0
		}
		return []string{}
	}
	if !budget.take(len(views)) {
		return unknown()
	}
	rows, complete := worktreeLandingInputs(p, budget)
	if !complete {
		return unknown()
	}
	g := readLandingRefs(ctx, w.Repo)
	repository := "git:" + w.config.ObjectFormat + ":" + w.config.Genesis
	remote := landingRemote(ctx, w.Repo)
	var tips, objects, targets []string
	objectSet := map[string]bool{}
	targetRefs := map[string]bool{}
	for i := range views {
		// The checkout listing is cached for eight seconds. A current ref
		// observation must replace its older tip before cleanup classification.
		if g.refsKnown && views[i].Branch != "" && !views[i].Detached {
			views[i].Head = g.refs["refs/heads/"+views[i].Branch]
		}
		tips = append(tips, views[i].Head)
	}
	for _, input := range rows {
		if !budget.take(1) {
			return unknown()
		}
		for head := range input.heads {
			if !budget.take(1) {
				return unknown()
			}
			objectSet[head] = true
		}
		if input.row.MergeHead != "" {
			objectSet[input.row.MergeHead] = true
		}
		if len(objectSet) > landingObjectLimit {
			return unknown()
		}
		if input.row.TargetRepo == repository && input.row.TargetRef != "" {
			targetRefs[input.row.TargetRef] = true
			targets = append(targets, input.row.TargetRef)
			tips = append(tips, g.refs[input.row.TargetRef])
		}
	}
	for head := range objectSet {
		objects = append(objects, head)
	}
	tracking := remoteTrackingRefs(ctx, w.Repo, remote, targets)
	for _, ref := range tracking {
		tips = append(tips, g.refs[ref])
	}
	g.load(ctx, w.Repo, tips, objects)
	measuredAt := time.Now().Unix()
	for i := range rows {
		if !budget.take(1) {
			return unknown()
		}
		r := &rows[i].row
		measurement := measureLanding(g, LandingInput{TargetRepo: r.TargetRepo, TargetRef: r.TargetRef, MergeHead: r.MergeHead}, repository, remote, tracking, measuredAt)
		if r.TargetRef != "" {
			r.Git = &measurement
		}
	}
	for i := range views {
		if !budget.take(1) {
			return unknown()
		}
		view := &views[i]
		view.Classification = "unmapped"
		uncertain, protected, tipSettled := !g.refsKnown || view.Head == "", false, false
		type match struct{ index, rank int }
		var matches []match
		for j, input := range rows {
			if !budget.take(1) {
				return unknown()
			}
			matched := input.branches[view.Branch] && view.Branch != ""
			rank := 0
			if matched {
				rank = 2
			}
			for head := range input.heads {
				if !budget.take(1) {
					return unknown()
				}
				contains := g.contains(view.Head, head)
				if contains == nil {
					if protectsWorktree(input.row) {
						uncertain = true
					}
					continue
				}
				if *contains {
					matched = true
					if rank < 1 {
						rank = 1
					}
					if head == view.Head {
						rank = 3
					}
				}
			}
			if input.unknownHead && protectsWorktree(input.row) {
				uncertain = true
			}
			if !matched {
				continue
			}
			if protectsWorktree(input.row) {
				protected = true
				rank += 4
			}
			matches = append(matches, match{j, rank})
			if input.row.Status == "abandoned" && input.heads[view.Head] {
				tipSettled = true
			}
			if input.row.Git != nil && input.row.LandingReceipt != "" {
				if contains := g.contains(input.row.Git.TargetHead, view.Head); contains != nil && *contains {
					tipSettled = true
				}
			}
		}
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].rank != matches[j].rank {
				return matches[i].rank > matches[j].rank
			}
			return matches[i].index > matches[j].index
		})
		for n, m := range matches {
			if n == worktreeRowLimit {
				view.RowsOmitted = len(matches) - n
				break
			}
			view.Rows = append(view.Rows, rows[m.index].row)
		}
		if len(view.Rows) > 0 {
			primary := view.Rows[0]
			view.Row = primary.Request
			if contains := g.contains(view.Head, primary.Candidate); contains != nil && *contains {
				view.Approved = primary.Approval
			}
			if primary.LandingReceipt != "" && primary.Candidate == view.Head {
				view.LandedInto = primary.TargetRef
				if primary.Git != nil {
					view.RemoteContains = primary.Git.RemoteContains
				}
			}
		}
		switch {
		case view.Current || view.Detached || view.State != "clean" || targetRefs["refs/heads/"+view.Branch]:
			view.Classification = "protected"
			view.ClassificationReason = "current, detached, non-clean or target checkout"
		case protected:
			view.Classification = "protected"
			view.ClassificationReason = "unsettled or approved-not-landed commitment"
		case uncertain:
			view.Classification = "unknown"
			view.ClassificationReason = "ancestry unavailable or inspection bound reached"
		case len(matches) > 0 && tipSettled:
			view.Classification = "deletable"
			deletable = append(deletable, view.Checkout)
		case len(matches) > 0:
			view.Classification = "protected"
			view.ClassificationReason = "current tip is not proved landed or explicitly abandoned"
		}
	}
	if !budget.take(0) {
		return unknown()
	}
	return deletable
}
