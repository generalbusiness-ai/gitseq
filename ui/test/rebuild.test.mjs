import test from "node:test";
import assert from "node:assert/strict";

import { rebuildQualifier } from "../src/lib/rebuild.ts";

const status = { durable: { head: "0123456789abcdef0123456789abcdef01234567", depth: 20091 } };

test("a retained status is qualified while the resident rebuilds, and only then", () => {
  assert.equal(rebuildQualifier(undefined, { running: true }), undefined, "a new reader has nothing to qualify; the rebuild notice covers that case");
  assert.equal(rebuildQualifier(status, undefined), undefined);
  assert.equal(rebuildQualifier(status, { running: false }), undefined);
  const counting = rebuildQualifier(status, { running: true });
  assert.match(counting, /Showing frontier 01234567 at depth 20,091/);
  assert.match(counting, /counting records/);
  assert.match(counting, /Nothing here is current/);
  const progress = rebuildQualifier(status, { running: true, verified: 1500, total: 20091 });
  assert.match(progress, /1,500 of 20,091 records verified/);
});
