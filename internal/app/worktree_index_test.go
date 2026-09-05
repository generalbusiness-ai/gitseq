package app

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

// This deliberately retains the original exhaustive matching and ordering
// algorithm as a differential oracle, with a generous test-only step budget.
// Production still uses the unchanged worktreeInspectionLimit.
func exhaustiveWorktreeRows(rows []worktreeLandingInput, views []WorktreeView, g *landingGraph, repository, remote string, tracking map[string]string, targetRefs map[string]bool) []string {
	deletable := []string{}
	budget := &worktreeInspectionBudget{ctx: context.Background(), remaining: 1000000}
	unknown := func() []string { return unknownWorktrees(views) }
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

type worktreeCapturedFixture struct {
	Projection workroom.Projection
	Views      []WorktreeView
	Graph      struct {
		Refs    map[string]string
		Objects map[string]bool
		Parents map[string][]string
		Shallow bool
	}
	Repository, Remote string
	Tracking           map[string]string
}

func (f worktreeCapturedFixture) graph() *landingGraph {
	return &landingGraph{refs: f.Graph.Refs, refsKnown: true, objects: f.Graph.Objects, parents: f.Graph.Parents, shallow: f.Graph.Shallow, ancestors: map[string]landingAncestors{}, walkRemaining: landingWalkLimit}
}
func readWorktreeCapturedFixture(t *testing.T) worktreeCapturedFixture {
	t.Helper()
	file, err := os.Open("testdata/worktree-captured.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	var f worktreeCapturedFixture
	if err = json.NewDecoder(gz).Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f
}
func compareWorktreeIndex(t *testing.T, f worktreeCapturedFixture) int {
	t.Helper()
	actual, expected := append([]WorktreeView(nil), f.Views...), append([]WorktreeView(nil), f.Views...)
	b := &worktreeInspectionBudget{ctx: context.Background(), remaining: worktreeInspectionLimit}
	if !b.take(len(actual)) {
		t.Fatal("views")
	}
	index, ok := worktreeLandingInputs(f.Projection, b)
	if !ok {
		t.Fatal("input budget")
	}
	g := f.graph()
	g.inspectionBudget = b
	if _, _, _, ok = index.gitInputs(actual, g, f.Repository, b); !ok {
		t.Fatal("git inputs")
	}
	// Include the production remote-tip collector's visits in the same budget.
	if !b.take(len(f.Tracking)) {
		t.Fatal("tracking inputs")
	}
	got := classifyWorktreeRows(index, actual, g, f.Repository, f.Remote, f.Tracking, b)
	if b.failed {
		t.Fatalf("index exhausted %d steps", worktreeInspectionLimit-b.remaining)
	}
	reference, ok := worktreeLandingInputs(f.Projection, &worktreeInspectionBudget{ctx: context.Background(), remaining: 1000000})
	if !ok {
		t.Fatal("reference inputs")
	}
	if _, _, _, ok = reference.gitInputs(expected, f.graph(), f.Repository, &worktreeInspectionBudget{ctx: context.Background(), remaining: 1000000}); !ok {
		t.Fatal("reference refs")
	}
	want := exhaustiveWorktreeRows(reference.rows, expected, f.graph(), f.Repository, f.Remote, f.Tracking, reference.targets[f.Repository])
	for _, vs := range [][]WorktreeView{actual, expected} {
		for i := range vs {
			for j := range vs[i].Rows {
				if vs[i].Rows[j].Git != nil {
					vs[i].Rows[j].Git.MeasuredAt = 0
				}
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deletable got=%v want=%v", got, want)
	}
	for i := range actual {
		if !reflect.DeepEqual(actual[i], expected[i]) {
			t.Fatalf("view %d differs\ngot=%+v\nwant=%+v", i, actual[i], expected[i])
		}
	}
	return worktreeInspectionLimit - b.remaining
}
func TestWorktreeCapturedRoomWithinOriginalBudget(t *testing.T) {
	f := readWorktreeCapturedFixture(t)
	if len(f.Views) != 35 || len(f.Projection.Commitments) != 2402 || len(f.Projection.Statements) != 12012 {
		t.Fatal("captured shape changed")
	}
	used := compareWorktreeIndex(t, f)
	t.Logf("captured room: %d/%d inspection steps; 35 checkouts, 2402 distinct commitment rows", used, worktreeInspectionLimit)
}
func TestWorktreeIndexMatchesExhaustiveRules(t *testing.T) {
	for _, mode := range []string{"complete", "missing-parent", "missing-object", "shallow", "unknown-ref"} {
		t.Run(mode, func(t *testing.T) {
			f := worktreeCapturedFixture{Repository: "repository", Remote: "origin", Tracking: map[string]string{"refs/heads/main": "refs/remotes/origin/main"}}
			id := func(i int) string { return fmt.Sprintf("%040x", i) }
			f.Graph.Refs = map[string]string{"refs/heads/main": id(4), "refs/remotes/origin/main": id(4)}
			f.Graph.Objects = map[string]bool{}
			f.Graph.Parents = map[string][]string{}
			for i := 1; i <= 5; i++ {
				f.Graph.Objects[id(i)] = true
				if i > 1 {
					f.Graph.Parents[id(i)] = []string{id(i - 1)}
				} else {
					f.Graph.Parents[id(i)] = nil
				}
			}
			f.Graph.Parents[id(5)] = []string{id(2)}
			for i := 0; i < 130; i++ {
				branch := fmt.Sprintf("branch-%d", i)
				head := id(i%5 + 1)
				f.Graph.Refs["refs/heads/"+branch] = head
				f.Views = append(f.Views, WorktreeView{Checkout: branch, Branch: branch, Head: id(1), State: "clean", Current: i == 0, Detached: i == 1})
			}
			for i := 0; i < 100; i++ {
				c := workroom.Commitment{Request: fmt.Sprintf("request-%d", i/3), Promise: fmt.Sprintf("promise-%d", i), Status: []string{"satisfied", "abandoned", "reneged", "promised", "cancelled", "future-status"}[i%6], Candidate: id(i%5 + 1)}
				if i%7 == 0 {
					c.ApprovedNotLanded = true
					c.Approval = "approval"
				}
				if i%11 == 0 {
					c.LandingReceipt = "receipt"
					c.TargetRepo = f.Repository
					c.TargetRef = "refs/heads/main"
				}
				if i%13 == 0 {
					c.Candidate = "malformed"
				}
				f.Projection.Commitments = append(f.Projection.Commitments, c)
				// Retired artifacts and direct request provenance still name older heads.
				event := fmt.Sprintf("artifact-%d", i)
				f.Projection.Statements = append(f.Projection.Statements, workroom.Statement{Event: event, Kind: workroom.KindArtifact, Retired: i%2 == 0, Body: map[string]string{"head": id(2), "branch": fmt.Sprintf("refs/heads/branch-%d", i)}})
				if f.Projection.Provenance == nil {
					f.Projection.Provenance = map[string][]string{}
				}
				f.Projection.Provenance[event] = []string{c.Request, c.Promise}
			}
			f.Projection.Statements = append(f.Projection.Statements, workroom.Statement{Event: "receipt", Body: map[string]string{"merge_head": id(4), "merge_target_repo": f.Repository, "merge_target_ref": "refs/heads/main"}})
			switch mode {
			case "missing-parent":
				delete(f.Graph.Parents, id(2))
			case "missing-object":
				delete(f.Graph.Objects, id(2))
			case "shallow":
				f.Graph.Shallow = true
			case "unknown-ref":
				delete(f.Graph.Refs, "refs/heads/branch-5")
			}
			compareWorktreeIndex(t, f)
		})
	}
}

func TestWorktreeInputIndexRetainsDirectHeadsAndDistinctPromises(t *testing.T) {
	id := func(i int) string { return fmt.Sprintf("%040x", i) }
	p := workroom.Projection{Commitments: []workroom.Commitment{
		{Request: "request", Promise: "first", Report: "report", Candidate: id(1), Status: "satisfied", LandingReceipt: "receipt"},
		{Request: "request", Promise: "second", Candidate: "invalid", Status: "promised"},
	}, Statements: []workroom.Statement{
		{Event: "request", Body: map[string]string{"head": id(2), "commit": id(3), "branch": "refs/heads/first"}},
		{Event: "artifact", Kind: workroom.KindArtifact, Retired: true, Body: map[string]string{"head": id(4), "branch": "second"}},
		{Event: "report", Body: map[string]string{"commit": id(5)}},
		{Event: "receipt", Body: map[string]string{"merge_head": id(6)}},
	}, Provenance: map[string][]string{"artifact": {"request", "first"}}}
	index, ok := worktreeLandingInputs(p, &worktreeInspectionBudget{ctx: context.Background(), remaining: worktreeInspectionLimit})
	if !ok || len(index.rows) != 2 {
		t.Fatal("distinct promises lost")
	}
	for i, row := range index.rows {
		for _, head := range []string{id(2), id(3), id(4)} {
			if !row.heads[head] {
				t.Fatalf("row %d lost %s", i, head)
			}
		}
		if !row.branches["first"] || !row.branches["second"] {
			t.Fatal("branch association lost")
		}
	}
	if !index.rows[0].heads[id(1)] || !index.rows[0].heads[id(5)] || index.rows[1].heads[id(5)] {
		t.Fatal("candidate/report association changed")
	}
	if !index.rows[1].unknownHead || !protectsWorktree(index.rows[1].row) {
		t.Fatal("invalid unfinished head lost protection")
	}
	if index.rows[0].row.MergeHead != id(6) || !index.rows[0].row.ReceiptLegacy || index.rows[1].row.MergeHead != "" || !index.objects[id(6)] {
		t.Fatal("selected receipt join changed")
	}
}

func TestWorktreeGraphFailureDiscardsPartialResults(t *testing.T) {
	f := readWorktreeCapturedFixture(t)
	for _, mode := range []string{"inspection", "walk", "cancelled", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := &worktreeInspectionBudget{ctx: ctx, remaining: worktreeInspectionLimit}
			index, ok := worktreeLandingInputs(f.Projection, b)
			if !ok {
				t.Fatal("input")
			}
			views := append([]WorktreeView(nil), f.Views...)
			g := f.graph()
			g.inspectionBudget = b
			switch mode {
			case "inspection":
				b.remaining = 15000
			case "walk":
				g.walkRemaining = 1
			case "cancelled":
				cancel()
			case "cycle":
				g.parents = map[string][]string{}
				for k, v := range f.Graph.Parents {
					g.parents[k] = v
				}
				head := views[0].Head
				g.parents[head] = []string{head}
			}
			for i := range views {
				views[i].Rows = []WorktreeRow{{Request: "old"}}
				views[i].Row = "old"
				views[i].RowsOmitted = 100
				views[i].Approved = "old"
				views[i].LandedInto = "old"
				yes := true
				views[i].RemoteContains = &yes
			}
			if got := classifyWorktreeRows(index, views, g, f.Repository, f.Remote, f.Tracking, b); len(got) != 0 {
				t.Fatalf("partial advice: %v", got)
			}
			for _, v := range views {
				if v.Classification != "unknown" || v.Row != "" || len(v.Rows) != 0 || v.RowsOmitted != 0 || v.Approved != "" || v.LandedInto != "" || v.RemoteContains != nil {
					t.Fatalf("partial fields escaped: %+v", v)
				}
			}
		})
	}
}

func TestCheckoutAncestryMatchesContains(t *testing.T) {
	id := func(i int) string { return fmt.Sprintf("%040x", i) }
	for _, mode := range []string{"complete", "incomplete", "shallow", "missing-object"} {
		t.Run(mode, func(t *testing.T) {
			g := &landingGraph{objects: map[string]bool{}, parents: map[string][]string{}, ancestors: map[string]landingAncestors{}, walkRemaining: landingWalkLimit}
			for i := 1; i <= 6; i++ {
				g.objects[id(i)] = true
				g.parents[id(i)] = nil
			}
			g.parents[id(2)] = []string{id(1)}
			g.parents[id(3)] = []string{id(2)}
			g.parents[id(4)] = []string{id(1)}
			g.parents[id(5)] = []string{id(3), id(4)}
			switch mode {
			case "incomplete":
				delete(g.parents, id(2))
			case "shallow":
				g.shallow = true
			case "missing-object":
				delete(g.objects, id(2))
			}
			views := make([]WorktreeView, 130)
			for i := range views {
				views[i].Head = id(i%7 + 1)
			}
			b := &worktreeInspectionBudget{ctx: context.Background(), remaining: worktreeInspectionLimit}
			a, ok := g.checkoutAncestry(views, b)
			if !ok {
				t.Fatal("bounded graph refused")
			}
			for i, v := range views {
				for object := 1; object <= 7; object++ {
					got, want := a.contains(i, id(object), g.objects[id(object)]), g.contains(v.Head, id(object))
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("tip %s object %s got=%v want=%v", v.Head, id(object), got, want)
					}
				}
			}
		})
	}
}
