import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

function workroom(presence, suppliedProjection) {
  const projection = suppliedProjection ?? {
    decisions: [],
    acts: [],
    statements: [],
    commitments: [],
    artifacts: [],
    actors: {},
    provenance: {},
  };
  return {
    actors: [
      { name: "claude", fingerprint: "a5d35aa7e4799472", roles: [], custody: true },
      { name: "claude", fingerprint: "0011223344ff5566", roles: [], custody: true },
    ],
    commits: [],
    graphTruncated: false,
    offline: false,
    localOffline: false,
    status: {
      durable: { genesis: "genesis", head: "head", depth: 1, projection },
      live: { cursor: { generation: "generation", position: 1 }, presence, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

const session = { id: "browser", live: true, setActor() {} };

test("an addressed proposal-ratification request offers the requested decision directly", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const { semanticActions } = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
    const viewer = "hugh-fingerprint";
    const proposal = {
      event: "proposal",
      actor: "codex-fingerprint",
      kind: "propose",
      text: "Use the bounded status contract.",
      timestamp: 1,
    };
    const request = {
      event: "request",
      actor: "codex-fingerprint",
      kind: "request",
      text: "Hugh: ratify the proposal or deny it.",
      body: { to: viewer },
      timestamp: 2,
    };
    const projection = {
      decisions: [proposal, request].map(({ event }) => ({ event, verdict: "effective", reason: "recorded" })),
      acts: [],
      statements: [proposal, request],
      commitments: [{ request: request.event, requester: request.actor, addressed_to: viewer, status: "open" }],
      artifacts: [],
      actors: {},
      provenance: { proposal: [], request: [proposal.event] },
    };
    const routed = [];
    const acted = [];
    const actions = semanticActions({
      statement: request,
      commitment: projection.commitments[0],
      decision: projection.decisions[1],
      projection,
      me: viewer,
      onRoute: (...args) => routed.push(args),
      doAct: (...args) => acted.push(args),
    });

    assert.deepEqual(actions.map(({ label }) => label), ["ratify yes", "deny"]);
    assert.doesNotMatch(actions.map(({ label }) => label).join(" "), /accept/);
    actions[0].run();
    actions[1].run();
    assert.deepEqual(acted, [["ratify:proposal", { act: "ratify", target: proposal.event }]]);
    assert.deepEqual(routed, [["dissent", proposal.event, ""]]);

    const ordinary = semanticActions({
      statement: { ...request, event: "ordinary-request", text: "Implement the feature." },
      commitment: { ...projection.commitments[0], request: "ordinary-request" },
      decision: { event: "ordinary-request", verdict: "effective", reason: "recorded" },
      projection: {
        ...projection,
        statements: [{ ...request, event: "ordinary-request", text: "Implement the feature." }],
        provenance: { "ordinary-request": [] },
      },
      me: viewer,
      onRoute() {},
      doAct() {},
    });
    assert.deepEqual(ordinary.map(({ label }) => label), ["accept"]);
  } finally {
    await vite.close();
  }
});



// The reason an act carries no force belongs beside that act. This is the
// regression that matters: the previous surface counted unreadable acts in a
// panel and a header badge while the explanation lived only in a hover title,
// so the room looked broken and no reader could learn why.








// One line, fixed columns, no wrapping at 1,024 pixels. A static render is
// where the row's shape is decided: five cells, each of them clipped rather
// than allowed to grow the row to two lines.
test("every row is one line of exactly five fixed columns", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    const statements = [
      { event: "e1", sequence: 1, actor: "hugh", kind: "request", text: "A request whose first line is the title\nand whose second line is not", timestamp: 1786500000 },
    ];
    const projection = {
      decisions: [{ event: "e1", sequence: 1, verdict: "effective", reason: "recorded" }],
      acts: [], statements,
      commitments: [{ request: "e1", requester: "hugh", addressed_to: "codex", status: "open" }],
      reviews: [], artifacts: [],
      actors: { codex: { name: "codex", kind: "agent", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } },
      provenance: {},
    };
    const markup = renderToStaticMarkup(
      React.createElement(RequestList, {
        workroom: {
          actors: [{ name: "codex", fingerprint: "codex", roles: [], custody: true }],
          offline: false,
          status: {
            durable: { genesis: "genesis", head: "head", depth: 1, projection },
            live: { cursor: { generation: "g", position: 1 }, presence: {}, activity: {}, conversations: [] },
            cursor: { frontier: [], live: { generation: "g", position: 1 } },
          },
        },
        onOpenThread() {},
      }),
    );

    assert.match(markup, /table-fixed/);
    const row = markup.slice(markup.indexOf("<tbody>"), markup.indexOf("</tbody>"));
    assert.equal((row.match(/<td/g) ?? []).length, 5, "a row is five columns and no more");
    assert.equal((row.match(/truncate/g) ?? []).length, 5, "every cell clips rather than wrapping");
    // The title is the request's first line only.
    assert.match(row, /A request whose first line is the title/);
    assert.doesNotMatch(row, /whose second line is not/);
    // No author, no topic, no worktree pill, no ratified badge, no per-row staleness.
    assert.doesNotMatch(row, /hugh|stale|worktree|topic/i);
    // Exactly one headline number, and it equals the rows below it.
    assert.match(markup, /1 open request<\/h2>/);
  } finally {
    await vite.close();
  }
});
