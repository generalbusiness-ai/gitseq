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

function enterText(element, value) {
  return act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value").set;
    setter.call(element, value);
    element.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
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

test("Link to draft visibly owns the thread draft and submits every exact selected basis", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  const mounted = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  const posted = [];
  globalThis.fetch = async (url, init = {}) => {
    assert.equal(url, "/v0/act");
    posted.push(JSON.parse(init.body));
    return { ok: true, statusText: "OK", json: async () => ({ id: `stored-${posted.length}` }) };
  };

  const genesis = "git:sha1:1111111111111111111111111111111111111111";
  const event = (hash) => `${genesis}#git:sha1:${hash.repeat(40).slice(0, 40)}`;
  const rootEvent = event("a");
  const firstEvent = event("b");
  const branchEvent = event("c");
  const linkedStatements = [
    statement(rootEvent, "codex", "request", "Build the draft link"),
    statement(firstEvent, "claude", "promise", "I will review the link"),
    statement(branchEvent, "hugh", "assert", "Keep the selected basis exact"),
  ];
  const linkedProjection = {
    decisions: linkedStatements.map((item) => ({ event: item.event, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements: linkedStatements,
    commitments: [],
    artifacts: [],
    actors: {},
    provenance: { [rootEvent]: [], [firstEvent]: [rootEvent], [branchEvent]: [rootEvent] },
  };
  const linkedRoom = {
    ...workroom,
    status: {
      ...workroom.status,
      durable: { ...workroom.status.durable, projection: linkedProjection },
    },
  };

  try {
    const [{ ThreadPane }, { durableEventBases, toggleLinkEvent }] = await Promise.all([
      vite.ssrLoadModule("/src/components/ThreadPane.tsx"),
      vite.ssrLoadModule("/src/components/Composer.tsx"),
    ]);

    // The same transition used by the room composer promotes a Temporary
    // draft before adding an exact durable basis. Merely selecting it does
    // not call the service.
    let promoted;
    toggleLinkEvent(
      { type: "say", restsOn: [], frames: [] },
      (next) => { promoted = next; },
      firstEvent,
    );
    assert.equal(promoted.type, "assert");
    assert.deepEqual(promoted.restsOn, [firstEvent]);
    assert.deepEqual(
      durableEventBases([rootEvent], [rootEvent, firstEvent, firstEvent, branchEvent]),
      [rootEvent, firstEvent, branchEvent],
      "automatic and selected bases are de-duplicated without shortening them",
    );
    assert.equal(posted.length, 0);

    await act(async () => {
      mounted.render(React.createElement(ThreadPane, {
        workroom: linkedRoom,
        session: { id: "browser", live: true, actor: "codex", setActor() {} },
        frames: [{
          conversation: "temporary",
          sequence: 0,
          about: rootEvent,
          text: "temporary discussion",
          actor: "claude",
          fingerprint: "claude",
          seen: 1,
          raw: { Conversation: "temporary", Sequence: 0, Payload: "", ActorKey: "" },
        }],
        target: { kind: "event", event: rootEvent },
        pending: [],
        onClose() {},
        onJumpTo() {},
        onOpenProfile() {},
        onRoute() {},
        doAct() {},
        onSay() { return "pending"; },
        onSayFailed() {},
        onOpenThread() {},
      }));
    });

    const linkButtons = [...document.querySelectorAll('[aria-label="link to draft"]')];
    assert.equal(linkButtons.length, 2, "only durable child events offer Link; the automatic root and temporary message do not");
    assert.equal(document.querySelector('[data-conversation="temporary"] [aria-label*="link"]'), null);

    await click(linkButtons[0]);
    assert.equal(posted.length, 0, "linking only edits the visible draft");
    assert.equal(linkButtons[0].getAttribute("aria-pressed"), "true");
    assert.equal(linkButtons[0].getAttribute("aria-label"), "remove link from draft");
    let chips = document.querySelector('[aria-label="Linked draft items"]');
    assert.match(chips.textContent, /I will review the link/);
    assert.equal(chips.querySelectorAll('button[aria-label^="remove link to"]').length, 1);

    await click(linkButtons[1]);
    chips = document.querySelector('[aria-label="Linked draft items"]');
    assert.equal(chips.querySelectorAll('button[aria-label^="remove link to"]').length, 2, "multiple selected events stay visible");

    await enterText(document.querySelector('[aria-label="thread reply"]'), "First kept reply");
    await click(document.querySelector('[aria-label="keep reply"]'));
    await act(async () => { await Promise.resolve(); });
    assert.deepEqual(posted[0].rests_on, [rootEvent, firstEvent, branchEvent]);
    assert.equal(document.querySelector('[aria-label="Linked draft items"]'), null, "successful submission clears the linked draft items");

    await click(linkButtons[0]);
    await click(linkButtons[1]);
    await click(document.querySelector(`button[aria-label="remove link to #2 · I will review the link"]`));
    assert.equal(linkButtons[0].getAttribute("aria-pressed"), "false");
    assert.equal(linkButtons[0].getAttribute("aria-label"), "link to draft");
    await enterText(document.querySelector('[aria-label="thread reply"]'), "Reply after unlinking");
    await click(document.querySelector('[aria-label="keep reply"]'));
    await act(async () => { await Promise.resolve(); });
    assert.deepEqual(posted[1].rests_on, [rootEvent, branchEvent], "unlinking removes only that exact basis before submission");
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => { mounted.unmount(); });
    await vite.close();
  }
});
