import test from "node:test";
import assert from "node:assert/strict";

import { probeRebuild, rebuildQualifier } from "../src/lib/rebuild.ts";

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

const tick = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// One probe per wait, one request at a time. A slow endpoint must not
// accumulate overlapping requests, and an answer that lands after the wait
// returned must not qualify whatever status the page shows next.
test("a probe keeps one request in flight and drops answers that arrive after it stopped", async () => {
  const pending = [];
  const reported = [];
  const stop = probeRebuild(
    () => new Promise((resolve) => pending.push(resolve)),
    (rebuild) => reported.push(rebuild),
    5,
  );
  try {
    await tick(60);
    assert.equal(pending.length, 1, "a tick with a request outstanding must not start another");
    pending[0]({ running: true, verified: 1, total: 2 });
    await tick(15);
    assert.deepEqual(reported, [{ running: true, verified: 1, total: 2 }]);
    assert.equal(pending.length, 2, "the next tick after an answer starts the next request");
    pending[1]({ running: false });
    await tick(15);
    assert.deepEqual(reported.at(-1), undefined, "a resident no longer rebuilding clears the qualification");
    assert.ok(pending.length >= 3, "a request is outstanding when the wait returns");
  } finally {
    stop();
  }
  const before = reported.length;
  const started = pending.length;
  for (const resolve of pending) resolve({ running: true, verified: 9, total: 9 });
  await tick(30);
  assert.equal(reported.length, before, "an answer after stop must not be reported");
  assert.equal(pending.length, started, "no tick fires after stop");
});

test("a probe survives a failing endpoint and keeps asking", async () => {
  let calls = 0;
  const reported = [];
  const stop = probeRebuild(
    () => {
      calls += 1;
      return calls === 1 ? Promise.reject(new Error("offline")) : Promise.resolve({ running: true });
    },
    (rebuild) => reported.push(rebuild),
    5,
  );
  try {
    await tick(40);
  } finally {
    stop();
  }
  assert.ok(calls >= 2, "a rejected request must not pin the probe as in flight");
  assert.deepEqual(reported[0], { running: true });
});
