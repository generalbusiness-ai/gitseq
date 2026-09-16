package app

import (
	"context"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// PreflightAct asks the fold what it would decide about an act that has not
// been signed. The answer is the fold's own, computed by appending the record
// this act would become to the projection this process last verified and
// reading the decision off it — the same rules, run a moment early, so an
// actor can be told that a promise has no request or that an artifact has no
// path before the attempt becomes a permanent row.
//
// judged is false whenever the question cannot be put honestly, and a caller
// that gets it must carry on and let the fold decide:
//
//   - the workroom cannot be folded here, or the folded world this process
//     holds is not the one the snapshot describes;
//   - the act is a verb whose payload this boundary does not build, which is
//     every guarded act: their own two-act admission guards judge them;
//   - the act would be refused before signing anyway, by the same builder
//     submission runs, which says so in better words than a fold reason.
//
// The judgement is advisory in one more way that matters. It reads the local
// verified log, and an act submitted to a resident joins that resident's
// frontier, which may already have moved. The fold at sequencing remains the
// authority; this only moves the ordinary refusals earlier, where they cost
// nothing but a retry.
func (w *Workspace) PreflightAct(ctx context.Context, actorFingerprint string, act Act) (workroom.Decision, bool) {
	snapshot, err := w.Snapshot(ctx)
	if err != nil {
		return workroom.Decision{}, false
	}
	record, ok := w.prospectiveRecord(ctx, snapshot, actorFingerprint, act)
	if !ok {
		return workroom.Decision{}, false
	}
	return w.previewDecision(snapshot, record)
}

// prospectiveRecord builds the record this act would become, by the same
// schema selection, body normalization and encoding the signing path uses. It
// stops short of signing: the only thing the fold reads that a signature
// carries is the actor, and the caller supplies that fingerprint from the
// roster, so an act can be refused before a private key is ever read.
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
		// body judged here is the body that would be signed rather than the
		// one that was typed.
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
		ID: w.prospectiveEventID(), Actor: actorFingerprint,
		Schema: schema, RestsOn: rests, Payload: encoded,
	}, true
}

// prospectiveEventID names an act that has not been sequenced. A canonical
// identifier always has the genesis hash width, so an all-zero object keeps the
// exact shape of the identifier this act will be given while naming no event
// any log can hold.
func (w *Workspace) prospectiveEventID() string {
	return w.EventID(strings.Repeat("0", len(w.config.Genesis)))
}

// previewDecision folds one prospective record against the verified projection
// this workspace holds. It is answered under the same lock that publishes that
// projection, so a preview can neither read a half-advanced fold nor be read as
// one: the fold hands back a decision and keeps nothing.
func (w *Workspace) previewDecision(snapshot Snapshot, record workroom.Record) (workroom.Decision, bool) {
	w.snapshotMu.Lock()
	defer w.snapshotMu.Unlock()
	if w.snapshotFolder == nil || w.snapshotCache == nil || w.snapshotCache.Head != snapshot.Head {
		return workroom.Decision{}, false
	}
	return w.snapshotFolder.Preview(record), true
}
