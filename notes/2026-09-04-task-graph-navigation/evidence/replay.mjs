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
const statements = new Map((projection.statements ?? []).map((x) => [x.event, x]));
const acts = new Map((projection.acts ?? []).map((x) => [x.event, x]));
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

  // --- Accounting, in one unit. A task is a request, which is a thread root.
  const drawnRoots = new Set(graph.nodes.filter((n) => !n.context).map((n) => n.thread));
  const rowsPerRoot = new Map();
  for (const r of rows) { const k = rootOf(r.event); rowsPerRoot.set(k, (rowsPerRoot.get(k) ?? 0) + 1); }
  const shownRows = rows.filter((r) => drawnRoots.has(rootOf(r.event))).length;
  const multiRoots = [...rowsPerRoot.values()].filter((v) => v > 1).length;

  console.log(`\n## Population "${name}"`);
  console.log(`  ACCOUNTING  tasks(roots)=${rowsPerRoot.size} shownTasks=${drawnRoots.size} hiddenTasks=${rowsPerRoot.size - drawnRoots.size}`);
  console.log(`              commitmentRows=${rows.length} rowsOnShownTasks=${shownRows} hiddenRows=${rows.length - shownRows} tasksWithMoreThanOneRow=${multiRoots}`);
  console.log(`              arithmetic: ${rows.length} rows - ${shownRows} on shown tasks = ${rows.length - shownRows} hidden rows; ${rowsPerRoot.size} tasks - ${drawnRoots.size} shown = ${rowsPerRoot.size - drawnRoots.size} hidden tasks`);
  console.log(`              contextCards=${graph.stats.contextNodes} totalCards=${graph.nodes.length}`);
  console.log(`  groups (all drawn relations)=${groups} contextOmitted=${graph.stats.omittedContextNodes} relationGroupsOmitted=${graph.stats.omittedEdges}`);

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

  // --- Restricted lineage: which drawn relations are task lineage, and which
  // are supporting citations grouped into the same thread-to-thread line.
  // Lineage is a relation whose dependent end is itself a `request` resting on
  // a request, a promise or an artifact, plus a projected successor_request
  // transfer. Everything else is a citation: real, acyclic, and not evidence
  // that one task precedes another.
  const kindOf = (event) => statements.get(event)?.kind ?? (acts.get(event) ? `act:${acts.get(event).type}` : "?");
  const LINEAGE_BASES = new Set((process.env.BASES ?? "request,promise,artifact").split(","));
  const pairs = {};
  const lineage = [], citations = [];
  for (const r of graph.relations) {
    let isLineage = false;
    for (const c of r.contributors) {
      if (c.kind === "successor-request") { isLineage = true; continue; }
      if (c.kind !== "provenance") continue;
      const bk = kindOf(c.basis), dk = kindOf(c.dependent);
      pairs[`${bk} -> ${dk}`] = (pairs[`${bk} -> ${dk}`] ?? 0) + 1;
      if (dk === "request" && LINEAGE_BASES.has(bk)) isLineage = true;
    }
    (isLineage ? lineage : citations).push(r);
  }
  const lo = new Map(graph.nodes.map((n) => [n.thread, []])), li = new Map(graph.nodes.map((n) => [n.thread, []]));
  const lu = new Map(graph.nodes.map((n) => [n.thread, []]));
  for (const r of lineage) { lo.get(r.source).push(r.target); li.get(r.target).push(r.source); lu.get(r.source).push(r.target); lu.get(r.target).push(r.source); }
  const lseen = new Set(); let lgroups = 0;
  for (const n of graph.nodes) if (!lseen.has(n.thread)) { lgroups += 1; for (const m of reach(n.thread, lu)) lseen.add(m); }
  // Tarjan over the lineage subgraph.
  const ix = new Map(), lw = new Map(), stk = [], onstk = new Set(); let counter = 0; const comps = [];
  const visit = (v) => { ix.set(v, counter); lw.set(v, counter); counter += 1; stk.push(v); onstk.add(v);
    for (const w of lo.get(v) ?? []) { if (!ix.has(w)) { visit(w); lw.set(v, Math.min(lw.get(v), lw.get(w))); }
      else if (onstk.has(w)) lw.set(v, Math.min(lw.get(v), ix.get(w))); }
    if (lw.get(v) !== ix.get(v)) return;
    const comp = []; while (stk.length) { const m = stk.pop(); onstk.delete(m); comp.push(m); if (m === v) break; } comps.push(comp); };
  for (const n of graph.nodes) if (!ix.has(n.thread)) visit(n.thread);
  const lcycles = comps.filter((c) => c.length > 1);
  // Longest-path layering over the lineage DAG, then same-column lineage edges.
  let sameColumnLineage = "n/a (cyclic)", lineageColumns = "";
  if (lcycles.length === 0) {
    const lay = new Map(graph.nodes.map((n) => [n.thread, 0])), deg = new Map(graph.nodes.map((n) => [n.thread, 0]));
    for (const r of lineage) deg.set(r.target, deg.get(r.target) + 1);
    const ready = [...deg].filter(([, d]) => d === 0).map(([n]) => n);
    for (let x = 0; x < ready.length; x += 1) for (const w of lo.get(ready[x]) ?? []) {
      lay.set(w, Math.max(lay.get(w), lay.get(ready[x]) + 1));
      deg.set(w, deg.get(w) - 1); if (deg.get(w) === 0) ready.push(w); }
    sameColumnLineage = String(lineage.filter((r) => lay.get(r.source) === lay.get(r.target)).length);
    const occ = {}; for (const v of lay.values()) occ[v] = (occ[v] ?? 0) + 1;
    lineageColumns = Object.entries(occ).sort((a, b) => a[0] - b[0]).map(([l, c]) => `L${l}=${c}`).join(" ");
  }
  let wb = 0, wa = 0, sumOn = 0;
  for (const n of graph.nodes) { const b = reach(n.thread, li).size - 1, a = reach(n.thread, lo).size - 1;
    wb = Math.max(wb, b); wa = Math.max(wa, a); sumOn += new Set([...reach(n.thread, li), ...reach(n.thread, lo)]).size; }
  console.log(`  RESTRICTED LINEAGE (bases: ${[...LINEAGE_BASES].join("/")})`);
  console.log(`     lineage relations ${lineage.length} of ${graph.relations.length}; citation-only ${citations.length}`);
  console.log(`     groups ${lgroups}; cycles ${lcycles.length}${lcycles.length ? ` sizes ${lcycles.map((c) => c.length).join(",")}` : ""}; same-column lineage edges ${sameColumnLineage}`);
  if (lineageColumns) console.log(`     lineage columns: ${lineageColumns}`);
  console.log(`     worst before ${wb}, worst after ${wa}, mean on-path ${(sumOn / graph.nodes.length).toFixed(1)} of ${graph.nodes.length}`);
  const topPairs = Object.entries(pairs).sort((a, b) => b[1] - a[1]).slice(0, 8);
  console.log(`     contributor kind pairs: ${topPairs.map(([k, v]) => `${k} x${v}`).join(", ")}`);

  for (const probe of (process.env.PROBE ?? "").split(",").filter(Boolean)) {
    const node = graph.nodes.find((n) => n.thread.endsWith(probe));
    if (!node) continue;
    const before = reach(node.thread, rev), after = reach(node.thread, fwd);
    const both = [...before].filter((t) => after.has(t) && t !== node.thread).length;
    const bases = new Set(rev.get(node.thread));
    const sib = new Set();
    for (const b of bases) for (const t of fwd.get(b) ?? []) if (t !== node.thread && !before.has(t) && !after.has(t)) sib.add(t);
    const union = new Set([...before, ...after]);
    console.log(`  PROBE ...${probe}`);
    console.log(`     grouped provenance (today): undirected ${reach(node.thread, undirected).size} of ${N}; before ${before.size - 1}, after ${after.size - 1}, both ${both}, siblings ${sib.size}; emphasised ${union.size} of ${N}`);
    const lb = reach(node.thread, li), la = reach(node.thread, lo);
    const lboth = [...lb].filter((t) => la.has(t) && t !== node.thread).length;
    const lbases = new Set(li.get(node.thread));
    const lsib = new Set();
    for (const b of lbases) for (const t of lo.get(b) ?? []) if (t !== node.thread && !lb.has(t) && !la.has(t)) lsib.add(t);
    console.log(`     restricted lineage:         before ${lb.size - 1}, after ${la.size - 1}, both ${lboth}, siblings ${lsib.size}; on path ${new Set([...lb, ...la]).size} of ${N}`);
    console.log(`     direct predecessors ${(li.get(node.thread) ?? []).length}, direct continuations ${(lo.get(node.thread) ?? []).length}`);
  }
}
