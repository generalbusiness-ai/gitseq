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

// The read position is browser-local, so only storage can say whether it
// survives a reload. Everything above clears storage before mounting and never
// looks at it again, which leaves four separate ways for the feature to stop
// working with every test still green: the mount never loads, reading never
// saves, and the key forgets either the actor or the room. The two tests below
// close those four. They name the key and the stored shape here rather than
// calling `loadForYouRead`/`saveForYouRead`, so a change to either side of the
// storage contract has to be made twice, on purpose.
//
// From `src/lib/memory.ts`: the key is `workroom.foryou.<genesis>.<fingerprint>`
// and the value is JSON `{"watermark": <number>, "read": [<ticket>, ...]}` —
// everything up to the watermark read, plus the tickets read out of order
// above it.
const forYouKey = (genesis, fingerprint) => `workroom.foryou.${genesis}.${fingerprint}`;
const storedPosition = (genesis, fingerprint) => {
  const raw = localStorage.getItem(forYouKey(genesis, fingerprint));
  return raw === null ? null : JSON.parse(raw);
};

// Four acts, tickets in log order. #1 is codex asking claude, so each identity
// has something of its own; #2 to #4 are addressed to codex.
const bothAddressed = () => [
  { event: "for-claude", actor: "codex-fingerprint", kind: "request", text: "Second the merge receipt", body: { to: "claude-fingerprint" }, timestamp: 10 },
  { event: "asked", actor: "claude-fingerprint", kind: "request", text: "Repair the citation anchors", body: { to: "codex-fingerprint" }, timestamp: 20 },
  { event: "mentioned", actor: "claude-fingerprint", kind: "assert", text: "Mentioning you about the gate", body: { mentions: "codex-fingerprint" }, timestamp: 30 },
  { event: "mentioned-again", actor: "claude-fingerprint", kind: "assert", text: "Sealing the review head", body: { mentions: "codex-fingerprint" }, timestamp: 40 },
];

// Mounts the real component repeatedly against the one real localStorage, so a
// remount is the only way a saved position can be seen again.
async function withRemounts(run) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  let root;
  try {
    const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
    const mount = async (actor, genesis = "genesis") => {
      if (root) await act(async () => root.unmount());
      const workroom = room(bothAddressed());
      workroom.status.durable.genesis = genesis;
      root = createRoot(document.getElementById("root"));
      await act(async () => {
        root.render(
          React.createElement(TopBar, {
            workroom,
            session: { actor, activity: { status: "available", focus: [] }, setActivity() {} },
            onJumpEvent() {},
            onPublish() {},
          }),
        );
      });
    };
    await run(mount);
  } finally {
    if (root) await act(async () => root.unmount());
    await vite.close();
  }
}

const chip = () => document.querySelector('button[aria-haspopup="menu"]');
const menuRows = () => [...document.querySelectorAll('[role="menuitem"]')];

test("a saved read position decides what is unread at mount and every reading is written back", async () => {
  localStorage.clear();
  // Read as far as #2 and no further: a position no fresh mount would invent.
  localStorage.setItem(forYouKey("genesis", "codex-fingerprint"), '{"watermark":2,"read":[]}');
  await withRemounts(async (mount) => {
    await mount("codex");
    assert.match(chip().getAttribute("title"), /2 for you/, "the mount did not load the saved read position");
    await click(chip());
    assert.equal(menuRows().length, 2, "the list did not honour the saved watermark");
    assert.doesNotMatch(document.querySelector('[role="menu"]').textContent, /Repair the citation anchors/, "an act below the saved watermark was listed as unread");

    // Read the newest row, leaving an older one unread: the out-of-order case
    // the stored shape exists for.
    await click(menuRows()[0]);
    assert.deepEqual(storedPosition("genesis", "codex-fingerprint"), { watermark: 2, read: [4] }, "reading one row out of order was not persisted");
    await click(chip());
    assert.equal(menuRows().length, 1, "reading one row did not leave exactly one unread");
    assert.match(menuRows()[0].textContent, /Mentioning you about the gate/, "the surviving row is not the older unread one");

    await click(buttonByText((text) => text === "mark all read"));
    assert.deepEqual(storedPosition("genesis", "codex-fingerprint"), { watermark: 4, read: [] }, "mark all read was not persisted");

    // A fresh mount, reading only what storage holds.
    await mount("codex");
    assert.match(chip().getAttribute("title"), /nothing for you/, "the remount did not apply the saved read position");
    await click(chip());
    assert.match(document.querySelector('[role="menu"]').textContent, /Nothing addressed to you is unread/);
  });
});

test("read positions stay apart by room and by actor", async () => {
  localStorage.clear();
  await withRemounts(async (mount) => {
    // Every position here is established through the component, so a key that
    // has forgotten a scope cannot hide behind a seed it would never read.
    await mount("codex");
    await click(chip());
    await click(buttonByText((text) => text === "mark all read"));

    await mount("codex", "other-genesis");
    assert.match(chip().getAttribute("title"), /3 for you/, "another room inherited this room's read position");
    await click(chip());
    await click(buttonByText((text) => text === "mark all read"));

    await mount("claude");
    assert.match(chip().getAttribute("title"), /1 for you/, "another actor inherited this actor's read position");
    await click(chip());
    assert.match(menuRows()[0].textContent, /Second the merge receipt/, "the other actor was shown somebody else's notification");
    await click(buttonByText((text) => text === "mark all read"));

    // Three scopes, three stored positions, each written where only its own
    // mount will look for it.
    assert.deepEqual(
      {
        codexHere: storedPosition("genesis", "codex-fingerprint"),
        codexElsewhere: storedPosition("other-genesis", "codex-fingerprint"),
        claudeHere: storedPosition("genesis", "claude-fingerprint"),
      },
      {
        codexHere: { watermark: 4, read: [] },
        codexElsewhere: { watermark: 4, read: [] },
        claudeHere: { watermark: 1, read: [] },
      },
      "the three scopes did not each keep their own stored position",
    );
  });
});
