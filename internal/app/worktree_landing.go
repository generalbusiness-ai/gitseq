package app

import (
	"context"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
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
	return row.ApprovedNotLanded || !workroom.SettledCommitment(row.Status)
}

type worktreeLandingIndex struct {
	rows    []worktreeLandingInput
	objects map[string]bool
	targets map[string]map[string]bool
	// pending maps each commit a pending decision is about to the live
	// proposal that makes it one. A checkout still holding such a commit is
	// protected while the artifact's own parent request is unsettled.
	pending map[string]string
}

// pendingDecisions finds the artifacts a live proposal cites and whose parent
// request is not settled, and returns the commit each of those artifacts
// names. It runs off the collections the one statement pass already made, so
// it adds no second walk of the projection and no second bound.
//
// The loss this closes was real: a note artifact was retired and its branch
// and checkout deleted while a proposal rested directly on it and the parent
// request still held a live promise. The work survived only because the commit
// outlived the deletion. Three things had to be got wrong at once for that to
// happen, and each is answered here.
//
// The structural `rests_on` edge is what is read. Prose naming a head is not a
// reference this can see, and assuming it was the only one is how a cited
// artifact looks uncited.
//
// A retired artifact still protects. Retirement withdraws a pointer; it does
// not decide the proposal that cites it, and reading one as the other is how a
// pending decision loses its subject.
//
// Staleness is not settlement, and a filtered board view is not the record. A
// request missing from a default actionable list is missing from a list, and
// its commitment is what says whether it closed.
func pendingDecisions(p workroom.Projection, scan pendingDecisionScan, budget *worktreeInspectionBudget) (map[string]string, bool) {
	pending := map[string]string{}
	for _, proposal := range scan.proposals {
		for _, basis := range p.Provenance[proposal] {
			if !budget.take(1) {
				return nil, false
			}
			commit := scan.artifactCommit[basis]
			if !exactObjectID(commit) {
				continue
			}
			if request, _, owned := reviewguard.OwnedEdge(p, scan.artifactStatement[basis]); owned && scan.settled[request] {
				continue
			}
			if existing := pending[commit]; existing == "" || proposal < existing {
				pending[commit] = proposal
			}
		}
	}
	return pending, true
}

// pendingDecisionScan is what the one statement pass collects on the way past
// for the pending-decision question. Every entry costs a step already charged
// for visiting that record.
type pendingDecisionScan struct {
	settled           map[string]bool
	proposals         []string
	artifactCommit    map[string]string
	artifactStatement map[string]workroom.Statement
}

func newPendingDecisionScan(commitments int) pendingDecisionScan {
	return pendingDecisionScan{
		settled:           make(map[string]bool, commitments),
		artifactCommit:    map[string]string{},
		artifactStatement: map[string]workroom.Statement{},
	}
}

func (s *pendingDecisionScan) observe(statement workroom.Statement) {
	switch statement.Kind {
	case workroom.KindArtifact:
		// The artifact's own statement, whatever its retirement: the owned
		// edge is read from the record that was signed, and a withdrawn
		// pointer was still signed by whoever signed it.
		s.artifactCommit[statement.Event] = statement.Body["commit"]
		s.artifactStatement[statement.Event] = statement
	case workroom.KindPropose:
		// A ratified proposal is a decision that has been taken, and a retired
		// one has been withdrawn. Neither is pending.
		if !statement.Ratified && !statement.Retired {
			s.proposals = append(s.proposals, statement.Event)
		}
	}
}

// worktreeLandingInputs associates all directly named heads, not just the
// latest reporting artifact. A later unapproved artifact must not hide an
// earlier approved obligation carried by another checkout.
func worktreeLandingInputs(p workroom.Projection, budget *worktreeInspectionBudget) (worktreeLandingIndex, bool) {
	if len(p.Commitments) > worktreeCommitmentLimit {
		return worktreeLandingIndex{}, false
	}
	rows := make([]worktreeLandingInput, len(p.Commitments))
	scan := newPendingDecisionScan(len(p.Commitments))
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
		scan.settled[c.Request] = workroom.SettledCommitment(c.Status) && !c.ApprovedNotLanded
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
		scan.observe(s)
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
	pending, ok := pendingDecisions(p, scan, budget)
	if !ok {
		return worktreeLandingIndex{}, false
	}
	for commit := range pending {
		if !budget.take(1) {
			return worktreeLandingIndex{}, false
		}
		if !objects[commit] && len(objects) == landingObjectLimit {
			complete = false
			break
		}
		objects[commit] = true
	}
	return worktreeLandingIndex{rows: rows, objects: objects, targets: targets, pending: pending}, complete && budget.take(0)
}

// ClassifyWorktrees is advice for W1, never a deletion operation. It captures
// one read of this repository and answers from it. A caller that also wants
// the reverse association captures the read itself and passes it to both, so
// that the two judgments answer about one world and share one bound.
func (w *Workspace) ClassifyWorktrees(ctx context.Context, p workroom.Projection, views []WorktreeView) []string {
	read := w.captureAround(ctx, views)
	defer read.Close()
	return w.ClassifyCheckouts(read, p)
}

// ClassifyCheckouts is that advice over an already captured read. It uses one
// immutable graph for every checkout and every named commitment head. Missing
// evidence protects the checkout rather than making the deletable set larger.
//
// It reads no association and no grade. Which durable record a checkout claims
// is a different question from whether that work is finished, and an unsigned
// source trailer is not something a deletion decision may stand on.
func (w *Workspace) ClassifyCheckouts(read *CheckoutRead, p workroom.Projection) []string {
	ctx, budget, views := read.ctx, read.budget, read.Worktrees
	unknown := func() []string { return unknownWorktrees(views, exhaustedRead) }
	if !budget.take(len(views)) {
		return unknown()
	}
	index, complete := worktreeLandingInputs(p, budget)
	if !complete {
		return unknown()
	}
	g := read.graph()
	repository := "git:" + w.config.ObjectFormat + ":" + w.config.Genesis
	remote := read.remoteName
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
	unknown := func() []string { return unknownWorktrees(views, exhaustedRead) }
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
	// A ref inventory answers one question here that no commitment does:
	// whether anything but this checkout can still reach its head.
	refValues := map[string]bool{}
	for _, oid := range g.refs {
		if !budget.take(1) {
			return unknown()
		}
		refValues[oid] = true
	}
	for commit, proposal := range index.pending {
		if !budget.take(1) {
			return unknown()
		}
		holders := matches.ancestry.positive[commit]
		if holders == nil || !g.objects[commit] {
			continue
		}
		for i := range views {
			if !budget.take(1) {
				return unknown()
			}
			// A checkout holding two pending decisions names the earlier
			// proposal. Reporting whichever the map happened to yield would
			// make the same repository answer differently between reads.
			if holders.has(i) && (views[i].PendingDecision == "" || proposal < views[i].PendingDecision) {
				views[i].PendingDecision = proposal
			}
		}
	}
	for i := range views {
		if !budget.take(1) {
			return unknown()
		}
		view := &views[i]
		view.Classification, view.ClassificationReason = "unmapped", ""
		view.Row, view.Approved, view.LandedInto = "", "", ""
		view.RemoteContains = nil
		view.ReflessHead = g.refsKnown && view.Head != "" && !refValues[view.Head]
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
		case view.OutsideRoot || view.SymlinkedPath:
			view.Classification = "protected"
			view.ClassificationReason = "checkout path is outside the checkout root or reached through a symbolic link"
		case view.ReflessHead:
			view.Classification = "protected"
			view.ClassificationReason = "no ref in this repository points at this head"
		case view.PendingDecision != "":
			view.Classification = "protected"
			view.ClassificationReason = "a live proposal cites work this checkout holds"
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

func unknownWorktrees(views []WorktreeView, reason string) []string {
	for i := range views {
		views[i].Classification = "unknown"
		views[i].ClassificationReason = reason
		views[i].Approved, views[i].LandedInto, views[i].Row = "", "", ""
		views[i].RemoteContains, views[i].Rows = nil, nil
		views[i].RowsOmitted = 0
		// A protection this pass never got to establish must not be reported
		// as one it looked for and did not find.
		views[i].ReflessHead, views[i].PendingDecision = false, ""
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
