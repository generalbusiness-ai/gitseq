package gitstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// LineageTipLimit and LineageCommitLimit bound one reverse-association pass:
// how many tips it may start from, and how many first-parent commits it reads
// from each of them. The per-tip bound is what keeps one long branch from
// consuming the whole read: a global count would let the first tip in the list
// spend the budget and leave every later tip unexamined, which is a silently
// incomplete answer rather than a bounded one.
const (
	LineageTipLimit    = 256
	LineageCommitLimit = 512
)

// Lineage reads the first-parent lineage of each named tip, newest first,
// stopping at the boundary revision or at the per-tip commit limit.
//
// It is Graph's read with an explicit ref set instead of the fixed newest
// commits across all branches and tags. The railway asks "what happened here
// lately"; this asks "what does this branch claim", and the answer has to
// follow one branch's own line of descent rather than whatever else was
// committed alongside it. First parent is that line: on a working branch it is
// the work, and the merge parents it skips are the target the work landed on.
//
// The framing, the field stride and the per-object hash re-verification are
// Graph's, unchanged and shared rather than copied. Every commit read here is
// therefore proved to be the object its hash names before any trailer on it is
// read, which matters more here than on the railway: a trailer read from an
// unverified object would be a claim about durable work derived from bytes
// nobody checked. One AuditBatch covers the whole pass, not one per tip.
//
// Duplicate tips and duplicate commits are collapsed, so two checkouts of one
// branch and two branches sharing history each cost one read of the shared
// commits. The order of the result follows the tip order given, newest commit
// first within each tip.
//
// The result says how the read fell short as well as what it read. A bound
// that silently clipped its answer, or a tip whose object this repository does
// not hold, would otherwise reach the caller as a shorter history that looks
// finished, and a caller cannot tell those apart from the commits alone.
type LineageResult struct {
	Commits []GraphCommit
	// Truncated names the tips whose first-parent read reached the per-tip
	// limit before it met the boundary or a root. Their lineage is cut off.
	Truncated []string
	// Unavailable names the tips this repository does not hold. That is a
	// different fact from a repository holding no commits at all: one is a
	// captured tip whose object has gone, and the other is a complete answer
	// of no commits.
	Unavailable []string
}

// Incomplete reports whether any tip was cut off or could not be read. A
// caller whose contract forbids a partial answer stops on it.
func (r LineageResult) Incomplete() bool {
	return len(r.Truncated) > 0 || len(r.Unavailable) > 0
}

// unheldTip reports the refusals that mean this repository does not hold the
// object it was asked for, as distinct from holding no commits at all.
func unheldTip(err error) bool {
	return strings.Contains(err.Error(), "bad object") || strings.Contains(err.Error(), "bad revision")
}

func (s Store) Lineage(ctx context.Context, tips []string, boundary string, perTip int) (LineageResult, error) {
	if perTip <= 0 || perTip > LineageCommitLimit {
		perTip = LineageCommitLimit
	}
	var result LineageResult
	seenTip := make(map[string]bool, len(tips))
	seenCommit := map[string]bool{}
	for _, tip := range tips {
		if tip == "" || seenTip[tip] {
			continue
		}
		if len(seenTip) == LineageTipLimit {
			return LineageResult{}, fmt.Errorf("lineage was asked for more than %d tips", LineageTipLimit)
		}
		seenTip[tip] = true
		// One more than the limit is read on purpose. Asking for exactly the
		// limit answers "here are 512 commits" for a lineage of 512 and for
		// one of 5,000 alike, and nothing in the result tells them apart.
		args := []string{"log", "-z", "--first-parent", "-n", strconv.Itoa(perTip + 1), "--format=" + graphFormat, tip}
		// A boundary equal to the tip would exclude the tip itself. The
		// question is what this tip claims, so the tip is always read.
		if boundary != "" && boundary != tip {
			args = append(args, "--not", boundary)
		}
		output, err := s.run(ctx, nil, nil, append(args, "--")...)
		if err != nil {
			// A tip this repository does not hold is reported, not skipped. A
			// stale checkout listing can name a head that has since been
			// pruned, and answering about the other checkouts as though that
			// one had simply no history is how a missing object becomes a
			// finished-looking table.
			if unheldTip(err) {
				result.Unavailable = append(result.Unavailable, tip)
				continue
			}
			// An unborn repository holds no commits at all. That is a complete
			// answer of nothing, not an unavailable tip.
			if emptyHistory(err) {
				continue
			}
			return LineageResult{}, err
		}
		read, err := parseGraphStream(output)
		if err != nil {
			return LineageResult{}, err
		}
		if len(read) > perTip {
			result.Truncated = append(result.Truncated, tip)
			read = read[:perTip]
		}
		for _, commit := range read {
			if seenCommit[commit.Hash] {
				continue
			}
			seenCommit[commit.Hash] = true
			result.Commits = append(result.Commits, commit)
		}
	}
	if err := s.refuseMalformedCommits(ctx, result.Commits); err != nil {
		return LineageResult{}, err
	}
	return result, nil
}
