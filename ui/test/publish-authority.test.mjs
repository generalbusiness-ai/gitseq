// Who is offered a state write, asserted on the screen.
//
// Publishing an artifact is a state write. `decideState` in
// internal/workroom/fold.go refuses a post-genesis state record whose signer
// is not a participant:
//
//   if record.index > 0 && !f.hasActor(record.record.Actor) { ... Ineffective }
//
// so a screen that gates the publish control on presence alone offers an act
// the fold refuses. Presence is advisory and session-bound: a departed
// principal still holds its key, can still open a session, and would then be
// invited to append a permanently ineffective row to an append-only log. The
// two questions are separate and both must be yes — a live session to sign
// with, and live participation for the signature to mean anything.
//
// The last case here pins the opposite rule. Superseding an act you authored
// yourself is decided by `decideSupersede` on
// `target.record.Actor == record.record.Actor` with no `hasActor` test, and
// docs/reference/architecture.md says so: "A departed actor may still
// supersede an earlier act they authored". The toolbar's `withdraw` is that
// act. Gating it would be a false refusal, so a later tightening of the
// membership test must not reach it.
//
// Everything below drives the real components in a DOM and reads the rendered
// controls, because the defect this replaces was a control on a screen and not
// a value in an array.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

// A real origin, because the top bar reads a browser-local read position and
// `localStorage` throws on an opaque one.
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.localStorage = dom.window.localStorage;
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
const { buildRecordIndex } = await import("../src/lib/records.ts");

const AT = 1_700_000_000;
const ME = "hugh-fingerprint";

const labelled = (label) => document.querySelector(`[aria-label="${label}"]`);

// The publish control as a person meets it: a button that says "publish".
// Offered means present and pressable; a disabled button is a refusal shown
// with its reason, which is the pattern the rest of this screen keeps.
function publishControl() {
  return [...document.querySelectorAll("button")].find((button) => button.textContent.trim() === "publish");
}

// The viewer's own roster entry is the only knob. `roles` is what the fold
// projects; `retired` is how it marks a principal whose every membership grant
// was superseded — still listed, because its signatures are permanent, and
// holding nothing.
function room({ roles, retired }) {
  const statements = [
    { event: "req", sequence: 1, actor: ME, kind: "request", text: "Record the decision.", timestamp: AT, body: { to: "codex" } },
  ];
  const projection = {
    decisions: statements.map((item) => ({ event: item.event, sequence: item.sequence, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements,
    commitments: [{ request: "req", requester: ME, addressed_to: "codex", status: "open", waiting_on: "codex" }],
    artifacts: [],
    actors: {
      [ME]: { name: "hugh", kind: "human", roles, retired, role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      codex: { name: "codex", kind: "agent", roles: ["participant"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    },
    provenance: {},
  };
  return {
    actors: [{ name: "hugh", fingerprint: ME, roles: [], custody: true }],
    commits: [],
    graphTruncated: false,
    offline: false,
    localOffline: false,
    status: {
      durable: {
        genesis: "genesis",
        head: "head",
        depth: statements.length,
        projection,
        vocabulary: {
          definitions: [
            { name: "request", satisfier: "none", render: "commitment" },
          ],
        },
      },
      live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

const sessionWith = (live) => ({
  credential: "browser",
  actor: "hugh",
  live,
  setActor() {},
  activity: { status: "available", focus: [] },
  setActivity() {},
});

async function mountTopBar(vite, root, workroom, live) {
  const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
  await act(async () => {
    root.render(
      React.createElement(TopBar, {
        workroom,
        session: sessionWith(live),
        onJumpEvent() {},
        onPublish() {},
      }),
    );
  });
}

async function mountThread(vite, root, workroom, at) {
  const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
  await act(async () => {
    root.render(
      React.createElement(Thread, {
        index: buildRecordIndex(workroom.status.durable.projection),
        workroom,
        session: sessionWith(true),
        frames: [],
        root: at,
        pending: [],
        onBack() {},
        onOpenThread() {},
        onSay: () => "",
        onSayFailed() {},
        doAct() {},
      }),
    );
  });
}

async function withScreen(body) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ branch: "main", commits: [] }) });
  try {
    await body(vite, root);
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => root.unmount());
    await vite.close();
  }
}

test("publish is offered to a live participant who is present, and to nobody else", async () => {
  await withScreen(async (vite, root) => {
    // Both answers yes. Withholding here would be the silent denial in the
    // other direction, which is its own harm: the fold would accept this.
    await mountTopBar(vite, root, room({ roles: ["participant"] }), true);
    const offered = publishControl();
    assert.ok(offered, "false denial: a present live participant was offered no publish control at all");
    assert.equal(
      offered.disabled,
      false,
      "false denial: the fold would accept an artifact from a present live participant, but the control is disabled",
    );

    // The security case. Departed — listed with `retired: true` and holding no
    // roles — but with a live session, because presence is session-bound and a
    // departed principal keeps its key. `decideState` refuses this record for
    // ever, so the screen must not invite it.
    await mountTopBar(vite, root, room({ roles: [], retired: true }), true);
    const departed = publishControl();
    assert.ok(
      departed === undefined || departed.disabled === true,
      "false offer: a departed actor with a live session was offered publish, and the fold refuses that state record as ineffective",
    );

    // Membership is not presence. A participant with no session has nothing to
    // sign with, so presence stays a condition rather than being replaced.
    await mountTopBar(vite, root, room({ roles: ["participant"] }), false);
    const absent = publishControl();
    assert.ok(
      absent === undefined || absent.disabled === true,
      "false offer: a live participant with no session was offered publish, but there is no session to sign with",
    );
  });
});

// The documented exception, pinned so that tightening the membership test
// cannot quietly remove it. `decideSupersede` returns Effective when the
// target's actor is the record's actor, with no `hasActor` test at all, and
// the architecture reference states the rule. Guarding `withdraw` would be a
// refusal the fold does not make.
test("withdraw stays offered to a departed author on a record they authored", async () => {
  await withScreen(async (vite, root) => {
    await mountThread(vite, root, room({ roles: [], retired: true }), "req");
    assert.ok(
      labelled("withdraw"),
      "false denial: a departed actor may still supersede an act they authored, but the screen withheld withdraw",
    );
  });
});
