// Two conditions of the thread pane are only observable in a browser: what a
// click does, and what an effect does when the pane is pointed at a different
// thread. Static rendering runs neither, so this file drives the pane in a
// DOM. Everything testable without one stays in components.test.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.MouseEvent = dom.window.MouseEvent;
// jsdom lays nothing out, so it ships no scrollTo; the pane only uses it to
// follow the tail.
dom.window.Element.prototype.scrollTo = function scrollTo() {};
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const statement = (event, actor, kind, text) => ({ event, actor, kind, text, timestamp: 1_700_000_000 });
const statements = [
  statement("root", "codex", "request", "Build the railway"),
  statement("first", "claude", "promise", "I will review it"),
  statement("branch", "hugh", "assert", "Keep the thread local"),
  statement("join", "codex", "report", "Railway ready"),
];
const projection = {
  decisions: statements.map((item) => ({ event: item.event, verdict: "effective", reason: "recorded" })),
  acts: [],
  statements,
  commitments: [],
  artifacts: [],
  actors: {},
  provenance: { root: [], first: ["root"], branch: ["root"], join: ["branch", "first"] },
};
const workroom = {
  actors: [{ name: "codex", fingerprint: "codex-fingerprint", roles: [], custody: true }],
  commits: [],
  graphTruncated: false,
  offline: false,
  localOffline: false,
  status: {
    durable: { genesis: "genesis", head: "head", depth: 1, projection },
    live: { cursor: { generation: "generation", position: 1 }, presence: {}, conversations: [] },
    cursor: { frontier: [], live: { generation: "generation", position: 1 } },
  },
};

function click(element) {
  return act(async () => {
    element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

const tabNamed = (name) => [...document.querySelectorAll("[role=tab]")].find((tab) => tab.textContent.trim() === name);

test("the railway tab shows the rail, its rows navigate, and retargeting returns to the conversation", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  const root = createRoot(document.getElementById("root"));
  const jumped = [];
  try {
    const { ThreadPane } = await vite.ssrLoadModule("/src/components/ThreadPane.tsx");
    const pane = (event) =>
      React.createElement(ThreadPane, {
        workroom,
        session: { id: "browser", live: true, actor: "codex", setActor() {} },
        frames: [],
        target: { kind: "event", event },
        pending: [],
        composer: { restsOn: [], frames: [], type: "assert" },
        onComposer() {},
        onClose() {},
        onJumpTo(target) { jumped.push(target); },
        onOpenProfile() {},
        onRoute() {},
        doAct() {},
        onSay() { return "pending"; },
        onSayFailed() {},
      });

    await act(async () => { root.render(pane("root")); });
    assert.equal(document.querySelector("[data-thread-railway]"), null, "the pane opens on the conversation");

    await click(tabNamed("Railway"));
    const rail = document.querySelector("[data-thread-railway]");
    assert.ok(rail, "the Railway tab shows the rail");

    // Every row on the rail is a way into that event.
    const rows = [...rail.querySelectorAll("[data-thread-rail-event]")];
    assert.deepEqual(rows.map((row) => row.dataset.threadRailEvent), ["root", "first", "branch", "join"]);
    await click(rows[2].querySelector("button"));
    assert.deepEqual(jumped, ["branch"]);

    // Pointing the pane at another thread must not leave the previous
    // thread's rail on screen.
    await act(async () => { root.render(pane("first")); });
    assert.equal(document.querySelector("[data-thread-railway]"), null, "retargeting returns to the conversation");
    assert.equal(tabNamed("Thread").getAttribute("aria-selected"), "true");
  } finally {
    await act(async () => { root.unmount(); });
    await vite.close();
  }
});
