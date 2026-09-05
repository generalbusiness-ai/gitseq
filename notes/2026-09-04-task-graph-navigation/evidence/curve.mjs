// The exact horizontal extent of the same-column edge path built by
// outcomeMapLayout.ts:63-70, evaluated rather than read off its control points.
//
//   node --experimental-strip-types curve.mjs
// Resolve the UI modules from a checkout that has ui/node_modules. Defaults to
// this repository; set GS_REPO when running from a worktree without it.
const root = process.env.GS_REPO
  ? new URL(`file://${process.env.GS_REPO.replace(/\/*$/, "/")}`)
  : new URL("../../../", import.meta.url);
const here = (p) => new URL(p.replace("../../../", ""), root);
const { OUTCOME_CARD, outcomeEdgePath } = await import(here("../../../ui/src/lib/outcomeMapLayout.ts"));
const W = OUTCOME_CARD.width;
const source = { x: 0, y: 0 }, target = { x: 0, y: 900 };
console.log(`path for a same-column edge: ${outcomeEdgePath(source, target)}`);
const bend = Math.max(44, Math.abs(target.x - (source.x + W)) / 2);
const P = [source.x + W, source.x + W + bend, target.x - bend, target.x];
console.log(`x control points: ${P.join(", ")}   (bend = ${bend})`);
const at = (t) => (1 - t) ** 3 * P[0] + 3 * (1 - t) ** 2 * t * P[1] + 3 * (1 - t) * t ** 2 * P[2] + t ** 3 * P[3];
let min = Infinity, max = -Infinity;
for (let i = 0; i <= 2_000_000; i += 1) { const v = at(i / 2_000_000); if (v < min) min = v; if (v > max) max = v; }
console.log(`evaluated x range: ${min.toFixed(3)} .. ${max.toFixed(3)}`);
console.log(`excursion left of the column: ${(-min).toFixed(3)}px; right of the card edge: ${(max - W).toFixed(3)}px`);
console.log(`the -${bend} control point is NOT the extent: the curve reaches ${(-min).toFixed(3)}px, not ${bend}px`);
console.log(`the curve does cross the full ${W}px column band once, entering the target from its left edge`);
