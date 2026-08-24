import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

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
