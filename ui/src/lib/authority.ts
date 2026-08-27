import type { ActorState, Statement } from "./api.ts";

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
// It is not asked of presence. Presence is advisory and session-bound, so a
// live session is not membership; and membership is not presence, so an
// absent participant has nothing to sign with. Publishing requires both.
//
// One documented act is deliberately outside this predicate: superseding an
// earlier act you authored yourself. `decideSupersede` returns Effective on
// `target.record.Actor == record.record.Actor` with no `hasActor` test, and
// docs/reference/architecture.md states the rule — "A departed actor may still
// supersede an earlier act they authored". The toolbar's `withdraw` is that
// act, so it must stay ungated: guarding it here would invent a refusal the
// fold does not make.
export function isLiveParticipant(actors: Record<string, ActorState>, me?: string): boolean {
  if (!me) return false;
  return actors[me]?.roles?.includes("participant") === true;
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
 * reachable when authority moved underneath it. Every durable act in the
 * browser passes through here immediately before `api.act`.
 *
 * It dispatches rather than deciding, because the fold does not apply one rule
 * to every act and a browser that pretended otherwise would refuse work the
 * fold accepts:
 *
 *   `state`      `decideState` refuses post-genesis state from a signer
 *                without the participant role, so this asks
 *                {@link isLiveParticipant}.
 *   `ratify`     `decideRatify` branches on the satisfier bound at admission:
 *                `originating-requester` also requires participation, while
 *                `role:<name>` requires only the role. {@link mayRatify}
 *                already models exactly that split, so this asks it and does
 *                not second-guess it. Guarding every ratification on
 *                participation would refuse a role-holder the fold accepts.
 *   `supersede`  `decideSupersede` returns Effective for an author retiring
 *                their own act with no `hasActor` test, and
 *                docs/reference/architecture.md documents that cleanup
 *                exception. Guarding it would invent a refusal, so this does
 *                not.
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
    /** The statement named by `act.target`, for `ratify`. */
    target?: Statement;
    /** The author of the request `act.target` answers, for `ratify`. */
    originatingRequester?: string;
  },
): string | undefined {
  const { live, actors, me, target, originatingRequester } = context;
  // No lease, no signature: this one is about the session rather than the
  // fold, and it is true of every act including the ones the fold would not
  // otherwise refuse.
  if (!live) return "not present yet";
  if (act.act === "supersede") return undefined;
  if (act.act === "ratify") {
    if (!target) return "the record this would ratify is not in the projection";
    return mayRatify(target, { actors, me, originatingRequester })
      ? undefined
      : "the fold would refuse this ratification from you now";
  }
  return isLiveParticipant(actors, me)
    ? undefined
    : "not a live participant: the fold would refuse this record";
}
