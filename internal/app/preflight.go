package app

import (
	"context"
	"fmt"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Filer is who an act would be signed as: the name the idempotency namespace is
// built from, and the roster fingerprint the fold reads. Both come from local
// configuration, so a surface can put every question below without touching the
// private key that would sign the act.
type Filer struct {
	Name        string
	Fingerprint string
}

// PreflightAct asks the fold what it would decide about an act that has not
// been signed. The answer is the fold's own: the record this act would become,
// folded onto the projection this process last verified, and its decision read
// back. An actor learns that a promise has no request before the attempt
// becomes a permanent row.
//
// judged is false whenever the question cannot be put honestly, and a caller
// that gets it must carry on and let the fold decide:
//
//   - the workroom cannot be folded here, or the folded world this process
//     holds is not the one the snapshot describes;
//   - the log already holds an act under this filer's idempotency key, so the
//     act is a retry and the kernel answers it: an identical intent replays the
//     accepted event and a different one is refused as a reused key. Judging it
//     as a fresh act would refuse a replay whose world had moved since, which
//     is the one recovery a filer must always have;
//   - the act is a verb whose payload this boundary does not build, which is
//     every guarded act: their own two-act admission guards judge them;
//   - the act's body cannot be settled here at all — an undefined kind, a
//     request that states no result, a reserved field supplied as input —
//     because the shared builder path this uses refuses to produce one, and
//     refuses the act again in its own words when it is signed. That is not the
//     whole of what the builder refuses: a cited retirement, a report basis and
//     an unratifiable target are judged later, after a key is read, and this
//     says nothing about them.
//
// It is advisory in one more way. It reads the local verified log, and an act
// submitted to a resident joins that resident's frontier, which may have moved.
// The fold at sequencing remains the authority.
func (w *Workspace) PreflightAct(ctx context.Context, filer Filer, act Act) (workroom.Decision, bool) {
	if _, held := w.AcceptedUnderKey(ctx, filer, act.IdempotencyKey); held {
		return workroom.Decision{}, false
	}
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return workroom.Decision{}, false
	}
	record, ok := w.prospectiveRecord(ctx, snapshot, filer.Fingerprint, act)
	if !ok {
		return workroom.Decision{}, false
	}
	return w.previewDecision(snapshot, record)
}

// ChainAct is one act of a chain as the fold will read it.
type ChainAct struct {
	Act Act
	// Event is the identifier this act is judged under: a prospective one for an
	// act still to be minted, or the identifier the log already holds it as.
	Event string
	// Replayed says the log already holds this act under its idempotency key.
	// The fold's world therefore contains it already: it is neither judged again
	// nor applied, and the sequencer will replay it or refuse its key.
	Replayed bool
}

// PreflightChain asks the fold about a chain before any of it is signed, and
// names the first act the fold would not admit. refused is false when it has
// nothing to say — every act admitted, or the question not honestly askable —
// because those are the same answer to a caller: carry on, the fold decides.
//
// Each act is judged against the world the acts before it would make, in a fold
// this call builds for the question and throws away, so an act citing one the
// chain has yet to mint is judged against the act that will exist. The verified
// projection this process publishes is untouched: appending to that is what the
// sequencer does.
//
// Replay is per act, because a chain is not all one thing. A retry of a chain
// whose prefix already landed carries accepted acts and new ones together: the
// accepted ones are already in the fold's world and are left to the sequencer,
// and the new suffix is judged against that world like any other act. Treating
// one accepted key as a replay of the whole chain let a malformed new act
// through behind it.
func (w *Workspace) PreflightChain(ctx context.Context, filer Filer, acts []ChainAct) (position int, decision workroom.Decision, refused bool) {
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return 0, workroom.Decision{}, false
	}
	folder, ok := w.scratchFold(ctx, snapshot.Head)
	if !ok {
		return 0, workroom.Decision{}, false
	}
	for index, entry := range acts {
		if entry.Replayed {
			continue
		}
		record, ok := w.prospectiveRecord(ctx, snapshot, filer.Fingerprint, entry.Act)
		if !ok {
			return 0, workroom.Decision{}, false
		}
		record.ID = entry.Event
		folder.Append(record)
		verdict, decided := folder.Decision(record.ID)
		if !decided {
			return 0, workroom.Decision{}, false
		}
		if verdict.Verdict != workroom.Effective {
			return index, verdict, true
		}
	}
	return 0, workroom.Decision{}, false
}

// ProspectiveEventID names the act a chain has yet to mint at this position. A
// canonical identifier always has the genesis hash width, so these keep the
// exact shape of the identifiers the acts will be given while naming no event
// any log can hold. A caller resolves its own intra-chain references to them, so
// that the fold reads the chain's citations as the identifiers they will become.
func (w *Workspace) ProspectiveEventID(index int) string {
	object := fmt.Sprintf("%0*x", len(w.config.Genesis), index+1)
	return w.EventID(object[len(object)-len(w.config.Genesis):])
}

// AcceptedUnderKey names the act this filer already holds under this idempotency
// key. It asks the kernel's own dedup index, and asks without the signing key:
// the identity that index is written under is the actor's fingerprint, which
// local configuration holds. An error is answered as nothing held, because this
// only decides whether to put a question, never whether to admit an act.
func (w *Workspace) AcceptedUnderKey(ctx context.Context, filer Filer, key string) (string, bool) {
	if key == "" || filer.Fingerprint == "" {
		return "", false
	}
	dedup := intent.DedupIdentityFor(w.workroomID(), filer.Fingerprint, w.idempotencyNamespace(filer.Name), key)
	prior, held, err := kernel.PriorAct(ctx, w.Store, w.workroomID(), dedup)
	if err != nil || !held {
		return "", false
	}
	return w.EventID(prior.Commit), true
}

// prospectiveRecord builds the record this act would become, by the same schema
// selection, body normalization and encoding the signing path uses. It stops
// short of signing: the only thing the fold reads that a signature carries is
// the actor, and that fingerprint comes from the roster.
func (w *Workspace) prospectiveRecord(ctx context.Context, snapshot Snapshot, actorFingerprint string, act Act) (workroom.Record, bool) {
	if actorFingerprint == "" {
		return workroom.Record{}, false
	}
	var schema string
	var payload any
	rests := append([]string(nil), act.RestsOn...)
	switch act.Verb {
	case VerbState:
		if act.GuardedReview {
			return workroom.Record{}, false
		}
		body, request, err := w.normalizeRequestShape(ctx, &snapshot, act.Kind, act.Body)
		if err != nil {
			return workroom.Record{}, false
		}
		if request {
			if body, err = w.resolveRequestChoice(ctx, act.Kind, body, nil); err != nil {
				return workroom.Record{}, false
			}
		}
		// Admission writes its own testimony onto the body it admits, so the
		// body judged here is the body that would be signed.
		if body, err = w.admitState(snapshot, stateAdmission{
			Kind: act.Kind, Body: body, RestsOn: rests, AllowDeadBasis: act.AllowDeadBasis,
		}, snapshot.Head); err != nil {
			return workroom.Record{}, false
		}
		schema = workroom.SchemaState
		if request {
			schema = workroom.SchemaStateV3
		}
		payload = workroom.State{Kind: act.Kind, Text: act.Text, Body: body}
	case VerbRatify:
		schema = workroom.SchemaRatify
		payload = workroom.Ratify{Target: act.Target}
		rests = []string{act.Target}
	case VerbSupersede:
		schema = workroom.SchemaSupersede
		payload = workroom.Supersede{Target: act.Target, Text: act.Text}
		rests = append([]string{act.Target}, rests...)
	default:
		return workroom.Record{}, false
	}
	normalized, err := w.normalizePayload(schema, payload)
	if err != nil {
		return workroom.Record{}, false
	}
	encoded, err := workroom.Encode(normalized)
	if err != nil {
		return workroom.Record{}, false
	}
	return workroom.Record{
		ID: w.ProspectiveEventID(0), Actor: actorFingerprint,
		Schema: schema, RestsOn: rests, Payload: encoded,
	}, true
}

// previewDecision folds one prospective record against the verified projection
// this workspace holds, under the same lock that publishes it: a preview can
// neither read a half-advanced fold nor leave anything in one.
func (w *Workspace) previewDecision(snapshot Snapshot, record workroom.Record) (workroom.Decision, bool) {
	w.snapshotMu.Lock()
	defer w.snapshotMu.Unlock()
	if w.snapshotFolder == nil || w.snapshotCache == nil || w.snapshotCache.Head != snapshot.Head {
		return workroom.Decision{}, false
	}
	return w.snapshotFolder.Preview(record), true
}

// scratchFold is a fold of this workroom at head that the caller may append to.
// It is built from its own verified read rather than copied from the published
// fold, because that fold backs the projection every reader in this process
// holds and nothing prospective may reach it. The read is the accelerated one,
// so it costs about what the snapshot this command already took cost.
func (w *Workspace) scratchFold(ctx context.Context, head string) (*workroom.Folder, bool) {
	selected, refusal := w.interpreter()
	if refusal != nil {
		return nil, false
	}
	reader := kernel.NewReader(w.Store, w.checkpointOptions())
	folder := selected.newFolder(nil)
	streamed := 0
	loaded, err := reader.LoadWithProgressStream(ctx, w.config.Genesis, nil, func(event kernel.Event) error {
		folder.Append(w.record(event))
		streamed++
		return nil
	})
	if err != nil || !loaded.Full || loaded.Verification.Head != head {
		return nil, false
	}
	// A checkpoint load validates and then transports its records, so they
	// arrive in the result rather than through the stream. Either way the fold
	// must hold the whole verified history and nothing else.
	if len(loaded.Events) > 0 {
		folder = selected.newFolder(nil)
		for index := range loaded.Events {
			folder.Append(w.record(loaded.Events[index]))
		}
	} else if streamed != loaded.Verification.Events {
		return nil, false
	}
	return folder, true
}
