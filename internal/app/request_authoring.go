package app

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Request authoring: the one place a request acquires its landing obligation.
//
// Every surface that files a request — the command line, the MCP adapter, the
// resident's HTTP act endpoint, a batch, a guarded reassignment — reaches this
// file through buildActRequest, so the result choice is stated once, in the
// same words, whoever asked. What the body alone can say is judged by
// workroom.ReadRequestChoice, which the fold reads from too; what needs the
// repository is done here, because the fold has none.
//
// The division of labour with the fold is the point. The fold stores
// target_head and never resolves it: it is a measurement, not a proof. This
// layer takes the measurement, from the ref, at filing. A caller does not get
// to hand one in.

// targetHeadReadLimit bounds the one ref read this path makes. A full object id
// and a newline is 65 bytes; anything larger is not an answer to the question
// asked.
const targetHeadReadLimit = 256

// resolveRequestChoice returns the body to sign for a request-lifecycle state,
// with the target triple completed from the repository, or the refusal that
// keeps the act from ever being appended.
//
// target_repo and target_head are refused as caller input outright rather than
// checked against what this layer would have resolved. The stricter rule is the
// honest one: a caller who supplies them is either guessing, or replaying a
// measurement taken somewhere else at some other time, and neither is the fact
// the field is supposed to carry. Accepting a matching value would also make
// the field look like something a client is expected to compute, which is how a
// stale hash gets copied forward. Callers state target_ref; this layer states
// where it lives and what it held.
//
// accepted, when it is not nil, carries the target_repo and target_head an act
// already accepted under this caller's idempotency key stated. Handed those,
// this recovers the measurement instead of taking one, and reads no ref at
// all. Everything else the caller stated — target_ref included — stands exactly
// as they sent it, so a changed destination under a reused key rebuilds
// different bytes and is refused rather than answered with the old request.
func (w *Workspace) resolveRequestChoice(ctx context.Context, kind workroom.Kind, body map[string]string, accepted map[string]string) (map[string]string, error) {
	normalized := cloneBody(body)
	if normalized == nil {
		normalized = make(map[string]string)
	}
	for _, field := range []string{"target_repo", "target_head"} {
		if _, present := normalized[field]; present {
			return nil, fmt.Errorf("%s body.%s is resolved at filing and cannot be supplied; state body.target_ref and this workroom fills the rest", kind, field)
		}
	}
	if ref, present := normalized["target_ref"]; present {
		if !workroom.ValidBranchRef(ref) {
			return nil, fmt.Errorf("%s body.target_ref must name a branch under refs/heads/", kind)
		}
		if accepted != nil {
			for field, stated := range accepted {
				normalized[field] = stated
			}
		} else {
			head, err := w.resolveTargetHead(ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("%s body.target_ref: %w", kind, err)
			}
			normalized["target_repo"] = w.workroomID()
			normalized["target_head"] = head
		}
	}
	if _, reason := workroom.ReadRequestChoice(w.workroomID(), normalized); reason != "" {
		return nil, fmt.Errorf("%s state: %s", kind, reason)
	}
	return normalized, nil
}

// workroomID is the canonical identifier of the repository this workspace
// files into: the same string every event id carries before its "#", and the
// only value target_repo may hold until multi-repository targets exist.
func (w *Workspace) workroomID() string {
	return "git:" + w.config.ObjectFormat + ":" + w.config.Genesis
}

// resolveTargetHead reads the commit a branch ref holds right now. It uses
// show-ref rather than rev-parse deliberately: rev-parse resolves a name
// through the whole revision grammar and would happily answer for something
// that is not the branch that was named, while show-ref --verify answers for
// exactly the ref or fails.
//
// A ref that does not resolve is a refusal, not an empty measurement. Filing a
// landing request against a branch that is not there names an obligation
// nobody can discharge.
func (w *Workspace) resolveTargetHead(ctx context.Context, ref string) (string, error) {
	if strings.TrimSpace(w.Repo) == "" {
		return "", fmt.Errorf("no repository is open here, so %s cannot be resolved", ref)
	}
	output, err := landingGit(ctx, w.Repo, "", targetHeadReadLimit, "show-ref", "--verify", "--hash", ref)
	if err != nil {
		return "", fmt.Errorf("%s does not resolve in %s", ref, w.Repo)
	}
	head := strings.TrimSpace(string(output))
	if !exactObjectID(head) {
		return "", fmt.Errorf("%s does not resolve to a full object id", ref)
	}
	return head, nil
}

// acceptedRequestMeasurement answers the one problem a filing-time measurement
// creates: a retry cannot measure the same world twice. The by-value target
// head is read from the ref when the request is signed, so an exact retry after
// the ref moved — or after the branch was deleted outright — would rebuild a
// different payload, or fail to build at all, for an act the caller has every
// right to see replayed.
//
// So the accepted act is recovered from the log first, before any ref is read.
// The retry identity the kernel indexes is target log, actor key, namespace and
// key, and none of those needs the repository measured, so the question can be
// asked with nothing resolved. When an act stands under it and stated a
// by-value target, this returns the server-derived half of what it stated —
// target_repo and target_head, never target_ref — for the caller's own body to
// carry into an ordinary rebuild.
//
// The rebuild is used only if it is byte for byte the accepted act, so this can
// produce an exact replay or nothing. It is not a retry cache, holds no state,
// and cannot turn a different act into an accepted one: everything the caller
// stated, the destination ref included, still has to agree.
func (w *Workspace) acceptedRequestMeasurement(ctx context.Context, private ed25519.PrivateKey,
	actorName, key string) (map[string]string, kernel.Event, bool) {
	dedup := intent.DedupIdentity(w.workroomID(), private.Public().(ed25519.PublicKey),
		w.idempotencyNamespace(actorName), key)
	prior, exists, err := kernel.PriorAct(ctx, w.Store, w.workroomID(), dedup)
	if err != nil || !exists {
		return nil, kernel.Event{}, false
	}
	accepted, err := workroom.Decode(prior.Intent.Schema, prior.Payload)
	if err != nil {
		return nil, kernel.Event{}, false
	}
	measurement := requestMeasurementOf(accepted)
	if measurement == nil {
		return nil, kernel.Event{}, false
	}
	return measurement, prior, true
}

// requestMeasurementOf reads the server-derived half of the target triple an
// accepted request stated, from either payload shape that carries one. The
// caller-selected target_ref is deliberately not among the fields returned: it
// is the caller's own intent, and recovering it would erase a difference that
// has to be refused.
func requestMeasurementOf(payload any) map[string]string {
	var body map[string]string
	switch value := payload.(type) {
	case *workroom.State:
		body = value.Body
	case *workroom.ReassignIfUnclaimed:
		body = value.Body
	default:
		return nil
	}
	if body["target_repo"] == "" || body["target_ref"] == "" || body["target_head"] == "" {
		return nil
	}
	return map[string]string{"target_repo": body["target_repo"], "target_head": body["target_head"]}
}
