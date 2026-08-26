import type { ActorState, Statement } from "./api.ts";

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
    // role, so this asks for the role and not for a roster entry. The two are
    // different questions: the fold keeps a departed principal listed with
    // `retired: true` and no roles at all, because it signed events that are
    // permanent, so reading the entry as membership fails open on exactly the
    // actor who has left. This is also the only satisfier that never consults
    // a role, which is why it is the only place that could fail open — the
    // `role:` branch below already refuses an actor holding no roles.
    return (
      originatingRequester !== undefined &&
      originatingRequester === me &&
      actors[me]?.roles?.includes("participant") === true
    );
  }
  if (satisfier.startsWith("role:")) {
    return actors[me]?.roles?.includes(satisfier.slice("role:".length)) === true;
  }
  return false;
}
