package app

import (
	"container/heap"
	"encoding/binary"
)

// Checkout membership is propagated through the captured graph once. Each
// word represents 64 checkouts; there is no additional checkout or graph cap.
type checkoutMask []uint64

func (m checkoutMask) has(i int) bool { return m[i/64]&(uint64(1)<<uint(i%64)) != 0 }
func (m checkoutMask) set(i int)      { m[i/64] |= uint64(1) << uint(i%64) }

type checkoutAncestry struct {
	positive map[string]checkoutMask
	exact    map[string]checkoutMask
	complete checkoutMask
	all      checkoutMask
}

func (g *landingGraph) checkoutAncestry(views []WorktreeView, b *worktreeInspectionBudget) (checkoutAncestry, bool) {
	words := (len(views) + 63) / 64
	a := checkoutAncestry{positive: map[string]checkoutMask{}, exact: map[string]checkoutMask{}, complete: make(checkoutMask, words), all: make(checkoutMask, words)}
	indegree := map[string]int{}
	for id, parents := range g.parents {
		if !b.take(1) {
			return a, false
		}
		if _, ok := indegree[id]; !ok {
			indegree[id] = 0
		}
		for _, parent := range parents {
			if !b.take(1) {
				return a, false
			}
			indegree[parent]++
		}
	}
	for i, v := range views {
		if !b.take(1) {
			return a, false
		}
		a.all.set(i)
		if a.exact[v.Head] == nil {
			a.exact[v.Head] = make(checkoutMask, words)
		}
		a.exact[v.Head].set(i)
		if !g.objects[v.Head] {
			continue
		}
		if a.positive[v.Head] == nil {
			a.positive[v.Head] = make(checkoutMask, words)
		}
		a.positive[v.Head].set(i)
		if !g.shallow {
			a.complete.set(i)
		}
		if _, ok := indegree[v.Head]; !ok {
			indegree[v.Head] = 0
		}
	}
	queue := []string{}
	for id, degree := range indegree {
		if !b.take(1) {
			return a, false
		}
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		if !b.take(1) {
			return a, false
		}
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		mask := a.positive[id]
		parents, known := g.parents[id]
		if mask != nil {
			if g.walkRemaining == 0 {
				return a, false
			}
			g.walkRemaining--
			if !known {
				for j := range mask {
					if !b.take(1) {
						return a, false
					}
					a.complete[j] &^= mask[j]
				}
			}
		}
		for _, parent := range parents {
			if !b.take(1) {
				return a, false
			}
			if mask != nil {
				if a.positive[parent] == nil {
					a.positive[parent] = make(checkoutMask, words)
				}
				for j := range mask {
					if !b.take(1) {
						return a, false
					}
					a.positive[parent][j] |= mask[j]
				}
			}
			indegree[parent]--
			if indegree[parent] == 0 {
				queue = append(queue, parent)
			}
		}
	}
	return a, visited == len(indegree) && b.take(0)
}

// Equivalent match outcomes share a bounded list of their newest rows. Their
// full count remains separate, so omitted rows and distinct promises survive.
type worktreeMatchGroup struct {
	matched, branch, exact checkoutMask
	protected              bool
	count                  int
	newest                 [worktreeRowLimit]int
}

func (g *worktreeMatchGroup) rank(i int) int {
	rank := 1
	if g.branch.has(i) {
		rank = 2
	}
	if g.exact.has(i) {
		rank = 3
	}
	if g.protected {
		rank += 4
	}
	return rank
}

type worktreeMatches struct {
	ancestry                      checkoutAncestry
	groups                        []*worktreeMatchGroup
	protected, uncertain, settled checkoutMask
}

func indexWorktreeMatches(rows []worktreeLandingInput, views []WorktreeView, g *landingGraph, b *worktreeInspectionBudget) (worktreeMatches, bool) {
	a, ok := g.checkoutAncestry(views, b)
	if !ok {
		return worktreeMatches{}, false
	}
	words := len(a.all)
	result := worktreeMatches{ancestry: a, protected: make(checkoutMask, words), uncertain: make(checkoutMask, words), settled: make(checkoutMask, words)}
	branches := map[string]checkoutMask{}
	for i, v := range views {
		if !b.take(1) {
			return result, false
		}
		if v.Branch == "" {
			continue
		}
		if branches[v.Branch] == nil {
			branches[v.Branch] = make(checkoutMask, words)
		}
		branches[v.Branch].set(i)
	}
	groups := map[string]*worktreeMatchGroup{}
	landed := map[string]checkoutMask{}
	for i, input := range rows {
		if !b.take(1) {
			return result, false
		}
		protect := protectsWorktree(input.row)
		match, branch, exact, abandoned := make(checkoutMask, words), make(checkoutMask, words), make(checkoutMask, words), make(checkoutMask, words)
		for head := range input.heads {
			if !b.take(1) {
				return result, false
			}
			positive, rawExact := a.positive[head], a.exact[head]
			for j := range match {
				if !b.take(1) {
					return result, false
				}
				var pos, ex uint64
				if rawExact != nil {
					ex = rawExact[j]
				}
				if g.objects[head] {
					if positive != nil {
						pos = positive[j]
					}
					exact[j] |= ex
					if protect {
						result.uncertain[j] |= a.all[j] &^ (a.complete[j] | pos)
					}
				} else if protect {
					result.uncertain[j] |= a.all[j]
				}
				match[j] |= pos
				if input.row.Status == "abandoned" {
					abandoned[j] |= ex
				}
			}
		}
		for name := range input.branches {
			if !b.take(1) {
				return result, false
			}
			if mask := branches[name]; mask != nil {
				for j := range match {
					if !b.take(1) {
						return result, false
					}
					branch[j] |= mask[j]
					match[j] |= mask[j]
				}
			}
		}
		var target checkoutMask
		if input.row.Git != nil && input.row.LandingReceipt != "" {
			head := input.row.Git.TargetHead
			var found bool
			target, found = landed[head]
			if !found {
				target = make(checkoutMask, words)
				for vi, v := range views {
					if !b.take(1) {
						return result, false
					}
					if contains := g.contains(head, v.Head); contains != nil && *contains {
						target.set(vi)
					}
				}
				landed[head] = target
			}
		}
		key := make([]byte, 0, 24*words+1)
		any := false
		for j := range match {
			if !b.take(1) {
				return result, false
			}
			if protect {
				result.protected[j] |= match[j]
			}
			if protect && input.unknownHead {
				result.uncertain[j] |= a.all[j]
			}
			settled := abandoned[j]
			if target != nil {
				settled |= target[j]
			}
			result.settled[j] |= match[j] & settled
			any = any || match[j] != 0
			key = binary.LittleEndian.AppendUint64(key, match[j])
			key = binary.LittleEndian.AppendUint64(key, branch[j])
			key = binary.LittleEndian.AppendUint64(key, exact[j])
		}
		if !any {
			continue
		}
		if protect {
			key = append(key, 1)
		} else {
			key = append(key, 0)
		}
		group := groups[string(key)]
		if group == nil {
			group = &worktreeMatchGroup{matched: match, branch: branch, exact: exact, protected: protect}
			groups[string(key)] = group
			result.groups = append(result.groups, group)
		}
		group.newest[group.count%worktreeRowLimit] = i
		group.count++
	}
	return result, b.take(0)
}

type worktreeMatchCursor struct {
	group        *worktreeMatchGroup
	rank, offset int
}

func (c worktreeMatchCursor) row() int {
	return c.group.newest[(c.group.count-1-c.offset)%worktreeRowLimit]
}

type worktreeMatchHeap struct {
	cursors []worktreeMatchCursor
	budget  *worktreeInspectionBudget
}

func (h worktreeMatchHeap) Len() int { return len(h.cursors) }
func (h worktreeMatchHeap) Less(i, j int) bool {
	if !h.budget.take(1) {
		return false
	}
	a, b := h.cursors[i], h.cursors[j]
	return a.rank > b.rank || a.rank == b.rank && a.row() > b.row()
}
func (h worktreeMatchHeap) Swap(i, j int) { h.cursors[i], h.cursors[j] = h.cursors[j], h.cursors[i] }
func (h *worktreeMatchHeap) Push(x any)   { h.cursors = append(h.cursors, x.(worktreeMatchCursor)) }
func (h *worktreeMatchHeap) Pop() any {
	n := len(h.cursors) - 1
	x := h.cursors[n]
	h.cursors = h.cursors[:n]
	return x
}

func (m worktreeMatches) selectRows(view int, rows []worktreeLandingInput, b *worktreeInspectionBudget) ([]WorktreeRow, int, bool) {
	h := &worktreeMatchHeap{budget: b}
	total := 0
	for _, group := range m.groups {
		if !b.take(1) {
			return nil, 0, false
		}
		if !group.matched.has(view) {
			continue
		}
		total += group.count
		h.cursors = append(h.cursors, worktreeMatchCursor{group: group, rank: group.rank(view)})
	}
	heap.Init(h)
	var selected []WorktreeRow
	for h.Len() > 0 && len(selected) < worktreeRowLimit {
		if !b.take(1) {
			return nil, 0, false
		}
		c := h.cursors[0]
		selected = append(selected, rows[c.row()].row)
		c.offset++
		if c.offset < c.group.count && c.offset < worktreeRowLimit {
			h.cursors[0] = c
			heap.Fix(h, 0)
		} else {
			heap.Pop(h)
		}
	}
	return selected, total - len(selected), b.take(0)
}

func (a checkoutAncestry) contains(view int, object string, confirmed bool) *bool {
	if !confirmed {
		return nil
	}
	if positive := a.positive[object]; positive != nil && positive.has(view) {
		yes := true
		return &yes
	}
	if a.complete.has(view) {
		no := false
		return &no
	}
	return nil
}
