// The work drawer's personal state lives in browser storage, and the wiring
// between the component and that storage is only observable in a DOM: the load
// happens in an effect, and the save happens in a click handler. Static
// rendering runs neither, so components.test.mjs supplies personal state
// through the initialPersonalMemory prop instead. That prop is not a smell —
// without it a server render could say nothing about personal state at all —
// but it means those tests pass whether or not the component ever reaches
// storage. Four reversions of this wiring shipped with the suite green.
//
// This file drives the drawer in a DOM against a storage shim, so that cutting
// either direction of the wiring fails a test. Everything that does not need a
// browser stays in components.test.mjs.
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
dom.window.Element.prototype.scrollTo = function scrollTo() {};
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

// A Map-backed store rather than jsdom's own localStorage, so a test can read
// what the component wrote without depending on jsdom's persistence rules.
const stored = new Map();
globalThis.localStorage = {
  getItem: (key) => (stored.has(key) ? stored.get(key) : null),
  setItem: (key, value) => stored.set(key, String(value)),
  removeItem: (key) => stored.delete(key),
  clear: () => stored.clear(),
};

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const GENESIS = "genesis";
const ME = "codex-fingerprint";
const key = `workroom.personal-work.${GENESIS}.${ME}`;

// One actor named codex, so the drawer resolves a single fingerprint for the
// session actor. An ambiguous name fails closed and would hide the wiring
// behind that guard instead of testing it.
const room = {
  actors: [{ name: "codex", fingerprint: ME, roles: [], custody: true }],
  commits: [],
  graphTruncated: false,
  offline: false,
  localOffline: false,
  status: {
    durable: {
      genesis: GENESIS,
      head: "head",
      depth: 1,
      projection: {
        decisions: ["open-a", "open-b"].map((event) => ({ event, verdict: "effective", reason: "ok" })),
        acts: [],
        artifacts: [],
        actors: {},
        statements: [
          { event: "open-a", actor: ME, kind: "request", text: "Open A", body: { to: ME }, timestamp: 10 },
          { event: "open-b", actor: ME, kind: "request", text: "Open B", body: { to: ME }, timestamp: 20 },
        ],
        commitments: [
          { request: "open-a", requester: ME, addressed_to: ME, status: "open" },
          { request: "open-b", requester: ME, addressed_to: ME, status: "open" },
        ],
        provenance: { "open-a": [], "open-b": [] },
      },
    },
    live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
    cursor: { frontier: [], live: { generation: "generation", position: 1 } },
  },
};

const session = { id: "browser", live: true, actor: "codex", setActor() {} };

// Topic rows carry no stable test attribute, so they are found by their
// heading, which is the same thing a reader looks for.
const topicRow = (title) =>
  [...document.querySelectorAll("article")].find((row) => row.querySelector("h2")?.textContent.trim() === title);
const followControl = (title) =>
  topicRow(title)?.querySelector('[aria-label="follow topic"], [aria-label="unfollow topic"]');

test("the work drawer reads personal state from storage and writes it back", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  const root = createRoot(document.getElementById("root"));
  try {
    const { WorkView } = await vite.ssrLoadModule("/src/components/WorkDrawer.tsx");
    const drawer = () =>
      React.createElement(WorkView, {
        workroom: room,
        session,
        highlight: { events: new Set(), commits: new Set() },
        onSelect() {},
        onOpenThread() {},
        // Deliberately no initialPersonalMemory: the point is that the
        // component reaches storage itself.
      });

    // The load half. Storage says open-b is followed before the drawer mounts,
    // so a drawer that never reads storage shows it unfollowed.
    stored.clear();
    stored.set(key, JSON.stringify({ followed: ["open-b"], viewed: {} }));
    await act(async () => { root.render(drawer()); });

    const followed = followControl("Open B");
    assert.ok(followed, "the drawer renders a follow control for Open B");
    assert.equal(
      followed.getAttribute("aria-label"),
      "unfollow topic",
      "a drawer that read storage shows Open B already followed",
    );
    assert.equal(
      followControl("Open A")?.getAttribute("aria-label"),
      "follow topic",
      "and shows an untouched topic unfollowed",
    );

    // The save half. Following open-a must reach storage, not just state:
    // the drawer is the only writer, so if this does not land, the follow is
    // forgotten the moment the browser reloads.
    await act(async () => {
      followControl("Open A").dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    const written = JSON.parse(stored.get(key) ?? "{}");
    assert.ok(
      (written.followed ?? []).includes("open-a"),
      `following a topic is written to storage, got ${stored.get(key)}`,
    );
    assert.ok(
      (written.followed ?? []).includes("open-b"),
      "and the follow already in storage survives the write",
    );
  } finally {
    await act(async () => { root.unmount(); });
    await vite.close();
  }
});
