import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

import {
  COMPANION_NOTE,
  INTERFACE_NOTE,
  PROPOSAL,
  realProjection,
  REVIEW_APPROVAL,
  REVIEW_PROMISE,
  REVIEW_REQUEST,
  SEED,
} from "./real-log.mjs";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

function workroom(projection) {
  return {
    actors: [],
    commits: [],
    graphTruncated: false,
    offline: false,
    localOffline: false,
    status: {
      durable: { genesis: "genesis", head: "head", depth: 1, projection },
      live: { cursor: { generation: "generation", position: 1 }, presence: [], activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

const session = { credential: "browser", live: true, setActor() {} };

// Proposal 2147's shape, which started this request: an effective, unratified
// propose citing an artifact for context, where that artifact answered a
// satisfied request two weeks earlier. Two hops of citation must not file the
// proposal under the unrelated request's thread.
function proposalProjection() {
  const unrelated = "1111111111111111111111111111111111111111";
  const artifact = "2222222222222222222222222222222222222222";
  const propose = "3333333333333333333333333333333333333333";
  const claude = "claudefingerprint0000000000000000";
  const projection = {
    decisions: [
      { event: unrelated, sequence: 1, verdict: "effective", reason: "recorded" },
      { event: artifact, sequence: 2, verdict: "effective", reason: "recorded" },
      { event: propose, sequence: 3, verdict: "effective", reason: "recorded" },
    ],
    acts: [],
    statements: [
      { event: unrelated, sequence: 1, actor: claude, kind: "request", text: "An old satisfied request", timestamp: 1786500000, body: { to: claude } },
      { event: artifact, sequence: 2, actor: claude, kind: "artifact", text: "Merge published the current artifact at ui", timestamp: 1786500100 },
      { event: propose, sequence: 3, actor: claude, kind: "propose", text: "Adopt the bounded status contract.", timestamp: 1786500200 },
    ],
    commitments: [{ request: unrelated, requester: claude, addressed_to: claude, status: "satisfied" }],
    reviews: [],
    artifacts: [],
    actors: { [claude]: { name: "claude", kind: "agent", roles: [] } },
    provenance: { [artifact]: [unrelated], [propose]: [artifact] },
  };
  return { projection, unrelated, artifact, propose, claude };
}

test("a proposal whose citation chain reaches an unrelated request opens as itself", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const { projection, unrelated, propose } = proposalProjection();
    const index = buildRecordIndex(projection);
    assert.notEqual(index.threadRoot(propose), unrelated, "the proposal is not filed under the request its cited artifact once answered");
    assert.equal(index.threadRoot(propose), propose, "a record belonging to no commitment opens as itself");
    // The commitment's own members still land on their request.
    assert.equal(index.threadRoot(unrelated), unrelated);
  } finally {
    await vite.close();
  }
});

test("a promise, a report and an act still open into the commitment they answer", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const request = "4444444444444444444444444444444444444444";
    const promise = "5555555555555555555555555555555555555555";
    const report = "6666666666666666666666666666666666666666";
    const ratify = "7777777777777777777777777777777777777777";
    const codex = "codexfingerprint0000000000000000";
    const projection = {
      decisions: [
        { event: request, sequence: 1, verdict: "effective", reason: "recorded" },
        { event: ratify, sequence: 4, verdict: "effective", reason: "recorded" },
      ],
      acts: [{ event: ratify, actor: codex, type: "ratify", target: promise, sequence: 4, verdict: "effective", reason: "recorded" }],
      statements: [
        { event: request, sequence: 1, actor: codex, kind: "request", text: "Do the thing", timestamp: 1786500000, body: { to: codex } },
        { event: promise, sequence: 2, actor: codex, kind: "promise", text: "Claimed", timestamp: 1786500100 },
        { event: report, sequence: 3, actor: codex, kind: "report", text: "Done", timestamp: 1786500200 },
      ],
      commitments: [{ request, requester: codex, addressed_to: codex, promise, report, status: "reported" }],
      reviews: [],
      artifacts: [],
      actors: { [codex]: { name: "codex", kind: "agent", roles: [] } },
      provenance: { [promise]: [request], [report]: [promise], [ratify]: [report] },
    };
    const index = buildRecordIndex(projection);
    assert.equal(index.threadRoot(promise), request, "the promise opens the thread of the request it answers");
    assert.equal(index.threadRoot(report), request, "so does the report");
    assert.equal(index.threadRoot(ratify), request, "an act opens where its target lives");
  } finally {
    await vite.close();
  }
});

test("a proposal opened directly is announced as a proposal and owes nobody a claim", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const { projection, propose } = proposalProjection();
    const markup = renderToStaticMarkup(
      React.createElement(Thread, {
        index: buildRecordIndex(projection),
        workroom: workroom(projection),
        session,
        frames: [],
        root: propose,
        pending: [],
        onBack() {},
        onOpenThread() {},
        onSay: () => "id",
        onSayFailed() {},
        doAct() {},
      }),
    );
    assert.match(markup, /Adopt the bounded status contract/, "the clicked record is identified on arrival");
    assert.match(markup, /data-station="root"[^<]/);
    assert.match(markup, /proposal/, "the rail says what the root is");
    assert.doesNotMatch(markup, /unclaimed/, "a proposal is not an unclaimed commitment");
  } finally {
    await vite.close();
  }
});

test("arrival opens and marks the record the user clicked without a further click", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const request = "8888888888888888888888888888888888888888";
    const note = "9999999999999999999999999999999999999999";
    const hugh = "hughfingerprint00000000000000000";
    const projection = {
      decisions: [
        { event: request, sequence: 1, verdict: "effective", reason: "recorded" },
        { event: note, sequence: 2, verdict: "effective", reason: "recorded" },
      ],
      acts: [],
      statements: [
        { event: request, sequence: 1, actor: hugh, kind: "request", text: "Do the thing", timestamp: 1786500000, body: { to: hugh } },
        { event: note, sequence: 2, actor: hugh, kind: "assert", text: "A note hiding behind Talk", timestamp: 1786500100 },
      ],
      commitments: [{ request, requester: hugh, addressed_to: hugh, status: "open" }],
      reviews: [],
      artifacts: [],
      actors: { [hugh]: { name: "hugh", kind: "person", roles: [] } },
      provenance: { [note]: [request] },
    };
    const markup = renderToStaticMarkup(
      React.createElement(Thread, {
        index: buildRecordIndex(projection),
        workroom: workroom(projection),
        session,
        frames: [],
        root: request,
        focus: note,
        pending: [],
        onBack() {},
        onOpenThread() {},
        onSay: () => "id",
        onSayFailed() {},
        doAct() {},
      }),
    );
    assert.match(markup, new RegExp(note), "the focused record's whole id is on the page on arrival");
    assert.match(markup, /aria-current="true"/, "the focused row is marked as the one the user asked for");
  } finally {
    await vite.close();
  }
});

// The grouping defect, driven on the records it was found on. See
// test/real-log.mjs: seven verbatim records from this workroom's own log,
// #1 through #11872.
//
// Reading the first basis as a reply is right inside one commitment and wrong
// across two. Review request #11869 opens by citing artifact #11867, which
// rests on proposal #6, which rests on the founding seed #1 — so the walk
// carried the request, its promise and its approval into the thread of "hugh
// begins the workroom", a record nothing in the log answers. On the whole log
// that was 3040 of 13800 records and 431 whole commitments filed under the
// seed; the approval landed under "Earlier rounds — superseded review
// verdicts", in a thread that has never had a review round.
//
// Both directions are asserted, because dropping the edge would be no better
// than inventing it if the records then belonged nowhere. They belong to their
// own commitment, and that is where the second half looks for them.
test("a commitment reached by citation is its own thread, not a descendant of what it cited", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { buildThreadIndex } = await vite.ssrLoadModule("/src/lib/threads.ts");
    const projection = realProjection();
    const index = buildThreadIndex(projection);

    const seeded = index.content(SEED).events;
    assert.deepEqual(
      seeded,
      [PROPOSAL, INTERFACE_NOTE, COMPANION_NOTE],
      "the seed's thread is the records that really reply to it: one proposal and the two artifacts resting on it",
    );
    assert.ok(seeded.length > 0, "and it is not empty, so the absences below are absences and not a walk that never ran");
    for (const [name, event] of [["request", REVIEW_REQUEST], ["promise", REVIEW_PROMISE], ["approval", REVIEW_APPROVAL]]) {
      assert.ok(!seeded.includes(event), `the review ${name} is not filed under the founding seed`);
    }

    assert.deepEqual(
      index.content(REVIEW_REQUEST).events,
      [REVIEW_PROMISE, REVIEW_APPROVAL],
      "and the three records are whole in the commitment that owns them, rather than dropped",
    );
  } finally {
    await vite.close();
  }
});

// The same fact on the screen, because the walk feeds the thread and the
// thread is what a reader sees. Opening the seed with the approval as the
// clicked record used to open the expander holding it and print it; now there
// is no expander holding it, and the arrival is quiet about a record that was
// never in this thread.
//
// The positive half is in the same markup rather than in a second test: the
// seed's own row and the two artifacts it really holds have to be on the page
// before "the approval is not on the page" says anything at all.
test("the founding seed's thread renders its own records and not another commitment's", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const projection = realProjection();
    const markup = renderToStaticMarkup(
      React.createElement(Thread, {
        index: buildRecordIndex(projection),
        workroom: workroom(projection),
        session,
        frames: [],
        root: SEED,
        focus: REVIEW_APPROVAL,
        pending: [],
        onBack() {},
        onOpenThread() {},
        onSay: () => "id",
        onSayFailed() {},
        doAct() {},
      }),
    );

    assert.match(markup, /hugh begins the workroom/, "the thread rendered: its root station names the seed");
    assert.match(markup, /Repair chain/, "and rendered the two retired artifacts that really descend from it");
    assert.match(markup, /Proposals/, "and the proposal that really rests on it");

    assert.doesNotMatch(markup, new RegExp(REVIEW_APPROVAL), "the approval's id is nowhere on the seed's thread");
    assert.doesNotMatch(markup, /APPROVED at exact head/, "nor its text, under any expander");
    assert.doesNotMatch(markup, /Ox-checker: independently review/, "nor the request that opened that commitment");
  } finally {
    await vite.close();
  }
});
