// The notification surface. "N for you" was a counter that jumped you to the
// oldest unread act, so the reader could not see what the number stood for
// and could not choose among them. It is now a list hanging off your own
// avatar. Both halves, the badge that says something is waiting and the row
// that takes you to one particular thing, are click-wired, so only a DOM can
// see them work. A url is required for a real origin: without one jsdom
// refuses localStorage, which the read position needs.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
globalThis.localStorage = dom.window.localStorage;

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

function click(element) {
  return act(async () => {
    element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

const buttonByText = (match) => [...document.querySelectorAll("button")].find((button) => match(button.textContent.trim()));

// Three acts: a request to me, a mention of me, and a request to somebody
// else that must never appear as mine. Tickets follow decision order.
function room(statements) {
  return {
    actors: [
      { name: "codex", fingerprint: "codex-fingerprint", roles: [], custody: true },
      { name: "claude", fingerprint: "claude-fingerprint", roles: [], custody: true },
    ],
    offline: false,
    status: {
      durable: {
        genesis: "genesis",
        head: "head",
        depth: statements.length,
        projection: {
          decisions: statements.map((statement, index) => ({ event: statement.event, sequence: index + 1, verdict: "effective", reason: "recorded" })),
          acts: [],
          statements,
          commitments: [],
          artifacts: [],
          actors: {},
          provenance: {},
        },
      },
      live: { cursor: { generation: "gen", position: 1 }, presence: {}, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "gen", position: 1 } },
    },
  };
}

const addressed = () =>
  room([
    { event: "ask-one", actor: "claude-fingerprint", kind: "request", text: "Repair the citation anchors", body: { to: "codex-fingerprint" }, timestamp: 10 },
    { event: "ask-two", actor: "claude-fingerprint", kind: "assert", text: "Mentioning you about the gate", body: { mentions: "codex-fingerprint" }, timestamp: 20 },
    { event: "not-mine", actor: "claude-fingerprint", kind: "request", text: "Somebody else's work", body: { to: "claude-fingerprint" }, timestamp: 30 },
  ]);

async function withTopBar(workroom, run) {
  localStorage.clear();
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const jumped = [];
  try {
    const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
    await act(async () => {
      root.render(
        React.createElement(TopBar, {
          workroom,
          session: { actor: "codex", activity: { status: "available", focus: [] }, setActivity() {} },
          onJumpEvent: (event) => jumped.push(event),
          onPublish() {},
        }),
      );
    });
    await run(jumped);
  } finally {
    await act(async () => root.unmount());
    await vite.close();
  }
}

test("the for-you count opens a list of what is addressed to you, newest first", async () => {
  await withTopBar(addressed(), async (jumped) => {
    // The old standalone counter is gone; the identity chip carries the count
    // and opens a menu rather than jumping anywhere.
    assert.equal(buttonByText((text) => /for you$/.test(text)), undefined, "the standalone for-you counter still renders");
    const chip = document.querySelector('button[aria-haspopup="menu"]');
    assert.ok(chip, "the identity chip is not a menu button");
    assert.equal(chip.getAttribute("aria-expanded"), "false", "the panel started open");
    assert.match(chip.getAttribute("title"), /2 for you/, "the chip does not say how many are waiting");
    assert.ok(chip.querySelector(".lucide-bell"), "the avatar carries no bell while notifications are waiting");
    assert.equal(jumped.length, 0, "rendering jumped somewhere on its own");

    await click(chip);
    const panel = document.querySelector('[role="menu"]');
    assert.ok(panel, "clicking the chip did not open the panel");
    const rows = [...panel.querySelectorAll('[role="menuitem"]')];
    assert.equal(rows.length, 2, "the panel did not list one row per notification");
    // Newest first: the mention (#2) above the request (#1).
    assert.match(rows[0].textContent, /claude mentioned you/, "the newest act is not first");
    assert.match(rows[0].textContent, /Mentioning you about the gate/);
    assert.match(rows[1].textContent, /claude asked you/, "a request to me is not shown as asked");
    assert.match(rows[1].textContent, /Repair the citation anchors/, "the row does not carry the record's title");
    assert.doesNotMatch(panel.textContent, /Somebody else's work/, "the panel listed an act addressed to another actor");
  });
});

test("opening one row jumps to that thread and leaves the others unread", async () => {
  await withTopBar(addressed(), async (jumped) => {
    await click(document.querySelector('button[aria-haspopup="menu"]'));
    // Choose the newest row first. A single watermark would bury the older
    // one as a side effect; the reader chose one act, so only that act is read.
    const rows = [...document.querySelectorAll('[role="menuitem"]')];
    await click(rows[0]);
    assert.deepEqual(jumped, ["ask-two"], "clicking a row did not open that row's event");
    assert.equal(document.querySelector('[role="menu"]'), null, "the panel stayed open after a choice");

    await click(document.querySelector('button[aria-haspopup="menu"]'));
    const left = [...document.querySelectorAll('[role="menuitem"]')];
    assert.equal(left.length, 1, "reading the newest notification did not leave exactly one unread");
    assert.match(left[0].textContent, /Repair the citation anchors/, "the surviving row is not the older one");
    assert.match(document.querySelector('button[aria-haspopup="menu"]').getAttribute("title"), /1 for you/);
  });
});

test("mark all read empties the list and the bell goes quiet", async () => {
  await withTopBar(addressed(), async (jumped) => {
    await click(document.querySelector('button[aria-haspopup="menu"]'));
    await click(buttonByText((text) => text === "mark all read"));
    assert.equal(document.querySelectorAll('[role="menuitem"]').length, 0, "marking all read left rows in the list");
    assert.match(document.querySelector('[role="menu"]').textContent, /Nothing addressed to you is unread/);
    const quiet = document.querySelector('button[aria-haspopup="menu"]');
    assert.match(quiet.getAttribute("title"), /nothing for you/);
    assert.equal(quiet.querySelector(".lucide-bell"), null, "the bell stayed lit with nothing unread");
    assert.equal(jumped.length, 0, "marking read navigated somewhere");
  });
});

test("with nothing addressed to you the list says so plainly", async () => {
  await withTopBar(room([{ event: "not-mine", actor: "claude-fingerprint", kind: "request", text: "Somebody else's work", body: { to: "claude-fingerprint" }, timestamp: 30 }]), async () => {
    const chip = document.querySelector('button[aria-haspopup="menu"]');
    assert.match(chip.getAttribute("title"), /nothing for you/);
    assert.equal(chip.querySelector(".lucide-bell"), null, "a bell lit with nothing addressed");
    await click(chip);
    assert.match(document.querySelector('[role="menu"]').textContent, /Nothing is addressed to you\./);
    assert.equal(document.querySelectorAll('[role="menuitem"]').length, 0);
  });
});

test("the panel closes on Escape and on a click outside it", async () => {
  await withTopBar(addressed(), async () => {
    const chip = document.querySelector('button[aria-haspopup="menu"]');
    await click(chip);
    assert.ok(document.querySelector('[role="menu"]'));
    await act(async () => {
      document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    assert.equal(document.querySelector('[role="menu"]'), null, "Escape did not close the panel");
    await click(chip);
    await act(async () => {
      document.body.dispatchEvent(new dom.window.MouseEvent("mousedown", { bubbles: true }));
    });
    assert.equal(document.querySelector('[role="menu"]'), null, "a click outside did not close the panel");
  });
});
