package app

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"slices"
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

// requestReproduction is what an act already accepted under a caller's
// idempotency key tells this layer about how to rebuild it: the schema it was
// signed under, and the server-derived half of the target triple it stated,
// when it stated one.
//
// The schema is half the answer and the harder half. A retry must reproduce
// the act the log holds, not the act this version of the code would sign
// today. A request filed before the landing obligation existed stands as
// workroom/state@2 and states no result at all; rebuilding it as
// workroom/state@3 refuses it for stating none, which is the one answer a
// caller who already owns that act must never get.
type requestReproduction struct {
	schema      string
	measurement map[string]string
}

// statesItsResult reports whether the schema being rebuilt is one the fold
// reads a result choice from. On a legacy record the same field names are
// opaque body text and confer nothing, so judging them as a choice would
// refuse the retry of an act already in the log. A nil reproduction is an
// ordinary fresh filing, which always states its result.
func (r *requestReproduction) statesItsResult() bool {
	return r == nil || r.schema == workroom.SchemaStateV3 || r.schema == workroom.SchemaReassignRequestV1
}

// accepted is the measurement to rebuild on, or nil to take a fresh one.
func (r *requestReproduction) accepted() map[string]string {
	if r == nil {
		return nil
	}
	return r.measurement
}

// signedAs is the schema to sign the rebuild under: the accepted act's own,
// or current when there is nothing to reproduce.
func (r *requestReproduction) signedAs(current string) string {
	if r == nil {
		return current
	}
	return r.schema
}

// reproducibleRequestSchemas names, for one verb, every schema whose payload
// shape this path can write. It is a table rather than a guard: an accepted
// act under any other schema carries a different payload type, so a rebuild
// could never be byte for byte that act anyway, and the key falls through to
// an ordinary fresh filing where the kernel refuses it as a reused one.
//
// Reproducing a schema is not the same as signing one. A fresh filing is
// always signed under the current schema; these older ones are reachable only
// by rebuilding an act the log already holds, and only when the rebuild is
// byte for byte that act, which means the submission replays and nothing is
// appended.
func reproducibleRequestSchemas(verb Verb) []string {
	switch verb {
	case VerbState:
		return []string{workroom.SchemaStateLegacy, workroom.SchemaStateV1, workroom.SchemaState, workroom.SchemaStateV3}
	case VerbReassignIfUnclaimed:
		return []string{workroom.SchemaReassignRequest, workroom.SchemaReassignRequestV1}
	}
	return nil
}

// acceptedRequestReproduction answers the one problem a filing-time
// measurement creates: a retry cannot measure the same world twice. The
// by-value target head is read from the ref when the request is signed, so an
// exact retry after the ref moved — or after the branch was deleted outright —
// would rebuild a different payload, or fail to build at all, for an act the
// caller has every right to see replayed. A legacy act has the same problem
// from the other side: it states no result, and today's rules refuse one.
//
// So the accepted act is recovered from the log first, before any ref is read.
// The retry identity the kernel indexes is target log, actor key, namespace
// and key, and none of those needs the repository measured, so the question
// can be asked with nothing resolved. What comes back is the schema that act
// was signed under and, when it stated a by-value target, the server-derived
// half of it — target_repo and target_head, never target_ref — for the
// caller's own body to carry into an ordinary rebuild.
//
// held reports whether any act at all is accepted under the key. A held key
// whose act this path cannot rebuild — a different verb, a payload shape it
// does not write — comes back held with a nil reproduction: the kernel would
// refuse the reuse, and the caller is told so before anything is appended.
func (w *Workspace) acceptedRequestReproduction(ctx context.Context, private ed25519.PrivateKey,
	actorName, key string, verb Verb) (reproduction *requestReproduction, prior kernel.Event, held bool) {
	prior, held = w.priorActUnderKey(ctx, private, actorName, key)
	if !held {
		return nil, kernel.Event{}, false
	}
	if !slices.Contains(reproducibleRequestSchemas(verb), prior.Intent.Schema) {
		return nil, prior, true
	}
	accepted, err := workroom.Decode(prior.Intent.Schema, prior.Payload)
	if err != nil {
		return nil, prior, true
	}
	return &requestReproduction{schema: prior.Intent.Schema, measurement: requestMeasurementOf(accepted)}, prior, true
}

// priorActUnderKey is the retry lookup itself: the act this actor's key
// already names in this log, found by the identity the kernel indexes —
// target log, actor key, namespace and key — which needs nothing measured.
func (w *Workspace) priorActUnderKey(ctx context.Context, private ed25519.PrivateKey, actorName, key string) (kernel.Event, bool) {
	if key == "" {
		return kernel.Event{}, false
	}
	dedup := intent.DedupIdentity(w.workroomID(), private.Public().(ed25519.PublicKey),
		w.idempotencyNamespace(actorName), key)
	prior, held, err := kernel.PriorAct(ctx, w.Store, w.workroomID(), dedup)
	if err != nil || !held {
		return kernel.Event{}, false
	}
	return prior, true
}

// AcceptedActUnderKey reports the event identifier this actor's key already
// names in the log. A batch uses it to let a label of an act the log already
// holds name that act rather than a placeholder, so a later act in the same
// batch that cites the label is rebuilt exactly and recognised as the retry it
// is.
func (w *Workspace) AcceptedActUnderKey(ctx context.Context, private ed25519.PrivateKey, actorName, key string) (string, bool) {
	prior, held := w.priorActUnderKey(ctx, private, actorName, key)
	if !held {
		return "", false
	}
	return w.EventID(prior.Commit), true
}

// requestRetry is what the log says about an act filed under a key: nothing
// is held there, the act is the one held there, or something else is.
type requestRetry int

const (
	// freshRequest: no accepted act holds the key, so this is a first filing
	// and measures the world as it stands.
	freshRequest requestRetry = iota
	// replayedRequest: the act rebuilds byte for byte to the accepted one, so
	// the submission replays and nothing is appended.
	replayedRequest
	// conflictingRequest: an accepted act holds the key and this act is not
	// it. The kernel refuses the reuse, and so does everything in front of it.
	conflictingRequest
)

// acceptedRequestReplay is the one classification of a keyed request filing,
// shared by the signing path and by the guarded-replacement preflight so the
// two cannot disagree about what a key already names.
//
// A held key is answered by rebuilding the caller's whole act — old request,
// words, bases, body, attachments — on the accepted act's schema and
// measurement, and comparing bytes. Everything the caller stated has to
// agree; the only value taken from the log rather than from the caller is the
// retirement event a guarded replacement names, because that is the result
// of the pair's first act and not a choice the caller made. A rebuild that
// differs, or that cannot be built at all, is a conflict: the key is spent on
// an act this one is not, and no fresh measurement is taken in its name.
//
// The rebuild is used only when it is byte for byte the accepted act, so this
// can produce an exact replay or a refusal, never an accepted act from a
// different one. It holds no state and is not a retry cache.
func (w *Workspace) acceptedRequestReplay(ctx context.Context, private ed25519.PrivateKey, actorName string,
	act Act, snapshot *Snapshot) (kernel.Request, requestRetry, error) {
	if !w.mayReproduceAcceptedRequest(ctx, snapshot, act) {
		return kernel.Request{}, freshRequest, nil
	}
	reproduction, prior, held := w.acceptedRequestReproduction(ctx, private, actorName, act.IdempotencyKey, act.Verb)
	if !held {
		return kernel.Request{}, freshRequest, nil
	}
	if reproduction == nil {
		return kernel.Request{}, conflictingRequest, nil
	}
	if act.Verb == VerbReassignIfUnclaimed && act.Retirement == "" {
		accepted, err := workroom.Decode(prior.Intent.Schema, prior.Payload)
		if err != nil {
			return kernel.Request{}, conflictingRequest, nil
		}
		if replacement, ok := accepted.(*workroom.ReassignIfUnclaimed); ok {
			act.Retirement = replacement.Expectation.Retirement
		}
	}
	replay, err := w.buildAct(ctx, private, actorName, act, snapshot, reproduction)
	if err != nil {
		return kernel.Request{}, conflictingRequest, err
	}
	if !replay.Signed.Equal(prior.Signed) {
		return kernel.Request{}, conflictingRequest, nil
	}
	return replay, replayedRequest, nil
}

// errReusedKey is the refusal a conflicting key earns here, carrying the
// kernel's own identity for it so a caller cannot tell the two apart and does
// not have to. It names the key and never the accepted act: that act is the
// caller's own to look up, and the refusal is not a pointer to it.
func errReusedKey(key string) error {
	return fmt.Errorf("%w: %q already names an accepted act this filing does not rebuild", kernel.ErrIdempotencyConflict, key)
}

// requestMeasurementOf reads the server-derived half of the target triple an
// accepted request stated, from either payload shape that carries one. The
// caller-selected target_ref is deliberately not among the fields returned: it
// is the caller's own intent, and recovering it would erase a difference that
// has to be refused. An act that stated no triple — an inheriting request, a
// no-artifact one, a legacy one — measured nothing, and nil is the honest
// answer for it.
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

// guardedReplacementBody prepares the caller-known half of a guarded
// replacement request: the shape rules, the reserved-field refusal, and the
// result the replacement states. It is exactly what a surface can judge before
// it appends the guarded retirement, and exactly what buildAct applies again
// when it signs, so the two cannot drift.
func (w *Workspace) guardedReplacementBody(ctx context.Context, body map[string]string, reproduction *requestReproduction) (map[string]string, error) {
	normalized, err := w.normalizeGuardedRequestShape(ctx, body, reproduction != nil)
	if err != nil {
		return nil, err
	}
	if err := refuseClientReservedFields(body); err != nil {
		return nil, err
	}
	// The replacement is a request like any other, so it states its own
	// result. It is filed under reassign-if-unclaimed@1, which is what tells
	// the fold to read that choice; @0 records already in the log keep reading
	// their body as opaque text.
	if reproduction.statesItsResult() {
		if normalized, err = w.resolveRequestChoice(ctx, workroom.KindRequest, normalized, reproduction.accepted()); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

// PreflightGuardedReplacement refuses a guarded replacement before its guarded
// retirement is appended.
//
// The pair is two acts in order: the retirement, then the replacement. Every
// refusal the replacement earns for what the caller stated — its required
// fields, an address nobody holds, a reserved field, the result it states,
// the ref that result names, and a key already spent on some other act — is
// knowable before either act. Learning it after the first act is what leaves
// the original request withdrawn with no successor and the frontier moved, so
// those rules run here, over the caller's whole intent, through the same code
// that will judge it again at signing. The authoritative judgement stays at
// append, where the frontier is fixed; nothing here decides anything, and the
// guard on the old request is not this preflight's question.
//
// act is the replacement exactly as the surface will file it, less the
// retirement it cannot yet name. A key already held is classified the way
// signing classifies it: the pair is a retry only if this whole replacement
// rebuilds to the accepted act, in which case no ref is read and a branch that
// has since gone cannot refuse a caller the acts they already hold; anything
// else under a held key is refused now, before a retirement is appended in its
// name. A fresh replacement is judged over the live roster and the ref as it
// stands.
func (w *Workspace) PreflightGuardedReplacement(ctx context.Context, private ed25519.PrivateKey,
	actorName string, act Act) error {
	_, retry, err := w.acceptedRequestReplay(ctx, private, actorName, act, nil)
	switch {
	case err != nil:
		return err
	case retry == replayedRequest:
		return nil
	case retry == conflictingRequest:
		return errReusedKey(act.IdempotencyKey)
	}
	_, err = w.guardedReplacementBody(ctx, act.Body, nil)
	return err
}
