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
import test, { mock } from "node:test";
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
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
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

// ─────────────────────────────────────────────────────────────────────────────
// Authority is a fact about now, not about the moment the dialog opened.
//
// The test above asks the question at the control that opens the dialog. That
// is where the answer is first needed and not where it has to hold: the dialog
// outlives the check that opened it. A lease expires on its own after thirty
// seconds without a heartbeat, and a membership grant can be superseded by
// somebody else while a form sits on screen half filled. Neither event touches
// the dialog, and before this test nothing rechecked — so the submit stayed
// offered, and pressing it signed a state record `decideState` refuses for
// ever, appending the permanently ineffective row this whole predicate exists
// to prevent.
//
// So this drives the real `App` against a stubbed resident and moves the world
// underneath an open dialog, twice: the session goes absent while membership
// holds, then membership is withdrawn while the session is live. Both are
// asked of the screen — is submit disabled — and of the boundary that actually
// signs.
//
// The boundary is called directly, past the disabled button, on purpose. A
// disabled control stops a click and stops nothing else; the question this
// test exists to answer is what happens when something asks `App` to publish
// anyway, which is what a stale render, a queued event or a future caller is.
// Reaching the handler through the fiber is the only way to ask it from
// outside, and it is asked with a fully valid input so that nothing but the
// authority check can refuse it. The last case publishes for real against the
// same stub, so that "refused" here means the authority and not a test that
// would refuse everything.
const PUBLISHED_PATH = "docs/decisions/0002-ask-the-fold.md";
const PUBLISHED_COMMIT = "89abcdef0123456789abcdef0123456789abcdef";
const PUBLISHED_TEXT = "Decision record drafted for adoption";

const jsonResponse = (body, ok = true) => ({
  ok,
  status: ok ? 200 : 401,
  statusText: ok ? "OK" : "Unauthorized",
  json: async () => body,
});

const dialogForm = () => document.querySelector('[role="dialog"][aria-labelledby="publish-title"]');
// The top bar's opener, told apart from the dialog's own submit — both say
// "publish", which is the point: the same word for the same act.
const opener = () => [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "publish" && b.type !== "submit");
const submitControl = () => dialogForm()?.querySelector('button[type="submit"]');
const field = (label) => dialogForm().querySelector(`input[aria-label="${label}"]`);
// What the dialog says about itself: the standing reason, and the refusal a
// submission got back.
const reasonShown = () => dialogForm()?.querySelector('[role="status"]')?.textContent.trim();
const refusalShown = () => dialogForm()?.querySelector('[role="alert"]')?.textContent.trim();

// The handler `App` hands the dialog: the function the submit button would
// have called. `onClose` is required alongside `onPublish` so this cannot pick
// up the top bar's opener, which takes no argument and would silently pass.
//
// Read from the committed tree — `root.current` — and not from the fiber
// hanging off the form element. React keeps two fibers per node and the one
// reachable from the DOM is whichever was built last, so its props can be a
// render behind: reading it here returned the handler from *before* the
// authority was lost, which is the very state this test exists to catch. The
// committed tree is by definition the one whose props produced what is on the
// screen, so it is the only honest place to ask what the button would call.
function signingBoundary(root) {
  const seen = [root._internalRoot.current];
  while (seen.length > 0) {
    const fiber = seen.pop();
    const props = fiber.memoizedProps;
    if (props && typeof props.onPublish === "function" && typeof props.onClose === "function") return props.onPublish;
    if (fiber.sibling) seen.push(fiber.sibling);
    if (fiber.child) seen.push(fiber.child);
  }
  throw new Error("no publish handler in the committed tree");
}

test("publish stops the moment authority does, at the dialog and at the boundary that signs", async () => {
  const signed = [];
  let leased = true;
  let seat = { roles: ["participant"] };
  let parked = [];

  const previousFetch = globalThis.fetch;
  const previousActor = localStorage.getItem("workroom.actor");
  globalThis.fetch = async (input, init) => {
    const path = String(input).replace(/^https?:\/\/[^/]*/, "");
    if (path === "/v0/status") return jsonResponse(room(seat).status);
    // The long poll: parked until the test moves the world.
    if (path === "/v0/wait") return new Promise((resolve) => parked.push(resolve));
    if (path === "/v0/actors") return jsonResponse(room(seat).actors);
    if (path === "/v0/worktrees") return jsonResponse({ repo: "", worktrees: [] });
    // The lease. A resident that no longer knows this credential answers with
    // exactly this error, which is what `renewCredential` forgets a credential
    // on, and what turns the session absent.
    if (path === "/v0/presence") {
      return leased ? jsonResponse({ credential: "browser", change: null }) : jsonResponse({ error: "credential is not valid" }, false);
    }
    if (path === "/v0/act") {
      signed.push(JSON.parse(init.body));
      return jsonResponse({ id: "signed-event" });
    }
    return jsonResponse({});
  };

  localStorage.setItem("workroom.actor", "hugh");
  // Only the heartbeat's interval is under the test's control. `setTimeout`
  // stays real, because React's own scheduling runs on it.
  mock.timers.enable({ apis: ["setInterval"] });

  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));

  // One turn of everything the page waits on: the heartbeat that renews or
  // loses the lease, and the status poll that carries a new roster.
  const pump = () =>
    act(async () => {
      mock.timers.tick(15_000);
      const waiting = parked;
      parked = [];
      waiting.forEach((resolve) => resolve(jsonResponse({ status: room(seat).status })));
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  const until = async (what, predicate, attempts = 60) => {
    for (let attempt = 0; attempt < attempts; attempt += 1) {
      if (predicate()) return;
      await pump();
    }
    throw new Error(`timed out waiting for ${what}`);
  };
  const click = (element) =>
    act(async () => {
      element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
    });
  const type = (element, value) =>
    act(async () => {
      Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value").set.call(element, value);
      element.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    });
  const askBoundary = () =>
    act(async () => {
      await signingBoundary(root)({ path: PUBLISHED_PATH, commit: PUBLISHED_COMMIT, text: PUBLISHED_TEXT });
    });

  try {
    const { default: App } = await vite.ssrLoadModule("/src/App.tsx");
    await act(async () => {
      root.render(React.createElement(App));
    });

    // PRESENT AND A PARTICIPANT. The dialog opens and takes a complete,
    // publishable form, so that every refusal below is about authority and
    // never about an unfinished field.
    await until("the top bar to offer publishing", () => opener() && !opener().disabled);
    await click(opener());
    await until("the dialog", () => dialogForm());
    await type(field("what this artifact records"), PUBLISHED_TEXT);
    await type(field("path"), PUBLISHED_PATH);
    await type(field("commit"), PUBLISHED_COMMIT);
    assert.equal(submitControl().disabled, false, "a filled form from a present live participant must be submittable");

    // ABSENT. The lease lapses under the open dialog. Membership is untouched:
    // this actor is still a participant and has simply stopped being here.
    leased = false;
    await until("the lapsed lease to reach the dialog", () => submitControl()?.disabled === true);
    assert.ok(dialogForm(), "the dialog stays open: losing authority closes the submit, it does not throw the typing away");
    assert.equal(
      reasonShown(),
      "not present yet",
      "the dialog must say which authority went missing, and this one is the session rather than the membership",
    );
    await askBoundary();
    assert.equal(
      signed.length,
      0,
      "false offer: App signed a state record for an absent session, and the fold refuses that record as ineffective",
    );
    assert.equal(
      refusalShown(),
      "not filed: not present yet",
      "a refused publish must say so on the screen rather than failing silently",
    );

    // Back, so that the next case is a departure and not a leftover absence.
    leased = true;
    await until("the lease to be renewed", () => submitControl()?.disabled === false);

    // DEPARTED, WITH A LIVE SESSION. The security case: every membership grant
    // superseded, still listed because the signatures are permanent, and the
    // key still opens a session. `decideState` refuses this signer for ever.
    seat = { roles: [], retired: true };
    await until("the departure to reach the dialog", () => submitControl()?.disabled === true);
    assert.equal(
      reasonShown(),
      "not a live participant: the fold would refuse this artifact",
      "membership is not presence: this refusal is the roster's, and the session is still live",
    );
    await askBoundary();
    assert.equal(
      signed.length,
      0,
      "false offer: App signed a state record for a departed actor, and the fold refuses that record as ineffective",
    );
    assert.equal(
      refusalShown(),
      "not filed: not a live participant: the fold would refuse this artifact",
      "a refused publish must say so on the screen rather than failing silently",
    );

    // AND THE OTHER DIRECTION. Restored to what it was at the start, the same
    // boundary signs — so nothing above is a check that refuses everything.
    seat = { roles: ["participant"] };
    await until("the restored roster to reach the dialog", () => submitControl()?.disabled === false);
    await askBoundary();
    assert.equal(signed.length, 1, "false denial: the boundary must sign for a present live participant");
    assert.equal(signed[0].kind, "artifact");
    assert.equal(signed[0].body.path, PUBLISHED_PATH);
    assert.equal(signed[0].body.commit, PUBLISHED_COMMIT);
  } finally {
    await act(async () => root.unmount());
    mock.timers.reset();
    await vite.close();
    globalThis.fetch = previousFetch;
    if (previousActor === null) localStorage.removeItem("workroom.actor");
    else localStorage.setItem("workroom.actor", previousActor);
  }
});
