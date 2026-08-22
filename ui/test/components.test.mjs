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

const session = { credential: "browser", live: true, setActor() {} };

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
        view: { query: "", population: "live" },
        onView() {},
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

// Withdrawing a ratification must bring the action back. The toolbar used to
// ask whether the viewer had ever ratified, which is a different question:
// once withdrawn, the act is no longer in force, and a lawful re-ratification
// was hidden forever. What is in force is `ratified_by`, and only the fold can
// say which act that is.
test("a withdrawn ratification offers the decision again", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const { semanticActions } = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
    const viewer = "hugh-fingerprint";
    const proposal = { event: "proposal", actor: "codex-fingerprint", kind: "propose", text: "Use it.", timestamp: 1 };
    const request = {
      event: "request", actor: "codex-fingerprint", kind: "request",
      text: "Hugh: ratify the proposal or deny it.", body: { to: viewer }, timestamp: 2,
    };
    // The viewer ratified, then withdrew it. ratified_by is what the fold says
    // stands now; the ratify act itself is still in the projection, still
    // marked effective, which is exactly why searching the acts cannot answer.
    const build = (ratifiedBy) => ({
      decisions: [
        { event: "proposal", verdict: "effective", reason: "recorded" },
        { event: "request", verdict: "effective", reason: "recorded" },
      ],
      acts: [
        { event: "ratify-a", actor: viewer, type: "ratify", target: "proposal", verdict: "effective", reason: "recorded" },
        { event: "withdraw-a", actor: viewer, type: "supersede", target: "ratify-a", verdict: "effective", reason: "recorded" },
      ],
      statements: [{ ...proposal, ratified: ratifiedBy !== undefined, ratified_by: ratifiedBy }, request],
      commitments: [{ request: "request", requester: request.actor, addressed_to: viewer, status: "open" }],
      artifacts: [],
      actors: {},
      provenance: { proposal: [], request: ["proposal"] },
    });

    const withdrawn = build(undefined);
    const afterWithdrawal = semanticActions({
      statement: request, commitment: withdrawn.commitments[0], decision: withdrawn.decisions[1],
      projection: withdrawn, me: viewer, onRoute() {}, doAct() {},
    });
    assert.deepEqual(
      afterWithdrawal.map(({ label }) => label),
      ["ratify yes", "deny"],
      "the ratification was withdrawn, so ratifying again is offered",
    );

    const standing = build("ratify-a");
    const whileStanding = semanticActions({
      statement: request, commitment: standing.commitments[0], decision: standing.decisions[1],
      projection: standing, me: viewer, onRoute() {}, doAct() {},
    });
    assert.deepEqual(
      whileStanding.map(({ label }) => label),
      ["deny"],
      "while the viewer's ratification stands, ratifying again is not offered",
    );
// The thread row abbreviates; the detail under it does not. Every fact the
// projection holds about the record is shown whole, and each basis is a link.
test("a record's detail shows full ids, body fields and both directions of provenance", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { RecordDetail } = await vite.ssrLoadModule("/src/components/RecordDetail.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const request = "1111111111111111111111111111111111111111";
    const promise = "2222222222222222222222222222222222222222";
    const report = "3333333333333333333333333333333333333333";
    const codex = "codexfingerprint0000000000000000";
    const hugh = "hughfingerprint00000000000000000";
    const projection = {
      decisions: [{ event: promise, sequence: 2, verdict: "effective", reason: "recorded" }],
      acts: [],
      statements: [
        { event: request, sequence: 1, actor: hugh, kind: "request", text: "Do the thing\nwith a second line", timestamp: 1786500000, body: { to: codex } },
        { event: promise, sequence: 2, actor: codex, kind: "promise", text: "Claimed", timestamp: 1786500100, body: { head: "abcdef0123456789abcdef0123456789abcdef01" }, ratified: true },
        { event: report, sequence: 3, actor: codex, kind: "report", text: "Done", timestamp: 1786500200 },
      ],
      commitments: [],
      reviews: [],
      artifacts: [],
      actors: { [codex]: { name: "codex", kind: "agent", roles: [] }, [hugh]: { name: "hugh", kind: "person", roles: [] } },
      provenance: { [promise]: [request], [report]: [promise] },
    };
    const opened = [];
    const markup = renderToStaticMarkup(
      React.createElement(RecordDetail, {
        event: promise,
        index: buildRecordIndex(projection),
        actors: projection.actors,
        tickets: new Map([[request, 1], [promise, 2], [report, 3]]),
        nameOf: (fingerprint) => projection.actors[fingerprint]?.name ?? fingerprint,
        onOpenThread: (event) => opened.push(event),
      }),
    );
    // The whole id, not a prefix; the actor by name and by whole fingerprint.
    assert.match(markup, new RegExp(promise));
    assert.match(markup, new RegExp(`codex.*${codex}`));
    // Every body field, unabbreviated.
    assert.match(markup, /abcdef0123456789abcdef0123456789abcdef01/);
    // Both directions of provenance, each naming the record it points at.
    const restsOn = markup.slice(markup.indexOf("rests on"), markup.indexOf("rested on by"));
    assert.match(restsOn, new RegExp(`#1.*Do the thing.*${request}`));
    assert.doesNotMatch(restsOn, /second line/);
    const restedOnBy = markup.slice(markup.indexOf("rested on by"));
    assert.match(restedOnBy, new RegExp(`#3.*Done.*${report}`));
    // The fold's ruling and the record's flags.
    assert.match(markup, /effective/);
    assert.match(markup, /ratified/);
  } finally {
    await vite.close();
  }
});

// A row is closed until clicked. The rail stays one line of fixed columns,
// and no detail is drawn for a station that names neither a record nor a commit.
test("thread rows start closed and only rows naming something are expandable", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
    const request = "1111111111111111111111111111111111111111";
    const projection = {
      decisions: [{ event: request, sequence: 1, verdict: "effective", reason: "recorded" }],
      acts: [],
      statements: [{ event: request, sequence: 1, actor: "hugh", kind: "request", text: "Do the thing", timestamp: 1786500000, body: { to: "codex" } }],
      commitments: [{ request, requester: "hugh", addressed_to: "codex", status: "open" }],
      reviews: [],
      artifacts: [],
      actors: { codex: { name: "codex", kind: "agent", roles: [] }, hugh: { name: "hugh", kind: "person", roles: [] } },
      provenance: {},
    };
    const markup = renderToStaticMarkup(
      React.createElement(Thread, {
        workroom: workroom({}, projection),
        session,
        frames: [],
        root: request,
        pending: [],
        onBack() {},
        onOpenThread() {},
        onSay: () => "id",
        onSayFailed() {},
        doAct() {},
      }),
    );
    const rows = markup.split(/(?=<li[^>]*data-station)/).filter((part) => part.includes("data-station"));
    assert.equal(rows.length, 2, "request and unclaimed promise");
    assert.match(rows[0], /data-station="request"/);
    assert.match(rows[0], /<button[^>]*aria-expanded="false"[^>]*aria-controls="detail-request"/, "the request row's text is a closed disclosure button");
    assert.doesNotMatch(rows[1], /aria-expanded/, "an unreached station has nothing to open");
    assert.doesNotMatch(markup, /data-record-detail/, "nothing is open until clicked");
  } finally {
    await vite.close();
  }
});

// The list shows the population its caller holds. Owning the view outside
// the list is what lets it survive a trip into a thread and back.
test("the request list renders the population and sort its caller holds", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    const projection = {
      decisions: [],
      acts: [],
      statements: [
        { event: "open", sequence: 1, actor: "hugh", kind: "request", text: "Still open", timestamp: 1786500000 },
        { event: "gone", sequence: 2, actor: "hugh", kind: "request", text: "Went stale", timestamp: 1786500000 },
      ],
      commitments: [
        { request: "open", requester: "hugh", addressed_to: "codex", status: "open" },
        { request: "gone", requester: "hugh", addressed_to: "codex", status: "stale" },
      ],
      reviews: [],
      artifacts: [],
      actors: { codex: { name: "codex", kind: "agent", roles: [] } },
      provenance: {},
    };
    const render = (view) =>
      renderToStaticMarkup(
        React.createElement(RequestList, {
          workroom: workroom({}, projection),
          onOpenThread() {},
          view,
          onView() {},
        }),
      );
    const stale = render({ query: "", population: "stale" });
    assert.match(stale, /1 stale request, not in flight<\/h2>/);
    assert.match(stale, /Went stale/);
    assert.doesNotMatch(stale.slice(stale.indexOf("<tbody>")), /Still open/);
    const live = render({ query: "", population: "live" });
    assert.match(live, /1 open request<\/h2>/);
  } finally {
    await vite.close();
  }
});
