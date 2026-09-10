package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gitseqhost "github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
)

// The deterministic ref that names one attempt at one governing record's work.
//
// `refs/gitseq/lanes/<governing event hash>/<attempt>` mirrors
// `refs/gitseq/merge-receipts/<key>`: an ordinary shared Git ref in the
// repository's common directory, outside the durable event log, that no fold
// reads and no admission decision consults. The namespace string is the
// adopted design's; the Go names here say "checkout attempt", because "lane"
// already names a work query lane in internal/statusview and a second meaning
// for one word in one repository is how two things come to be confused.
//
// The ref answers one direction only. Given a governing record it names the
// tips of the attempts made at it, which is what survives the checkout being
// removed and the branch being deleted. The other direction — given a tip,
// which record governs it — stays what it was: a source trailer plus durable
// corroboration. A ref is written by anyone who can write to this repository
// and can point anywhere, so read back it is a claim and nothing more.
const checkoutAttemptNamespace = "refs/gitseq/lanes/"

// checkoutAttemptLimit bounds allocation the way the resident claim bounds
// contention. Each pass of the loop is one read and at most one
// compare-and-swap, and exhausting the budget refuses rather than looping: a
// namespace whose free attempt keeps being taken under us is a repository
// several processes are starting work in, and spinning there would trade a
// clear refusal for an unbounded wait. Eight is the number the resident claim
// already uses for the same shape of contention, and reusing it keeps one
// answer rather than two.
const checkoutAttemptLimit = 8

// CheckoutAttempt is one allocated attempt: the governing record it was
// allocated for, the attempt number, the ref that names it, and the tip that
// ref points at.
//
// It grants nothing. A checkout, a branch and this ref are all claimed
// identity; none of them is evidence about who may sign what, and none widens
// what may be deleted.
type CheckoutAttempt struct {
	Governing string `json:"governing"`
	Number    int    `json:"attempt"`
	Ref       string `json:"ref"`
	Tip       string `json:"tip"`
}

// CheckoutAttemptRef builds the ref for one attempt at one governing record.
//
// The attempt suffix is not decoration. One ref per governing hash cannot give
// every recut its own ref, because a recut under one request shares that
// request's hash; the suffix is what makes two attempts at one record two
// distinguishable things.
//
// A bare `<hash>` form is refused outright rather than left implicit. Git
// cannot hold both `refs/gitseq/lanes/<hash>` and
// `refs/gitseq/lanes/<hash>/1`, so a builder that could emit the first would
// be a builder that could make every later attempt at that record
// unwriteable.
func CheckoutAttemptRef(governing string, attempt int) (string, error) {
	if attempt < 1 {
		return "", fmt.Errorf("attempt must be 1 or more, got %d", attempt)
	}
	hash, err := governingEventHash(governing)
	if err != nil {
		return "", err
	}
	return checkoutAttemptNamespace + hash + "/" + strconv.Itoa(attempt), nil
}

// governingEventHash is the event half of a canonical identifier: the object
// name after the "#", without its format prefix. The whole identifier is
// validated first, so a fragment, a record number or prose can never reach the
// ref namespace.
func governingEventHash(governing string) (string, error) {
	if !gitseqhost.ValidEventID(governing) {
		return "", fmt.Errorf("governing record %q is not a canonical event identifier", governing)
	}
	_, event, _ := strings.Cut(governing, "#")
	hash := event[strings.LastIndex(event, ":")+1:]
	if hash == "" {
		return "", fmt.Errorf("governing record %q names no event hash", governing)
	}
	return hash, nil
}

// refStore is the ref half of the object store, named as an interface so a
// test can put a failing or contending writer in front of the real one
// without a process-wide seam. gitstore.Store satisfies it as it stands.
type refStore interface {
	RefValue(ctx context.Context, ref string) (string, bool, error)
	UpdateRef(ctx context.Context, ref, newOID, oldOID string) error
}

var _ refStore = gitstore.Store{}

// ClaimCheckoutAttempt allocates the next free attempt for one governing
// record and points its ref at tip.
//
// Allocation is a create against a missing ref: the compare-and-swap carries
// an empty expected old value, which is Git's "this ref must not exist", the
// same idiom the kernel already uses to seal an event commit. It is never a
// read followed by an unconditional write, because between the two a second
// process allocating the same number would be silently overwritten and one
// ref would then name two attempts.
//
// A refused create is not an error. Git refuses for one reason that matters
// here — somebody else got there first — and parsing Git's prose to tell that
// case from a transient one would be a second answer to a question the ref
// itself answers: after a refusal the ref is read again, and it is contention
// exactly when somebody's value is now there. A transient failure leaves the
// ref still free, so the same attempt number is tried again rather than
// abandoned, and the whole loop is bounded either way.
//
// An existing attempt is never reused, whatever it points at. A second
// checkout for one governing record is an ordinary thing — a recut, a review
// tree — and it is a second attempt; reusing the first would make one ref name
// two of them and lose exactly what the suffix exists for. Nothing about the
// caller reaches this decision: no confirmation flag, no duplicate warning and
// no checkout path is an input here.
func (w *Workspace) ClaimCheckoutAttempt(ctx context.Context, governing, tip string) (CheckoutAttempt, error) {
	return claimCheckoutAttempt(ctx, w.Store, governing, tip)
}

func claimCheckoutAttempt(ctx context.Context, refs refStore, governing, tip string) (CheckoutAttempt, error) {
	if !exactObjectID(tip) {
		return CheckoutAttempt{}, fmt.Errorf("attempt tip %q is not a full object name", tip)
	}
	attempt := 1
	for try := 0; try < checkoutAttemptLimit; try++ {
		ref, err := CheckoutAttemptRef(governing, attempt)
		if err != nil {
			return CheckoutAttempt{}, err
		}
		// A store that cannot be read is not contention. Reporting it as a
		// taken attempt would walk the whole budget and then refuse for the
		// wrong reason.
		_, present, err := refs.RefValue(ctx, ref)
		if err != nil {
			return CheckoutAttempt{}, fmt.Errorf("read %s: %w", ref, err)
		}
		if present {
			attempt++
			continue
		}
		if err := refs.UpdateRef(ctx, ref, tip, ""); err == nil {
			return CheckoutAttempt{Governing: governing, Number: attempt, Ref: ref, Tip: tip}, nil
		}
		if _, taken, readErr := refs.RefValue(ctx, ref); readErr == nil && taken {
			attempt++
		}
	}
	hash, _ := governingEventHash(governing)
	return CheckoutAttempt{}, fmt.Errorf("every attempt under %s%s was taken or refused across %d tries; another process is allocating attempts in this repository, so this one refuses rather than waiting",
		checkoutAttemptNamespace, hash, checkoutAttemptLimit)
}

// PointCheckoutAttempt moves an allocated attempt to the tip the checkout
// actually holds, under compare-and-swap against the value this process last
// saw.
//
// A tip the ref already names is a no-op: nothing is written and no attempt is
// allocated, which is what makes pointing an attempt at the same place twice
// harmless. A ref that has moved to some third value belongs to whoever moved
// it, and the swap refuses rather than taking it.
func (w *Workspace) PointCheckoutAttempt(ctx context.Context, attempt CheckoutAttempt, tip string) (CheckoutAttempt, error) {
	return pointCheckoutAttempt(ctx, w.Store, attempt, tip)
}

func pointCheckoutAttempt(ctx context.Context, refs refStore, attempt CheckoutAttempt, tip string) (CheckoutAttempt, error) {
	if attempt.Ref == "" || attempt.Tip == "" {
		return CheckoutAttempt{}, errors.New("cannot point an attempt that was never allocated")
	}
	if !exactObjectID(tip) {
		return CheckoutAttempt{}, fmt.Errorf("attempt tip %q is not a full object name", tip)
	}
	if tip == attempt.Tip {
		return attempt, nil
	}
	if err := refs.UpdateRef(ctx, attempt.Ref, tip, attempt.Tip); err != nil {
		return CheckoutAttempt{}, fmt.Errorf("point %s at %s: %w", attempt.Ref, tip, err)
	}
	attempt.Tip = tip
	return attempt, nil
}
