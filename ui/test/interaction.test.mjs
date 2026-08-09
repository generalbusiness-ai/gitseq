import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { RetryKeys, parsePresenceLabel, threadTargetKey } from "../src/lib/interaction.ts";
import { mentionAt, mentionFingerprints, mentionNames, mentionTokens } from "../src/lib/mentions.ts";
import { buildThreadIndex } from "../src/lib/threads.ts";
import { belongsInRoom, interpretationGaps, kindLabel, statusLabel } from "../src/lib/util.ts";

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

test("the room hides work records and translates workflow status", () => {
  assert.equal(belongsInRoom("assert"), true);
  assert.equal(belongsInRoom("request"), true);
  for (const kind of ["roster", "infra-key", "seal", "artifact"]) assert.equal(belongsInRoom(kind), false);
  assert.equal(statusLabel("requested"), "waiting");
  assert.equal(statusLabel("promised"), "in progress");
  assert.equal(statusLabel("reported"), "ready");
  assert.equal(statusLabel("satisfied"), "done");
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

test("one interpretation gap per distinct refusal, however many events it refused", () => {
  const refusal = (event, reason) => ({ event, verdict: "uninterpretable", reason });
  const gaps = interpretationGaps({
    decisions: [
      { event: "e0", verdict: "effective", reason: "statement recorded" },
      refusal("e1", "activated interpreter execution is not held"),
      refusal("e2", "activated interpreter execution is not held"),
      refusal("e3", "activated interpreter execution is not held"),
      { event: "e4", verdict: "undefined-kind", reason: 'undefined kind "finding"' },
    ],
  });
  assert.equal(gaps.length, 2);
  assert.deepEqual(gaps[0].events, ["e1", "e2", "e3"]);
  assert.deepEqual(gaps[1].events, ["e4"]);
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
