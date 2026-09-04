import test from "node:test";
import assert from "node:assert/strict";

import { buildOutcomeMap, DEFAULT_OUTCOME_MAP_LIMITS } from "../src/lib/outcomeMap.ts";
import { fitOutcomeScale, OUTCOME_CARD, OUTCOME_SCALE, layoutOutcomeMap, outcomeEdgePath } from "../src/lib/outcomeMapLayout.ts";
import { buildThreadIndex } from "../src/lib/threads.ts";

const statement = (event, sequence, kind = "request", extra = {}) => ({
  event,
  sequence,
  actor: "actor",
  kind,
  text: event,
  ...extra,
});

const commitment = (request, extra = {}) => ({
  request,
  requester: "actor",
  status: "open",
  ...extra,
});

function projection({ statements = [], acts = [], commitments = [], provenance = {} } = {}) {
  const events = [...statements, ...acts];
  return {
    statements,
    acts,
    commitments,
    provenance,
    decisions: events.map((event, index) => ({
      event: event.event,
      sequence: event.sequence ?? index + 1,
      verdict: "effective",
      reason: "fixture",
    })),
    reviews: [],
    artifacts: [],
    actors: {},
  };
}

test("thread roots reuse the commitment-boundary reply rule", () => {
  const value = projection({
    statements: [
      statement("request-a", 1),
      statement("promise-a", 2, "promise"),
      statement("artifact-a", 3, "artifact"),
      statement("request-b", 4),
    ],
    commitments: [
      commitment("request-a", { promise: "promise-a", status: "promised" }),
      commitment("request-b"),
    ],
    provenance: {
      "promise-a": ["request-a"],
      "artifact-a": ["promise-a"],
      // This is a real citation but not conversational containment: request B
      // belongs to a different commitment.
      "request-b": ["artifact-a"],
    },
  });
  const threads = buildThreadIndex(value);
  assert.equal(threads.root("promise-a"), "request-a");
  assert.equal(threads.root("artifact-a"), "request-a");
  assert.equal(threads.root("request-b"), "request-b");
});

test("a non-commitment record with an absent first basis remains its own thread root", () => {
  const value = projection({
    statements: [statement("unanchored-assert", 1, "assert")],
    provenance: { "unanchored-assert": ["missing-basis"] },
  });

  assert.equal(buildThreadIndex(value).root("unanchored-assert"), "unanchored-assert");
});

test("a focal thread pulls one-hop context and preserves the exact direct relation", () => {
  const value = projection({
    statements: [
      statement("request-a", 1),
      statement("promise-a", 2, "promise"),
      statement("artifact-a", 3, "artifact"),
      statement("request-b", 4, "request", { body: { conditions: "depends on imaginary-task" } }),
    ],
    commitments: [commitment("request-a", { promise: "promise-a" }), commitment("request-b")],
    provenance: {
      "promise-a": ["request-a"],
      "artifact-a": ["promise-a"],
      "request-b": ["artifact-a"],
    },
  });
  const graph = buildOutcomeMap(value, ["request-b"]);

  assert.deepEqual(graph.nodes.map(({ thread, context, layer }) => ({ thread, context, layer })), [
    { thread: "request-a", context: true, layer: 0 },
    { thread: "request-b", context: false, layer: 1 },
  ]);
  assert.equal(graph.stats.focalNodes, 1);
  assert.equal(graph.stats.contextNodes, 1);
  assert.equal(graph.relations.length, 1);
  assert.deepEqual(graph.relations[0], {
    id: "rests-on:request-a->request-b",
    family: "rests-on",
    source: "request-a",
    target: "request-b",
    contributors: [{
      kind: "provenance",
      family: "rests-on",
      dependent: "request-b",
      basis: "artifact-a",
      ratification: undefined,
      forward: "artifact-a is a direct basis for request-b.",
      reverse: "request-b rests on artifact-a.",
    }],
  });
  assert.equal(graph.nodes.find((node) => node.thread === "request-b").basisOutsideView, false);
  assert.equal(graph.relations.some((relation) => relation.id.includes("imaginary-task")), false);
});

test("ratification uses only a direct basis and the fold-projected ratified_by act", () => {
  const value = projection({
    statements: [
      statement("proposal", 1, "propose", { ratified: true, ratified_by: "ratify-act" }),
      statement("request-one", 3),
      statement("request-two", 4),
    ],
    acts: [{
      event: "ratify-act",
      sequence: 2,
      actor: "ratifier",
      type: "ratify",
      target: "proposal",
      verdict: "effective",
      reason: "fixture",
    }],
    commitments: [commitment("request-one"), commitment("request-two")],
    provenance: {
      "ratify-act": ["proposal"],
      "request-one": ["proposal"],
      "request-two": ["ratify-act"],
    },
  });
  const graph = buildOutcomeMap(value, ["request-one", "request-two"]);
  assert.deepEqual(graph.relations.map((relation) => [relation.family, relation.source, relation.target]), [
    ["ratified-by", "proposal", "request-one"],
    ["ratified-by", "proposal", "request-two"],
  ]);
  assert.deepEqual(graph.relations.map((relation) => relation.contributors[0].ratification), ["ratify-act", "ratify-act"]);
});

test("a three-thread provenance chain preserves only its two recorded direct relations", () => {
  const value = projection({
    statements: [statement("first", 1), statement("second", 2), statement("third", 3)],
    commitments: [commitment("first"), commitment("second"), commitment("third")],
    provenance: { second: ["first"], third: ["second"] },
  });

  const graph = buildOutcomeMap(value, ["first", "second", "third"]);
  assert.deepEqual(graph.relations.map((relation) => [relation.source, relation.target]), [
    ["first", "second"],
    ["second", "third"],
  ]);
  assert.equal(graph.relations.some((relation) => relation.source === "first" && relation.target === "third"), false);
});

test("parallel direct relations retain every exact contributor and both readings", () => {
  const value = projection({
    statements: [
      statement("basis", 1),
      statement("basis-detail", 2, "assert"),
      statement("dependent", 3),
    ],
    commitments: [commitment("basis"), commitment("dependent")],
    provenance: {
      "basis-detail": ["basis"],
      dependent: ["basis", "basis-detail"],
    },
  });

  const graph = buildOutcomeMap(value, ["basis", "dependent"]);
  assert.equal(graph.relations.length, 1);
  assert.deepEqual(
    graph.relations[0].contributors.map(({ basis, dependent, forward, reverse }) => ({ basis, dependent, forward, reverse })),
    [
      {
        basis: "basis",
        dependent: "dependent",
        forward: "basis is a direct basis for dependent.",
        reverse: "dependent rests on basis.",
      },
      {
        basis: "basis-detail",
        dependent: "dependent",
        forward: "basis-detail is a direct basis for dependent.",
        reverse: "dependent rests on basis-detail.",
      },
    ],
  );
});

test("supersession names the exact act and projected successor without guessing a replacement", () => {
  const actProjection = projection({
    statements: [statement("old-request", 1), statement("new-request", 2)],
    acts: [{
      event: "supersede-act",
      sequence: 3,
      actor: "actor",
      type: "supersede",
      target: "old-request",
      verdict: "effective",
      reason: "fixture",
    }],
    commitments: [commitment("old-request"), commitment("new-request")],
    provenance: { "supersede-act": ["new-request"] },
  });
  const actGraph = buildOutcomeMap(actProjection, ["old-request", "new-request"]);
  assert.deepEqual(actGraph.relations.map((relation) => [relation.family, relation.source, relation.target]), [
    ["superseded", "new-request", "old-request"],
  ]);
  assert.equal(actGraph.relations[0].contributors[0].act, "supersede-act");
  assert.equal(actGraph.relations[0].contributors[0].target, "old-request");

  const successorProjection = projection({
    statements: [statement("old-request", 1), statement("new-request", 2)],
    commitments: [commitment("old-request", { successor_request: "new-request", status: "superseded" }), commitment("new-request")],
  });
  const successorGraph = buildOutcomeMap(successorProjection, ["old-request", "new-request"]);
  assert.deepEqual(successorGraph.relations.map((relation) => [relation.family, relation.source, relation.target]), [
    ["superseded", "old-request", "new-request"],
  ]);
  assert.equal(successorGraph.relations[0].contributors[0].kind, "successor-request");
});

test("serialization order cannot change nodes, relations, or placement", () => {
  const statements = [statement("a", 1), statement("b", 2), statement("c", 3)];
  const commitments = [commitment("a"), commitment("b"), commitment("c")];
  const left = projection({ statements, commitments, provenance: { b: ["a"], c: ["a"] } });
  const right = projection({
    statements: [...statements].reverse(),
    commitments: [...commitments].reverse(),
    provenance: { c: ["a"], b: ["a"] },
  });
  assert.deepEqual(buildOutcomeMap(left, ["c", "b"]), buildOutcomeMap(right, ["b", "c"]));
});

test("one deterministic lifecycle row supplies a focal card's title, state, and waiting actor", () => {
  const statements = [
    statement("request", 1),
    statement("promise-old", 2, "promise"),
    statement("report-new", 3, "report"),
  ];
  const older = commitment("request", { promise: "promise-old", status: "promised", waiting_on: "codex" });
  const newer = commitment("request", { report: "report-new", status: "reported", waiting_on: "hugh" });
  const olderRow = { event: "request", key: "promise-old", title: "Older lifecycle", state: "in progress", waitsOn: "codex" };
  const newerRow = { event: "request", key: "report-new", title: "Newer lifecycle", state: "reported", waitsOn: "hugh" };

  const forward = buildOutcomeMap(
    projection({ statements, commitments: [older, newer] }),
    [newerRow, olderRow],
  );
  const reversed = buildOutcomeMap(
    projection({ statements: [...statements].reverse(), commitments: [newer, older] }),
    [olderRow, newerRow],
  );

  assert.deepEqual(forward, reversed, "serialization order selected a different lifecycle row");
  assert.deepEqual(
    (({ title, state, waitsOn }) => ({ title, state, waitsOn }))(forward.nodes[0]),
    { title: "Newer lifecycle", state: "reported", waitsOn: "hugh" },
    "the focal card mixed fields from different projected lifecycle rows",
  );
});

test("the presentation layout keeps multiple roots leftmost and every arrow on card boundaries", () => {
  const layout = layoutOutcomeMap([
    { thread: "second-root", focal: true, context: false, rootOfView: true, recordedBasis: false, basisVisible: false, basisOutsideView: false, layer: 0, order: 2 },
    { thread: "first-root", focal: true, context: false, rootOfView: true, recordedBasis: false, basisVisible: false, basisOutsideView: false, layer: 0, order: 1 },
    { thread: "dependent", focal: true, context: false, rootOfView: false, recordedBasis: true, basisVisible: true, basisOutsideView: false, layer: 1, order: 0 },
  ]);
  const first = layout.positions.get("first-root");
  const second = layout.positions.get("second-root");
  const dependent = layout.positions.get("dependent");
  assert.equal(first.x, second.x, "multiple roots do not share the leftmost layer");
  assert.ok(first.y < second.y, "root order is not stable and vertical");
  assert.ok(first.x < dependent.x, "dependents are not to the right of their bases");
  const path = outcomeEdgePath(first, dependent);
  assert.match(path, new RegExp(`M ${first.x + OUTCOME_CARD.width} `), "arrow does not leave its source card edge");
  assert.match(path, new RegExp(`, ${dependent.x} ${dependent.y + OUTCOME_CARD.height / 2}$`), "arrow does not end at its target card edge");
});

test("fit uses the real largest legal layouts and nonzero viewport dimensions", () => {
  const node = (thread, layer, order) => ({
    thread,
    context: false,
    title: thread,
    kind: "request",
    state: "unclaimed",
    rootOfView: layer === 0,
    recordedBasis: layer > 0,
    basisOutsideView: false,
    layer,
    order,
  });
  const maximumColumn = layoutOutcomeMap(
    Array.from({ length: DEFAULT_OUTCOME_MAP_LIMITS.nodes }, (_, index) => node(`column-${index}`, 0, index)),
  );
  const maximumLayers = layoutOutcomeMap(
    Array.from({ length: DEFAULT_OUTCOME_MAP_LIMITS.nodes }, (_, index) => node(`layer-${index}`, index, 0)),
  );
  const viewport = { width: 800, height: 512 };

  for (const layout of [maximumColumn, maximumLayers]) {
    const scale = fitOutcomeScale(layout, viewport.width, viewport.height);
    assert.ok(scale > 0 && scale < 0.35, "the old fixed minimum still controls Fit to view");
    assert.ok(layout.width * scale <= viewport.width - OUTCOME_SCALE.inset + 1e-9);
    assert.ok(layout.height * scale <= viewport.height - OUTCOME_SCALE.inset + 1e-9);
  }
});

test("cycles remain visible, share a stable layer, and produce a bounded warning", () => {
  const value = projection({
    statements: [statement("a", 1), statement("b", 2)],
    commitments: [commitment("a"), commitment("b")],
    provenance: { a: ["b"], b: ["a"] },
  });
  const graph = buildOutcomeMap(value, ["a", "b"]);
  assert.equal(graph.relations.length, 2);
  assert.equal(graph.nodes[0].layer, graph.nodes[1].layer);
  assert.equal(graph.nodes.every((node) => node.rootOfView === false), true);
  assert.equal(graph.warnings.some((warning) => warning.kind === "cycle" && warning.events.includes("a") && warning.events.includes("b")), true);
});

test("a missing recorded basis is truthful context outside the view, not a broken line", () => {
  const value = projection({
    statements: [statement("request", 1)],
    commitments: [commitment("request")],
    provenance: { request: ["missing-event"] },
  });
  const graph = buildOutcomeMap(value, ["request"]);
  assert.equal(graph.relations.length, 0);
  assert.equal(graph.nodes[0].recordedBasis, true);
  assert.equal(graph.nodes[0].basisOutsideView, true);
  assert.match(graph.warnings[0].message, /absent from the projection/);
});

test("the default preserves measured-size direct context and bounds whole cards and complete edges atomically", () => {
  // The review measurement found 27 default focal threads with roughly 70
  // direct-context threads. The default allowance preserves that whole view.
  assert.deepEqual(DEFAULT_OUTCOME_MAP_LIMITS, {
    nodes: 160,
    contextNodes: 96,
    edges: 160,
    contributorsPerEdge: 64,
    warnings: 20,
  });
  const contextCount = 70;
  const contexts = Array.from({ length: contextCount }, (_, index) => statement(`context-${String(index).padStart(3, "0")}`, index + 1));
  const focal = statement("focal", contextCount + 1);
  const value = projection({
    statements: [...contexts, focal],
    commitments: [...contexts.map(({ event }) => commitment(event)), commitment("focal")],
    provenance: { focal: contexts.map(({ event }) => event) },
  });
  const defaultGraph = buildOutcomeMap(value, ["focal"]);
  assert.equal(defaultGraph.stats.contextNodes, contextCount);
  assert.equal(defaultGraph.warnings.some((warning) => warning.kind === "bounded"), false);

  const graph = buildOutcomeMap(value, ["focal"], { contextNodes: 12, edges: 8 });
  assert.equal(graph.stats.focalNodes, 1);
  assert.equal(graph.stats.contextNodes, 8);
  assert.equal(graph.stats.edges, 8);
  assert.equal(graph.stats.omittedContextNodes, 62);
  assert.equal(graph.nodes.filter((node) => node.context).every((node) => graph.relations.some((edge) => edge.source === node.thread || edge.target === node.thread)), true);
  assert.equal(graph.warnings.some((warning) => warning.kind === "bounded"), true);
});

test("the total-card bound caps a large focal population", () => {
  const focalCount = 200;
  const statements = Array.from({ length: focalCount }, (_, index) => statement(`thread-${String(index).padStart(3, "0")}`, index + 1));
  const value = projection({
    statements,
    commitments: statements.map(({ event }) => commitment(event)),
  });

  const graph = buildOutcomeMap(value, statements.map(({ event }) => event));
  assert.equal(graph.nodes.length, 160);
  assert.equal(graph.stats.focalNodes, 160);
  assert.deepEqual(graph.nodes.map((node) => node.thread), statements.slice(0, 160).map(({ event }) => event));
  assert.equal(graph.warnings.some((warning) => warning.kind === "bounded" && /Selected board threads/.test(warning.message)), true);
});

test("direct context uses only the total-card budget left after focal threads", () => {
  const contexts = Array.from({ length: 10 }, (_, index) => statement(`context-${index}`, index + 1));
  const focals = Array.from({ length: 159 }, (_, index) => statement(`focal-${String(index).padStart(3, "0")}`, index + 11));
  const value = projection({
    statements: [...contexts, ...focals],
    commitments: [...contexts, ...focals].map(({ event }) => commitment(event)),
    provenance: { "focal-000": contexts.map(({ event }) => event) },
  });

  const graph = buildOutcomeMap(value, focals.map(({ event }) => event));
  assert.equal(graph.nodes.length, 160);
  assert.equal(graph.stats.focalNodes, 159);
  assert.equal(graph.stats.contextNodes, 1);
  assert.equal(graph.stats.omittedContextNodes, 9);
});

test("an oversized aggregate is omitted rather than lying with a partial contributor list", () => {
  const value = projection({
    statements: [statement("basis", 1), statement("basis-detail", 2, "assert"), statement("focal", 3)],
    commitments: [commitment("basis"), commitment("focal")],
    provenance: {
      "basis-detail": ["basis"],
      focal: ["basis", "basis-detail"],
    },
  });
  const graph = buildOutcomeMap(value, ["focal"], { contributorsPerEdge: 1 });
  assert.equal(graph.relations.length, 0);
  assert.equal(graph.nodes.some((node) => node.context), false);
  assert.equal(graph.stats.omittedEdges, 1);
  assert.match(graph.warnings.find((warning) => warning.kind === "bounded").message, /omitted rather than truncated/);
});

test("high-cardinality contributors are bounded while they are accumulated", () => {
  const contributorCount = 4096;
  const details = Array.from(
    { length: contributorCount },
    (_, index) => statement(`basis-detail-${String(index).padStart(4, "0")}`, index + 2, "assert"),
  );
  const provenance = Object.fromEntries(details.map(({ event }) => [event, ["basis"]]));
  provenance.focal = details.map(({ event }) => event);
  const value = projection({
    statements: [statement("basis", 1), ...details, statement("focal", contributorCount + 2)],
    commitments: [commitment("basis"), commitment("focal")],
    provenance,
  });

  const started = performance.now();
  const graph = buildOutcomeMap(value, ["focal"]);
  const elapsed = performance.now() - started;

  assert.equal(graph.relations.length, 0, "an oversized relation exposed a partial contributor list");
  assert.equal(graph.stats.contributors, 0);
  assert.equal(graph.stats.omittedEdges, 1);
  assert.match(graph.warnings.find((warning) => warning.kind === "bounded").message, /more than 64 contributors/);
  assert.ok(elapsed < 250, `bounded accumulation took ${elapsed.toFixed(1)} ms`);
});

test("relation-group selection stays bounded and deterministic before materialisation", () => {
  const bases = Array.from({ length: 5 }, (_, index) => statement(`basis-${index}`, index + 1));
  const focal = statement("focal", 6);
  const commitments = [...bases, focal].map(({ event }) => commitment(event));
  const provenance = { focal: bases.map(({ event }) => event) };
  const left = buildOutcomeMap(
    projection({ statements: [...bases, focal], commitments, provenance }),
    [...bases.map(({ event }) => event), "focal"],
    { edges: 2 },
  );
  const right = buildOutcomeMap(
    projection({ statements: [focal, ...bases].reverse(), commitments: [...commitments].reverse(), provenance: { focal: [...provenance.focal].reverse() } }),
    ["focal", ...bases.map(({ event }) => event).reverse()],
    { edges: 2 },
  );

  assert.deepEqual(left, right);
  assert.deepEqual(left.relations.map(({ source, target }) => [source, target]), [
    ["basis-0", "focal"],
    ["basis-1", "focal"],
  ]);
  assert.equal(left.stats.omittedEdges, 3);
  assert.match(left.warnings.find((warning) => warning.kind === "bounded").message, /relation groups were bounded at 2/);
});
