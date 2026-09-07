// The rebuild probe as the page actually runs it: useWorkroom in a DOM, with
// the long poll and /v0/rebuild answered by hand. Two races only a real wait
// loop can show: an answer from an earlier wait's probe landing after a newer
// status, and a slow endpoint asked again before it answered.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const status = (head, depth, profile = "app@fold-1") => ({
  durable: { genesis: "genesis", head, depth, projection: { decisions: [], acts: [], statements: [], commitments: [], artifacts: [], actors: {}, provenance: {} } },
  live: { cursor: { generation: "g", position: depth }, presence: {}, activity: {}, conversations: [] },
  cursor: { frontier: [{ genesis: "genesis", head, depth }], live: { generation: "g", position: depth } },
  trust_boundary: "trusted-process",
  profile,
});
const reply = (value) => ({ ok: true, json: async () => value });

// Drives the hook with the probe timer and both endpoints under test control.
// Only the one-second probe timer is intercepted; everything else keeps the
// real timers so React's own scheduling is untouched.
async function withHook(body) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const { useWorkroom } = await vite.ssrLoadModule("/src/lib/store.ts");
  const { rebuildQualifier } = await vite.ssrLoadModule("/src/lib/rebuild.ts");
  const native = { setInterval: globalThis.setInterval, clearInterval: globalThis.clearInterval, fetch: globalThis.fetch };
  const ticks = new Map();
  const waits = [];
  const rebuilds = [];
  const unanswered = new Set();
  let latest;
  globalThis.setInterval = (fn, ms, ...rest) => {
    if (ms !== 1000) return native.setInterval(fn, ms, ...rest);
    const id = { unref() {} };
    ticks.set(id, fn);
    return id;
  };
  globalThis.clearInterval = (id) => {
    if (!ticks.delete(id)) native.clearInterval(id);
  };
  globalThis.fetch = (url, options = {}) => {
    if (url === "/v0/status") return Promise.resolve(reply(status("old00000", 1)));
    if (url === "/v0/actors") return Promise.resolve(reply([]));
    if (url === "/v0/worktrees") return Promise.resolve(reply({ repo: "", worktrees: [] }));
    if (url === "/v0/wait") return new Promise((resolve) => waits.push(resolve));
    if (url === "/v0/rebuild") {
      return new Promise((resolve, reject) => {
        const token = {};
        unanswered.add(token);
        rebuilds.push((value) => {
          unanswered.delete(token);
          resolve(value);
        });
        options.signal?.addEventListener("abort", () => {
          unanswered.delete(token);
          reject(new Error("aborted"));
        }, { once: true });
      });
    }
    return Promise.reject(new Error(`unexpected fetch ${url}`));
  };
  function View() {
    latest = useWorkroom();
    if (!latest.status) return React.createElement("div", {}, "no status");
    return React.createElement("div", {}, rebuildQualifier(latest.status, latest.rebuilding) ?? "current");
  }
  const root = createRoot(document.getElementById("root"));
  try {
    await act(async () => root.render(React.createElement(View)));
    assert.equal(waits.length, 1, "the hook must reach its first long poll");
    assert.equal(ticks.size, 1, "the hook must start one probe timer for that wait");
    const tick = () => act(async () => { for (const fn of ticks.values()) fn(); });
    await body({ tick, waits, rebuilds, unanswered, latest: () => latest });
  } finally {
    await act(async () => root.unmount());
    Object.assign(globalThis, native);
    await vite.close();
  }
}

test("a probe answer from an earlier wait cannot qualify a newer status", async () => {
  await withHook(async ({ tick, waits, rebuilds, latest }) => {
    await tick();
    assert.equal(rebuilds.length, 1, "the first tick asks the rebuild endpoint");
    await act(async () => waits[0](reply({ status: status("new00000", 2) })));
    assert.equal(latest().status.durable.head, "new00000");
    assert.equal(latest().rebuilding, undefined);
    assert.equal(waits.length, 2, "the next wait is open");
    await act(async () => rebuilds[0](reply({ running: true, verified: 1, total: 10 })));
    assert.equal(latest().rebuilding, undefined, "the old wait's answer must not qualify the new frontier");
    assert.equal(document.body.textContent, "current");
  });
});

test("a slow rebuild endpoint is not asked again before it answers", async () => {
  await withHook(async ({ tick, rebuilds }) => {
    await tick();
    await tick();
    await tick();
    assert.equal(rebuilds.length, 1, "one rebuild request in flight per wait");
    await act(async () => rebuilds[0](reply({ running: true })));
    await tick();
    assert.equal(rebuilds.length, 2, "after an answer the next tick asks again");
    assert.match(document.body.textContent, /Showing frontier old00000/);
  });
});

test("a completed wait aborts the rebuild request it left in flight", async () => {
  await withHook(async ({ tick, waits, unanswered }) => {
    for (let i = 0; i < 3; i++) {
      await tick();
      await act(async () => waits[i](reply({ status: status(`new${i}0000`, 2 + i) })));
      assert.equal(waits.length, i + 2, "the wait loop advances");
    }
    assert.equal(unanswered.size, 0, "no completed wait leaves a rebuild request unanswered and unaborted");
  });
});

// Case B against case E. A rebuild under the same fold profile is a re-audit:
// the retained status was produced under the contract this process still
// implements, so it stays, qualified. A rebuild under another profile means a
// different binary replaced the resident; what the page holds was produced
// under a contract that binary does not implement, so it is dropped and the
// page shows what a new reader sees.
test("a rebuild under the same profile keeps the retained status, another profile drops it", async () => {
  await withHook(async ({ tick, rebuilds, latest }) => {
    await tick();
    await act(async () => rebuilds[0](reply({ running: true, verified: 3, total: 9, profile: "app@fold-1" })));
    assert.equal(latest().status.durable.head, "old00000", "same profile: the status is retained");
    assert.match(document.body.textContent, /Showing frontier old00000/);
    await tick();
    await act(async () => rebuilds[1](reply({ running: true, verified: 4, total: 9, profile: "app@fold-2" })));
    assert.equal(latest().status, undefined, "another profile: the retained status is dropped");
    assert.equal(document.body.textContent, "no status");
  });
});

test("a rebuild report without a profile cannot drop a retained status", async () => {
  await withHook(async ({ tick, rebuilds, latest }) => {
    await tick();
    await act(async () => rebuilds[0](reply({ running: true })));
    assert.equal(latest().status.durable.head, "old00000");
  });
});

// The case with nothing running. A new binary that reuses the signed kernel
// checkpoint re-interprets the projection without a cold audit, so the
// rebuild report says running:false while the wait is still pending. The
// profile on that report is what tells the page the retained status came
// from another contract.
test("a checkpoint-backed re-interpretation under another profile drops the retained status without a cold audit", async () => {
  await withHook(async ({ tick, rebuilds, latest }) => {
    await tick();
    await act(async () => rebuilds[0](reply({ running: false, profile: "app@fold-1" })));
    assert.equal(latest().status.durable.head, "old00000", "same profile, nothing running: retained, unqualified");
    assert.equal(document.body.textContent, "current");
    await tick();
    await act(async () => rebuilds[1](reply({ running: false, profile: "app@fold-2" })));
    assert.equal(latest().status, undefined, "another profile with nothing running still drops the retained status");
    assert.equal(document.body.textContent, "no status");
  });
});
