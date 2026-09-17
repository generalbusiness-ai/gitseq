package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

// attachImportGate pins a concurrent ref advance between verification and
// compare-and-swap in tests. Production leaves it empty.
var attachImportGate = func() {}

// attachCheckpointGate pins a storage failure after the ref CAS in tests.
var attachCheckpointGate = func() {}

// AttachSequence imports an immutable fetched candidate. expected is the
// authoritative ref observed before fetching, or empty if it was absent.
// Remote tracking refs are observations, never the authoritative sequence.
func AttachSequence(ctx context.Context, repo, genesis, objectFormat, head, expected string) (kernel.Verification, error) {
	if err := apphost.ValidateGenesis(objectFormat, genesis); err != nil {
		return kernel.Verification{}, fmt.Errorf("invalid attachment genesis: %w", err)
	}
	if err := apphost.ValidateGenesis(objectFormat, head); err != nil {
		return kernel.Verification{}, fmt.Errorf("invalid fetched head: %w", err)
	}
	if expected != "" {
		if err := apphost.ValidateGenesis(objectFormat, expected); err != nil {
			return kernel.Verification{}, fmt.Errorf("invalid expected authoritative head: %w", err)
		}
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return kernel.Verification{}, err
	}
	store := gitstore.Store{Repo: commonDir}
	verified, err := kernel.VerifyAt(ctx, store, genesis, head)
	if err != nil {
		return kernel.Verification{}, err
	}
	if expected != "" && expected != head {
		if _, err := sequenceContinuation(ctx, store, expected, head); err != nil {
			return kernel.Verification{}, fmt.Errorf("refuse non-descendant authoritative sequence: %w", err)
		}
	}
	metaDir, opened, err := ensureAttachmentConfig(ctx, repo, genesis, objectFormat)
	if err != nil {
		return kernel.Verification{}, err
	}
	refAccepted := false
	_, err = apphost.UpdateConfig(metaDir, opened, func(current *apphost.Config) (bool, error) {
		decision, err := checkVerifiedFrontier(ctx, store, genesis, current.VerifiedFrontier, verified)
		if err != nil {
			return false, err
		}
		attachImportGate()
		old := expected
		if old == "" {
			old = strings.Repeat("0", len(genesis))
		}
		// Even an unchanged candidate must compare: a concurrent local
		// append may have arrived since the caller observed expected.
		if err := store.UpdateRef(ctx, kernel.Ref(genesis), head, old); err != nil {
			return false, fmt.Errorf("refuse sequence import after the authoritative ref changed: %w", err)
		}
		refAccepted = true
		// The candidate the ref now carries is the witness only when the
		// judgement said so: a position the witness already covers leaves it
		// alone rather than writing a shorter or repeated frontier.
		if decision == frontierKeep {
			return false, nil
		}
		attachCheckpointGate()
		current.VerifiedFrontier = &apphost.VerifiedFrontier{Head: head, Depth: verified.Depth}
		return true, nil
	})
	if err != nil {
		if refAccepted {
			return kernel.Verification{}, fmt.Errorf("verified sequence is installed but local rollback witness could not advance; previous checkpoint retained, retry attach after restoring metadata writes: %w", err)
		}
		return kernel.Verification{}, err
	}
	return verified, nil
}

func sequenceContinuation(ctx context.Context, store gitstore.Store, previous, head string) (int, error) {
	commits, err := store.RevListAfter(ctx, previous, head)
	if err != nil {
		return 0, err
	}
	if len(commits) == 0 {
		return 0, fmt.Errorf("%s does not contain previous head %s", head, previous)
	}
	parents, err := store.CommitParents(ctx, commits[0])
	if err != nil {
		return 0, err
	}
	if len(parents) != 1 || parents[0] != previous {
		return 0, fmt.Errorf("%s does not continue previous head %s", head, previous)
	}
	return len(commits), nil
}

// frontierDecision says what the caller's transaction may do with the stored
// rollback witness once a verification has been judged against it.
type frontierDecision int

const (
	// frontierStore records the judged verification as the new witness.
	frontierStore frontierDecision = iota
	// frontierKeep leaves the stored witness exactly where it is, because the
	// verification describes a position that witness already covers.
	frontierKeep
)

// checkVerifiedFrontier judges one verification against the witness the
// stored configuration holds, inside that configuration's transaction, and
// says whether the witness may move.
//
// The witness exists to refuse a sequence ref that moved backwards. A
// verification shorter than the witness is not by itself that. Verifying a
// deep sequence takes minutes, and another process on the same checkout may
// append and advance the witness while that read runs; the finished read is
// then merely stale, and refusing it costs the caller a whole further
// verification for nothing. Such a read is admitted and the witness stays
// where the appender left it, so the marker never moves backwards.
//
// A stale read is admitted only on both of these, read here and not from
// memory: the sequence ref as it now stands is the witnessed head or
// continues it, and the shorter verification is an ancestor of the witnessed
// head at exactly the depth separating them. A ref that itself moved back
// fails the first test, and a sibling history fails the second, so both are
// still refused as rollbacks.
func checkVerifiedFrontier(ctx context.Context, store gitstore.Store, genesis string, previous *apphost.VerifiedFrontier, verified kernel.Verification) (frontierDecision, error) {
	if previous == nil {
		return frontierStore, nil
	}
	if verified.Depth < previous.Depth {
		if staleVerification(ctx, store, genesis, previous, verified) {
			return frontierKeep, nil
		}
		return frontierKeep, fmt.Errorf("refuse verified frontier rollback: depth %d is shorter than previously verified depth %d", verified.Depth, previous.Depth)
	}
	if verified.Head == previous.Head {
		if verified.Depth != previous.Depth {
			return frontierKeep, errors.New("refuse inconsistent verified frontier depth")
		}
		return frontierKeep, nil
	}
	count, err := sequenceContinuation(ctx, store, previous.Head, verified.Head)
	if err != nil {
		return frontierKeep, fmt.Errorf("refuse non-descendant verified frontier: %w", err)
	}
	if verified.Depth != previous.Depth+count {
		return frontierKeep, fmt.Errorf("refuse non-descendant verified frontier: %s does not continue previously verified head %s at depth %d", verified.Head, previous.Head, previous.Depth)
	}
	return frontierStore, nil
}

// staleVerification reports whether a verification shorter than the witness is
// a read that finished after the world moved on rather than a rollback. Any
// error answers no, so an unreadable ref or an unreachable history is refused
// as a rollback exactly as before.
func staleVerification(ctx context.Context, store gitstore.Store, genesis string, previous *apphost.VerifiedFrontier, verified kernel.Verification) bool {
	head, err := store.Head(ctx, kernel.Ref(genesis))
	if err != nil {
		return false
	}
	if head != previous.Head {
		if _, err := sequenceContinuation(ctx, store, previous.Head, head); err != nil {
			return false
		}
	}
	count, err := sequenceContinuation(ctx, store, verified.Head, previous.Head)
	if err != nil {
		return false
	}
	return count == previous.Depth-verified.Depth
}
