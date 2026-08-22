// Two conditions of the thread pane are only observable in a browser: what a
// click does, and what an effect does when the pane is pointed at a different
// thread. Static rendering runs neither, so this file drives the pane in a
// DOM. Everything testable without one stays in components.test.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

// A url is required for a real origin: without one jsdom refuses localStorage,
// which the notification read position needs.
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.MouseEvent = dom.window.MouseEvent;
// The notification read position is browser-local, so the panel only behaves
// correctly when there is somewhere to remember it.
globalThis.localStorage = dom.window.localStorage;
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
            selectedEvent: "event-one",
            onJumpEvent() {},
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
          onJumpEvent() {},
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


// WorkDrawer renders the focus marker at two sites: the list row and the board
// card. Breaking the shared FocusActors body reddens tests, but that only shows
// the component is used somewhere — removing the board site alone left the whole
// suite green. This drives the real presentation toggle so the board site is
// pinned on its own.
// The main-view selector chooses Work or Activity and nothing else. It used to
// repeat the global active count and the overlapping attention figure, both of
// which already sit beside the filters they describe in the Work view — the
// same number in two places, only one of which can be acted on. Nothing tested
// that they were there, so nothing would notice them coming back.

// WorkDrawer is the single visible owner of the lifecycle filter counts: the
// main-view selector deliberately carries none, so if a count stops rendering
// here it stops being shown anywhere. Only Active was pinned, and removing
// count={counts.attention} and count={counts.closed} left the whole suite
// green — the two figures could vanish silently. Condition 6 of the request.
//
// The counts are deliberately non-zero and distinct from each other. FilterCheck
// renders {count ?? 0}, so an unwired prop still renders a span containing "0";
// only asserting the exact value catches that, and only distinct values catch a
// count wired to the wrong filter.
const countingRoom = () => {
  const room = structuredClone(workroom);
  const commitment = (request, status, stale) => ({ request, requester: "codex-fingerprint", performer: "codex-fingerprint", status, ...(stale ? { stale: true } : {}) });
  room.status.durable.projection.statements = [];
  room.status.durable.projection.decisions = [];
  room.status.durable.projection.provenance = {};
  // active 2, closed 3, attention 4 — attention overlaps both, which is why it
  // is a qualifier rather than a lifecycle status.
  room.status.durable.projection.commitments = [
    commitment("a", "open", true),
    commitment("b", "promised", true),
    commitment("c", "satisfied", true),
    commitment("d", "withdrawn", true),
    commitment("e", "cancelled", false),
  ];
  return room;
};


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

// ---------------------------------------------------------------------------
// The two screens. Sorting and expanding are only observable in a browser: a
// static render runs neither click.
// ---------------------------------------------------------------------------

const NOW_S = 1787000000;

function listRoom() {
  const request = (event, text, ts) => ({ event, sequence: Number(event.slice(1)), actor: "hugh", kind: "request", text, timestamp: ts });
  const statements = [
    request("e1", "Zebra work", NOW_S - 5 * 86400),
    request("e2", "Alpha work", NOW_S - 2 * 86400),
    request("e3", "Middle work", NOW_S - 86400),
  ];
  const projection = {
    decisions: statements.map((item) => ({ event: item.event, sequence: item.sequence, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements,
    commitments: [
      { request: "e1", requester: "hugh", performer: "claude", promise: "p1", status: "promised", waiting_on: "claude" },
      { request: "e2", requester: "hugh", addressed_to: "codex", status: "open" },
      { request: "e3", requester: "hugh", performer: "claude", promise: "p3", report: "r3", status: "reported", waiting_on: "hugh" },
    ],
    reviews: [],
    artifacts: [],
    actors: {
      hugh: { name: "hugh", kind: "human", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      claude: { name: "claude", kind: "agent", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      codex: { name: "codex", kind: "agent", roles: [], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    },
    provenance: {},
  };
  return {
    actors: [
      { name: "hugh", fingerprint: "hugh", roles: [], custody: true },
      { name: "claude", fingerprint: "claude", roles: [], custody: true },
      { name: "codex", fingerprint: "codex", roles: [], custody: true },
    ],
    offline: false,
    status: {
      durable: { genesis: "genesis", head: "head", depth: 3, projection },
      live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

const titlesOnScreen = () => [...document.querySelectorAll("tbody tr")].map((row) => row.cells[3].textContent);
const headerNamed = (label) =>
  [...document.querySelectorAll("thead th button")].find((button) => button.textContent.trim().startsWith(label));

test("clicking a column sorts, clicking again reverses, and a third click restores priority order", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    await act(async () => {
      root.render(React.createElement(RequestList, { workroom: listRoom(), onOpenThread() {} }));
    });

    // Priority: unclaimed, then waiting on a human, then running.
    const priority = titlesOnScreen();
    assert.deepEqual(priority, ["Alpha work", "Middle work", "Zebra work"]);
    assert.match(document.body.textContent, /sorted by priority/);
    // No ordering subheads: the state column says which group a row is in.
    assert.equal(document.querySelectorAll("tbody tr").length, 3);

    await click(headerNamed("title"));
    assert.deepEqual(titlesOnScreen(), ["Alpha work", "Middle work", "Zebra work"]);
    assert.equal(headerNamed("title").closest("th").getAttribute("aria-sort"), "ascending");

    await click(headerNamed("title"));
    assert.deepEqual(titlesOnScreen(), ["Zebra work", "Middle work", "Alpha work"]);
    assert.equal(headerNamed("title").closest("th").getAttribute("aria-sort"), "descending");

    await click(headerNamed("title"));
    assert.deepEqual(titlesOnScreen(), priority, "a third click did not restore priority order");
    assert.equal(headerNamed("title").closest("th").getAttribute("aria-sort"), "none");
    // A sort reorders the rows that are there; it never removes one.
    assert.equal(document.querySelectorAll("tbody tr").length, 3);
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

test("exactly one number heads the list, and each other number opens to its own rows", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    const workroom = listRoom();
    const projection = workroom.status.durable.projection;
    projection.commitments[0].stale = true;
    projection.statements.push({ event: "e9", sequence: 9, actor: "hugh", kind: "request", text: "Abandoned work", timestamp: NOW_S - 40 * 86400 });
    projection.decisions.push({ event: "e9", sequence: 9, verdict: "effective", reason: "recorded" });
    projection.commitments.push({ request: "e9", requester: "hugh", status: "stale" });

    await act(async () => {
      root.render(React.createElement(RequestList, { workroom, onOpenThread() {} }));
    });
    const heading = document.querySelector("h2");
    assert.equal(heading.textContent, "3 open requests");
    assert.equal(document.querySelectorAll("tbody tr").length, 3);

    const summaries = [...document.querySelectorAll("p button")];
    assert.deepEqual(summaries.map((button) => button.textContent), [
      "1 of these rest on reasoning that has moved.",
      "1 stale requests, not in flight.",
    ]);

    await click(summaries[1]);
    assert.equal(document.querySelector("h2").textContent, "1 stale request, not in flight");
    assert.deepEqual(titlesOnScreen(), ["Abandoned work"]);

    await click([...document.querySelectorAll("p button")][0]);
    assert.equal(document.querySelector("h2").textContent, "1 resting on reasoning that has moved");
    assert.deepEqual(titlesOnScreen(), ["Zebra work"]);
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

test("clicking a row opens that request's thread", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const opened = [];
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    await act(async () => {
      root.render(React.createElement(RequestList, { workroom: listRoom(), onOpenThread: (event) => opened.push(event) }));
    });
    await click(document.querySelector("tbody tr"));
    assert.deepEqual(opened, ["e2"]);
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

function threadRoom() {
  const at = NOW_S - 7 * 86400;
  const statements = [
    { event: "req", sequence: 1, actor: "codex", kind: "request", text: "re-cut the repair", timestamp: at, body: { to: "claude" } },
    { event: "promise", sequence: 2, actor: "claude", kind: "promise", text: "Claimed.", timestamp: at },
    { event: "art", sequence: 3, actor: "claude", kind: "artifact", text: "old artifact", timestamp: at, retired: true },
    { event: "report", sequence: 4, actor: "claude", kind: "report", text: "ready", timestamp: at, body: { status: "ready-for-review", head: "acf134411781f16ec16817ab7e084a104acb0fac" } },
    { event: "verdict", sequence: 5, actor: "codex", kind: "report", text: "APPROVED", timestamp: at, body: { verdict: "approved", head: "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d" } },
    { event: "note", sequence: 6, actor: "hugh", kind: "assert", text: "a passing remark", timestamp: at },
  ];
  const projection = {
    decisions: statements.map((item) => ({ event: item.event, sequence: item.sequence, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements,
    commitments: [{ request: "req", requester: "codex", addressed_to: "claude", performer: "claude", promise: "promise", report: "report", status: "reported", waiting_on: "codex" }],
    reviews: [{ report: "verdict", reviewer: "codex", verdict: "approved", head: "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d", independence: "independent", ratified: true }],
    artifacts: [],
    actors: {},
    provenance: { promise: ["req"], art: ["promise"], report: ["promise"], verdict: ["report"], note: ["report"] },
  };
  return {
    actors: [{ name: "codex", fingerprint: "codex", roles: [], custody: true }],
    offline: false,
    status: {
      durable: { genesis: "genesis", head: "head", depth: 6, projection },
      live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

test("the thread draws one rail of salient stations and keeps its history collapsed", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const asked = [];
  const previousFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    asked.push(JSON.parse(init.body));
    return {
      ok: true,
      json: async () => ({
        branch: "main",
        commits: [{ commit: "b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d", status: "landed", merge: "44d8b4fa7d62f04d9b240434e8c044eddc00b496", time: NOW_S - 7 * 86400 }],
      }),
    };
  };
  try {
    const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
    await act(async () => {
      root.render(
        React.createElement(Thread, {
          workroom: threadRoom(),
          session: { credential: "browser", actor: "codex", live: true, setActor() {}, activity: { status: "available", focus: [] }, setActivity() {} },
          frames: [],
          root: "req",
          pending: [],
          onBack() {}, onOpenThread() {}, onSay: () => "", onSayFailed() {}, doAct() {},
        }),
      );
    });

    // One rail, one node per row, five stations and one blocker branch.
    const stations = [...document.querySelectorAll("[data-station]")];
    assert.deepEqual(stations.map((row) => row.dataset.station), ["request", "promise", "report", "verdict", "merge", "blocker-open"]);
    // The merge station asked git rather than reading a stored field.
    assert.deepEqual(asked, [{ commits: ["b3bf30833b93aaec5fc3adab7ffa0b6f0fe7792d"] }]);
    assert.match(document.body.textContent, /landed on main/);
    assert.match(document.body.textContent, /shipped but never closed/);

    // History is behind explicit expansion, and every expander says its count.
    const expanders = [...document.querySelectorAll("[aria-expanded]")];
    assert.ok(expanders.length > 0, "no expanders rendered");
    for (const expander of expanders) assert.equal(expander.getAttribute("aria-expanded"), "false");
    assert.doesNotMatch(document.body.textContent, /old artifact/);
    assert.doesNotMatch(document.body.textContent, /a passing remark/);

    const repair = expanders.find((button) => button.textContent.includes("Repair chain"));
    assert.match(repair.textContent, /Repair chain1/);
    await click(repair);
    assert.equal(repair.getAttribute("aria-expanded"), "true");
    assert.match(document.body.textContent, /old artifact/);
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => root.unmount());
    await vite.close();
  }
});

// The notification surface. "N for you" was a counter that jumped you to the
// oldest unread act, so the reader could not see what the number stood for
// and could not choose among them. It is now a list hanging off your own
// face. Both halves — the bell that says there is something, and the row that
// takes you to one particular thing — are click-wired, so only a DOM can see
// them work.

const forYouRoom = () => {
  const room = structuredClone(workroom);
  room.actors = [
    { name: "codex", fingerprint: "codex-fingerprint", roles: [], custody: true },
    { name: "claude", fingerprint: "claude-fingerprint", roles: [], custody: true },
  ];
  room.status.durable.projection.statements = [
    { event: "ask-one", actor: "claude-fingerprint", kind: "request", text: "Repair the citation anchors", body: { to: "codex-fingerprint" }, timestamp: 10 },
    { event: "ask-two", actor: "claude-fingerprint", kind: "assert", text: "Mentioning you about the gate", body: { mentions: "codex-fingerprint" }, timestamp: 20 },
    { event: "not-mine", actor: "claude-fingerprint", kind: "request", text: "Somebody else's work", body: { to: "claude-fingerprint" }, timestamp: 30 },
  ];
  room.status.durable.projection.decisions = [
    { event: "ask-one", verdict: "effective", reason: "recorded" },
    { event: "ask-two", verdict: "effective", reason: "recorded" },
    { event: "not-mine", verdict: "effective", reason: "recorded" },
  ];
  room.status.durable.projection.commitments = [];
  room.status.durable.projection.provenance = {};
  room.status.live.presence = {};
  room.status.live.activity = {};
  return room;
};

const mountTopBar = async (root, room, jumped) => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
  await act(async () => {
    root.render(
      React.createElement(TopBar, {
        workroom: room,
        session: { actor: "codex", activity: { status: "available", focus: [] }, setActivity() {} },
        onJumpEvent: (event) => jumped.push(event),
      }),
    );
  });
  return vite;
};

test("notifications hang off your own avatar and open as a clickable list", async () => {
  localStorage.clear();
  const root = createRoot(document.getElementById("root"));
  const jumped = [];
  let vite;
  try {
    vite = await mountTopBar(root, forYouRoom(), jumped);

    // The old counter is gone: no separate button announcing a number.
    assert.equal(buttonByText((text) => /for you$/.test(text)), undefined, "the standalone for-you counter still renders");

    // Your own chip carries the count, and opens a menu rather than jumping.
    const chip = document.querySelector('button[aria-haspopup="menu"]');
    assert.ok(chip, "the identity chip is not a menu button");
    assert.equal(chip.getAttribute("aria-expanded"), "false", "the panel started open");
    assert.match(chip.getAttribute("title"), /2 for you/, "the chip does not say how many are waiting");
    // A bell on your own face is the whole signal that something is waiting.
    assert.ok(chip.querySelector(".lucide-bell"), "the avatar carries no bell while notifications are waiting");
    assert.match(chip.textContent, /2/, "the bell badge does not carry the count");
    assert.equal(jumped.length, 0, "rendering jumped somewhere on its own");

    await click(chip);
    const panel = document.querySelector('[role="menu"]');
    assert.ok(panel, "clicking the avatar did not open the panel");

    // Every notification is listed, named by who sent it and why. The request
    // addressed to somebody else is not mine and must not appear.
    const rows = [...panel.querySelectorAll('[role="menuitem"]')];
    assert.equal(rows.length, 2, "the panel did not list one row per notification");
    assert.match(rows[0].textContent, /claude asked you/, "a request to me is not shown as asked");
    assert.match(rows[0].textContent, /Repair the citation anchors/, "the row does not carry the record's title");
    assert.match(rows[1].textContent, /claude mentioned you/, "a mention is not shown as a mention");
    assert.doesNotMatch(panel.textContent, /Somebody else's work/, "the panel listed an act addressed to another actor");
  } finally {
    await act(async () => root.unmount());
    await vite?.close();
  }
});

test("opening one notification jumps to that one and leaves the others unread", async () => {
  localStorage.clear();
  const root = createRoot(document.getElementById("root"));
  const jumped = [];
  let vite;
  try {
    vite = await mountTopBar(root, forYouRoom(), jumped);
    await click(document.querySelector('button[aria-haspopup="menu"]'));

    // Choose the SECOND row. A single watermark would bury the first one as a
    // side effect; the reader chose one act, so only that act is read.
    const rows = [...document.querySelectorAll('[role="menuitem"]')];
    await click(rows[1]);
    assert.deepEqual(jumped, ["ask-two"], "clicking a row did not open that row's event");
    assert.equal(document.querySelector('[role="menu"]'), null, "the panel stayed open after a choice");

    await click(document.querySelector('button[aria-haspopup="menu"]'));
    const left = [...document.querySelectorAll('[role="menuitem"]')];
    assert.equal(left.length, 1, "reading the second notification did not leave exactly one unread");
    assert.match(left[0].textContent, /Repair the citation anchors/, "the surviving row is not the one left unread");
  } finally {
    await act(async () => root.unmount());
    await vite?.close();
  }
});

test("mark all read empties the list and the bell goes quiet", async () => {
  localStorage.clear();
  const root = createRoot(document.getElementById("root"));
  const jumped = [];
  let vite;
  try {
    vite = await mountTopBar(root, forYouRoom(), jumped);
    await click(document.querySelector('button[aria-haspopup="menu"]'));
    await click(buttonByText((text) => text === "mark all read"));

    assert.equal(document.querySelectorAll('[role="menuitem"]').length, 0, "marking all read left rows in the list");
    assert.match(document.querySelector('[role="menu"]').textContent, /Nothing addressed to you is unread/);
    const quiet = document.querySelector('button[aria-haspopup="menu"]');
    assert.match(quiet.getAttribute("title"), /nothing for you/);
    assert.equal(quiet.querySelector(".lucide-bell"), null, "the bell stayed lit with nothing unread");
    assert.equal(jumped.length, 0, "marking read navigated somewhere");
  } finally {
    await act(async () => root.unmount());
    await vite?.close();
  }
});
