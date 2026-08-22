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
      hugh: { name: "hugh", kind: "human", roles: ["ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
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
      durable: {
        genesis: "genesis",
        head: "head",
        depth: 3,
        projection,
        vocabulary: {
          definitions: [
            { name: "propose", satisfier: "role:ratifier", render: "proposal" },
            { name: "assert", satisfier: "role:ratifier", render: "note" },
            { name: "request", satisfier: "originating-requester", render: "commitment" },
          ],
        },
      },
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
    // A proposal is not a commitment, so it can only ever reach the screen
    // through a population of its own. The assert beside it names the ratifier
    // role too and must not be counted.
    projection.statements.push({ event: "e10", sequence: 10, actor: "codex", kind: "propose", text: "Bounded status", timestamp: NOW_S - 3 * 86400 });
    projection.statements.push({ event: "e11", sequence: 11, actor: "codex", kind: "assert", text: "A note", timestamp: NOW_S - 3 * 86400 });
    projection.decisions.push({ event: "e10", sequence: 10, verdict: "effective", reason: "recorded" });
    projection.decisions.push({ event: "e11", sequence: 11, verdict: "effective", reason: "recorded" });

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
      "1 act awaits ratification.",
    ]);

    await click(summaries[1]);
    assert.equal(document.querySelector("h2").textContent, "1 stale request, not in flight");
    assert.deepEqual(titlesOnScreen(), ["Abandoned work"]);

    await click([...document.querySelectorAll("p button")][0]);
    assert.equal(document.querySelector("h2").textContent, "1 resting on reasoning that has moved");
    assert.deepEqual(titlesOnScreen(), ["Zebra work"]);

    await click([...document.querySelectorAll("p button")][2]);
    assert.equal(document.querySelector("h2").textContent, "1 act awaits ratification");
    assert.deepEqual(titlesOnScreen(), ["Bounded status"]);
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});

// An empty queue and an undischargeable one look identical on screen unless
// the screen says which it is, so the disclosure is the condition, not a
// decoration on it. Deleting it left all 68 tests green: every assertion about
// it was a doesNotMatch on the resolved case, which any missing element
// satisfies. A guard is only pinned by asserting it is present when it should
// be.
test("the ratification queue says when nobody, or nobody in particular, can discharge it", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  let root;
  try {
    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    // A fresh mount per case. The population is component state, so reusing
    // one root means the second click toggles back to the live list instead of
    // opening the queue — which passed silently as "no warning present".
    const show = async (roles) => {
      if (root) await act(async () => root.unmount());
      root = createRoot(document.getElementById("root"));
      const workroom = listRoom();
      const projection = workroom.status.durable.projection;
      projection.actors.hugh.roles = roles.hugh;
      projection.actors.codex.roles = roles.codex;
      projection.statements.push({ event: "e10", sequence: 10, actor: "codex", kind: "propose", text: "Bounded status", timestamp: NOW_S - 3 * 86400 });
      projection.decisions.push({ event: "e10", sequence: 10, verdict: "effective", reason: "recorded" });
      await act(async () => {
        root.render(React.createElement(RequestList, { workroom, onOpenThread() {} }));
      });
      const summaries = [...document.querySelectorAll("p button")];
      await click(summaries[2]);
    };

    await show({ hugh: [], codex: [] });
    assert.match(document.body.textContent, /Nobody in this room holds .ratifier./,
      "an owed act with no ratifier must not read as an empty queue");
    assert.equal(document.querySelectorAll("tbody tr").length, 1,
      "the obligation still exists when nobody can discharge it");
    assert.equal([...document.querySelectorAll("tbody tr")][0].cells[1].textContent, "",
      "with no ratifier the queue names nobody rather than guessing");

    await show({ hugh: ["ratifier"], codex: ["ratifier"] });
    assert.match(document.body.textContent, /2 actors hold .ratifier./,
      "two candidates do not determine a next mover, and the screen must say so");
    assert.equal(document.querySelectorAll("tbody tr").length, 1);
    assert.equal([...document.querySelectorAll("tbody tr")][0].cells[1].textContent, "");

    await show({ hugh: ["ratifier"], codex: [] });
    assert.doesNotMatch(document.body.textContent, /holds .ratifier./,
      "with exactly one ratifier there is nothing to disclose");
    assert.equal([...document.querySelectorAll("tbody tr")][0].cells[1].textContent, "hugh");
  } finally {
    if (root) await act(async () => root.unmount());
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
