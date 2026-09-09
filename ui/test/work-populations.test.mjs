// The browser's half of the cross-language count gate; the Go half is
// internal/wireparity/work_populations_test.go, and why the populations have
// one owner is in internal/workroom/work.go. This file holds the board to that
// owner's answer on a frozen projection both languages read.
//
// The counts here come from the selector the tabs actually use — workRows,
// once per population — not from a second rule written to agree with Go.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));
const fixture = JSON.parse(readFileSync(new URL("./fixtures/work-populations.json", import.meta.url), "utf8"));
const projection = fixture.durable.projection;
const vocabulary = fixture.durable.vocabulary;

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

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const context = {
  nameOf: (fingerprint) => projection.actors[fingerprint]?.name ?? fingerprint,
  tickets: new Map(),
  actors: projection.actors,
};

async function rowsModule() {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const module = await vite.ssrLoadModule("/src/lib/rows.ts");
  return { module, close: () => vite.close() };
}

// The board's answer, in the shape the Go owner puts on the wire. Every
// population count is the length of the row list that tab renders, so a
// grouping change in rows.ts moves these numbers.
function boardCounts({ workRows }, source = projection) {
  const rowsIn = (population) => workRows(source, context, population);
  const open = rowsIn("live");
  // Which lifecycle each open row came from, read back through the row key
  // the selector assigns: the promise, else the report, else the request.
  const byKey = new Map(source.commitments.map((commitment) => [
    commitment.promise ?? commitment.report ?? commitment.request,
    commitment.status,
  ]));
  const lifecycles = {
    open: 0, promised: 0, reported: 0, "awaiting-review": 0, "awaiting-authorization": 0, "awaiting-landing": 0,
  };
  for (const row of open) lifecycles[byKey.get(row.key)] += 1;
  return {
    scope: "workroom",
    commitments: source.commitments.length,
    open: open.length,
    open_lifecycles: lifecycles,
    completed: rowsIn("done").length,
    closed_not_completed: rowsIn("closed").length,
    stale: rowsIn("stale").length,
    reasoning_moved: rowsIn("moved").length,
    artifact_landing_audit: rowsIn("approved").length,
  };
}

test("the board's row selector reaches the same named populations as the Go owner", async () => {
  const { module, close } = await rowsModule();
  try {
    assert.deepEqual(boardCounts(module), fixture.work);
    // The partition adds up, and the two subsets are outside it.
    const { open, completed, closed_not_completed: closed, stale, commitments } = fixture.work;
    assert.equal(open + completed + closed + stale, commitments);
    assert.equal(Object.values(fixture.work.open_lifecycles).reduce((a, b) => a + b, 0), open);
    assert.ok(fixture.work.reasoning_moved <= open);
    // The landing audit falls on a live row and on a closed one, so it is an
    // audit across the board rather than a seventh population.
    assert.equal(fixture.work.artifact_landing_audit, 2);
  } finally {
    await close();
  }
});

// The negative control the request asked for: drop one awaiting-review member
// and the comparison must fail. A parity check nobody has watched fail is a
// parity check nobody knows is running.
test("dropping an awaiting-review member fails the parity check", async () => {
  const { module, close } = await rowsModule();
  try {
    const index = projection.commitments.findIndex((commitment) => commitment.status === "awaiting-review");
    assert.notEqual(index, -1, "the fixture has no awaiting-review commitment; the control proves nothing");
    const damaged = {
      ...projection,
      commitments: projection.commitments.filter((_, position) => position !== index),
    };
    const counts = boardCounts(module, damaged);
    assert.notDeepEqual(counts, fixture.work);
    assert.equal(counts.open, fixture.work.open - 1);
    assert.equal(counts.open_lifecycles["awaiting-review"], fixture.work.open_lifecycles["awaiting-review"] - 1);
  } finally {
    await close();
  }
});

// The other half of the control: one surface regrouping awaiting-review out of
// the open population is caught rather than absorbed.
test("a surface that groups awaiting-review outside open fails the parity check", async () => {
  const { module, close } = await rowsModule();
  try {
    const counts = boardCounts(module);
    const awaiting = counts.open_lifecycles["awaiting-review"];
    assert.ok(awaiting > 0);
    const regrouped = {
      ...counts,
      open: counts.open - awaiting,
      closed_not_completed: counts.closed_not_completed + awaiting,
      open_lifecycles: { ...counts.open_lifecycles, "awaiting-review": 0 },
    };
    assert.notDeepEqual(regrouped, fixture.work);
  } finally {
    await close();
  }
});

// Ratification is a different duty, owed by a role holder. It has its own
// queue on the board and enters no commitment count on any surface.
test("an act awaiting ratification is counted beside the commitments, never inside them", async () => {
  const { module, close } = await rowsModule();
  try {
    const { rows, ratifiers } = module.ratificationRows(projection, vocabulary, context);
    assert.equal(rows.length, 1);
    assert.deepEqual(rows.map((row) => row.title), ["Adopt the shared Work count"]);
    assert.deepEqual(ratifiers.map(context.nameOf), ["alice"]);
    const counts = boardCounts(module);
    assert.equal(counts.commitments, fixture.work.commitments);
    assert.equal(counts.open + counts.completed + counts.closed_not_completed + counts.stale, counts.commitments);
  } finally {
    await close();
  }
});

// A searched board is a different question from a workroom-wide one, and the
// numbers it shows must not be read as the counts the command line prints.
test("a search scopes the counts and the screen says so", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  try {
    const { workRows, matchingRows } = await vite.ssrLoadModule("/src/lib/rows.ts");
    const open = workRows(projection, context, "live");
    const matching = matchingRows(open, "counter");
    assert.ok(matching.length > 0 && matching.length < open.length, "the search must select some rows and not all");

    const { RequestList } = await vite.ssrLoadModule("/src/components/RequestList.tsx");
    const room = {
      actors: [],
      commits: [],
      graphTruncated: false,
      offline: false,
      localOffline: false,
      status: {
        durable: fixture.durable,
        live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
        cursor: { frontier: [], live: { generation: "generation", position: 1 } },
      },
    };
    // The list's view belongs to its caller, the way App holds it.
    function Host() {
      const [view, setView] = React.useState({ query: "", population: "live" });
      return React.createElement(RequestList, { workroom: room, view, onView: setView, onOpenThread() {} });
    }
    const type = (element, value) =>
      act(async () => {
        const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value").set;
        setter.call(element, value);
        element.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
      });

    await act(async () => root.render(React.createElement(Host)));
    const tabs = () => [...document.querySelectorAll('[role="tab"]')].slice(0, 7);
    // The rendered tab counts, in a real DOM, are the numbers the Go owner
    // publishes for this frontier.
    assert.deepEqual(tabs().map((tab) => tab.textContent), [
      `open${fixture.work.open}`,
      `reasoning moved${fixture.work.reasoning_moved}`,
      `artifact landing audit${fixture.work.artifact_landing_audit}`,
      `stale, not in flight${fixture.work.stale}`,
      `completed${fixture.work.completed}`,
      `closed, not completed${fixture.work.closed_not_completed}`,
      "awaiting ratification1",
    ]);
    assert.equal(document.querySelector("h2").textContent, `${fixture.work.open} open requests`);

    // Typed into the real search box, in a real DOM.
    await type(document.querySelector('[aria-label="Search requests"]'), "counter");
    assert.equal(
      document.querySelector("h2").textContent,
      `${matching.length} open requests, matching your search — ${fixture.work.open} in the workroom`,
    );
    // The tab counts moved with the search; the headline is where the screen
    // says so, and it names the workroom-wide number the counts came from.
    assert.notEqual(tabs()[0].textContent, `open${fixture.work.open}`);
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
});
