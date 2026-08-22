import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const HUGH = "7fbc80f1aaaa0000";
const CODEX = "5f12e916bbbb0000";

// The real vocabulary's shape for the two kinds that matter here: both are
// satisfied by the ratifier role, and only one of them is inert until it gets
// that satisfaction.
const VOCABULARY = {
  definitions: [
    { name: "propose", satisfier: "role:ratifier", render: "proposal" },
    { name: "assert", satisfier: "role:ratifier", render: "note" },
    { name: "roster", satisfier: "role:ratifier", render: "governance" },
    { name: "request", satisfier: "originating-requester", render: "commitment" },
  ],
};

function statement(event, kind, extra = {}) {
  return { event, sequence: 1, timestamp: 1, actor: CODEX, kind, text: `${kind} ${event}`, ...extra };
}

function projectionOf(statements, actors) {
  return {
    decisions: statements.map((s) => ({ event: s.event, sequence: 1, verdict: "effective" })),
    acts: [],
    statements,
    commitments: [],
    artifacts: [],
    actors,
    provenance: {},
  };
}

const CONTEXT = { nameOf: (f) => (f === HUGH ? "hugh" : "codex"), tickets: new Map(), actors: {} };

async function rowsModule() {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const module = await vite.ssrLoadModule("/src/lib/rows.ts");
  return [module, () => vite.close()];
}

test("an unratified act that needs no ratification is not counted, and one that needs it is", async () => {
  const [{ ratificationRows }, close] = await rowsModule();
  try {
    const actors = { [HUGH]: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } };
    const projection = projectionOf(
      [statement("a-note", "assert"), statement("a-proposal", "propose"), statement("a-governance", "roster")],
      actors,
    );
    const view = ratificationRows(projection, VOCABULARY, { ...CONTEXT, actors });

    // The whole point of reading `render` rather than `satisfier` alone: an
    // assert names the ratifier role too, and counting it would bury the queue.
    assert.deepEqual(view.rows.map((row) => row.event).sort(), ["a-governance", "a-proposal"]);
    assert.equal(view.rows[0].waitsOnName, "hugh");
  } finally {
    await close();
  }
});

test("a proposal belonging to no commitment and no thread still appears", async () => {
  const [{ ratificationRows }, close] = await rowsModule();
  try {
    const actors = { [HUGH]: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } };
    // No commitment references it, and `commitments` is empty: a count summed
    // over threads would miss this row entirely, which is the undercount this
    // change exists to remove.
    const projection = projectionOf([statement("orphan", "propose")], actors);
    assert.deepEqual(ratificationRows(projection, VOCABULARY, { ...CONTEXT, actors }).rows.map((r) => r.event), ["orphan"]);
  } finally {
    await close();
  }
});

test("a ratified or retired act is no longer owed", async () => {
  const [{ ratificationRows }, close] = await rowsModule();
  try {
    const actors = { [HUGH]: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } };
    const projection = projectionOf(
      [
        statement("done", "propose", { ratified: true }),
        statement("withdrawn", "propose", { retired: true }),
        statement("open", "propose"),
      ],
      actors,
    );
    assert.deepEqual(ratificationRows(projection, VOCABULARY, { ...CONTEXT, actors }).rows.map((r) => r.event), ["open"]);
  } finally {
    await close();
  }
});

test("an act the fold refused is not owed to anybody", async () => {
  const [{ ratificationRows }, close] = await rowsModule();
  try {
    const actors = { [HUGH]: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } };
    const projection = projectionOf([statement("refused", "propose"), statement("open", "propose")], actors);
    projection.decisions = projection.decisions.map((decision) =>
      decision.event === "refused" ? { ...decision, verdict: "ineffective" } : decision,
    );
    assert.deepEqual(ratificationRows(projection, VOCABULARY, { ...CONTEXT, actors }).rows.map((r) => r.event), ["open"]);
  } finally {
    await close();
  }
});

test("with no ratifier or several, the queue names nobody rather than guessing", async () => {
  const [{ ratificationRows }, close] = await rowsModule();
  try {
    const none = {};
    const empty = ratificationRows(projectionOf([statement("open", "propose")], none), VOCABULARY, { ...CONTEXT, actors: none });
    assert.equal(empty.rows.length, 1, "the obligation exists whether or not anybody can discharge it");
    assert.equal(empty.ratifiers.length, 0);
    assert.equal(empty.rows[0].waitsOn, "");

    const both = {
      [HUGH]: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      [CODEX]: { name: "codex", kind: "agent", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    };
    const shared = ratificationRows(projectionOf([statement("open", "propose")], both), VOCABULARY, { ...CONTEXT, actors: both });
    assert.equal(shared.ratifiers.length, 2);
    assert.equal(shared.rows[0].waitsOn, "", "two candidates do not determine a next mover");
  } finally {
    await close();
  }
});

test("the list shows the ratification queue as its own count, and says when nobody can discharge it", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    const render = (actors) => {
      const projection = projectionOf([statement("open", "propose"), statement("noise", "assert")], actors);
      return renderToStaticMarkup(
        React.createElement(RequestList, {
          workroom: {
            actors: [],
            commits: [],
            graphTruncated: false,
            offline: false,
            localOffline: false,
            status: {
              durable: { genesis: "g", head: "h", depth: 1, projection, vocabulary: VOCABULARY },
              live: { cursor: { generation: "g", position: 1 }, presence: {}, activity: {}, conversations: [] },
              cursor: { frontier: [], live: { generation: "g", position: 1 } },
            },
          },
          onOpenThread() {},
          // Rendered on the ratification tab, because the undischargeable
          // note belongs to that population and appears only there. The
          // count is read off the tab, which is where it now lives.
          view: { query: "", population: "ratification" },
          onView() {},
        }),
      );
    };

    const withRatifier = render({ [HUGH]: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } });
    assert.match(withRatifier, /awaiting ratification<span[^>]*>1<\/span>/, "counted on its own tab, not added to the open requests");
    assert.match(withRatifier, /1 act awaits ratification/, "and the tab it selects heads the list with that count");
    assert.doesNotMatch(withRatifier, /holds `ratifier`/);

    // An empty queue and an undischargeable one look identical on screen
    // unless the screen says which it is.
    const without = render({});
    assert.match(without, /1 act awaits ratification/);
  } finally {
    await vite.close();
  }
});
