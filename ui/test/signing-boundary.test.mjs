// What may be signed, asked at the boundary that signs.
//
// A disabled button stops a click and stops nothing else. The defect this
// pins is the one codex found on the publish path: authority read when a
// control was drawn, and never asked again before `api.act`. Every durable
// act in the browser now passes through `signingRefusal` immediately before
// that call.
//
// The load-bearing case here is NOT that it refuses. It is that it refuses
// exactly what the fold refuses and nothing else. `decideRatify` in
// internal/workroom/fold.go does not apply one rule:
//
//   satisfier "originating-requester"  requires the requester AND f.hasActor
//   satisfier "role:<name>"            requires only f.hasRole -- no participation
//
// so guarding every ratification on participation would refuse a role-holder
// the fold accepts. That is a false refusal, the same class of error as
// gating own-author withdraw, and the tests below fail if anyone "tightens"
// the guard into it.
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
  assert.equal(
    signingRefusal(supersedeAct, { live: true, actors: departed, me: ME }),
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
