package app

import (
	"context"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const worktreeCommitmentLimit = 4096
const worktreeRowLimit = 20
const worktreeInspectionLimit = 65536

// One budget spans association construction, graph and membership joins, and
// bounded output selection. Unique object limits alone do not bound fanout.
type worktreeInspectionBudget struct {
	ctx       context.Context
	remaining int
	failed    bool
}

func (b *worktreeInspectionBudget) take(n int) bool {
	if b.failed || b.ctx.Err() != nil || n > b.remaining {
		b.failed = true
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

type worktreeLandingIndex struct {
	rows    []worktreeLandingInput
	objects map[string]bool
	targets map[string]map[string]bool
}

// worktreeLandingInputs associates all directly named heads, not just the
// latest reporting artifact. A later unapproved artifact must not hide an
// earlier approved obligation carried by another checkout.
func worktreeLandingInputs(p workroom.Projection, budget *worktreeInspectionBudget) (worktreeLandingIndex, bool) {
	if len(p.Commitments) > worktreeCommitmentLimit {
		return worktreeLandingIndex{}, false
	}
	rows := make([]worktreeLandingInput, len(p.Commitments))
	byEvent := map[string][]int{}
	byReceipt := map[string][]int{}
	objects := map[string]bool{}
	targets := map[string]map[string]bool{}
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
			return worktreeLandingIndex{}, false
		}
		rows[i] = worktreeLandingInput{row: WorktreeRow{Request: c.Request, Promise: c.Promise, Status: c.Status, LandingDetails: LandingDetailsFor(c)}, heads: map[string]bool{}, branches: map[string]bool{}}
		addHead(i, c.Candidate)
		if c.TargetRef != "" {
			if targets[c.TargetRepo] == nil {
				targets[c.TargetRepo] = map[string]bool{}
			}
			targets[c.TargetRepo][c.TargetRef] = true
		}
		if c.LandingReceipt != "" {
			byReceipt[c.LandingReceipt] = append(byReceipt[c.LandingReceipt], i)
		}
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
			return worktreeLandingIndex{}, false
		}
		for _, i := range byReceipt[s.Event] {
			if !budget.take(1) {
				return worktreeLandingIndex{}, false
			}
			fillLandingReceipt(&rows[i].row.LandingDetails, s)
			if head := rows[i].row.MergeHead; head != "" {
				objects[head] = true
				if len(objects) > landingObjectLimit {
					return worktreeLandingIndex{}, false
				}
			}
		}
		// Statements without an association value cannot add a head or branch.
		if s.Body["head"] == "" && s.Body["commit"] == "" && s.Body["branch"] == "" {
			continue
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
			return worktreeLandingIndex{}, false
		}
		if s.Kind == workroom.KindArtifact {
			for _, basis := range p.Provenance[s.Event] {
				if !budget.take(1) || !associate(basis) {
					return worktreeLandingIndex{}, false
				}
			}
		}
	}
	return worktreeLandingIndex{rows: rows, objects: objects, targets: targets}, complete && budget.take(0)
}

// ClassifyWorktrees is advice for W1, never a deletion operation. It uses one
// immutable graph for every checkout and every named commitment head. Missing
// evidence protects the checkout rather than making the deletable set larger.
func (w *Workspace) ClassifyWorktrees(ctx context.Context, p workroom.Projection, views []WorktreeView) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	budget := &worktreeInspectionBudget{ctx: ctx, remaining: worktreeInspectionLimit}
	return w.classifyWorktrees(ctx, p, views, budget)
}

func (w *Workspace) classifyWorktrees(ctx context.Context, p workroom.Projection, views []WorktreeView, budget *worktreeInspectionBudget) []string {
	unknown := func() []string { return unknownWorktrees(views) }
	if !budget.take(len(views)) {
		return unknown()
	}
	index, complete := worktreeLandingInputs(p, budget)
	if !complete {
		return unknown()
	}
	g := readLandingRefs(ctx, w.Repo)
	g.inspectionBudget = budget
	repository := "git:" + w.config.ObjectFormat + ":" + w.config.Genesis
	remote := landingRemote(ctx, w.Repo)
	tips, objects, targets, complete := index.gitInputs(views, g, repository, budget)
	if !complete {
		return unknown()
	}
	tracking := remoteTrackingRefs(ctx, w.Repo, remote, targets)
	for _, ref := range tracking {
		if !budget.take(1) {
			return unknown()
		}
		tips = append(tips, g.refs[ref])
	}
	g.load(ctx, w.Repo, tips, objects)
	return classifyWorktreeRows(index, views, g, repository, remote, tracking, budget)
}

func classifyWorktreeRows(index worktreeLandingIndex, views []WorktreeView, g *landingGraph, repository, remote string, tracking map[string]string, budget *worktreeInspectionBudget) []string {
	deletable := []string{}
	unknown := func() []string { return unknownWorktrees(views) }
	rows := index.rows
	targetRefs := index.targets[repository]
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
	matches, complete := indexWorktreeMatches(rows, views, g, budget)
	if !complete {
		return unknown()
	}
	for i := range views {
		if !budget.take(1) {
			return unknown()
		}
		view := &views[i]
		view.Classification, view.ClassificationReason = "unmapped", ""
		view.Row, view.Approved, view.LandedInto = "", "", ""
		view.RemoteContains = nil
		uncertain := !g.refsKnown || view.Head == "" || matches.uncertain.has(i)
		protected, tipSettled := matches.protected.has(i), matches.settled.has(i)
		var ok bool
		view.Rows, view.RowsOmitted, ok = matches.selectRows(i, rows, budget)
		if !ok {
			return unknown()
		}
		if len(view.Rows) > 0 {
			primary := view.Rows[0]
			view.Row = primary.Request
			if contains := matches.ancestry.contains(i, primary.Candidate, g.objects[primary.Candidate]); contains != nil && *contains {
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
		case len(view.Rows) > 0 && tipSettled:
			view.Classification = "deletable"
			deletable = append(deletable, view.Checkout)
		case len(view.Rows) > 0:
			view.Classification = "protected"
			view.ClassificationReason = "current tip is not proved landed or explicitly abandoned"
		}
	}
	if !budget.take(0) {
		return unknown()
	}
	return deletable
}

func unknownWorktrees(views []WorktreeView) []string {
	for i := range views {
		views[i].Classification = "unknown"
		views[i].ClassificationReason = "worktree inspection limit or cancellation"
		views[i].Approved, views[i].LandedInto, views[i].Row = "", "", ""
		views[i].RemoteContains, views[i].Rows = nil, nil
		views[i].RowsOmitted = 0
	}
	return []string{}
}

func (index worktreeLandingIndex) gitInputs(views []WorktreeView, g *landingGraph, repository string, budget *worktreeInspectionBudget) ([]string, []string, []string, bool) {
	var tips, objects, targets []string
	targetRefs := index.targets[repository]
	for i := range views {
		// The checkout listing is cached for eight seconds. A current ref
		// observation must replace its older tip before cleanup classification.
		if g.refsKnown && views[i].Branch != "" && !views[i].Detached {
			views[i].Head = g.refs["refs/heads/"+views[i].Branch]
		}
		tips = append(tips, views[i].Head)
	}
	for target := range targetRefs {
		if !budget.take(1) {
			return nil, nil, nil, false
		}
		targets = append(targets, target)
		tips = append(tips, g.refs[target])
	}
	for head := range index.objects {
		if !budget.take(1) {
			return nil, nil, nil, false
		}
		objects = append(objects, head)
	}

	return tips, objects, targets, budget.take(0)
}
