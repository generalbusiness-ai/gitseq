// Replay the board UI's own pipeline over a saved `GET /v0/status` response and
// print every measurement the note beside this directory cites.
//
//   node --experimental-strip-types replay.mjs <status.json> [label]
//
// It imports ui/src/lib/*.ts directly, so the numbers are the ones the browser
// computes. Run it after `npm ci` in ui/, which is where `clsx` resolves from.
// Nothing here writes, and nothing here contacts a resident.
import { readFileSync } from "node:fs";

// Resolve the UI modules from a checkout that has ui/node_modules. Defaults to
// this repository; set GS_REPO when running from a worktree without it.
const root = process.env.GS_REPO
  ? new URL(`file://${process.env.GS_REPO.replace(/\/*$/, "/")}`)
  : new URL("../../../", import.meta.url);
const here = (p) => new URL(p.replace("../../../", ""), root);
const { POPULATIONS, workRows, matchingRows, ratificationRows } = await import(here("../../../ui/src/lib/rows.ts"));
const { buildOutcomeMap } = await import(here("../../../ui/src/lib/outcomeMap.ts"));
const { buildThreadIndex } = await import(here("../../../ui/src/lib/threads.ts"));
const { layoutOutcomeMap, fitOutcomeScale, OUTCOME_CARD } = await import(here("../../../ui/src/lib/outcomeMapLayout.ts"));

const [, , file, label = file] = process.argv;
if (!file) { console.error("usage: replay.mjs <status.json> [label]"); process.exit(2); }
const status = JSON.parse(readFileSync(file, "utf8"));
const projection = status.durable.projection;
const vocabulary = status.durable.vocabulary;
const actors = projection.actors ?? {};
const tickets = new Map();
projection.decisions.forEach((d, i) => tickets.set(d.event, d.sequence || i + 1));
const context = { nameOf: (f) => actors[f]?.name ?? String(f).slice(0, 8), tickets, actors };
const threads = buildThreadIndex(projection);
const known = new Set([...(projection.statements ?? []).map((s) => s.event), ...(projection.acts ?? []).map((a) => a.event)]);
const rootOf = (e) => (known.has(e) ? threads.root(e) : e);

// A relation is walkable as lineage when it carries a provenance or a projected
// successor-request contributor. A supersede act is retirement, never a step.
const walkable = (r) => r.contributors.some((c) => c.kind === "provenance" || c.kind === "successor-request");

console.log(`# ${label}`);
console.log(`frontier: genesis=${status.durable.genesis} head=${status.durable.head} depth=${status.durable.depth}`);
console.log(`projection: decisions=${projection.decisions.length} statements=${projection.statements.length} acts=${projection.acts.length} commitments=${projection.commitments.length}`);

const supersedes = (projection.acts ?? []).filter((a) => a.type === "supersede" && a.verdict === "effective");
const crossThread = supersedes.filter((a) => known.has(a.event) && known.has(a.target) && rootOf(a.event) !== rootOf(a.target));
console.log(`effective supersede acts: ${supersedes.length}, of which cross-thread: ${crossThread.length}`);
console.log(`commitments naming a successor_request: ${(projection.commitments ?? []).filter((c) => c.successor_request).length}`);
console.log();

const ratification = vocabulary ? ratificationRows(projection, vocabulary, context) : { rows: [] };
console.log("## Tab counts, search empty");
const pops = {};
for (const { key, label: name } of POPULATIONS) {
  pops[key] = matchingRows(key === "ratification" ? ratification.rows : workRows(projection, context, key), "");
  console.log(`  ${name.padEnd(24)} ${String(pops[key].length).padStart(5)}   (key=${key})`);
}

for (const { key, label: name } of POPULATIONS) {
  const rows = pops[key];
  if (key === "ratification" || rows.length === 0) continue;
  const graph = buildOutcomeMap(projection, rows);
  const layout = layoutOutcomeMap(graph.nodes);
  const layer = new Map(graph.nodes.map((n) => [n.thread, n.layer]));

  const perRoot = new Map();
  for (const r of rows) perRoot.set(rootOf(r.event), (perRoot.get(rootOf(r.event)) ?? 0) + 1);
  const multi = [...perRoot.values()].filter((v) => v > 1).length;

  // Groups: weakly connected components over every drawn relation.
  const undirected = new Map(graph.nodes.map((n) => [n.thread, []]));
  const fwd = new Map(graph.nodes.map((n) => [n.thread, []]));
  const rev = new Map(graph.nodes.map((n) => [n.thread, []]));
  for (const r of graph.relations) {
    undirected.get(r.source).push(r.target); undirected.get(r.target).push(r.source);
    if (walkable(r)) { fwd.get(r.source).push(r.target); rev.get(r.target).push(r.source); }
  }
  const reach = (start, adj) => { const s = new Set([start]); const q = [start];
    for (let i = 0; i < q.length; i += 1) for (const n of adj.get(q[i]) ?? []) if (!s.has(n)) { s.add(n); q.push(n); }
    return s; };
  const seen = new Set(); let groups = 0;
  for (const n of graph.nodes) if (!seen.has(n.thread)) { groups += 1; for (const m of reach(n.thread, undirected)) seen.add(m); }

  console.log(`\n## Population "${name}"`);
  console.log(`  rows=${rows.length} distinctThreads=${perRoot.size} focalCards=${graph.stats.focalNodes} contextCards=${graph.stats.contextNodes} totalCards=${graph.nodes.length}`);
  console.log(`  groups=${groups} cardsHoldingMoreThanOneLifecycle=${multi} focalThreadsOmitted=${perRoot.size - graph.stats.focalNodes} contextOmitted=${graph.stats.omittedContextNodes} relationGroupsOmitted=${graph.stats.omittedEdges}`);

  const shapes = {}; let backward = 0;
  const sameCol = [];
  for (const r of graph.relations) {
    const dl = layer.get(r.target) - layer.get(r.source);
    const kind = dl === 0 ? "same-column" : dl < 0 ? "BACKWARD" : dl === 1 ? "adjacent" : `forward+${dl}`;
    shapes[`${kind} / ${r.family}`] = (shapes[`${kind} / ${r.family}`] ?? 0) + 1;
    if (dl < 0) backward += 1;
    if (dl === 0) sameCol.push(r);
  }
  console.log(`  edge shapes: ${Object.entries(shapes).sort().map(([k, v]) => `${k}=${v}`).join(", ")}`);
  console.log(`  backward edges (drawn right to left): ${backward}`);

  // Gutter lane demand: how many same-column edges in one column overlap vertically.
  const byCol = new Map();
  for (const r of sameCol) {
    const s = layout.positions.get(r.source), d = layout.positions.get(r.target);
    const c = layer.get(r.source);
    byCol.set(c, [...(byCol.get(c) ?? []), [Math.min(s.y, d.y), Math.max(s.y, d.y)]]);
  }
  let lanes = 0, maxSpan = 0;
  for (const ivs of byCol.values()) {
    const ev = []; for (const [a, b] of ivs) { ev.push([a, 1]); ev.push([b, -1]); maxSpan = Math.max(maxSpan, b - a); }
    ev.sort((x, y) => x[0] - y[0] || x[1] - y[1]);
    let cur = 0; for (const [, d] of ev) { cur += d; lanes = Math.max(lanes, cur); }
  }
  console.log(`  same-column edges: ${sameCol.length} of ${graph.relations.length}; longest vertical span ${maxSpan}px; worst overlapping set ${lanes} (gutter lanes needed to draw them all separately)`);

  const occupancy = new Map();
  for (const n of graph.nodes) occupancy.set(n.layer, (occupancy.get(n.layer) ?? 0) + 1);
  console.log(`  column occupancy: ${[...occupancy.entries()].sort((a, b) => a[0] - b[0]).map(([l, c]) => `L${l}=${c}`).join(" ")}`);
  console.log(`  canvas ${layout.width}x${layout.height}px; fit in the served 1022x510 pane = ${fitOutcomeScale(layout, 1022, 510).toFixed(4)} (card ${(OUTCOME_CARD.width * fitOutcomeScale(layout, 1022, 510)).toFixed(0)}x${(OUTCOME_CARD.height * fitOutcomeScale(layout, 1022, 510)).toFixed(0)}px, 12px title renders at ${(12 * fitOutcomeScale(layout, 1022, 510)).toFixed(2)}px)`);
  console.log(`  fit in a 1022x900 pane = ${fitOutcomeScale(layout, 1022, 900).toFixed(4)} (12px title renders at ${(12 * fitOutcomeScale(layout, 1022, 900)).toFixed(2)}px)`);

  // Permanent edge labels: OutcomeMap.tsx:134-135 places each at the midpoint.
  const labels = graph.relations.map((r) => {
    const s = layout.positions.get(r.source), d = layout.positions.get(r.target);
    return { x: (s.x + OUTCOME_CARD.width + d.x) / 2, y: (s.y + d.y + OUTCOME_CARD.height) / 2 - 7 };
  });
  let collide = 0, inCard = 0;
  for (let i = 0; i < labels.length; i += 1) {
    for (let j = i + 1; j < labels.length; j += 1)
      if (Math.abs(labels[i].x - labels[j].x) < 30 && Math.abs(labels[i].y - labels[j].y) < 11) collide += 1;
    for (const n of graph.nodes) {
      const p = layout.positions.get(n.thread);
      if (labels[i].x > p.x && labels[i].x < p.x + OUTCOME_CARD.width && labels[i].y > p.y && labels[i].y < p.y + OUTCOME_CARD.height) { inCard += 1; break; }
    }
  }
  console.log(`  permanent edge labels: ${labels.length}; overlapping pairs ${collide}; drawn inside a card ${inCard}`);

  // Selection emphasis: the undirected component the code uses today against
  // the directed lineage this note proposes. Self is excluded from before/after.
  let sumU = 0, sumD = 0, worstU = 0;
  for (const n of graph.nodes) {
    const u = reach(n.thread, undirected).size;
    const before = reach(n.thread, rev), after = reach(n.thread, fwd);
    const union = new Set([...before, ...after]).size;
    sumU += u; sumD += union; worstU = Math.max(worstU, u);
  }
  const N = graph.nodes.length;
  console.log(`  emphasis, undirected component: mean ${(sumU / N).toFixed(1)} of ${N} (${(100 * sumU / N / N).toFixed(0)}%), worst ${worstU}`);
  console.log(`  emphasis, directed lineage:     mean ${(sumD / N).toFixed(1)} of ${N} (${(100 * sumD / N / N).toFixed(0)}%)`);

  for (const probe of (process.env.PROBE ?? "").split(",").filter(Boolean)) {
    const node = graph.nodes.find((n) => n.thread.endsWith(probe));
    if (!node) continue;
    const before = reach(node.thread, rev), after = reach(node.thread, fwd);
    const both = [...before].filter((t) => after.has(t) && t !== node.thread).length;
    const bases = new Set(rev.get(node.thread));
    const sib = new Set();
    for (const b of bases) for (const t of fwd.get(b) ?? []) if (t !== node.thread && !before.has(t) && !after.has(t)) sib.add(t);
    const union = new Set([...before, ...after]);
    console.log(`  PROBE ...${probe}: undirected ${reach(node.thread, undirected).size} of ${N}; before ${before.size - 1}, after ${after.size - 1}, both ${both}, siblings ${sib.size}; emphasised ${union.size} of ${N}`);
  }
}
