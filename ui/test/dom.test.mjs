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
  let rejectNext = false;
  globalThis.fetch = async (url, init = {}) => {
    assert.equal(url, "/v0/act");
    posted.push(JSON.parse(init.body));
    if (rejectNext) {
      rejectNext = false;
      throw new Error("deliberate linked-draft failure");
    }
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
    const [{ ThreadPane }, { durableEventBases }] = await Promise.all([
      vite.ssrLoadModule("/src/components/ThreadPane.tsx"),
      vite.ssrLoadModule("/src/components/Composer.tsx"),
    ]);

    assert.deepEqual(
      durableEventBases([rootEvent], [rootEvent, firstEvent, firstEvent, branchEvent]),
      [rootEvent, firstEvent, branchEvent],
      "automatic and selected bases are de-duplicated without shortening them",
    );
    assert.equal(posted.length, 0);

    const paneProps = {
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
    };
    await act(async () => {
      mounted.render(React.createElement(ThreadPane, paneProps));
    });

    const linkButtons = [...document.querySelectorAll('[aria-label="link to draft"]')];
    assert.equal(linkButtons.length, 2, "only durable child events offer Link; the automatic root and temporary message do not");
    assert.equal(document.querySelector('[data-conversation="temporary"] [aria-label*="link"]'), null);

    const keptToggle = document.querySelector('[aria-label="make reply temporary"]');
    await click(keptToggle);
    assert.equal(keptToggle.getAttribute("aria-label"), "keep reply");
    assert.equal(keptToggle.getAttribute("aria-pressed"), "false");
    await click(linkButtons[0]);
    assert.equal(posted.length, 0, "linking only edits the visible draft");
    assert.equal(keptToggle.getAttribute("aria-label"), "make reply temporary", "linking the shipped Temporary draft promotes it to Kept");
    assert.equal(keptToggle.getAttribute("aria-pressed"), "true");
    assert.equal(linkButtons[0].getAttribute("aria-pressed"), "true");
    assert.equal(linkButtons[0].getAttribute("aria-label"), "remove link from draft");
    let chips = document.querySelector('[aria-label="Linked draft items"]');
    assert.match(chips.textContent, /I will review the link/);
    assert.equal(chips.querySelectorAll('button[aria-label^="remove link to"]').length, 1);

    await enterText(document.querySelector('[aria-label="thread reply"]'), "Promoted kept reply");
    await click(document.querySelector('[aria-label="keep reply"]'));
    await act(async () => { await Promise.resolve(); });
    assert.equal(posted[0].act, "state");
    assert.equal(posted[0].kind, "assert", "the promoted draft is submitted durably");
    assert.deepEqual(posted[0].rests_on, [rootEvent, firstEvent]);
    assert.equal(document.querySelector('[aria-label="Linked draft items"]'), null, "successful submission clears the linked draft items");

    await click(linkButtons[0]);
    await click(linkButtons[1]);
    chips = document.querySelector('[aria-label="Linked draft items"]');
    assert.equal(chips.querySelectorAll('button[aria-label^="remove link to"]').length, 2, "multiple selected events stay visible");

    await enterText(document.querySelector('[aria-label="thread reply"]'), "Retain links after failure");
    rejectNext = true;
    await click(document.querySelector('[aria-label="keep reply"]'));
    await act(async () => { await Promise.resolve(); });
    assert.deepEqual(posted[1].rests_on, [rootEvent, firstEvent, branchEvent]);
    assert.match(document.querySelector('[role="alert"]').textContent, /deliberate linked-draft failure/);
    assert.equal(document.querySelectorAll('[aria-label="Linked draft items"] button[aria-label^="remove link to"]').length, 2, "failed submission retains every linked draft item");

    await click(document.querySelector('[aria-label="keep reply"]'));
    await act(async () => { await Promise.resolve(); });
    assert.deepEqual(posted[2].rests_on, [rootEvent, firstEvent, branchEvent]);
    assert.equal(document.querySelector('[aria-label="Linked draft items"]'), null, "successful submission clears the linked draft items");

    await click(linkButtons[0]);
    await click(linkButtons[1]);
    await click(document.querySelector(`button[aria-label="remove link to #2 · I will review the link"]`));
    assert.equal(linkButtons[0].getAttribute("aria-pressed"), "false");
    assert.equal(linkButtons[0].getAttribute("aria-label"), "link to draft");
    await enterText(document.querySelector('[aria-label="thread reply"]'), "Reply after unlinking");
    await click(document.querySelector('[aria-label="keep reply"]'));
    await act(async () => { await Promise.resolve(); });
    assert.deepEqual(posted[3].rests_on, [rootEvent, branchEvent], "unlinking removes only that exact basis before submission");

    await act(async () => {
      mounted.render(React.createElement(ThreadPane, {
        ...paneProps,
        key: "routed-reply",
        route: { id: "promise-route", mode: "promise", prefill: "Promise reply" },
      }));
    });
    await click(document.querySelector('[aria-label="link to draft"]'));
    assert.notEqual(document.querySelector('[aria-label="Linked draft items"]'), null);
    await click(document.querySelector('[aria-label="cancel reply action"]'));
    assert.equal(document.querySelector('[aria-label="Linked draft items"]'), null, "cancelling a routed reply restores the default draft without stale links");
    assert.notEqual(document.querySelector('[aria-label="make reply temporary"]'), null);
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => { mounted.unmount(); });
    await vite.close();
  }
});

// A rebuilding resident is not a broken one, and for several minutes the
// browser could not tell a reader which it was looking at. This pins what the
// notice must say and must not: that verification is happening, how far it has
// got, and no claim that anything failed or that anything shown is current.
test("the rebuild notice explains verification without implying failure or currentness", async () => {
  const vite = await createServer({ root: uiRoot, server: { middlewareMode: true }, appType: "custom" });
  const mounted = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;

  let report = { running: true, verified: 300, total: 1200 };
  globalThis.fetch = async () => ({ ok: true, statusText: "OK", json: async () => report });

  try {
    const { RebuildNotice } = await vite.ssrLoadModule("/src/components/RebuildNotice.tsx");

    await act(async () => {
      mounted.render(React.createElement(RebuildNotice, { poll: 1000000 }));
    });
    await act(async () => { await Promise.resolve(); });

    const notice = document.querySelector('[role="status"]');
    assert.notEqual(notice, null, "a rebuild in flight showed no notice at all");
    assert.equal(notice.getAttribute("aria-live"), "polite");
    assert.match(notice.textContent, /Verifying durable history/);
    assert.match(notice.textContent, /verified 300 of 1,200/, "the measured count is not shown");
    assert.doesNotMatch(document.body.textContent, /Loading work/, "measured rebuild progress was shown alongside generic loading");

    const bar = document.querySelector('[role="progressbar"]');
    assert.equal(bar.getAttribute("aria-valuenow"), "300");
    assert.equal(bar.getAttribute("aria-valuemax"), "1200");
    assert.match(bar.getAttribute("aria-valuetext"), /300 of 1200 records verified/);

    for (const forbidden of [/error/i, /failed/i, /broken/i, /nothing is wrong/i, /up to date/i, /current as of/i]) {
      assert.doesNotMatch(notice.textContent, forbidden, `the notice implies ${forbidden}`);
    }

    // Rev-list enumeration can itself take time. Explain that cold work before
    // claiming a denominator the kernel does not know yet.
    report = { running: true };
    await act(async () => {
      mounted.render(React.createElement(RebuildNotice, { poll: 1000001 }));
    });
    await act(async () => { await Promise.resolve(); });
    assert.match(document.body.textContent, /Preparing to verify durable history/);
    assert.equal(document.querySelector('[role="progressbar"]'), null);

    // Verification can finish before checkpoint persistence and the Workroom
    // fold. Keep the measured rebuild state, but say what remains.
    report = { running: true, verified: 1200, total: 1200 };
    await act(async () => {
      mounted.render(React.createElement(RebuildNotice, { poll: 1000002 }));
    });
    await act(async () => { await Promise.resolve(); });
    assert.match(document.body.textContent, /Preparing the verified work view/);
    assert.doesNotMatch(document.body.textContent, /Loading work/);

    // Warm: show only the ordinary brief loading state, not a finished bar.
    report = { running: false };
    await act(async () => {
      mounted.render(React.createElement(RebuildNotice, { poll: 1000003 }));
    });
    await act(async () => { await Promise.resolve(); });
    assert.equal(document.querySelector('[role="progressbar"]'), null, "a warm resident still showed a progress bar");
    assert.match(document.body.textContent, /Loading work/);
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => { mounted.unmount(); });
    await vite.close();
  }
});

// Advisory focus and shared selection are wired through click handlers, so a
// source regex can neither see the wiring break nor survive a harmless
// refactor. These drive the real components in a DOM and read what the handler
// was called with. The rendering half of focus is pinned behaviourally in
// components.test.mjs; only the click half needs a browser.

const focusRoom = () => {
  const room = structuredClone(workroom);
  room.status.durable.projection.statements = [
    { event: "event-one", actor: "codex-fingerprint", kind: "request", text: "repair the UI", body: { to: "codex-fingerprint" }, timestamp: 10 },
  ];
  room.status.durable.projection.decisions = [{ event: "event-one", verdict: "effective", reason: "recorded" }];
  room.status.durable.projection.commitments = [
    { request: "event-one", requester: "codex-fingerprint", addressed_to: "codex-fingerprint", status: "open" },
  ];
  room.status.durable.projection.provenance = { "event-one": [] };
  room.status.live.presence = { "handle:codex": "codex (codex-fingerprin)" };
  room.status.live.activity = { "handle:codex": { status: "blocked", focus: ["event-one"], note: "waiting on review" } };
  return room;
};

const buttonByText = (match) =>
  [...document.querySelectorAll("button")].find((button) => match(button.textContent.trim()));

test("the focus button toggles advisory focus for the selected event", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const calls = [];
  try {
    const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
    const mount = (focus) =>
      act(async () => {
        root.render(
          React.createElement(TopBar, {
            workroom: focusRoom(),
            session: { actor: "codex", activity: { status: "available", focus }, setActivity: (next) => calls.push(next) },
            mainView: "work",
            selection: { kind: "event", id: "event-one" },
            onShowWork() {}, onShowActivity() {}, onJumpEvent() {}, onOpenProfile() {},
          }),
        );
      });

    await mount([]);
    const focusButton = buttonByText((text) => text.startsWith("focus"));
    assert.ok(focusButton, "focus button did not render for a selected event");
    await click(focusButton);
    assert.deepEqual(calls.at(-1), { focus: ["event-one"] }, "focusing did not add the selected event");

    // Focused already: the same button must remove it rather than add it twice.
    await mount(["event-one"]);
    const unfocusButton = buttonByText((text) => text === "unfocus");
    assert.ok(unfocusButton, "unfocus button did not render for a focused selection");
    await click(unfocusButton);
    assert.deepEqual(calls.at(-1), { focus: [] }, "unfocusing did not remove the selected event");
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

test("the clear button empties advisory focus", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const calls = [];
  try {
    const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
    await act(async () => {
      root.render(
        React.createElement(TopBar, {
          workroom: focusRoom(),
          session: {
            actor: "codex",
            activity: { status: "available", focus: ["event-one", "event-two"] },
            setActivity: (next) => calls.push(next),
          },
          mainView: "work",
          onShowWork() {}, onShowActivity() {}, onJumpEvent() {}, onOpenProfile() {},
        }),
      );
    });
    const clear = buttonByText((text) => text === "clear");
    assert.ok(clear, "clear button did not render while focus was held");
    await click(clear);
    assert.deepEqual(calls.at(-1), { focus: [] }, "clear did not empty the held focus");
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

test("opening a work item selects the event and opens its thread", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const selected = [];
  const opened = [];
  try {
    const { WorkView } = await vite.ssrLoadModule("/src/components/WorkDrawer.tsx");
    await act(async () => {
      root.render(
        React.createElement(WorkView, {
          workroom: focusRoom(),
          session: { actor: "codex", activity: { status: "available", focus: [] }, setActivity() {} },
          highlight: { events: new Set(), commits: new Set() },
          onSelect: (selection) => selected.push(selection),
          onOpenThread: (event) => opened.push(event),
        }),
      );
    });
    const row = [...document.querySelectorAll("button")].find((button) => button.textContent.includes("repair the UI"));
    assert.ok(row, "no work row rendered for the open commitment");
    await click(row);
    // Both halves matter: selection drives the rest of the surface, and the
    // thread only opens because the same handler asks for it.
    assert.deepEqual(selected.at(-1), { kind: "event", id: "event-one" }, "opening a work item did not select its event");
    assert.deepEqual(opened.at(-1), "event-one", "opening a work item did not open its thread");
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

// WorkDrawer renders the focus marker at two sites: the list row and the board
// card. Breaking the shared FocusActors body reddens tests, but that only shows
// the component is used somewhere — removing the board site alone left the whole
// suite green. This drives the real presentation toggle so the board site is
// pinned on its own.
test("advisory focus renders on the board card, not only the list row", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  try {
    const { WorkView } = await vite.ssrLoadModule("/src/components/WorkDrawer.tsx");
    await act(async () => {
      root.render(
        React.createElement(WorkView, {
          workroom: focusRoom(),
          session: { actor: "codex", activity: { status: "available", focus: [] }, setActivity() {} },
          highlight: { events: new Set(), commits: new Set() },
          onSelect() {}, onOpenThread() {},
        }),
      );
    });
    const focusMarker = () => document.querySelector('[aria-label^="Focused here:"]');
    assert.ok(focusMarker(), "the list row did not render the focus marker");

    // Switch presentations through the control a reader would use, so this also
    // fails if the toggle stops reaching the board.
    const boardButton = [...document.querySelectorAll("button")].find((button) => button.textContent.trim() === "Board");
    assert.ok(boardButton, "no Board presentation control rendered");
    await click(boardButton);

    assert.ok(focusMarker(), "the board card did not render the focus marker");
    assert.match(focusMarker().getAttribute("aria-label"), /codex \(blocked\)/, "board focus marker named the wrong actor or status");
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

// A durable record carries the committer date as an unbounded int64, so a
// corrupt or hostile one can sit outside the range Date can represent.
// EventTime called new Date(t*1000).toISOString(), which throws RangeError
// past +/-8.64e15 ms; React 19 unmounts the whole tree on an uncaught render
// throw, and a reload replays the same record — so one poisoned event took the
// entire page down permanently. Finding #4 of the 2026-08-11 security
// assessment.
test("an out-of-range timestamp degrades instead of throwing", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  try {
    const { EventTime } = await vite.ssrLoadModule("/src/components/EventTime.tsx");
    const beyondRange = 8.64e15 / 1000 + 1;
    await act(async () => {
      root.render(React.createElement(EventTime, { timestamp: beyondRange }));
    });
    assert.match(document.getElementById("root").textContent, /unreadable time/,
      "an out-of-range timestamp did not degrade to a readable fallback");
    assert.equal(document.querySelector("time"), null,
      "a <time> element was rendered for a date that has no valid dateTime");

    // The ordinary case must keep its machine-readable dateTime.
    await act(async () => {
      root.render(React.createElement(EventTime, { timestamp: 1786000000 }));
    });
    const element = document.querySelector("time");
    assert.ok(element, "a valid timestamp stopped rendering a <time> element");
    assert.ok(element.getAttribute("datetime").startsWith("20"), "the valid case lost its dateTime attribute");
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

// Containment, not prevention: the boundary exists so that a render failure
// nobody predicted costs one subtree rather than the whole session.
test("a render throw is contained by the error boundary", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const consoleError = console.error;
  console.error = () => {};
  try {
    const { ErrorBoundary } = await vite.ssrLoadModule("/src/components/ErrorBoundary.tsx");
    const Boom = () => { throw new Error("poisoned record"); };
    await act(async () => {
      root.render(React.createElement(ErrorBoundary, null, React.createElement(Boom)));
    });
    const text = document.getElementById("root").textContent;
    assert.match(text, /could not be rendered/, "the boundary did not render its fallback");
    assert.match(text, /poisoned record/, "the boundary hid the underlying message");
    assert.ok(document.querySelector('[role="alert"]'), "the fallback is not announced as an alert");
  } finally {
    console.error = consoleError;
    await act(async () => root.unmount());
    await vite.close();
  }
});
