// For every two-row subset of the open population, how many separate groups the
// operator would actually see. This is a synthetic subset of a real population:
// it answers "if this board said 2 open, what would the graph draw?" without
// waiting for the board to be at 2.
//
//   node --experimental-strip-types twoopen.mjs <status.json> [label]
import { readFileSync } from "node:fs";
// Resolve the UI modules from a checkout that has ui/node_modules. Defaults to
// this repository; set GS_REPO when running from a worktree without it.
const root = process.env.GS_REPO
  ? new URL(`file://${process.env.GS_REPO.replace(/\/*$/, "/")}`)
  : new URL("../../../", import.meta.url);
const here = (p) => new URL(p.replace("../../../", ""), root);
const { workRows, matchingRows } = await import(here("../../../ui/src/lib/rows.ts"));
const { buildOutcomeMap } = await import(here("../../../ui/src/lib/outcomeMap.ts"));

const [, , file, label = file] = process.argv;
if (!file) { console.error("usage: twoopen.mjs <status.json> [label]"); process.exit(2); }
const status = JSON.parse(readFileSync(file, "utf8"));
const projection = status.durable.projection;
const actors = projection.actors ?? {};
const tickets = new Map();
projection.decisions.forEach((d, i) => tickets.set(d.event, d.sequence || i + 1));
const rows = matchingRows(workRows(projection, { nameOf: (f) => actors[f]?.name ?? String(f).slice(0, 8), tickets, actors }, "live"), "");

const groupsOf = (subset) => {
  const g = buildOutcomeMap(projection, subset);
  const adj = new Map(g.nodes.map((n) => [n.thread, []]));
  for (const r of g.relations) { adj.get(r.source).push(r.target); adj.get(r.target).push(r.source); }
  const seen = new Set(); let c = 0;
  for (const n of g.nodes) { if (seen.has(n.thread)) continue; c += 1; const q = [n.thread]; seen.add(n.thread);
    for (let i = 0; i < q.length; i += 1) for (const m of adj.get(q[i]) ?? []) if (!seen.has(m)) { seen.add(m); q.push(m); } }
  return { groups: c, cards: g.nodes.length, focal: g.stats.focalNodes, context: g.stats.contextNodes, edges: g.relations.length };
};

console.log(`# ${label}`);
console.log(`frontier: genesis=${status.durable.genesis} head=${status.durable.head} depth=${status.durable.depth}`);
console.log(`open population: ${rows.length} rows${rows.length ? ` (${rows.map((r) => `#${r.ticket}`).join(" ")})` : ""}`);
if (rows.length === 0) { console.log("nothing open; RequestList.tsx:200-203 renders no graph at all"); process.exit(0); }
const whole = groupsOf(rows);
console.log(`whole population: ${whole.focal} focal + ${whole.context} context = ${whole.cards} cards, ${whole.edges} edges, ${whole.groups} group(s)`);
console.log("every synthetic two-row subset:");
for (let i = 0; i < rows.length; i += 1) for (let j = i + 1; j < rows.length; j += 1) {
  const r = groupsOf([rows[i], rows[j]]);
  console.log(`  #${rows[i].ticket} + #${rows[j].ticket}: ${r.focal} focal + ${r.context} context = ${r.cards} cards, ${r.edges} edges -> ${r.groups} group(s)${r.groups === 1 ? "   <== two open requests, ONE visible group" : ""}`);
}
