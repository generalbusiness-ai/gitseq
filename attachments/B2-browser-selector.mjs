// Counts the named populations with the browser's own row selector — the same
// workRows the tabs render — on the frozen /v0/status answer the Go owner was
// run over. vite is imported by absolute path so nothing is written into the
// reviewed worktree.
import { readFileSync } from "node:fs";
const [uiRoot, file] = process.argv.slice(2);
const { createServer } = await import(`${uiRoot}/node_modules/vite/dist/node/index.js`);

const status = JSON.parse(readFileSync(file, "utf8"));
const projection = status.durable.projection;

const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
const { workRows } = await vite.ssrLoadModule("/src/lib/rows.ts");
const context = { nameOf: (f) => f, tickets: new Map(), actors: projection.actors };
const count = (population) => workRows(projection, context, population).length;

const open = workRows(projection, context, "live");
const byKey = new Map(projection.commitments.map((c) => [c.promise ?? c.report ?? c.request, c.status]));
const lifecycles = { open: 0, promised: 0, reported: 0, "awaiting-review": 0, "awaiting-authorization": 0, "awaiting-landing": 0 };
for (const row of open) lifecycles[byKey.get(row.key)] += 1;

console.log(JSON.stringify({
  frontier: status.durable.head,
  depth: status.durable.depth,
  scope: "workroom",
  commitments: projection.commitments.length,
  open: open.length,
  open_lifecycles: lifecycles,
  completed: count("done"),
  closed_not_completed: count("closed"),
  stale: count("stale"),
  reasoning_moved: count("moved"),
  artifact_landing_audit: count("approved"),
}, null, 2));
await vite.close();
