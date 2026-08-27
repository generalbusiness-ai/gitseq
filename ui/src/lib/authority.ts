import type { ActorState, Decision, Statement } from "./api.ts";

// The membership question the fold asks, asked once and asked here.
//
// `decideState` in internal/workroom/fold.go refuses any post-genesis state
// record whose signer is not a participant:
//
//   if record.index > 0 && !f.hasActor(record.record.Actor) { ... Ineffective }
//
// and `hasActor` is the participant role, not a roster entry. Those are
// different questions. The fold keeps a departed principal listed with
// `retired: true` and no roles at all, because it signed events that are
// permanent and dropping it would leave those signatures attributed to
// nothing, so reading the entry as membership fails open on exactly the actor
// who has left. Offering an act the fold refuses appends a permanent
// ineffective row to an append-only log, which is what this predicate exists
// to prevent.
//
// WHAT IT COVERS TODAY, stated exactly rather than aspirationally. Two
// functions call this predicate: `publishRefusal` below, and `mayRatify` below
// for the originating-requester case. `publishRefusal` is in turn asked at
// three points on the publish path — the top bar's `publish` control
// (`components/TopBar.tsx`), the artifact dialog's submit gate
// (`components/Publish.tsx`, given the answer as a prop), and the signing
// boundary in `App.tsx`'s `publish`, which is the one that actually decides
// whether a record is written. The first two are courtesies that show a
// refusal early; the third is the guarantee, because the dialog can outlive
// the authority that opened it. Nothing else asks it yet.
//
// WHAT IT DOES NOT COVER YET. Seven state-writing affordances in
// `components/Toolbar.tsx` are still offered on authorship or commitment role
// with no membership test: `deny` and `accept` on a request addressed to you,
// `disagree` on a proposal, `propose adoption` and `request review` on an
// artifact, `mark done` as performer, and `needs work` as requester. None of
// the seven submits an act itself: each calls `onRoute`, which only opens the
// composer, and one call site in `Thread.tsx` signs act `state` for all of
// them. The fold therefore refuses all seven from a departed signer exactly as
// it refuses publish. The toolbar's other `accept` is not one of them — it
// calls `doAct` to submit act `ratify` directly, and `canRatify` gates it
// already — and neither is `withdraw`, whose composer route signs act
// `supersede`, deliberately ungated for the reason given below. They are the
// same defect at different sites and have their own request; they are listed
// here so the next reader does not have to rediscover them, and so this comment
// cannot be mistaken for a claim that the surface is already covered.
//
// `withdraw` is not ungated either, though it is gated on authorship rather
// than membership: see the `supersede` branch of `signingRefusal`.
//
// It is not asked of presence. Presence is advisory and session-bound, so a
// live session is not membership; and membership is not presence, so an
// absent participant has nothing to sign with. Publishing requires both.
//
// One documented act is deliberately outside this predicate: superseding an
// earlier act you authored yourself. `decideSupersede` returns Effective on
// `target.record.Actor == record.record.Actor` with no `hasActor` test, and
// docs/reference/architecture.md states the rule — "A departed actor may still
// supersede an earlier act they authored". The toolbar's `withdraw` is that
// act, so it must stay outside *this* predicate: asking membership of it would
// invent a refusal the fold does not make. That is not the same as ungated —
// `signingRefusal` still holds it to the fold's authorship rule.
export function isLiveParticipant(actors: Record<string, ActorState>, me?: string): boolean {
  if (!me) return false;
  return actors[me]?.roles?.includes("participant") === true;
}

// One role test, so the callers below read the roster the same way.
function hasRole(actors: Record<string, ActorState>, role: string, me?: string): boolean {
  if (!me) return false;
  return actors[me]?.roles?.includes(role) === true;
}

// May this viewer publish an artifact right now, and if not, why not.
//
// Asked wherever the publish path can still be stopped, so that the control,
// the dialog and the signing boundary cannot disagree: one rule, one place,
// three callers. Answering with the reason rather than a bare boolean is what
// lets each caller show the refusal it is holding instead of inventing words
// for it, and what lets the signing boundary tell the operator why nothing was
// filed.
//
// The two questions are independent and both must be yes. Presence says there
// is a live session to sign with; participation says the fold will accept what
// it signs. A departed principal keeps its key and can open a session, and a
// member who has gone home has no session to press anything with.
//
// It is asked again at the moment of signing because both answers can change
// while the dialog is open: a lease expires, or a membership grant is
// superseded, and the form on screen was rendered before either happened.
export function publishRefusal(
  /** A live lease: `Session.live`. */
  live: boolean,
  /** The projected roster, keyed by fingerprint. */
  actors: Record<string, ActorState>,
  /** The viewer's fingerprint. */
  me?: string,
): string | undefined {
  if (!live) return "not present yet";
  if (!isLiveParticipant(actors, me)) return "not a live participant: the fold would refuse this artifact";
  return undefined;
}

// Who may ratify what, read from the fold's own decision about this exact
// record rather than re-derived from the screen.
//
// The fold decides a ratification by the target kind's *satisfier* — but by
// the satisfier captured on that kind's definition when the target statement
// was admitted, not by whichever definition of the kind stands now. The
// projection publishes that captured value per statement, beside `lifecycle`,
// which is settled the same way and for the same reason:
//
//   role:<name>            the actor must hold that role
//   originating-requester  only the request's author, and only while they are
//                          still a participant
//   anything else, absent  the statement is not ratifiable at all
//
// Reading it from the live vocabulary instead was wrong in both directions,
// and each direction is its own harm. A kind narrowed since admission hid an
// act the fold would accept, silently, from an actor entitled to it. A kind
// widened since admission offered an act the fold refuses, and pressing it
// appends a durable record marked ineffective: a permanent row in an
// append-only log saying somebody tried to do something they were never
// allowed to do. The browser must not offer an act the fold will refuse, and
// the only value that answers that question is the one the fold itself reads.
//
// Both inputs still come from the fold: the satisfier from the statement, the
// roles from the projected roster. Neither is inferred here.
export function mayRatify(
  statement: Statement,
  context: {
    /** The projected roster, keyed by fingerprint. */
    actors: Record<string, ActorState>;
    /** The viewer's fingerprint. */
    me?: string;
    /**
     * The author of the request this record answers, for the one satisfier
     * that names a person rather than a role. Undefined when the record
     * answers no request, which is itself a refusal for that satisfier.
     */
    originatingRequester?: string;
  },
): boolean {
  const { actors, me, originatingRequester } = context;
  if (!me) return false;
  const satisfier = statement.satisfier;
  // No satisfier, no proof. A record the fold bound no definition to is not
  // ratifiable, and a client reading a projection too old to carry the field
  // does not know what the fold requires — the honest thing to show then is
  // nothing, which is the same rule layer 6 already follows when it refuses to
  // present a partial projection as authoritative.
  if (!satisfier) return false;
  if (satisfier === "originating-requester") {
    // Being the requester is not enough on its own: the fold also refuses a
    // requester who has left the room, because a ratified approval is what a
    // merge consumes. That refusal is `hasActor`, which is the participant
    // role, so this asks `isLiveParticipant` above and not for a roster entry.
    // This is also the only satisfier that never consults a role, which is why
    // it is the only place that could fail open — the `role:` branch below
    // already refuses an actor holding no roles.
    return (
      originatingRequester !== undefined &&
      originatingRequester === me &&
      isLiveParticipant(actors, me)
    );
  }
  if (satisfier.startsWith("role:")) {
    return actors[me]?.roles?.includes(satisfier.slice("role:".length)) === true;
  }
  return false;
}

/**
 * May this act be signed, right now, by this viewer?
 *
 * Asked at the signing boundary rather than only where the control is drawn.
 * A disabled button stops a click; it does not stop a submit that was already
 * reachable when authority moved underneath it.
 *
 * Two signing boundaries ask this: `doAct` in `App.tsx` and `send` in
 * `components/Thread.tsx`. Publishing an artifact is the third durable path
 * and it does not come through here — it asks {@link publishRefusal} at its
 * own boundary. An earlier version of this comment claimed every durable
 * browser act passed through this function; it did not, and the claim is
 * corrected rather than quietly dropped because a guard that overstates its
 * own reach is how the next reader stops checking.
 *
 * It dispatches rather than deciding, because the fold does not apply one rule
 * to every act and a browser that pretended otherwise would refuse work the
 * fold accepts:
 *
 *   `state`      `decideState` refuses post-genesis state from a signer
 *                without the participant role, so this asks
 *                {@link isLiveParticipant}.
 *   `ratify`     `decideRatify` refuses on the target's *standing* before it
 *                ever reaches the satisfier: a target the fold has not ruled
 *                effective, and a retired target, are both refused outright.
 *                Only past those does it branch on the satisfier bound at
 *                admission — `originating-requester` also requires
 *                participation, while `role:<name>` requires only the role,
 *                which {@link mayRatify} models. Standing is checked here
 *                first, in the fold's own order, and fails closed: a caller
 *                that cannot resolve the target's decision gets a refusal,
 *                because a guard that cannot see the fact must not vouch for
 *                it.
 *   `supersede`  `decideSupersede` admits the target's own author or a
 *                ratifier. Own-authorship carries no participation test —
 *                docs/reference/architecture.md documents that cleanup
 *                exception, "a departed actor may still supersede an earlier
 *                act they authored" — so this must not ask
 *                {@link isLiveParticipant} on that branch. It narrows only
 *                when the target resolves to a statement, which is the shape
 *                the withdraw control offers; artifact targets reach paths
 *                this guard does not model (roster governance, authorized
 *                merge receipts), and restating those here would be a second
 *                copy of the fold to keep in step.
 */
export function signingRefusal(
  act: { act: string; target?: string },
  context: {
    /** A live lease: `Session.live`. */
    live: boolean;
    /** The projected roster, keyed by fingerprint. */
    actors: Record<string, ActorState>;
    /** The viewer's fingerprint. */
    me?: string;
    /** The statement named by `act.target`, for `ratify` and `supersede`. */
    target?: Statement;
    /**
     * The fold's decision about `act.target`. Required for `ratify`: the fold
     * refuses a target it has not ruled effective, and this guard cannot know
     * that from the statement alone, because the projection carries
     * effectiveness in `decisions` and not on the statement.
     */
    targetDecision?: Decision;
    /** The author of the request `act.target` answers, for `ratify`. */
    originatingRequester?: string;
  },
): string | undefined {
  const { live, actors, me, target, targetDecision, originatingRequester } = context;
  // No lease, no signature: this one is about the session rather than the
  // fold, and it is true of every act including the ones the fold would not
  // otherwise refuse.
  if (!live) return "not present yet";
  if (act.act === "supersede") {
    // Own-authorship first, and with no participation test: that is the
    // documented cleanup exception, and asking membership here would invent a
    // refusal the fold does not make.
    if (target && me && target.actor !== me && !hasRole(actors, "ratifier", me)) {
      return "only the record's author or a ratifier may retire it";
    }
    return undefined;
  }
  if (act.act === "ratify") {
    if (!target) return "the record this would ratify is not in the projection";
    // Standing before satisfier, in the fold's own order. Both of these
    // refusals sit above the satisfier branches in `decideRatify`, so a guard
    // that asked only `mayRatify` would offer a control the fold refuses and
    // append a permanent ineffective row to say so.
    //
    // The comparison is written as "not effective" rather than "is
    // ineffective" so that an absent or unreadable decision refuses too.
    if (targetDecision?.verdict !== "effective") {
      return "the fold has not ruled this record effective, so it cannot be ratified";
    }
    if (target.retired) return "a retired record cannot be ratified";
    return mayRatify(target, { actors, me, originatingRequester })
      ? undefined
      : "the fold would refuse this ratification from you now";
  }
  return isLiveParticipant(actors, me)
    ? undefined
    : "not a live participant: the fold would refuse this record";
}
