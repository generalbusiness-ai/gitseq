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
		if err := checkVerifiedFrontier(ctx, store, current.VerifiedFrontier, verified); err != nil {
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
		if current.VerifiedFrontier != nil && current.VerifiedFrontier.Head == head {
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

func checkVerifiedFrontier(ctx context.Context, store gitstore.Store, previous *apphost.VerifiedFrontier, verified kernel.Verification) error {
	if previous == nil {
		return nil
	}
	if verified.Depth < previous.Depth {
		return fmt.Errorf("refuse verified frontier rollback: depth %d is shorter than previously verified depth %d", verified.Depth, previous.Depth)
	}
	if verified.Head == previous.Head {
		if verified.Depth != previous.Depth {
			return errors.New("refuse inconsistent verified frontier depth")
		}
		return nil
	}
	count, err := sequenceContinuation(ctx, store, previous.Head, verified.Head)
	if err != nil {
		return fmt.Errorf("refuse non-descendant verified frontier: %w", err)
	}
	if verified.Depth != previous.Depth+count {
		return fmt.Errorf("refuse non-descendant verified frontier: %s does not continue previously verified head %s at depth %d", verified.Head, previous.Head, previous.Depth)
	}
	return nil
}
