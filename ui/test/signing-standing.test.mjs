// What actually reaches the wire, asserted on a rendered pane.
//
// The signing-boundary unit tests next door call `signingRefusal` directly.
// That is a real check of the rule and it is blind to the only thing that
// makes the rule matter: whether the code that signs actually asks it. Codex
// found this exactly here -- the guard shipped correct in the helper and
// bypassed at the wiring, and every test stayed green. So these tests never
// call the helper. They render the real component, drive the real control,
// and count POSTs to `/v0/act`, which is where `api.act` goes.
//
// Each test carries a POSITIVE control that asserts exactly one call under
// good standing. Without it "zero calls" proves nothing: a pane that failed to
// render, a control that was never found, and a guard working correctly all
// produce the same zero.
//
// The mutation in each case happens AFTER the control has been offered, which
// is the whole point of guarding at the boundary rather than at the button. A
// composer outlives the authority that opened it.
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
dom.window.Element.prototype.scrollTo = function scrollTo() {};
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.localStorage = dom.window.localStorage;
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

// App reads the chosen actor from storage at mount, so the join gate is
// already answered and the pane renders the room rather than the picker.
localStorage.setItem("workroom.actor", "hugh");

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");
const { buildRecordIndex } = await import("../src/lib/records.ts");

const AT = 1_700_000_000;
const ME = "hugh-fingerprint";
const PATH = "docs/decisions/0001-use-postgres.md";
const COMMIT = "0123456789abcdef0123456789abcdef01234567";

const click = (element) =>
  act(async () => {
    element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });

const typeInto = (element, value) =>
  act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value").set;
    setter.call(element, value);
    element.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });

// Enter in the composer's textarea, which is a second way to reach `send` and
// the only one the boundary is the sole guard on. See the lease test below.
const pressEnter = (element) =>
  act(async () => {
    element.dispatchEvent(
      new dom.window.KeyboardEvent("keydown", { key: "Enter", shiftKey: false, bubbles: true, cancelable: true }),
    );
  });

const labelled = (label) => document.querySelector(`[aria-label="${label}"]`);

// Every POST to /v0/act, in order. This is the wire, not a module spy: a
// bypass that reached `api.act` by another route would still be counted.
let filed = [];

function workroomAround(projection, definitions) {
  return {
    actors: [{ name: "hugh", fingerprint: ME, roles: [], custody: true }],
    commits: [],
    graphTruncated: false,
    offline: false,
    localOffline: false,
    status: {
      durable: { genesis: "genesis", head: "head", depth: projection.statements.length, projection, vocabulary: { definitions } },
      live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

// `roles` is the viewer's roster entry. `verdict` and `retired` are the
// standing of the record the pane is opened on, which is what the ratify guard
// must read and did not.
function room({ roles = ["participant"], verdict = "effective", retired = false } = {}) {
  const statements = [
    { event: "req", sequence: 1, actor: "codex", kind: "request", text: "Record the decision.", timestamp: AT, body: { to: ME } },
    { event: "art", sequence: 2, actor: "codex", kind: "artifact", text: "the decision record", timestamp: AT, body: { path: PATH, commit: COMMIT }, retired },
  ];
  const projection = {
    decisions: [
      { event: "req", sequence: 1, verdict: "effective", reason: "recorded" },
      { event: "art", sequence: 2, verdict, reason: "recorded" },
    ],
    acts: [],
    statements,
    commitments: [{ request: "req", requester: "codex", addressed_to: ME, status: "open", waiting_on: ME }],
    artifacts: [{ event: "art", path: PATH, commit: COMMIT }],
    actors: { [ME]: { name: "hugh", kind: "human", roles, role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} } },
    provenance: { art: ["req"] },
  };
  return workroomAround(projection, [
    { name: "propose", satisfier: "role:participant", render: "proposal" },
    { name: "artifact", satisfier: "none", render: "artifact" },
    { name: "request", satisfier: "none", render: "commitment" },
  ]);
}

async function mount(vite, root, workroom, at, { live = true } = {}) {
  const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
  const projection = workroom.status.durable.projection;
  await act(async () => {
    root.render(
      React.createElement(Thread, {
        index: buildRecordIndex(projection),
        workroom,
        session: { credential: "browser", actor: "hugh", live, setActor() {}, activity: { status: "available", focus: [] }, setActivity() {} },
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

async function withPane(body) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  filed = [];
  globalThis.fetch = async (url, options) => {
    const path = typeof url === "string" ? url : String(url);
    // Exact paths first: `/v0/actors` contains `/v0/act`.
    if (path.includes("/v0/actors")) return { ok: true, json: async () => [] };
    if (path.includes("/v0/act")) {
      filed.push(JSON.parse(options?.body ?? "{}"));
      return { ok: true, json: async () => ({ id: "filed" }) };
    }
    return { ok: true, json: async () => ({ branch: "main", commits: [] }) };
  };
  try {
    await body(vite, root);
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => root.unmount());
    await vite.close();
  }
}

// Open the composer on the artifact row and write a line into it. Returns the
// send control, which is the last thing between the operator and `api.act`.
async function compose(vite, root, workroom, { live = true } = {}) {
  await mount(vite, root, workroom, "art", { live });
  const opener = labelled("propose adoption");
  assert.ok(opener, "the pane never offered the control this test is about");
  await click(opener);
  const box = labelled("thread reply");
  assert.ok(box, "the composer never opened");
  await typeInto(box, "Adopt the decision record.");
  const send = labelled("keep reply");
  assert.ok(send, "the composer never entered its durable mode");
  return send;
}

// The positive control for the whole file. If this ever stops filing exactly
// one record, every "zero calls" assertion below has become vacuous and is
// passing for the wrong reason.
test("a live participant's reply does reach the wire", async () => {
  await withPane(async (vite, root) => {
    const send = await compose(vite, root, room({ roles: ["participant"] }));
    await click(send);
    assert.equal(filed.length, 1, "a participant with a live lease must be able to file: the guard is refusing lawful work");
    assert.equal(filed[0].act, "state");
  });
});

// Membership moves under an open composer. The seven ordinary Toolbar routes
// ask membership before opening it; this test withdraws membership after that
// offer, so the signing boundary is the last thing standing between a departed
// signer and a permanently ineffective row.
test("membership lost after the control is offered files nothing", async () => {
  await withPane(async (vite, root) => {
    const send = await compose(vite, root, room({ roles: ["participant"] }));
    // The grant is superseded while the composer sits open. Re-rendering with
    // the new roster is what the running page does when the projection
    // arrives; the composer's own state survives it, which is the point.
    await mount(vite, root, room({ roles: [] }), "art");
    const stale = labelled("keep reply") ?? send;
    await click(stale);
    assert.equal(filed.length, 0, "false offer: the fold refuses post-genesis state from a non-participant, and the browser signed it anyway");
  });
});

// The positive control for the two Enter-driven cases below: the same
// invocation path, under good standing, files exactly one record. Without it,
// "Enter filed nothing" is also what a keydown that never reached the handler
// looks like.
test("Enter in the composer does reach the wire under a live lease", async () => {
  await withPane(async (vite, root) => {
    await compose(vite, root, room({ roles: ["participant"] }));
    await pressEnter(labelled("thread reply"));
    assert.equal(filed.length, 1, "Enter is a real path to send: if it stops filing, the lease case below proves nothing");
    assert.equal(filed[0].act, "state");
  });
});

// The lease expires under an open composer. Distinct from membership: this one
// is about there being a session to sign with at all.
//
// It is driven from the textarea's Enter handler and NOT from the send button,
// and that is the whole substance of this test. The button carries
// `disabled={busy || !text.trim() || !session.live || !ready}`, so once the
// lease lapses the browser drops the click before any of our code runs: a
// clicked button files nothing whether the final guard is present, bypassed,
// or deleted, and the assertion passes for a reason that has nothing to do with
// what it claims. `onKeyDown` on the textarea has no such gate -- it calls
// `send()` directly, exactly as the running page does -- so past that point
// `signingRefusal` at the signing boundary is the only thing left between an
// expired lease and a filed record.
test("a lease that expires after the control is offered files nothing", async () => {
  await withPane(async (vite, root) => {
    await compose(vite, root, room({ roles: ["participant"] }));
    await mount(vite, root, room({ roles: ["participant"] }), "art", { live: false });
    await pressEnter(labelled("thread reply"));
    assert.equal(filed.length, 0, "no lease, no signature, and the boundary is where that has to be enforced");
  });
});

// ---------------------------------------------------------------------------
// The defect itself, through the boundary that actually signs a ratification.
//
// `canRatify` in Toolbar.tsx is `mayRatify` and nothing else: it reads the
// satisfier and the roster, and asks neither whether the fold ruled the target
// effective nor whether it has been retired. So the control IS offered on a
// retired or ineffective record -- that is not a hypothetical, it is what the
// screen does today -- and `doAct` in App.tsx is the only thing between that
// button and a permanently ineffective row in an append-only log.
//
// `doAct` is a prop that App builds and hands down, so this mounts App itself.
// A test that rendered Thread with its own `doAct` would be asserting against
// a stub of the very code under test.

const status = (projection) => ({
  durable: {
    genesis: "genesis",
    head: "head",
    depth: projection.statements.length,
    projection,
    vocabulary: {
      definitions: [
        { name: "request", satisfier: "none", render: "commitment" },
        { name: "promise", satisfier: "none", render: "commitment" },
        { name: "report", satisfier: "originating-requester", render: "commitment" },
      ],
    },
  },
  live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
  cursor: { frontier: [], live: { generation: "generation", position: 1 } },
});

// A reported commitment the viewer is the requester of, which is the shape the
// list actually surfaces and the shape the `accept` control answers:
// `doAct(..., { act: "ratify", target: commitment.report })`. Only the
// REPORT's standing changes between the cases below.
function reportedRoom({ verdict = "effective", retired = false, said = "Done." } = {}) {
  const statements = [
    { event: "req", sequence: 1, actor: ME, kind: "request", text: "Record the decision.", timestamp: AT, body: { to: "codex" } },
    { event: "prom", sequence: 2, actor: "codex", kind: "promise", text: "I will do it.", timestamp: AT },
    { event: "rep", sequence: 3, actor: "codex", kind: "report", text: said, timestamp: AT, satisfier: "originating-requester", retired },
  ];
  return {
    decisions: [
      { event: "req", sequence: 1, verdict: "effective", reason: "recorded" },
      { event: "prom", sequence: 2, verdict: "effective", reason: "recorded" },
      { event: "rep", sequence: 3, verdict, reason: "recorded" },
    ],
    acts: [],
    statements,
    commitments: [
      { request: "req", requester: ME, addressed_to: "codex", performer: "codex", promise: "prom", report: "rep", status: "reported", waiting_on: ME },
    ],
    artifacts: [],
    actors: {
      [ME]: { name: "hugh", kind: "human", roles: ["participant"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      codex: { name: "codex", kind: "agent", roles: ["participant"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    },
    provenance: { prom: ["req"], rep: ["prom"] },
  };
}

// `body` is handed `deliver`, which swaps the projection under whatever is on
// screen. It is not a re-render with different props: it goes through the one
// wait loop that drives the whole page (`useWorkroom` in lib/store.ts), which
// is how a running browser learns that a target was retired or ruled
// ineffective while a control for it sat under the operator's cursor. Mounting
// the bad projection from the start, which these tests used to do, tests a page
// that was never in good standing — and so never tests the only thing the
// boundary exists for, which is authority moving after the offer.
async function withApp(projection, body) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const root = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  let current = projection;
  // The resolver for the parked long poll, held so a test can answer it.
  let release;
  filed = [];
  globalThis.fetch = async (url, options) => {
    const path = typeof url === "string" ? url : String(url);
    const reply = (value) => ({ ok: true, json: async () => value });
    // `/v0/actors` contains `/v0/act` as a substring, so the exact paths are
    // matched first. Matched the other way round, the actor roster is counted
    // as a filed record and every assertion in this file reads a number that
    // has nothing to do with what was signed.
    if (path.includes("/v0/actors")) return reply([{ name: "hugh", fingerprint: ME, roles: ["participant"], custody: true }]);
    if (path.includes("/v0/presence")) return reply({ credential: "leased", change: null });
    if (path.includes("/v0/status")) return reply(status(current));
    if (path.includes("/v0/act")) {
      filed.push(JSON.parse(options?.body ?? "{}"));
      return reply({ id: "filed" });
    }
    if (path.includes("/v0/worktrees")) return reply({ repo: "", worktrees: [] });
    // The long poll, parked. Resolving it immediately spins the page's own
    // loop hot enough to starve the test; parking it on a timer instead keeps
    // the timer alive and holds the whole test file open for its duration.
    // A promise that never settles does neither. It is parked on a resolver
    // rather than on nothing so that `deliver` below can answer it, which is
    // the page's own route for a projection that arrived after the render.
    if (path.includes("/v0/wait")) return new Promise((resolve_) => { release = resolve_; });
    return reply({ branch: "main", commits: [] });
  };
  // Answer the parked poll with a new projection, and let the page settle. The
  // assertion that the page was actually waiting is load-bearing: if the loop
  // had stopped, this would return without changing anything and every test
  // below would quietly go back to asserting against the projection it mounted.
  const deliver = async (next) => {
    current = next;
    const resume = release;
    release = undefined;
    assert.ok(resume, "the page is not waiting on the projection: nothing would deliver the change these tests turn on");
    await act(async () => {
      resume({ ok: true, json: async () => ({ status: status(next) }) });
      await new Promise((resolve_) => setTimeout(resolve_, 200));
    });
  };
  try {
    const { default: App } = await vite.ssrLoadModule("/src/App.tsx");
    await act(async () => {
      root.render(React.createElement(App));
      await new Promise((resolve_) => setTimeout(resolve_, 200));
    });
    await body(deliver);
  } finally {
    // Unmount BEFORE restoring fetch: the session's cleanup departs its lease,
    // and the real fetch cannot resolve a relative path.
    await act(async () => root.unmount());
    globalThis.fetch = previousFetch;
    await vite.close();
  }
}

// Walk from the list into the commitment's thread, then hand back the ratify
// control the toolbar drew. The walk is a loop rather than a fixed sequence
// because the list renders as its own loops settle.
async function acceptControl() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const accept = labelled("accept");
    if (accept) return accept;
    const row = [...document.querySelectorAll("tr, li")].find((node) => node.textContent?.includes("Record the decision"));
    const target = row?.querySelector("button, [role=\"button\"], a") ?? row;
    if (target) await click(target);
    await act(async () => {
      await new Promise((resolve_) => setTimeout(resolve_, 50));
    });
  }
  return null;
}

test("App files a ratification when the target's standing is good", async () => {
  // The positive control for the two below, and it carries a `deliver` of its
  // own on purpose. The two negative tests click AFTER a projection arrives, so
  // a control that a delivery quietly detached would file nothing there for a
  // reason that has nothing to do with the guard. This proves a delivery leaves
  // a working control behind: the projection changes, standing stays good, and
  // the click still files exactly one.
  await withApp(reportedRoom(), async (deliver) => {
    const agree = await acceptControl();
    assert.ok(agree, "the pane never offered the ratify control this test is about");
    await deliver(reportedRoom({ said: "Done, and checked." }));
    const still = labelled("accept") ?? agree;
    await click(still);
    assert.equal(filed.length, 1, "false refusal: an effective, unretired target with a satisfied role must be ratifiable");
    assert.equal(filed[0].act, "ratify");
  });
});

// The two cases the boundary exists for. Standing is GOOD when the control is
// drawn, and moves afterwards -- which is the only shape in which the button
// and the signature can disagree, and so the only shape that tests the
// signature. Mounting the bad projection from the start, as these did, meant
// the offer and the click read the same facts and the guard at the boundary was
// never the thing under test.

test("App files nothing when the ratify target is retired after the control is offered", async () => {
  await withApp(reportedRoom(), async (deliver) => {
    const agree = await acceptControl();
    assert.ok(agree, "the pane never offered the ratify control under the good standing this test starts from");
    // The report is retired while the control sits under the cursor.
    await deliver(reportedRoom({ retired: true }));
    // The control being offered is the defect's other half, and it is checked
    // rather than tolerated: if the toolbar ever starts hiding it, this test
    // must be rewritten rather than passing for a new reason. Checking it also
    // keeps the zero below honest -- a control that vanished would file nothing
    // whatever the guard did.
    const still = labelled("accept");
    assert.ok(still, "canRatify does not read retirement, so the control is expected to still be offered here");
    await click(still);
    assert.equal(filed.length, 0, "false offer: decideRatify refuses \"retired statement cannot be ratified\", and the browser signed it anyway");
  });
});

test("App files nothing when the ratify target loses its effective ruling after the control is offered", async () => {
  await withApp(reportedRoom(), async (deliver) => {
    const agree = await acceptControl();
    assert.ok(agree, "the pane never offered the ratify control under the good standing this test starts from");
    await deliver(reportedRoom({ verdict: "ineffective" }));
    const still = labelled("accept");
    assert.ok(still, "canRatify does not read the decision, so the control is expected to still be offered here");
    await click(still);
    assert.equal(filed.length, 0, "false offer: decideRatify refuses \"ratify target is not effective\", and the browser signed it anyway");
  });
});
