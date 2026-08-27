// What may be signed, asked at the boundary that signs.
//
// A disabled button stops a click and stops nothing else. The defect this
// pins is the one codex found on the publish path: authority read when a
// control was drawn, and never asked again before `api.act`. The two act
// boundaries -- `doAct` in App.tsx and `send` in Thread.tsx -- ask
// `signingRefusal` immediately before that call. Publishing is the third
// durable path and asks `publishRefusal` instead; an earlier version of this
// comment said every durable act came through here, which was not true.
//
// The guard must refuse exactly what the fold refuses, and it fails in BOTH
// directions. `decideRatify` in internal/workroom/fold.go refuses in a
// specific order, and the order is the point:
//
//   target not effective     refused, before any satisfier is consulted
//   target retired           refused, before any satisfier is consulted
//   "originating-requester"  requires the requester AND f.hasActor
//   "role:<name>"            requires only f.hasRole -- no participation
//
// The first two were missing here, and their absence was a FALSE OFFER: the
// guard said yes to a ratification the fold refuses, appending a permanent
// ineffective row to an append-only log. The last two are the mirror: guarding
// every ratification on participation would refuse a role-holder the fold
// accepts. Tests for both directions live below, because a guard fixed only in
// the direction someone noticed is how the other direction gets shipped.
//
// Standing fails CLOSED. A caller that cannot resolve the target's decision
// gets a refusal, which is why every ratify fixture below states a decision
// explicitly: a guard that cannot see the fact must not vouch for it.
import test from "node:test";
import assert from "node:assert/strict";

const { signingRefusal } = await import("../src/lib/authority.ts");

const ME = "me-fingerprint";
const participant = { [ME]: { roles: ["participant"] } };
const roleOnly = { [ME]: { roles: ["ratifier"] } };          // holds a role, NOT a participant
const departed = { [ME]: { retired: true, roles: [] } };
const empty = {};

const stateAct = { act: "state" };
const supersedeAct = { act: "supersede", target: "some-record" };
const ratifyAct = { act: "ratify", target: "target-event" };

const requesterSatisfied = { event: "target-event", satisfier: "originating-requester" };
const roleSatisfied = { event: "target-event", satisfier: "role:ratifier" };

// Effectiveness is not carried on the statement: the projection publishes it in
// `decisions`, so the guard is handed the decision separately and every fixture
// that expects an offer has to say the fold ruled the target effective.
const effective = { event: "target-event", sequence: 1, verdict: "effective", reason: "recorded" };
const ineffective = { event: "target-event", sequence: 1, verdict: "ineffective", reason: "report has no promise" };

test("a state write is refused exactly when the fold would refuse it", () => {
  assert.equal(
    signingRefusal(stateAct, { live: true, actors: participant, me: ME }),
    undefined,
    "false refusal: the fold accepts post-genesis state from a live participant",
  );
  assert.ok(
    signingRefusal(stateAct, { live: false, actors: participant, me: ME }),
    "a signer with no lease cannot sign, and the boundary must say so",
  );
  assert.ok(
    signingRefusal(stateAct, { live: true, actors: departed, me: ME }),
    "decideState refuses post-genesis state from a signer without the participant role",
  );
  assert.ok(
    signingRefusal(stateAct, { live: true, actors: empty, me: undefined }),
    "no identity, no signature: the roster read must fail closed",
  );
});

test("own-author supersession stays outside the guard, for a departed author", () => {
  // decideSupersede returns Effective on target.record.Actor == record.record.Actor
  // with no hasActor test, and docs/reference/architecture.md documents that
  // cleanup exception. Guarding it invents a refusal the fold does not make.
  //
  // The target is stated. An earlier version of this test omitted it and passed
  // for the wrong reason: the branch fell through to `undefined` for ANY act
  // with no resolved target, so it was asserting the fail-open below rather
  // than the exception it names.
  assert.equal(
    signingRefusal(supersedeAct, { live: true, actors: departed, me: ME, target: { event: "some-record", actor: ME } }),
    undefined,
    "false refusal: a departed actor may still withdraw an act they authored",
  );
});

test("a role-satisfied ratification is NOT refused for want of participation", () => {
  // The case that makes this a dispatcher rather than one predicate. The fold's
  // role: branch asks hasRole and never hasActor, so a role-holding
  // non-participant ratifies lawfully. Guarding this on isLiveParticipant would
  // refuse an act the fold accepts.
  assert.equal(
    signingRefusal(ratifyAct, {
      live: true,
      actors: roleOnly,
      me: ME,
      target: roleSatisfied,
      targetDecision: effective,
    }),
    undefined,
    "false refusal: the fold's role: satisfier requires the role, not participation",
  );
});

test("an originating-requester ratification does require participation", () => {
  assert.equal(
    signingRefusal(ratifyAct, {
      live: true,
      actors: participant,
      me: ME,
      target: requesterSatisfied,
      targetDecision: effective,
      originatingRequester: ME,
    }),
    undefined,
    "false refusal: the requester is present and a participant",
  );
  assert.ok(
    signingRefusal(ratifyAct, {
      live: true,
      actors: departed,
      me: ME,
      target: requesterSatisfied,
      targetDecision: effective,
      originatingRequester: ME,
    }),
    "a departed requester must not confer force: a ratified approval is what gs merge consumes",
  );
  assert.ok(
    signingRefusal(ratifyAct, {
      live: true,
      actors: participant,
      me: ME,
      target: requesterSatisfied,
      targetDecision: effective,
      originatingRequester: "somebody-else",
    }),
    "only the originating requester may declare satisfaction",
  );
});

test("a ratification whose target is not in the projection is refused, not guessed", () => {
  assert.ok(
    signingRefusal(ratifyAct, { live: true, actors: participant, me: ME, target: undefined }),
    "the boundary cannot ask mayRatify without the record, and must not assume yes",
  );
});

// The defect codex reproduced at head 6bcb4798. The guard checked that the
// target existed and then asked `mayRatify`, which reads the satisfier and the
// roster and nothing else -- neither the target's decision nor its retirement.
// The fold refuses on both of those BEFORE it reaches the satisfier, so the
// browser offered, and signed, ratifications the fold was always going to
// refuse. Each one is a permanent ineffective row.
test("a ratification of a target the fold never ruled effective is refused", () => {
  assert.ok(
    signingRefusal(ratifyAct, {
      live: true,
      actors: roleOnly,
      me: ME,
      target: roleSatisfied,
      targetDecision: ineffective,
    }),
    "false offer: decideRatify refuses \"ratify target is not effective\" before it reads the satisfier",
  );
});

test("a ratification of a retired target is refused", () => {
  assert.ok(
    signingRefusal(ratifyAct, {
      live: true,
      actors: roleOnly,
      me: ME,
      target: { ...roleSatisfied, retired: true },
      targetDecision: effective,
    }),
    "false offer: decideRatify refuses \"retired statement cannot be ratified\" before it reads the satisfier",
  );
});

test("standing fails closed when the decision cannot be resolved", () => {
  // Not a hypothetical: `index.decision(event)` returns undefined for anything
  // the projection has not published a decision for, and a projection can be
  // older than the record a route names. Allowing on an unreadable fact is how
  // a guard vouches for something it never saw.
  assert.ok(
    signingRefusal(ratifyAct, {
      live: true,
      actors: roleOnly,
      me: ME,
      target: roleSatisfied,
      targetDecision: undefined,
    }),
    "an unresolvable decision must refuse, not assume the target is effective",
  );
});

// The mirror of the standing repair. Superseding was a blanket pass, which is
// looser than the fold: `decideSupersede` admits the target's own author or a
// ratifier, and refuses everyone else. The own-author branch still carries no
// participation test, so the departed-author case above must keep passing --
// that is the false refusal this narrowing must not introduce.
test("superseding somebody else's record is refused unless you hold ratifier", () => {
  const mine = { event: "some-record", actor: ME };
  const theirs = { event: "some-record", actor: "somebody-else" };
  assert.equal(
    signingRefusal(supersedeAct, { live: true, actors: departed, me: ME, target: mine }),
    undefined,
    "false refusal: a departed actor may still withdraw an act they authored",
  );
  assert.ok(
    signingRefusal(supersedeAct, { live: true, actors: participant, me: ME, target: theirs }),
    "false offer: the fold admits only the target's author or a ratifier",
  );
  assert.equal(
    signingRefusal(supersedeAct, { live: true, actors: roleOnly, me: ME, target: theirs }),
    undefined,
    "false refusal: a ratifier may retire another actor's record",
  );
});

// The three fail-open cases codex probed at head b1daa567 and got ALLOWED from.
// Each is the guard answering a question it had no facts for, which is exactly
// what the ratify branch above was repaired not to do; the supersede branch was
// left doing it.

test("a supersede whose target is not in the projection is refused, not guessed", () => {
  // `index.statement(event)` returns undefined for anything this projection has
  // not caught up with, and a route can name a record newer than the projection
  // the page is holding. `decideSupersede` refuses "supersede target is
  // unknown" before it reads anything else; a guard that allows here has
  // vouched for a record it never saw.
  assert.ok(
    signingRefusal(supersedeAct, { live: true, actors: participant, me: ME, target: undefined }),
    "false offer: decideSupersede refuses an unknown target outright",
  );
});

test("a supersede with no viewer identity is refused", () => {
  // Every branch past the target check compares the signer against somebody --
  // the target's author, or the roster. With no fingerprint there is nobody to
  // compare, and the roster read `hasRole` already fails closed on it. The
  // branch must not reach a verdict the comparison never made.
  assert.ok(
    signingRefusal(supersedeAct, { live: true, actors: participant, me: undefined, target: { event: "some-record", actor: ME } }),
    "no identity, no signature: the supersede branch must fail closed like the state branch",
  );
});

test("withdrawing your own roster grant is refused: the fold decides it by standing", () => {
  // The projection emits a statement row for every state record, roster
  // included, so a membership or role grant reaches the generic surfaces as an
  // ordinary statement -- and the only action the toolbar computes for an
  // unrecognised kind is `withdraw`, on authorship. `decideSupersede` sends a
  // roster target through governance FIRST and never asks who wrote it: the
  // founding seed can never be retired, an operator grant needs `operator`,
  // and every other roster change needs `ratifier`. Own-authorship is not on
  // that list, so the departed-author exception must not reach it.
  const myGrant = { event: "some-record", actor: ME, kind: "roster", body: { actor: ME, name: "me", role: "participant", kind: "human" } };
  assert.ok(
    signingRefusal(supersedeAct, { live: true, actors: participant, me: ME, target: myGrant }),
    "false offer: decideSupersede requires ratifier standing to change roster governance, and authorship is not standing",
  );
  assert.ok(
    signingRefusal(supersedeAct, { live: true, actors: departed, me: ME, target: myGrant }),
    "the cleanup exception is for ordinary records: a departed author does not get to retire a roster grant",
  );
  // Excluded for the ratifier too, and deliberately. The fold would admit this
  // one, so it is a refusal the fold does not make -- the price of not keeping
  // a second copy of the governance ladder in the browser, paid in the safe
  // direction. `gs supersede` files it and the fold rules.
  assert.ok(
    signingRefusal(supersedeAct, { live: true, actors: roleOnly, me: ME, target: myGrant }),
    "roster governance is excluded from this surface rather than modelled, ratifier or not",
  );
  // And the exclusion is by kind, not by anything the fixture happens to share
  // with it: the same actor withdrawing an ordinary record still passes.
  assert.equal(
    signingRefusal(supersedeAct, { live: true, actors: participant, me: ME, target: { event: "some-record", actor: ME, kind: "assert" } }),
    undefined,
    "false refusal: the roster exclusion must not swallow the ordinary withdraw path it sits in front of",
  );
});
