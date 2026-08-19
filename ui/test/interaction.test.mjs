import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { hueOf, initialsOf } from "../src/lib/avatar.ts";
import { RetryKeys, eventDiscussionEntries, fingerprintOfPresentActor, fingerprintsIdentifySameActor, parsePresenceLabel, pendingForThread, pendingMatchesFrame, presentActors, reconciledPendingIDs, sendTemporaryReply, temporaryReplyDelivery, threadTargetKey, toggleActivityFocus } from "../src/lib/interaction.ts";
import { mentionAt, mentionFingerprints, mentionNames, mentionTokens } from "../src/lib/mentions.ts";
import { buildThreadIndex } from "../src/lib/threads.ts";
import { decodeFrame } from "../src/lib/api.ts";
import { soleCurrentSupersedeBasis } from "../src/lib/supersedeLinks.ts";
import { age, sortAfterClick, sortRows, workRows } from "../src/lib/rows.ts";
import { buildSpine } from "../src/lib/spine.ts";
import { interpretationNotice, isInterpretationGap, kindLabel } from "../src/lib/util.ts";

test("a retry keeps its key until the same payload succeeds", () => {
  let next = 0;
  const keys = new RetryKeys(() => `key-${++next}`);

  const first = keys.forAttempt("main", "same payload");
  assert.equal(keys.forAttempt("main", "same payload"), first);
  assert.notEqual(keys.forAttempt("main", "edited payload"), first);

  const edited = keys.forAttempt("main", "edited payload");
  keys.succeeded("main", edited);
  assert.notEqual(keys.forAttempt("main", "edited payload"), edited);
});

test("a completed semantic action gets a new key when attempted again", () => {
  let next = 0;
  const keys = new RetryKeys(() => `key-${++next}`);
  const first = keys.forAttempt("ratify:event-1", "ratify:event-1");
  keys.succeeded("ratify:event-1", first);
  assert.notEqual(keys.forAttempt("ratify:event-1", "ratify:event-1"), first);
});

test("thread identity includes its exact target", () => {
  assert.notEqual(
    threadTargetKey({ kind: "event", event: "event-1" }),
    threadTargetKey({ kind: "frame", conversation: "event-1", sequence: 0 }),
  );
  assert.notEqual(
    threadTargetKey({ kind: "frame", conversation: "chat", sequence: 1 }),
    threadTargetKey({ kind: "frame", conversation: "chat", sequence: 2 }),
  );
});

test("temporary event discussion is isolated by exact event anchor", () => {
  const frames = [
    { about: "event-one", text: "one" },
    { about: "event-two", text: "two" },
    { about: "event-one", re: "conversation:0", text: "nested" },
  ];
  assert.deepEqual(
    eventDiscussionEntries("event-one", frames.map((frame, sequence) => ({ conversation: "conversation", sequence, ...frame })))
      .map((entry) => entry.frame.text),
    ["one", "nested"],
  );

  const pending = [
    { id: "one", about: "event-one" },
    { id: "two", about: "event-two" },
    { id: "frame", re: "conversation:7" },
  ];
  assert.deepEqual(pendingForThread({ kind: "event", event: "event-one" }, pending).map((item) => item.id), ["one"]);
  assert.deepEqual(pendingForThread({ kind: "frame", conversation: "conversation", sequence: 7 }, pending).map((item) => item.id), ["frame"]);
});

test("temporary event discussion keeps nested replies visible with their exact depth", () => {
  const frames = [
    { conversation: "one", sequence: 0, about: "event-one", text: "root" },
    { conversation: "one", sequence: 1, about: "event-one", re: "one:0", text: "reply" },
    { conversation: "one", sequence: 2, about: "event-one", re: "one:1", text: "nested" },
    { conversation: "other", sequence: 0, about: "event-two", text: "elsewhere" },
    { conversation: "one", sequence: 3, about: "event-one", re: "expired:7", text: "orphan" },
  ];
  assert.deepEqual(
    eventDiscussionEntries("event-one", frames).map(({ frame, depth }) => [frame.text, depth, frame.conversation, frame.re]),
    [
      ["root", 0, "one", undefined],
      ["reply", 1, "one", "one:0"],
      ["nested", 2, "one", "one:1"],
      ["orphan", 0, "one", "expired:7"],
    ],
  );
});

test("optimistic temporary replies reconcile only inside their sending scope", () => {
  assert.equal(pendingMatchesFrame({ about: "event-one" }, { about: "event-one" }), true);
  assert.equal(pendingMatchesFrame({ about: "event-one" }, { about: "event-two" }), false);
  assert.equal(pendingMatchesFrame({ re: "conversation:1" }, { about: "event-one", re: "conversation:1" }), true);
  assert.equal(pendingMatchesFrame({}, { about: "the workroom" }), true);
  assert.equal(pendingMatchesFrame({}, { about: "event-one" }), false);
});

test("view-independent reconciliation is exact, arrival-sensitive, and one-to-one", () => {
  const pending = [
    { id: "one", text: "same", at: 10, about: "event-one" },
    { id: "two", text: "same", at: 10, about: "event-two" },
    { id: "duplicate", text: "same", at: 10, about: "event-one" },
    { id: "nested", text: "nested", at: 10, re: "conversation:4" },
    { id: "old", text: "old words", at: 20, about: "event-one" },
  ];
  const frames = [
    { actor: "claude", text: "same", seen: 11, about: "event-one" },
    { actor: "claude", text: "same", seen: 11, about: "event-two" },
    { actor: "claude", text: "nested", seen: 11, about: "event-one", re: "conversation:4" },
    { actor: "claude", text: "old words", seen: 19, about: "event-one" },
    { actor: "someone-else", text: "same", seen: 11, about: "event-one" },
  ];
  assert.deepEqual(reconciledPendingIDs(pending, frames, "claude"), ["one", "two", "nested"]);
  assert.deepEqual(reconciledPendingIDs(pending, frames, undefined), []);
});



test("temporary delivery anchors an event exactly and preserves frame conversation identity", () => {
  assert.deepEqual(temporaryReplyDelivery({ kind: "event", event: "full-event-id" }), { about: "full-event-id" });
  assert.deepEqual(
    temporaryReplyDelivery(
      { kind: "frame", conversation: "conversation", sequence: 4 },
      { conversation: "conversation", sequence: 4, about: "full-event-id" },
    ),
    { about: "full-event-id", conversation: "conversation", re: "conversation:4" },
  );
  assert.equal(temporaryReplyDelivery({ kind: "frame", conversation: "gone", sequence: 1 }), undefined);
});

test("failed temporary delivery removes its optimistic echo and preserves the transport failure", async () => {
  const calls = [];
  const failure = new Error("nexus unavailable");
  await assert.rejects(
    sendTemporaryReply("try again", { about: "event-one" }, {
      optimistic(text, re, about) {
        calls.push(["optimistic", text, re, about]);
        return "pending-one";
      },
      async publish(delivery, text) {
        calls.push(["publish", delivery, text]);
        throw failure;
      },
      failed(id) {
        calls.push(["failed", id]);
      },
    }),
    (error) => error === failure,
  );
  assert.deepEqual(calls, [
    ["optimistic", "try again", undefined, "event-one"],
    ["publish", { about: "event-one" }, "try again"],
    ["failed", "pending-one"],
  ]);
});

test("presence labels preserve actor names containing spaces", () => {
  assert.deepEqual(parsePresenceLabel("Ada Lovelace (abc123)"), {
    name: "Ada Lovelace",
    fingerprint: "abc123",
  });
  assert.deepEqual(parsePresenceLabel("service"), { name: "service", fingerprint: "" });
});

test("presence counts people, not the sessions each of them leases", () => {
  const people = presentActors({
    "handle:1": "claude (a5d35aa7e479)",
    "handle:2": "codex (5f12e916d136)",
    "handle:3": "claude (a5d35aa7e479)",
    "handle:4": "hugh (7fbc80f1ba06)",
    "handle:5": "claude (a5d35aa7e479)",
  });
  assert.deepEqual(people, [
    { label: "claude (a5d35aa7e479)", name: "claude", fingerprint: "a5d35aa7e479", sessions: 3, status: "available", focus: [], note: undefined },
    { label: "codex (5f12e916d136)", name: "codex", fingerprint: "5f12e916d136", sessions: 1, status: "available", focus: [], note: undefined },
    { label: "hugh (7fbc80f1ba06)", name: "hugh", fingerprint: "7fbc80f1ba06", sessions: 1, status: "available", focus: [], note: undefined },
  ]);
});

test("multiple activity leases aggregate deterministically and stay bounded", () => {
  const presence = {
    "handle:2": "codex (5f12e916d136)",
    "handle:1": "codex (5f12e916d136)",
  };
  const activity = {
    "handle:1": { status: "busy", focus: ["event:z", "event:a"], note: "later" },
    "handle:2": { status: "blocked", focus: ["event:b", "event:a"], note: "earlier" },
  };
  assert.deepEqual(presentActors(presence, activity), [{
    label: "codex (5f12e916d136)", name: "codex", fingerprint: "5f12e916d136", sessions: 2,
    status: "blocked", focus: ["event:a", "event:b", "event:z"], note: "earlier",
  }]);

  const many = Object.fromEntries(Array.from({ length: 10 }, (_, index) => [`h${index}`, { status: "available", focus: [`event:${9 - index}`] }]));
  const labels = Object.fromEntries(Object.keys(many).map((handle) => [handle, "codex (5f12e916d136)"]));
  assert.deepEqual(presentActors(labels, many)[0].focus, ["event:0", "event:1", "event:2", "event:3", "event:4", "event:5", "event:6", "event:7"]);
});

test("UI focus selection adds, removes, and stays bounded", () => {
  assert.deepEqual(toggleActivityFocus(["event:b"], "event:a"), ["event:a", "event:b"]);
  assert.deepEqual(toggleActivityFocus(["event:a", "event:b"], "event:a"), ["event:b"]);
  const full = Array.from({ length: 8 }, (_, index) => `event:${index}`);
  assert.equal(toggleActivityFocus(full, "event:z").length, 8);
});

// Shared selection and advisory focus were pinned here by four regexes over
// component source. A source regex fails in both directions: it misses a
// deleted render site, because the matched text survives, and it reddens on a
// harmless refactor. The wiring is now driven in a DOM in dom.test.mjs, and
// the focus rendering behaviourally in components.test.mjs.

test("browser heartbeats renew the lease without revalidating activity focus", () => {
  const session = readFileSync(new URL("../src/lib/session.ts", import.meta.url), "utf8");
  assert.match(session, /const renew = \(\) =>\s*api\s*\.announce\(effective, id\)/);
  assert.match(session, /setActivity:[\s\S]*api\.announce\(effective, id, next\)/);
  assert.doesNotMatch(session, /const renew = \(\) =>[\s\S]*?announce\(effective, id, activityRef\.current\)/);
});

test("avatar initials read the actor's name, not a decorated label", () => {
  assert.equal(initialsOf("claude"), "CL");
  assert.equal(initialsOf("Ada Lovelace"), "AL");
  // The label a multi-session avatar hovers with must never be passed as the
  // name: these initials are what that mistake looks like.
  assert.equal(initialsOf("claude — 3 sessions"), "C3");
  assert.equal(hueOf("a5d35aa7e479"), hueOf("a5d35aa7e479"));
});

test("presence keeps distinct actors apart even when they share a name", () => {
  const people = presentActors({
    "handle:1": "claude (a5d35aa7e479)",
    "handle:2": "claude (0011223344ff)",
  });
  assert.deepEqual(people.map((person) => person.fingerprint), ["0011223344ff", "a5d35aa7e479"]);
  assert.deepEqual(presentActors(undefined), []);
});

test("presence fingerprints expand without falling back to a shared name", () => {
  const actors = [
    { name: "claude", fingerprint: "a5d35aa7e4799472" },
    { name: "claude", fingerprint: "0011223344ff5566" },
  ];
  const people = presentActors({
    "handle:1": "claude (a5d35aa7e479)",
    "handle:2": "claude (0011223344ff)",
  });
  assert.deepEqual(people.map((person) => fingerprintOfPresentActor(person, actors)), [
    "0011223344ff5566",
    "a5d35aa7e4799472",
  ]);
  assert.equal(fingerprintsIdentifySameActor("a5d35aa7e479", "a5d35aa7e4799472"), true);
  assert.equal(fingerprintsIdentifySameActor("0011223344ff", "a5d35aa7e4799472"), false);
});

test("quoted mentions address actor names containing spaces", () => {
  const actors = [
    { name: "Ada Lovelace", fingerprint: "actor:ada", kind: "human", roles: ["participant"], custody: true },
    { name: "grace", fingerprint: "actor:grace", kind: "agent", roles: ["participant"], custody: true },
  ];
  const text = 'Thanks @"Ada Lovelace" and @grace.';
  assert.deepEqual(mentionNames(text), ["Ada Lovelace", "grace"]);
  assert.deepEqual(mentionFingerprints(text, actors), ["actor:ada", "actor:grace"]);
  assert.deepEqual(
    mentionTokens(text).filter((token) => token.mention).map((token) => token.mention),
    ["Ada Lovelace", "grace"],
  );
  assert.deepEqual(mentionAt('hello @"Ada L', 13), { start: 6, partial: "Ada L", quoted: true });
});

test("mentions require token boundaries and unique roster names", () => {
  const actors = [
    { name: "alice", fingerprint: "actor:alice", roles: ["participant"], custody: true },
    { name: "same", fingerprint: "actor:one", roles: ["participant"], custody: true },
    { name: "SAME", fingerprint: "actor:two", roles: ["participant"], custody: true },
  ];
  const text = "@alice email@alice foo@alice @alice/path @\"alice\"suffix (@alice), @same";
  assert.deepEqual(mentionNames(text), ["alice", "alice", "same"]);
  assert.deepEqual(mentionFingerprints(text, actors), ["actor:alice"]);
  assert.deepEqual(mentionTokens(text).filter((token) => token.mention).map((token) => token.mention), ["alice", "alice", "same"]);
});

test("browser frame decoding accepts legacy and addressed signed payloads", () => {
  const frame = (payload) => ({ Conversation: "conversation", Sequence: 3, ActorKey: "actor-key", Payload: Buffer.from(JSON.stringify(payload)).toString("base64") });
  const legacy = decodeFrame(frame({ about: "topic", text: "legacy", re: "conversation:2" }));
  const addressed = decodeFrame(frame({ about: "topic", text: "addressed", recipients: ["fingerprint"] }));
  assert.deepEqual({ about: legacy.about, text: legacy.text, re: legacy.re }, { about: "topic", text: "legacy", re: "conversation:2" });
  assert.deepEqual({ about: addressed.about, text: addressed.text, re: addressed.re }, { about: "topic", text: "addressed", re: undefined });
});

test("thread indexing keeps citations out of reply summaries and thread content", () => {
  const statement = (event, actor) => ({ event, actor, kind: "assert", text: event });
  const projection = {
    decisions: [],
    acts: [{ event: "a1", actor: "ada", type: "ratify", target: "e2", verdict: "effective", reason: "ok" }],
    statements: [statement("e0", "margaret"), statement("e1", "ada"), statement("e2", "grace"), statement("e3", "linus"), statement("e4", "barbara")],
    commitments: [],
    artifacts: [],
    actors: {},
    provenance: { e0: [], e1: [], e2: ["e1"], e3: ["e2", "e1"], e4: ["e0", "e1"], a1: ["e2"] },
  };
  const index = buildThreadIndex(projection);
  assert.deepEqual(index.summary("e1"), { count: 3, people: ["grace"] });
  assert.deepEqual(index.summary("e2"), { count: 2, people: ["linus", "ada"] });
  assert.deepEqual(index.content("e1").statements.map((item) => item.event), ["e2", "e3"]);
  assert.deepEqual(index.content("e1").acts.map((item) => item.event), ["a1"]);
  assert.deepEqual(index.content("e1").events, ["e2", "e3", "a1"]);
  assert.deepEqual(index.content("e0").statements.map((item) => item.event), ["e4"]);
});




const originalSupersede = "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:eab3b0e6064e5b31a04c2e2c3bababc618997946";
const originalTarget = "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ea42714b164813209725a5ab191d3bbd8f1c6089";
const originalLinkedItem = "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:154d1df1e664556bb73172b59d7ca518f23a0d6c";

function supersedeProjection(candidates, statements = candidates.map((event) => ({ event, actor: "codex", kind: "request", text: event }))) {
  const act = { event: originalSupersede, actor: "codex", type: "supersede", target: originalTarget, verdict: "effective", reason: "authorized" };
  return {
    act,
    projection: {
      decisions: [
        { event: originalSupersede, verdict: "effective", reason: "authorized" },
        ...statements.map((statement) => ({ event: statement.event, verdict: "effective", reason: "recorded" })),
      ],
      acts: [act],
      statements,
      commitments: [],
      artifacts: [],
      actors: {},
      provenance: { [originalSupersede]: [originalTarget, ...candidates] },
    },
  };
}

test("the original supersession exposes its sole current linked item", () => {
  const { act, projection } = supersedeProjection([originalLinkedItem]);
  assert.equal(soleCurrentSupersedeBasis(act, projection), originalLinkedItem);
});

test("a supersession with no additional current basis does not guess", () => {
  const { act, projection } = supersedeProjection([]);
  assert.equal(soleCurrentSupersedeBasis(act, projection), undefined);
});

test("a supersession with multiple current bases does not guess", () => {
  const { act, projection } = supersedeProjection(["linked-one", "linked-two"]);
  assert.equal(soleCurrentSupersedeBasis(act, projection), undefined);
});

test("retired provenance does not make one current linked item ambiguous", () => {
  const retired = { event: "retired-evidence", actor: "codex", kind: "assert", text: "old", retired: true };
  const live = { event: "current-link", actor: "codex", kind: "request", text: "new" };
  const { act, projection } = supersedeProjection([retired.event, live.event], [retired, live]);
  assert.equal(soleCurrentSupersedeBasis(act, projection), live.event);
});

test("a stale-only basis does not produce a false link", () => {
  const stale = { event: "stale-evidence", actor: "codex", kind: "report", text: "old finding", stale: true };
  const { act, projection } = supersedeProjection([stale.event], [stale]);
  assert.equal(soleCurrentSupersedeBasis(act, projection), undefined);
});

test("stale provenance does not make one current linked item ambiguous", () => {
  const stale = { event: "stale-evidence", actor: "codex", kind: "report", text: "old finding", stale: true };
  const live = { event: "current-link", actor: "codex", kind: "request", text: "new" };
  const { act, projection } = supersedeProjection([stale.event, live.event], [stale, live]);
  assert.equal(soleCurrentSupersedeBasis(act, projection), live.event);
});

test("the fc209 evidence-only artifact remains a neutral linked item", () => {
  const act = {
    event: "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fc209b57d88d0fcde4ca220595fa6af2fd08eef3",
    actor: "codex", type: "supersede",
    target: "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0b4cc9f7c5751cdb1cccde6d8145e4400581fe04",
    text: "An integrated exact-head replacement will follow.", verdict: "effective", reason: "authorized",
  };
  const artifact = { event: "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:259617504d7a2a099d107c81bda80ed85183b1bf", actor: "codex", kind: "artifact", text: "current main" };
  const projection = {
    decisions: [{ event: act.event, verdict: "effective", reason: "authorized" }, { event: artifact.event, verdict: "effective", reason: "recorded" }],
    acts: [act], statements: [{ event: act.target, actor: "codex", kind: "request", text: "old review", retired: true }, artifact],
    commitments: [], artifacts: [], actors: {}, provenance: { [act.event]: [act.target, act.target, artifact.event] },
  };
  assert.equal(soleCurrentSupersedeBasis(act, projection), artifact.event);
});

test("the e8e same-kind evidence artifact is not promoted to a typed replacement", () => {
  const act = {
    event: "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:e8e5764d86bfcf3b68eb355356129188406880cd",
    actor: "codex", type: "supersede",
    target: "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:47c3f2f68d71aea4ed781d7622a65a213d87f97b",
    text: "A replacement artifact will name the integrated head.", verdict: "effective", reason: "authorized",
  };
  const artifact = { event: "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:259617504d7a2a099d107c81bda80ed85183b1bf", actor: "codex", kind: "artifact", text: "current main" };
  const projection = {
    decisions: [{ event: act.event, verdict: "effective", reason: "authorized" }, { event: artifact.event, verdict: "effective", reason: "recorded" }],
    acts: [act], statements: [{ event: act.target, actor: "codex", kind: "artifact", text: "old head", retired: true }, artifact],
    commitments: [], artifacts: [], actors: {}, provenance: { [act.event]: [act.target, artifact.event] },
  };
  assert.equal(soleCurrentSupersedeBasis(act, projection), artifact.event);
});

test("supersede provenance links are labeled neutrally on every surface", () => {
  const source = readFileSync(new URL("../src/components/Thread.tsx", import.meta.url), "utf8");
  assert.match(source, /linked item/);
  assert.doesNotMatch(source, /replacement/);
});





















test("every kind wears its own name, bar the two whose names read as jargon", () => {
  assert.equal(kindLabel("assert"), "note");
  assert.equal(kindLabel("propose"), "proposal");
  for (const kind of ["finding", "review-note", "request", "promise"]) assert.equal(kindLabel(kind), kind);
});

test("an unreadable act carries its own reason and consequence", () => {
  const undefinedKind = interpretationNotice("undefined-kind", 'undefined kind "commit"');
  assert.equal(undefinedKind.reason, 'undefined kind "commit"');
  assert.match(undefinedKind.consequence, /recorded without force/);
  // Citations are NOT among the things that fail to form. The fold projects
  // rests_on provenance for every record before it judges any of them, so an
  // unreadable act still cites what it cited; claiming otherwise was false
  // against the two live undefined-kind acts, which each have a citation edge.
  assert.doesNotMatch(undefinedKind.consequence, /citation/i);

  // An unbound interpreter is remediable and says so.
  const unbound = interpretationNotice("uninterpretable", "uninterpretable: activated interpreter execution is not held");
  assert.match(unbound.consequence, /unless one is bound/);

  // A permanently invalid definition is the same verdict and must NOT promise
  // that binding an interpreter would rescue it — nothing can.
  const permanent = interpretationNotice("uninterpretable", 'uninterpretable kind definition: basis kind "finding" is undefined');
  assert.doesNotMatch(permanent.consequence, /unless one is bound/);
  assert.match(permanent.consequence, /not something a later interpreter would resolve/);

  // The collision the two cases share a channel for. A rejected operand is
  // quoted back verbatim, so a permanently invalid definition can carry the
  // fold's own unbound-interpreter words inside its reason — here as a field
  // type. Binding an interpreter cannot make that type supported, so the copy
  // must not offer it. Only the whole reason tells the two apart; searching
  // inside one reads this as remediable. The fold is pinned to emit exactly
  // this string by TestInvalidConstraintAlgebraIsTypedUninterpretable.
  const collision = interpretationNotice(
    "uninterpretable",
    'uninterpretable kind definition: unsupported type "interpreter execution is not held"',
  );
  assert.doesNotMatch(collision.consequence, /unless one is bound/);
  assert.match(collision.consequence, /not something a later interpreter would resolve/);

  // Ordinary verdicts get no notice: this surface is only for acts the room
  // could not read, not for every act that lacks force.
  assert.equal(interpretationNotice("effective", "statement recorded"), undefined);
  assert.equal(interpretationNotice("ineffective", "promise actor is not the requested performer"), undefined);
  assert.equal(interpretationNotice(undefined, undefined), undefined);
  assert.equal(isInterpretationGap("undefined-kind"), true);
  assert.equal(isInterpretationGap("disputed"), false);
});

test("the everyday surface does not expose record taxonomy or authority roles", () => {
  const read = (name) => readFileSync(new URL(`../src/${name}`, import.meta.url), "utf8");
  const app = read("App.tsx");
  const surface = [app, read("components/RequestList.tsx"), read("components/Thread.tsx"), read("components/TopBar.tsx")].join("\n");

  assert.doesNotMatch(surface, /Only you are holding|remove citation|cites the parent message|formal statement/);
  assert.doesNotMatch(surface, /actor\.roles|roles\.join/);
  // Two screens, and nothing that chooses between presentations of one of them.
  // Named for the identifiers the deleted controls carried, so prose about
  // their absence cannot satisfy the gate.
  assert.doesNotMatch(surface, /setPresentation|WorkBoard|BoardCard|TopicList|TopicCounts|FilterCheck|PersonalFilter|PresentationButton/);
  assert.match(app, /kind: "list"/);
  assert.match(app, /kind: "thread"/);
});


// The drawer's headline numbers described two populations at once: Active came
// from the projection, Closed from the topics, and Attention added retired or
// stale *artifacts* to a commitment count. The three then summed past the number
// of commitments, and a reader comparing them with `gs status` — which never
// adds artifacts to commitments — could only conclude one surface was
// miscounting. Neither was; they were answering different questions.

// ---------------------------------------------------------------------------
// The list: one row per open request, and the priority rule as the default
// sort rather than as prose.
// ---------------------------------------------------------------------------

const HOUR = 3600;
const DAY = 86400;
const NOW = 1787000000;

// A projection built from rows of {event, kind, actor, ts, parent, body}. Log
// order is the order given, and the ticket is the position, so a test can say
// "#3" and mean it.
function room(records, extra = {}) {
  const statements = [];
  const acts = [];
  const decisions = [];
  const provenance = {};
  records.forEach((record, index) => {
    decisions.push({ event: record.event, sequence: index + 1, verdict: record.verdict ?? "effective", reason: "recorded" });
    if (record.parent) provenance[record.event] = [record.parent, ...(record.cites ?? [])];
    if (record.type) acts.push({ event: record.event, actor: record.actor, type: record.type, target: record.target, verdict: "effective", reason: "authorized", timestamp: record.ts, text: record.text });
    else statements.push({ event: record.event, sequence: index + 1, actor: record.actor, kind: record.kind, text: record.text ?? record.event, timestamp: record.ts, body: record.body, retired: record.retired, stale: record.stale, describes_superseded_world: record.world });
  });
  return {
    decisions, acts, statements, provenance,
    commitments: extra.commitments ?? [],
    reviews: extra.reviews ?? [],
    artifacts: extra.artifacts ?? [],
    actors: extra.actors ?? { hugh: { name: "hugh", kind: "human", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} }, codex: { name: "codex", kind: "agent", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} }, claude: { name: "claude", kind: "agent", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } },
  };
}

const context = (projection) => ({
  nameOf: (fingerprint) => fingerprint,
  tickets: new Map(projection.decisions.map((decision) => [decision.event, decision.sequence])),
  actors: projection.actors,
});

test("a row says state, waits-on, age, title and ticket, and nothing else", () => {
  const projection = room(
    [
      { event: "r1", kind: "request", actor: "hugh", ts: NOW - 7 * DAY, text: "Repair the resident\nsecond line is not the title", body: { to: "claude" } },
      { event: "p1", kind: "promise", actor: "claude", ts: NOW - 2 * HOUR, parent: "r1" },
    ],
    { commitments: [{ request: "r1", requester: "hugh", addressed_to: "claude", performer: "claude", promise: "p1", status: "promised", waiting_on: "claude", stale: true }] },
  );
  const [row] = workRows(projection, context(projection));
  assert.deepEqual(Object.keys(row).sort(), [
    "attention", "event", "group", "moved", "search", "stale", "state", "ticket", "title", "waitsOn", "waitsOnHuman", "waitsOnName",
  ]);
  assert.equal(row.state, "in progress");
  assert.equal(row.waitsOnName, "claude");
  assert.equal(row.waitsOnHuman, false);
  assert.equal(row.title, "Repair the resident");
  assert.equal(row.ticket, 1);
  // Ordinary staleness is carried for the count below the list and is never a
  // row state: 27 of 42 live commitments have it, so a state fed from it would
  // colour nearly two rows in three.
  assert.equal(row.stale, true);
  assert.equal(row.attention, false);
});

test("only world-staleness and dispute make a row need attention", () => {
  const projection = room(
    [
      { event: "quiet", kind: "request", actor: "hugh", ts: NOW - DAY, stale: true },
      { event: "loud", kind: "request", actor: "hugh", ts: NOW - DAY, world: true },
      { event: "argued", kind: "request", actor: "hugh", ts: NOW - DAY },
    ],
    {
      commitments: [
        { request: "quiet", requester: "hugh", status: "open", stale: true },
        { request: "loud", requester: "hugh", status: "open" },
        { request: "argued", requester: "hugh", status: "disputed" },
      ],
    },
  );
  const byEvent = new Map(workRows(projection, context(projection)).map((row) => [row.event, row]));
  assert.equal(byEvent.get("quiet").state, "unclaimed");
  assert.equal(byEvent.get("loud").state, "needs attention");
  // A disputed commitment is not one of the live statuses, so it is not a row
  // in the default list at all; what matters is that ordinary staleness alone
  // never promotes one.
  assert.equal(byEvent.has("argued"), false);
});

test("a row ages from the last durable event anywhere in its thread", () => {
  const projection = room([
    { event: "r1", kind: "request", actor: "hugh", ts: NOW - 21 * DAY },
    { event: "p1", kind: "promise", actor: "claude", ts: NOW - HOUR, parent: "r1" },
  ], { commitments: [{ request: "r1", requester: "hugh", performer: "claude", promise: "p1", status: "promised", waiting_on: "claude" }] });
  const [row] = workRows(projection, context(projection));
  // A three-week-old request that moved an hour ago is not neglected work.
  assert.equal(row.moved, NOW - HOUR);
  assert.equal(age(row.moved, NOW), "1h");
  assert.equal(age(NOW - 7 * DAY, NOW), "7d");
  assert.equal(age(NOW - 180, NOW), "3m");
});

test("the default order is the priority rule, oldest first except while running", () => {
  const projection = room(
    [
      { event: "running-old", kind: "request", actor: "hugh", ts: NOW - 5 * DAY },
      { event: "running-new", kind: "request", actor: "hugh", ts: NOW - HOUR },
      { event: "human", kind: "request", actor: "codex", ts: NOW - 2 * DAY },
      { event: "unclaimed", kind: "request", actor: "hugh", ts: NOW - DAY },
      { event: "attention", kind: "request", actor: "hugh", ts: NOW - 30, world: true },
    ],
    {
      commitments: [
        { request: "running-old", requester: "hugh", performer: "claude", promise: "x", status: "promised", waiting_on: "claude" },
        { request: "running-new", requester: "hugh", performer: "claude", promise: "y", status: "promised", waiting_on: "claude" },
        { request: "human", requester: "codex", performer: "codex", promise: "z", report: "rz", status: "reported", waiting_on: "hugh" },
        { request: "unclaimed", requester: "hugh", addressed_to: "codex", status: "open" },
        { request: "attention", requester: "hugh", performer: "claude", promise: "w", status: "promised", waiting_on: "claude" },
      ],
    },
  );
  const order = sortRows(workRows(projection, context(projection))).map((row) => row.event);
  assert.deepEqual(order, ["attention", "unclaimed", "human", "running-new", "running-old"]);
});

test("a third click on a column returns to priority order", () => {
  let sort = sortAfterClick(undefined, "age");
  assert.deepEqual(sort, { key: "age", descending: false });
  sort = sortAfterClick(sort, "age");
  assert.deepEqual(sort, { key: "age", descending: true });
  assert.equal(sortAfterClick(sort, "age"), undefined);
  // Moving to a different column starts that column afresh rather than
  // inheriting the previous direction.
  assert.deepEqual(sortAfterClick({ key: "age", descending: true }, "title"), { key: "title", descending: false });
});

test("a sort reorders the rows that are there and hides none", () => {
  const projection = room(
    [
      { event: "a", kind: "request", actor: "hugh", ts: NOW - DAY, text: "Zebra" },
      { event: "b", kind: "request", actor: "hugh", ts: NOW - 2 * DAY, text: "Alpha" },
    ],
    {
      commitments: [
        { request: "a", requester: "hugh", status: "open" },
        { request: "b", requester: "hugh", status: "open" },
      ],
    },
  );
  const rows = workRows(projection, context(projection));
  for (const key of ["state", "waits", "age", "title", "ticket"]) {
    for (const descending of [false, true]) {
      assert.equal(sortRows(rows, { key, descending }).length, rows.length);
    }
  }
  assert.deepEqual(sortRows(rows, { key: "title", descending: false }).map((row) => row.title), ["Alpha", "Zebra"]);
  assert.deepEqual(sortRows(rows, { key: "title", descending: true }).map((row) => row.title), ["Zebra", "Alpha"]);
});

test("lifecycle-stale requests are a separate population, not rows in the default list", () => {
  const projection = room(
    [
      { event: "live", kind: "request", actor: "hugh", ts: NOW - DAY },
      { event: "abandoned", kind: "request", actor: "hugh", ts: NOW - 30 * DAY },
    ],
    {
      commitments: [
        { request: "live", requester: "hugh", status: "open" },
        { request: "abandoned", requester: "hugh", status: "stale" },
      ],
    },
  );
  assert.deepEqual(workRows(projection, context(projection)).map((row) => row.event), ["live"]);
  assert.deepEqual(workRows(projection, context(projection), true).map((row) => row.event), ["abandoned"]);
});

// ---------------------------------------------------------------------------
// The thread: the salient set on one rail, everything else behind counted
// expanders.
// ---------------------------------------------------------------------------

const spineContext = (projection, extra = {}) => ({
  projection,
  tickets: new Map(projection.decisions.map((decision) => [decision.event, decision.sequence])),
  nameOf: (fingerprint) => fingerprint,
  ...extra,
});

// The acceptance thread's shape: a request, a promise, a report, four review
// rounds of which the last is the one that counts, and a pile of retired
// artifacts and supersessions behind them.
function acceptanceThread() {
  const records = [
    { event: "req", kind: "request", actor: "codex", ts: NOW - 8 * DAY, text: "re-cut the shipped back-pressure repair", body: { to: "claude" } },
    { event: "promise", kind: "promise", actor: "claude", ts: NOW - 8 * DAY, parent: "req" },
    { event: "art-old", kind: "artifact", actor: "claude", ts: NOW - 8 * DAY, parent: "promise", retired: true, body: { path: "internal/kernel", commit: "1111111111111111111111111111111111111111" } },
    { event: "kill-art-old", type: "supersede", actor: "claude", ts: NOW - 8 * DAY, parent: "art-old", target: "art-old" },
    { event: "report", kind: "report", actor: "claude", ts: NOW - 7 * DAY, parent: "promise", body: { status: "ready-for-review", head: "acf134411781f16ec16817ab7e084a104acb0fac" } },
    { event: "v-changes", kind: "report", actor: "codex", ts: NOW - 7 * DAY, parent: "report", body: { verdict: "changes-requested", head: "1111111111111111111111111111111111111111" } },
    { event: "v-first", kind: "report", actor: "codex", ts: NOW - 7 * DAY, parent: "report", body: { verdict: "approved", head: "acf134411781f16ec16817ab7e084a104acb0fac" } },
    { event: "v-latest", kind: "report", actor: "codex", ts: NOW - 7 * DAY, parent: "report", body: { verdict: "approved", head: "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d" } },
    { event: "chat", kind: "assert", actor: "claude", ts: NOW - 7 * DAY, parent: "report", text: "a note about it" },
  ];
  return room(records, {
    commitments: [{ request: "req", requester: "codex", addressed_to: "claude", performer: "claude", promise: "promise", report: "report", status: "reported", waiting_on: "codex" }],
    reviews: [
      { report: "v-changes", reviewer: "codex", verdict: "changes-requested", head: "1111111111111111111111111111111111111111", independence: "independent", ratified: true },
      { report: "v-first", reviewer: "codex", verdict: "approved", head: "acf134411781f16ec16817ab7e084a104acb0fac", independence: "independent", ratified: true },
      { report: "v-latest", reviewer: "codex", verdict: "approved", head: "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d", independence: "independent", ratified: true },
    ],
  });
}

test("salience takes the latest verdict, not the first approval it finds", () => {
  const projection = acceptanceThread();
  const spine = buildSpine("req", spineContext(projection));
  const verdict = spine.stations.find((station) => station.kind === "verdict");
  // Two of the three verdicts are ratified approvals. A rendering that showed
  // the first one it found would name the wrong head.
  assert.equal(verdict.event, "v-latest");
  assert.equal(spine.head, "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d");
  assert.match(verdict.what, /approved by codex, ratified/);
});

test("the rail carries the salient set and elides the rest into counted expanders", () => {
  const projection = acceptanceThread();
  const spine = buildSpine("req", spineContext(projection));
  assert.deepEqual(
    spine.stations.map((station) => station.kind),
    ["request", "promise", "report", "verdict", "merge", "open"],
  );
  // Every record in the thread is either on the rail or in exactly one
  // expander: elision is stated, never silent.
  const railed = new Set(spine.stations.map((station) => station.event).filter(Boolean));
  const elided = spine.expanders.flatMap((expander) => expander.events);
  assert.equal(new Set(elided).size, elided.length);
  assert.deepEqual(
    [...spine.records].filter((event) => !railed.has(event)).sort(),
    [...elided].sort(),
  );
  const byId = new Map(spine.expanders.map((expander) => [expander.id, expander.events]));
  assert.deepEqual(byId.get("repair").sort(), ["art-old", "kill-art-old"]);
  assert.deepEqual(byId.get("rounds").sort(), ["v-changes", "v-first"]);
  assert.deepEqual(byId.get("talk"), ["chat"]);
  // An expander whose count is zero is not drawn.
  assert.equal(byId.has("superseded"), false);
});

test("the merge station reads git, and a check that could not run is not a negative", () => {
  const projection = acceptanceThread();
  const head = "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d";

  const asking = buildSpine("req", spineContext(projection)).stations.find((s) => s.kind === "merge");
  assert.equal(asking.present, false);
  assert.match(asking.what, /asking git/);
  assert.doesNotMatch(asking.what, /not on/);

  const unknown = buildSpine("req", spineContext(projection, {
    landings: new Map([[head, { commit: head, status: "unknown", reason: "no such commit" }]]),
    branch: "main",
  })).stations.find((s) => s.kind === "merge");
  assert.equal(unknown.present, false);
  assert.match(unknown.what, /could not be determined/);
  assert.doesNotMatch(unknown.what, /is not on main yet/);

  const absent = buildSpine("req", spineContext(projection, {
    landings: new Map([[head, { commit: head, status: "absent" }]]),
    branch: "main",
  })).stations.find((s) => s.kind === "merge");
  assert.equal(absent.present, false);
  assert.match(absent.what, /is not on main yet/);
});

test("shipped but never closed reads off the rail without opening anything", () => {
  const projection = acceptanceThread();
  const head = "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d";
  const spine = buildSpine("req", spineContext(projection, {
    landings: new Map([[head, { commit: head, status: "landed", merge: "44d8b4fa7d62f04d9b240434e8c044eddc00b496", time: NOW - 7 * DAY }]]),
    branch: "main",
  }));
  const merge = spine.stations.find((station) => station.kind === "merge");
  assert.equal(merge.present, true);
  assert.match(merge.what, /landed on main/);
  assert.equal(merge.commit, "44d8b4fa7d62f04d9b240434e8c044eddc00b496");
  // The sentence needs both sources. The fold alone says only "reported,
  // waiting on codex"; git alone says only that the head is on main.
  const blocker = spine.stations.find((station) => station.branch);
  assert.match(blocker.what, /shipped but never closed/);
  assert.match(blocker.what, /waiting on codex/);
  assert.equal(blocker.tone, "danger");
});

test("a station that has not happened names what would fill it and who owes it", () => {
  const projection = room(
    [{ event: "req", kind: "request", actor: "hugh", ts: NOW - DAY, body: { to: "codex" } }],
    { commitments: [{ request: "req", requester: "hugh", addressed_to: "codex", status: "open" }] },
  );
  const spine = buildSpine("req", spineContext(projection));
  const promise = spine.stations.find((station) => station.kind === "promise");
  assert.equal(promise.present, false);
  assert.equal(promise.what, "unclaimed, addressed to codex");
  // No head means no merge station: the rail does not draw a question nobody
  // asked.
  assert.equal(spine.stations.some((station) => station.kind === "merge"), false);
  assert.equal(spine.head, undefined);
});

test("a world-stale approval is loud on the rail, and ordinary staleness is not", () => {
  const quiet = acceptanceThread();
  assert.equal(buildSpine("req", spineContext(quiet)).stations.some((s) => /superseded world/.test(s.what)), false);

  const loud = acceptanceThread();
  loud.statements.find((statement) => statement.event === "v-latest").describes_superseded_world = true;
  const blockers = buildSpine("req", spineContext(loud)).stations.filter((station) => station.branch);
  assert.equal(blockers.some((station) => /superseded world/.test(station.what)), true);
});
