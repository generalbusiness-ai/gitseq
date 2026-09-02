// The address is the only durable thing a reader takes away from a screen:
// what it names must be what is shown, ids ride in it exactly as the fold
// writes them, and back and forward walk the visit inside the application.
// Static rendering runs no history, so this file drives the whole App in a
// DOM. Everything testable without one stays with the pure module.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://workroom.local/" });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.MouseEvent = dom.window.MouseEvent;
dom.window.Element.prototype.scrollTo = function scrollTo() {};
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
globalThis.localStorage = dom.window.localStorage;
dom.window.localStorage.clear();

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

// A full canonical id, with its own interior '#', exactly as the fold cites
// events. An address that abbreviated or reassembled this would silently
// name nothing.
const genesis = "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8";
const request = `${genesis}#git:sha1:aabbccdd00000000000000000000000000000001`;
const stranger = `${genesis}#git:sha1:deadbeef000000000000000000000000000000ff`;

const projection = {
  decisions: [{ event: request, sequence: 2, verdict: "effective", reason: "statement recorded" }],
  acts: [],
  statements: [
    { event: request, sequence: 2, actor: "planner", kind: "request", lifecycle: "request", text: "Build the addresses", timestamp: 1_700_000_000, body: { to: "builder" } },
  ],
  commitments: [{ request, requester: "planner", addressed_to: "builder", status: "open" }],
  artifacts: [],
  actors: {},
  provenance: {},
};
const status = {
  durable: { genesis: "g", head: "h", depth: 2, projection },
  live: { cursor: { generation: "gen", position: 1 }, presence: {}, conversations: [] },
  cursor: { frontier: [], live: { generation: "gen", position: 1 } },
};

const waitNever = new Promise(() => {});
globalThis.fetch = async (input) => {
  const url = String(input);
  // The wait long-poll must stay pending: an instant answer would spin the
  // poll loop and starve the very event loop these assertions run on.
  if (url.includes("/v0/wait")) return waitNever;
  const body = url.includes("/v0/status")
    ? status
    : url.includes("/v0/actors")
      ? []
      : url.includes("/v0/worktrees")
        ? { repo: "/repo", worktrees: [] }
        : [];
  return { ok: true, statusText: "OK", json: async () => body };
};

function mountAt(hash) {
  dom.window.history.replaceState({}, "", hash || "/");
  return createRoot(document.getElementById("root"));
}

async function renderApp(vite, root) {
  const { default: App } = await vite.ssrLoadModule("/src/App.tsx");
  await act(async () => {
    root.render(React.createElement(App));
  });
  await act(async () => {
    await Promise.resolve();
  });
}

async function traverse(step) {
  const arrived = new Promise((resolve) => dom.window.addEventListener("popstate", resolve, { once: true }));
  step();
  await act(async () => {
    await arrived;
    await Promise.resolve();
  });
  await act(async () => {
    await Promise.resolve();
  });
}

async function withApp(hash, run) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const root = mountAt(hash);
    await renderApp(vite, root);
    try {
      await run(vite);
    } finally {
      root.unmount();
    }
  } finally {
    await vite.close();
  }
}

test("addresses parse and format each screen, and an id rides whole", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { formatAddress, parseAddress } = await vite.ssrLoadModule("/src/lib/address.ts");
    assert.equal(formatAddress({ kind: "thread", event: request }), `#/thread/${request}`);
    assert.equal(formatAddress({ kind: "thread", event: request, focus: stranger }), `#/thread/${request}/${stranger}`);
    assert.deepEqual(parseAddress(`#/thread/${request}`), { kind: "thread", event: request, focus: undefined });
    assert.deepEqual(parseAddress(formatAddress({ kind: "list", population: "ratification" })), { kind: "list", population: "ratification" });
    // Unknown shapes degrade to the default list rather than to nothing.
    assert.deepEqual(parseAddress(""), { kind: "list", population: undefined });
    assert.deepEqual(parseAddress("#/list/nonsense"), { kind: "list", population: undefined });
    assert.deepEqual(parseAddress("#/elsewhere/x"), { kind: "list", population: undefined });
    assert.deepEqual(parseAddress("#/thread"), { kind: "list" });
  } finally {
    await vite.close();
  }
});

test("a deep link opens the record it names and keeps the id whole in the bar", async () => {
  await withApp(`#/thread/${request}`, async () => {
    // The thread header names the ticket; the list would show the same title
    // as a row, so only the header proves the thread screen opened.
    assert.match(document.body.textContent, /#2 · Build the addresses/, "the deep link did not open the thread it named");
    assert.equal(document.querySelector('[role="tablist"]'), null, "the list rendered under a thread address");
    assert.equal(dom.window.location.hash, `#/thread/${request}`, "the address bar did not keep the id whole");
  });
});

test("a list address opens its tab, and opening a thread then going back and forward walks the visit", async () => {
  await withApp("#/list/ratification", async () => {
    // The tab strip names every tab; only the selected one proves derivation.
    const selected = document.querySelector('[role="tab"][aria-selected="true"]');
    assert.ok(selected, "no tab is selected");
    assert.match(selected.textContent, /awaiting ratification/, "the tab named in the address was not selected");

    await traverse(() => {
      dom.window.history.pushState({}, "", `#/thread/${request}`);
      dom.window.dispatchEvent(new dom.window.PopStateEvent("popstate"));
    });
    assert.match(document.body.textContent, /Build the addresses/, "navigating to a thread did not open it");

    await traverse(() => dom.window.history.back());
    assert.doesNotMatch(document.body.textContent, /#2 · Build the addresses/, "back left the thread on screen");
    assert.ok(document.querySelector('[role="tablist"]'), "back did not return to the list");
    assert.equal(dom.window.location.hash, "#/list/ratification", "back did not restore the list address with its tab");
    assert.match(document.querySelector('[role="tab"][aria-selected="true"]').textContent, /awaiting ratification/, "back did not restore the tab");

    await traverse(() => dom.window.history.forward());
    assert.match(document.body.textContent, /#2 · Build the addresses/, "forward did not reopen the thread");
    assert.equal(dom.window.location.hash, `#/thread/${request}`);
  });
});

test("an address naming an unknown record shows the board, says so, and keeps the address", async () => {
  await withApp(`#/thread/${stranger}`, async () => {
    const notice = document.querySelector('[data-testid="unknown-address"]');
    assert.ok(notice, "the unknown address was not reported");
    assert.match(notice.textContent, /No record at this address/);
    assert.ok(notice.textContent.includes(stranger), "the raw id was not shown whole");
    assert.match(document.body.textContent, /Build the addresses/, "the board did not render under the unknown address");
    assert.equal(dom.window.location.hash, `#/thread/${stranger}`, "the address was not preserved in the bar");
  });
});
