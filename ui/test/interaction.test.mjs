import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { hueOf, initialsOf } from "../src/lib/avatar.ts";
import { RetryKeys, fingerprintOfPresentActor, fingerprintsIdentifySameActor, parsePresenceLabel, presentActors, threadTargetKey } from "../src/lib/interaction.ts";
import { mentionAt, mentionFingerprints, mentionNames, mentionTokens } from "../src/lib/mentions.ts";
import { emptyPersonalWorkMemory, followWorkTopic, loadPersonalWorkMemory, savePersonalWorkMemory, viewWorkTopic } from "../src/lib/memory.ts";
import { buildThreadIndex } from "../src/lib/threads.ts";
import { soleCurrentSupersedeBasis } from "../src/lib/supersedeLinks.ts";
import { CLOSED_WORK_STATUSES, buildWorkProjection, filterPersonalWorkProjection, filterWorkProjection, topicChangeSince, workAttentionCount, workItemNeedsAction, workItemState } from "../src/lib/work.ts";
import { belongsInRoom, commitmentRelationship, interpretationNotice, isInterpretationGap, kindLabel, statusLabel } from "../src/lib/util.ts";
import { groupOpenWork, worktreesForCommitment } from "../src/lib/worktrees.ts";

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
    { label: "claude (a5d35aa7e479)", name: "claude", fingerprint: "a5d35aa7e479", sessions: 3 },
    { label: "codex (5f12e916d136)", name: "codex", fingerprint: "5f12e916d136", sessions: 1 },
    { label: "hugh (7fbc80f1ba06)", name: "hugh", fingerprint: "7fbc80f1ba06", sessions: 1 },
  ]);
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
  const read = (name) => readFileSync(new URL(`../src/components/${name}`, import.meta.url), "utf8");
  for (const name of ["SequencePane.tsx", "ThreadPane.tsx", "Stream.tsx"]) {
    const source = read(name);
    assert.match(source, /linked item/);
    assert.doesNotMatch(source, /replacement/);
  }
});

test("the room hides work records and translates workflow status", () => {
  assert.equal(belongsInRoom("assert"), true);
  assert.equal(belongsInRoom("request"), true);
  for (const kind of ["roster", "infra-key", "seal", "artifact"]) assert.equal(belongsInRoom(kind), false);
  assert.equal(statusLabel("open"), "open");
  assert.equal(statusLabel("promised"), "in progress");
  assert.equal(statusLabel("reported"), "ready");
  assert.equal(statusLabel("satisfied"), "done");
});

test("work groups distinguish available, in-progress, and review commitments", () => {
  const commitment = (request, status) => ({ request, requester: "human", performer: "agent", status });
  const groups = groupOpenWork([
    commitment("available", "open"),
    commitment("building", "promised"),
    commitment("review", "reported"),
    commitment("attention", "stale"),
  ]);
  assert.deepEqual(groups.available.map((item) => item.request), ["available"]);
  assert.deepEqual(groups.inProgress.map((item) => item.request), ["building"]);
  assert.deepEqual(groups.review.map((item) => item.request), ["review"]);
});

test("unclaimed requests are addressed without waiting on their addressee", () => {
  const nameOf = (fingerprint) => ({ agent: "Agent", human: "Human" })[fingerprint] ?? fingerprint;
  const unclaimed = { request: "request", requester: "human", addressed_to: "agent", status: "open" };
  assert.equal(commitmentRelationship(unclaimed, nameOf), "addressed to Agent · unclaimed");
  assert.equal(unclaimed.waiting_on, undefined);
  assert.equal(unclaimed.performer, undefined);

  const promised = { request: "request", requester: "human", performer: "agent", promise: "promise", status: "promised", waiting_on: "agent" };
  assert.equal(commitmentRelationship(promised, nameOf), "waiting on Agent");
  const reported = { ...promised, report: "report", status: "reported", waiting_on: "human" };
  assert.equal(commitmentRelationship(reported, nameOf), "waiting on Human");
});

test("Work groups by conversational ancestry without treating later citations as parents", () => {
  const statement = (event, actor, kind, text, body) => ({ event, actor, kind, text, body, timestamp: Number(event.slice(1)) || 1 });
  const projection = {
    decisions: ["r1", "p1", "r2", "p2", "x2", "r3", "r4", "r5"].map((event) => ({ event, verdict: "effective", reason: "ok" })),
    acts: [],
    statements: [
      statement("r1", "hugh", "request", "Ship the deployment design"),
      statement("p1", "codex", "promise", "I will work", { branch: "task/deployment-story" }),
      statement("r2", "claude", "request", "Check deploy readiness"),
      statement("p2", "codex", "promise", "I will check"),
      statement("x2", "codex", "assert", "The remembered alias is deploy-readiness"),
      statement("r3", "claude", "request", "Independent root"),
      statement("r4", "claude", "request", "Replies to independent root but cites deployment"),
      statement("r5", "hugh", "request", "Old closed work"),
    ],
    commitments: [
      { request: "r1", requester: "hugh", status: "open" },
      { request: "r2", requester: "claude", performer: "codex", promise: "p2", status: "promised", stale: true },
      { request: "r3", requester: "claude", status: "open" },
      { request: "r4", requester: "claude", status: "open" },
      { request: "r5", requester: "hugh", performer: "codex", promise: "old-promise", report: "old-report", status: "satisfied" },
    ],
    artifacts: [{ event: "artifact", path: "notes/deploy.md", commit: "abc", stale: false }],
    actors: {},
    provenance: {
      r1: [], p1: ["r1"], r2: ["p1"], p2: ["r2"], x2: ["p2"],
      r3: [], r4: ["r3", "r1"], r5: [], artifact: ["x2"],
    },
  };
  const work = buildWorkProjection(projection);
  const deployment = work.topics.find((topic) => topic.event === "r1");
  assert.deepEqual(deployment.items.map((item) => item.request.event), ["r2", "r1"]);
  assert.equal(deployment.author, "hugh");
  assert.equal(deployment.attentionCount, 1);
  assert.equal(work.topics.find((topic) => topic.event === "r3").items.length, 2);
  assert.equal(work.topics.some((topic) => topic.event === "r4"), false);

  const defaults = filterWorkProjection(work, { open: true, attention: true, closed: false });
  assert.equal(defaults.topics.some((topic) => topic.event === "r5"), false);
  assert.deepEqual(filterWorkProjection(work, { open: true, attention: true, closed: false, author: "hugh" }).topics.map((topic) => topic.event), ["r1"]);
  assert.deepEqual(filterWorkProjection(work, { open: true, attention: true, closed: false, query: "deploy-readiness" }).topics.map((topic) => topic.event), ["r1"]);
  assert.equal(filterWorkProjection(work, { open: true, attention: true, closed: true, query: "deployment" }).topics[0].event, "r1");
  assert.deepEqual(filterWorkProjection(work, { open: true, attention: true, closed: false, query: "notes/deploy.md" }).topics.map((topic) => topic.event), ["r1"]);
  assert.deepEqual(filterWorkProjection(work, { open: false, attention: false, closed: true }).topics.map((topic) => topic.event), ["r5"]);
});

test("attention qualifies rather than replaces a lifecycle lane", () => {
  assert.deepEqual(workItemState({ request: "r", requester: "hugh", performer: "codex", status: "reported", stale: true }), {
    open: true,
    attention: true,
    closed: false,
    lane: "review",
  });
  assert.deepEqual(workItemState({ request: "r", requester: "hugh", status: "withdrawn" }), {
    open: false,
    attention: false,
    closed: true,
    lane: "closed",
  });
});

test("Needs my action follows unresolved semantic responsibility, never a read watermark", () => {
  const item = (commitment) => ({ commitment, request: { event: commitment.request }, key: commitment.request, topicEvent: "topic", order: 1 });
  assert.equal(workItemNeedsAction(item({ request: "offered", requester: "human", addressed_to: "codex", status: "open" }), "codex"), true);
  assert.equal(workItemNeedsAction({ ...item({ request: "stale-offer", requester: "human", status: "stale" }), request: { event: "stale-offer", body: { to: "codex" } } }, "codex"), true);
  assert.equal(workItemNeedsAction(item({ request: "unclaimed", requester: "human", addressed_to: "claude", status: "open" }), "codex"), false);
  assert.equal(workItemNeedsAction(item({ request: "building", requester: "human", performer: "codex", promise: "promise", status: "promised" }), "codex"), true);
  assert.equal(workItemNeedsAction(item({ request: "review", requester: "codex", performer: "claude", promise: "promise", report: "report", status: "reported" }), "codex"), true);
  assert.equal(workItemNeedsAction(item({ request: "done", requester: "codex", performer: "claude", promise: "promise", report: "report", status: "satisfied" }), "codex"), false);
  assert.equal(workItemNeedsAction(item({ request: "repair", requester: "codex", performer: "claude", promise: "promise", report: "report", status: "reported", stale: true }), "codex"), true);
});

test("authored and followed topics expose only other people's changes after their watermark", () => {
  const topic = {
    event: "topic",
    author: "codex",
    items: [{ commitment: { status: "reported" } }],
    activity: [
      { event: "root", actor: "codex", order: 0, timestamp: 10 },
      { event: "reply", actor: "claude", order: 1, timestamp: 20 },
      { event: "self", actor: "codex", order: 2, timestamp: 30 },
      { event: "latest-other", actor: "hugh", order: 3, timestamp: 40 },
    ],
  };
  assert.deepEqual(topicChangeSince(topic, "codex", new Set(), 1), {
    event: "latest-other", actor: "hugh", order: 3, timestamp: 40, status: "reported",
  });
  assert.equal(topicChangeSince(topic, "codex", new Set(), 3), undefined);
  assert.equal(topicChangeSince({ ...topic, author: "claude" }, "codex", new Set(), -1), undefined);
  assert.equal(topicChangeSince({ ...topic, author: "claude" }, "codex", new Set(["topic"]), 3), undefined);
  assert.equal(topicChangeSince({ ...topic, activity: [{ event: "self", actor: "codex", order: 4 }] }, "codex", new Set(), -1), undefined);
});

test("personal Work filters select responsibility, unread topics, and explicit follows without rewriting lifecycle truth", () => {
  const actionable = { key: "a", request: { event: "a" }, commitment: { request: "a", requester: "human", addressed_to: "codex", status: "open" }, open: true, attention: false, closed: false, lane: "available", order: 1 };
  const theirs = { key: "b", request: { event: "b" }, commitment: { request: "b", requester: "human", addressed_to: "claude", status: "open" }, open: true, attention: false, closed: false, lane: "available", order: 2 };
  const topic = (event, author, items, activity) => ({ event, author, items, activity, latestOrder: 2, openCount: items.length, attentionCount: 0, closedCount: 0 });
  const work = {
    authors: ["codex", "claude"], attention: [],
    topics: [
      topic("mine", "codex", [actionable, theirs], [{ event: "change", actor: "claude", order: 2 }]),
      topic("followed", "claude", [theirs], [{ event: "change-2", actor: "hugh", order: 2 }]),
    ],
  };
  const followed = new Set(["followed"]);
  const needs = filterPersonalWorkProjection(work, "needs", "codex", followed, {});
  assert.deepEqual(needs.topics.map((topic) => topic.event), ["mine"]);
  assert.deepEqual(needs.topics[0].items.map((item) => item.key), ["a"]);
  assert.deepEqual(filterPersonalWorkProjection(work, "needs", "codex", followed, { mine: 999 }).topics.map((topic) => topic.event), ["mine"]);
  assert.deepEqual(filterPersonalWorkProjection(work, "unread", "codex", followed, {}).topics.map((topic) => topic.event), ["mine", "followed"]);
  assert.deepEqual(filterPersonalWorkProjection(work, "following", "codex", followed, {}).topics.map((topic) => topic.event), ["followed"]);
  assert.equal(work.topics[0].items.length, 2);
});

test("personal topic memory is isolated by room and exact actor and following starts at now", () => {
  const previous = globalThis.localStorage;
  const values = new Map();
  globalThis.localStorage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: (key) => values.delete(key),
  };
  try {
    let memory = emptyPersonalWorkMemory();
    memory = followWorkTopic(memory, "topic", true, 12);
    assert.deepEqual(memory, { followed: ["topic"], viewed: { topic: 12 } });
    memory = { ...memory, viewed: { ...memory.viewed, untouched: 7 } };
    memory = viewWorkTopic(memory, "topic", 18);
    assert.deepEqual(memory.viewed, { topic: 18, untouched: 7 });
    savePersonalWorkMemory("room-one", "actor-one", memory);
    assert.deepEqual(loadPersonalWorkMemory("room-one", "actor-one"), { followed: ["topic"], viewed: { topic: 18, untouched: 7 } });
    assert.deepEqual(loadPersonalWorkMemory("room-one", "actor-two"), emptyPersonalWorkMemory());
    assert.deepEqual(loadPersonalWorkMemory("room-two", "actor-one"), emptyPersonalWorkMemory());
    assert.deepEqual(followWorkTopic(memory, "topic", false, 18), { followed: [], viewed: { topic: 18, untouched: 7 } });
  } finally {
    if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous;
  }
});

test("personal topic memory rejects malformed local storage fields", () => {
  const previous = globalThis.localStorage;
  const values = new Map([
    ["workroom.personal-work.room.actor", JSON.stringify({
      followed: ["kept", "", 7, null, "kept"],
      viewed: { kept: 4, negative: -1, text: "5", empty: null, "": 8 },
    })],
  ]);
  globalThis.localStorage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: (key) => values.delete(key),
  };
  try {
    assert.deepEqual(loadPersonalWorkMemory("room", "actor"), { followed: ["kept"], viewed: { kept: 4 } });
  } finally {
    if (previous === undefined) delete globalThis.localStorage; else globalThis.localStorage = previous;
  }
});

test("the Work summary counts every terminal lifecycle as closed", () => {
  assert.deepEqual([...CLOSED_WORK_STATUSES], ["satisfied", "withdrawn", "cancelled", "reneged"]);
});

test("Work accounts for qualifier attention, stale artifacts, and unlinked promises", () => {
  const projection = {
    decisions: [
      { event: "request", verdict: "effective", reason: "ok" },
      { event: "promise", verdict: "ineffective", reason: "dangling promise has no request" },
      { event: "artifact", verdict: "effective", reason: "ok" },
    ],
    acts: [], actors: {},
    statements: [
      { event: "request", actor: "hugh", kind: "request", text: "review me" },
      { event: "promise", actor: "codex", kind: "promise", text: "unlinked work" },
      { event: "artifact", actor: "codex", kind: "artifact", text: "old build" },
    ],
    commitments: [{ request: "request", requester: "hugh", performer: "codex", status: "reported", stale: true }],
    artifacts: [{ event: "artifact", path: "ui", commit: "abcdef012345", stale: true }],
    provenance: { request: [], promise: [], artifact: ["request"] },
  };
  const work = buildWorkProjection(projection);
  assert.equal(workAttentionCount(projection), 3);
  assert.deepEqual(work.attention.map((item) => item.kind), ["artifact", "unlinked-promise"]);
  assert.equal(filterWorkProjection(work, { open: true, attention: true, closed: false }).attention.length, 2);
  assert.equal(filterWorkProjection(work, { open: true, attention: false, closed: false }).attention.length, 0);
  assert.equal(filterWorkProjection(work, { open: true, attention: true, closed: false, author: "hugh" }).attention.length, 0);
});

test("local worktrees join current promise, docs report, and exact commit-trailer shapes", () => {
  const commitment = { request: "request", requester: "human", performer: "agent", promise: "promise", report: "report", status: "reported" };
  const projection = {
    decisions: [],
    acts: [],
    statements: [
      { event: "request", actor: "human", kind: "request", text: "implement" },
      { event: "promise", actor: "agent", kind: "promise", text: "working", body: { branch: "task/current" } },
      { event: "report", actor: "agent", kind: "report", text: "ready", body: { head: "review-head" } },
    ],
    commitments: [commitment],
    artifacts: [{ event: "artifact", path: ".", commit: "artifact-head", stale: false }],
    actors: {},
    provenance: { request: [], promise: ["request"], report: ["promise"], artifact: ["report"] },
  };
  const commits = [{ hash: "trailer-head", parents: null, subject: "implementation", author: "agent", time: 1, rests_on: ["request"] }];
  const worktrees = [
    { checkout: "current", branch: "task/current", head: "advanced-head", state: "dirty", current: true },
    { checkout: "docs", branch: "task/docs", head: "review-head", state: "clean" },
    { checkout: "bootstrap", branch: "task/bootstrap", head: "artifact-head", state: "clean" },
    { checkout: "trailer", branch: "task/trailer", head: "trailer-head", state: "clean" },
    { checkout: "unrelated", branch: "task/else", head: "other", state: "clean" },
  ];
  const associations = worktreesForCommitment(commitment, projection, commits, worktrees);
  assert.deepEqual(
    associations.map((association) => association.worktree.checkout),
    ["current", "docs", "trailer"],
  );
  assert.equal(associations[0].headMatches, false);
  assert.equal(associations.find((association) => association.worktree.checkout === "trailer").evidence, "local-trailer");
  assert.equal(associations.some((association) => association.worktree.checkout === "unrelated"), false);
});

test("artifact provenance joins bootstrap-style reports without explicit head fields", () => {
  const commitment = { request: "bootstrap-request", requester: "human", performer: "agent", promise: "bootstrap-promise", report: "bootstrap-report", status: "reported" };
  const projection = {
    decisions: [], acts: [], actors: {}, commitments: [commitment],
    statements: [
      { event: "bootstrap-request", actor: "human", kind: "request", text: "bootstrap" },
      { event: "bootstrap-promise", actor: "agent", kind: "promise", text: "working" },
      { event: "bootstrap-report", actor: "agent", kind: "report", text: "ready" },
    ],
    artifacts: [{ event: "bootstrap-artifact", path: ".", commit: "bootstrap-head", stale: false }],
    provenance: {
      "bootstrap-request": [],
      "bootstrap-promise": ["bootstrap-request"],
      "bootstrap-report": ["bootstrap-promise"],
      "bootstrap-artifact": ["bootstrap-report"],
    },
  };
  const [association] = worktreesForCommitment(commitment, projection, [], [
    { checkout: "bootstrap", branch: "task/bootstrap", head: "bootstrap-head", state: "clean" },
  ]);
  assert.equal(association.worktree.checkout, "bootstrap");
  assert.equal(association.headMatches, true);
});

test("a single exact review head marks an associated branch that moved", () => {
  const commitment = { request: "request", requester: "human", performer: "agent", promise: "promise", report: "report", status: "reported" };
  const projection = {
    decisions: [], acts: [], artifacts: [], actors: {}, commitments: [commitment], provenance: {},
    statements: [
      { event: "request", actor: "human", kind: "request", text: "implement" },
      { event: "promise", actor: "agent", kind: "promise", text: "working", body: { branch: "task/review" } },
      { event: "report", actor: "agent", kind: "report", text: "ready", body: { head: "approved-head" } },
    ],
  };
  const [association] = worktreesForCommitment(commitment, projection, [], [
    { checkout: "review", branch: "task/review", head: "new-head", state: "clean" },
  ]);
  assert.equal(association.expectedHead, "approved-head");
  assert.equal(association.headMatches, false);
});

test("artifact provenance stops at a different commitment boundary", () => {
  const commitment = { request: "wanted-request", requester: "human", performer: "agent", promise: "wanted-promise", status: "promised" };
  const projection = {
    decisions: [], acts: [], actors: {}, commitments: [commitment],
    statements: [
      { event: "wanted-request", actor: "human", kind: "request", text: "wanted" },
      { event: "wanted-promise", actor: "agent", kind: "promise", text: "working", body: { branch: "task/wanted" } },
      { event: "other-request", actor: "human", kind: "request", text: "next" },
      { event: "other-artifact", actor: "agent", kind: "artifact", text: "other", body: { path: ".", commit: "other-head" } },
    ],
    artifacts: [{ event: "other-artifact", path: ".", commit: "other-head", stale: false }],
    provenance: {
      "wanted-request": [], "wanted-promise": ["wanted-request"],
      "other-request": ["wanted-promise"], "other-artifact": ["other-request"],
    },
  };
  const associations = worktreesForCommitment(commitment, projection, [], [
    { checkout: "wanted", branch: "task/wanted", head: "wanted-head", state: "clean" },
    { checkout: "other", branch: "task/other", head: "other-head", state: "clean" },
  ]);
  assert.deepEqual(associations.map((association) => association.worktree.checkout), ["wanted"]);
});

test("multiple declared heads still mark a branch at neither head as moved", () => {
  const commitment = { request: "request", requester: "human", performer: "agent", promise: "promise", report: "report", status: "reported" };
  const projection = {
    decisions: [], acts: [], artifacts: [], actors: {}, commitments: [commitment], provenance: {},
    statements: [
      { event: "request", actor: "human", kind: "request", text: "implement" },
      { event: "promise", actor: "agent", kind: "promise", text: "working", body: { branch: "task/review", head: "old-head" } },
      { event: "report", actor: "agent", kind: "report", text: "ready", body: { head: "review-head" } },
    ],
  };
  const [association] = worktreesForCommitment(commitment, projection, [], [
    { checkout: "review", branch: "task/review", head: "different-head", state: "clean" },
  ]);
  assert.deepEqual(association.expectedHeads, ["old-head", "review-head"]);
  assert.equal(association.headMatches, false);
});

test("declared render classes, not kind names, place new vocabulary in the UI", () => {
  const definition = (name, render) => ({ name, render });
  const vocabulary = {
    definitions: [definition("finding", "note"), definition("policy", "governance"), definition("release", "artifact")],
    binding: { status: "unbound", transitions: [] },
  };
  assert.equal(belongsInRoom("finding", vocabulary), true);
  assert.equal(belongsInRoom("policy", vocabulary), false);
  assert.equal(belongsInRoom("release", vocabulary), false);
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
  const composer = read("components/Composer.tsx");
  const profile = read("components/ProfilePane.tsx");
  const app = read("App.tsx");
  const room = [composer, profile, read("components/Stream.tsx"), read("components/ThreadPane.tsx")].join("\n");

  assert.doesNotMatch(composer, /Only you are holding|label: "(?:Note|Proposal|Request)"/);
  assert.doesNotMatch(profile, /actor\.roles|roles\.join/);
  assert.doesNotMatch(room, /remove citation|cites the parent message|formal statement/);
  assert.doesNotMatch(app, /pane\?\.kind !== "thread"/);
  assert.match(composer, /Temporary/);
  assert.match(composer, /Kept/);
});

test("Work is the default center and List and Board share one projection", () => {
  const read = (name) => readFileSync(new URL(`../src/${name}`, import.meta.url), "utf8");
  const app = read("App.tsx");
  const work = read("components/WorkDrawer.tsx");
  assert.match(app, /useState<MainView>\("work"\)/);
  assert.match(app, /mainView === "work"/);
  assert.match(work, /buildWorkProjection/);
  assert.match(work, /open: true, attention: true, closed: false/);
  assert.match(work, /presentation === "list"/);
  assert.match(work, /<WorkBoard/);
  assert.match(work, /Other attention/);
  assert.match(work, /asked by \{nameOf\(item\.request\.actor\)\}/);
  assert.doesNotMatch(work, /written by \{nameOf\(topic\.author\)\}/);
  assert.doesNotMatch(work, /draggable|onDrag|drop/i);
});
