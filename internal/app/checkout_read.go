package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"time"
)

// CheckoutRead is one captured observation of this repository, shared by every
// judgment a worktrees request reaches.
//
// It exists because those judgments have to answer about one world. Reading
// the checkout listing or the ref inventory a second time answers about a
// different repository whenever a branch or a checkout moves in between, and
// there is no useful sense in which the two answers then describe the same
// place. Two separate budgets have the same defect one level up: each bounds
// its own judgment and neither says what the request costs, so a request can
// spend twice the bound its documentation names and still report that every
// limit held.
//
// So the listing, the ref inventory, the remote name, the deadline and the
// step budget are captured once here, and every judgment reads and spends from
// this one read.
//
// What is shared is the observation, never the conclusion. The cleanup
// classification and the reverse association each read this and neither reads
// the other's result: an unsigned source trailer must not become cleanup
// authority, and a cleanup verdict must not decide what a commit claims. The
// sharing is of inputs and bounds, and the two judgments stay two.
type CheckoutRead struct {
	// LocalRepo is the captured listing: the served path, the linkable remote
	// and the one Worktrees slice both judgments annotate in place.
	LocalRepo

	// refs is the captured ref inventory and refsKnown says the read finished
	// inside its bound. Nothing writes to the map after capture, so the
	// traversals built over it stay independent of each other.
	refs      map[string]string
	refsKnown bool
	// remoteName is the configured remote the landing measurement compares
	// against, read once with everything else.
	remoteName string

	// records is what each captured checkout's own private record claims, in
	// the order of Worktrees. It is captured here with everything else so the
	// association answers about the same repository the refs and the listing
	// came from; reading a record later would answer about a checkout that may
	// since have been stamped, removed or copied into.
	records []capturedCheckoutRecord

	// ctx carries the one deadline and budget the one step allowance. Both are
	// spent by every judgment: exhaustion in one is exhaustion of the read,
	// which is what makes the bound a bound on the request.
	ctx    context.Context
	budget *worktreeInspectionBudget
	cancel context.CancelFunc
}

// capturedCheckoutRecord is one checkout's record as it was found. A checkout
// with no record has neither: the absence is ordinary, because nothing
// requires a checkout to have been created by `gs worktree`.
type capturedCheckoutRecord struct {
	present bool
	record  CheckoutRecord
	// reason says why a record that is there could not be read. A file that
	// exists and does not decode is a different fact from no file at all, and
	// reporting it as absence would hide a corrupted or copied record.
	reason string
}

// Close releases the captured deadline. Every capture is closed.
func (r *CheckoutRead) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

// Exhausted reports whether the shared budget or deadline is already spent. A
// judgment that has not started yet still has to say unknown once it is.
func (r *CheckoutRead) Exhausted() bool { return r.budget.failed || r.ctx.Err() != nil }

// CaptureCheckouts takes the one observation a worktrees request answers from.
// The listing keeps its own eight-second cache; everything else here is read
// now, once.
func (w *Workspace) CaptureCheckouts(ctx context.Context) (*CheckoutRead, error) {
	local, err := w.LocalWorktrees(ctx)
	if err != nil {
		return nil, err
	}
	read := w.captureAround(ctx, local.Worktrees)
	read.Path, read.Remote = local.Path, local.Remote
	return read, nil
}

// captureAround is the same capture around a listing the caller already holds.
// It is what the single-judgment entry points use, so that one mechanism
// serves both shapes: a caller with one question still gets one ref inventory
// read, one deadline and one budget, and a caller with two gets them shared.
func (w *Workspace) captureAround(ctx context.Context, views []WorktreeView) *CheckoutRead {
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	refs, known := readRefInventory(bounded, w.Repo)
	return &CheckoutRead{
		LocalRepo:  LocalRepo{Worktrees: views},
		refs:       refs,
		refsKnown:  known,
		records:    captureCheckoutRecords(views),
		remoteName: landingRemote(bounded, w.Repo),
		ctx:        bounded,
		budget:     &worktreeInspectionBudget{ctx: bounded, remaining: worktreeInspectionLimit},
		cancel:     cancel,
	}
}

// AdviseCheckouts is the whole of what a worktrees request may publish from one
// captured read: the cleanup candidates and the association table, or neither.
// It is the one response-disposition step, and `handleWorktrees` obtains its
// pair only through it. The two judgments stay separately callable, which is
// how the tests exercise each of them on its own.
//
// Both judgments run over the shared read, and then one question is asked of
// the pair: did either judgment finish under the conditions it was given. The
// answer is no when the shared budget or deadline is spent, and equally when
// the association stopped at a bound of its own, whether that is the tip
// limit, the per-tip depth limit or a captured tip this repository does not
// hold. Those are the same kind of fact as running out of steps: a read that
// did not finish.
//
// When the answer is no, everything both judgments produced is withheld: no
// candidate is offered, every checkout reads unknown for its classification
// and its grade, and both carry the reason. The adopted design requires the
// whole batch's advice to be discarded, and the whole batch is the response
// rather than the judgment that happened to notice. Publishing the first
// answer because it was computed before the bound was reached offers a
// deletion candidate derived from a read that never finished looking for the
// thing that would have protected it, which is the one direction this layer
// must never fail in.
//
// Withholding is all it does. No claim becomes evidence and no judgment reads
// the other; the step can only turn an answer into unknown, never the reverse.
func (w *Workspace) AdviseCheckouts(read *CheckoutRead, snapshot Snapshot) ([]string, AssociationTable) {
	deletable := w.ClassifyCheckouts(read, snapshot.Projection)
	table := w.AssociateCheckouts(read, snapshot)
	if !read.Exhausted() && table.Complete {
		return deletable, table
	}
	// The table's own reason names the bound that stopped it. Exhaustion is
	// the only case that can arrive without one.
	reason := table.Reason
	if reason == "" {
		reason = exhaustedRead
	}
	return unknownWorktrees(read.Worktrees, reason), unknownAssociationTable(read.Worktrees, reason)
}

// exhaustedRead is the one reason every bound of the shared read reports.
const exhaustedRead = "worktree inspection limit or cancellation"

// graph is one traversal over the captured inventory, charged to the captured
// budget. Each judgment that needs ancestry takes its own; they accumulate
// separately and read the same refs.
func (r *CheckoutRead) graph() *landingGraph {
	g := newLandingGraph(r.refs, r.refsKnown)
	g.inspectionBudget = r.budget
	return g
}

// captureCheckoutRecords reads the private record each captured checkout
// carries. It starts no Git process: a checkout names its own private Git
// directory in its `.git` entry, so this is one small file read per checkout,
// under the same 128-checkout cap the listing already enforces.
//
// Nothing here decides anything. A record is a claim, exactly like a source
// trailer, and it is graded by the same grader against the same verified event
// set. What is captured is the bytes that were there when everything else was
// read.
func captureCheckoutRecords(views []WorktreeView) []capturedCheckoutRecord {
	records := make([]capturedCheckoutRecord, len(views))
	for i, view := range views {
		path, found := checkoutRecordPath(view.path)
		if !found {
			continue
		}
		record, err := decodeCheckoutRecord(path)
		if err != nil {
			if !os.IsNotExist(err) {
				records[i] = capturedCheckoutRecord{reason: err.Error()}
			}
			continue
		}
		records[i] = capturedCheckoutRecord{present: true, record: record}
	}
	return records
}

// checkoutRecordDigest is the captured records exactly as this pass used
// them, for the association cache key. A record written, changed or removed
// while no ref and no checkout moved is a different world to associate, and a
// cache keyed on less than its inputs stays wrong rather than going stale.
func checkoutRecordDigest(records []capturedCheckoutRecord) string {
	sum := sha256.New()
	for _, captured := range records {
		present := byte(0)
		if captured.present {
			present = 1
		}
		sum.Write([]byte{present})
		sum.Write([]byte(captured.record.Genesis))
		sum.Write([]byte{0})
		sum.Write([]byte(captured.record.ObjectFormat))
		sum.Write([]byte{0})
		sum.Write([]byte(captured.record.Governing))
		sum.Write([]byte{0})
		sum.Write([]byte(captured.reason))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}
