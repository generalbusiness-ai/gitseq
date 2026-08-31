// What the operator is shown before they sign, asserted on the screen.
//
// Both conditions here were previously tested one layer too low — against the
// arrays a helper returned rather than the controls a person sees — and both
// defects survived that testing. So everything below drives the real pane in a
// DOM and reads the rendered result.
//
// The two conditions:
//
//   1. A ratification is offered when, and only when, the fold would accept
//      it. The fold judges by the satisfier captured on the target's kind
//      definition at the moment that statement was admitted. A kind redefined
//      afterwards does not change what the fold decides about records already
//      in the log, so it must not change what the screen offers either.
//   2. Every citation the composer will sign is readable in the composer
//      first. Signing a causal reference nobody was shown is a consent
//      defect, not a cosmetic one.
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
const PATH = "docs/decisions/0001-use-postgres.md";
const COMMIT = "0123456789abcdef0123456789abcdef01234567";

function click(element) {
  return act(async () => {
    element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

const labelled = (label) => document.querySelector(`[aria-label="${label}"]`);

// The workroom the pane is handed, around a projection and the kind definitions
// in force. Only the projection differs between the fixtures below, so this is
// everything the pane needs that neither of them is about.
const workroomAround = (projection, definitions) => ({
  actors: [{ name: "hugh", fingerprint: ME, roles: [], custody: true }],
  commits: [],
  graphTruncated: false,
  offline: false,
  localOffline: false,
  status: {
    durable: {
      genesis: "genesis",
      head: "head",
      depth: projection.statements.length,
      projection,
      vocabulary: { definitions },
    },
    live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
    cursor: { frontier: [], live: { generation: "generation", position: 1 } },
  },
});

// One room, two knobs. `admitted` is the satisfier the fold captured on the
// proposal when it admitted it — projected per statement, the way `lifecycle`
// already is. `live` is the satisfier the current vocabulary publishes for
// that same kind. The whole defect lives in the gap between them, so a fixture
// that cannot set them apart cannot see it.
function room({ admitted, live, roles }) {
  const statements = [
    { event: "req", sequence: 1, actor: "codex", kind: "request", text: "Record the decision.", timestamp: AT, body: { to: ME } },
    { event: "art", sequence: 2, actor: "codex", kind: "artifact", text: "the decision record", timestamp: AT, body: { path: PATH, commit: COMMIT } },
    // Ratified, so `request review` has a second event to cite: the case
    // where hiding the citation hides the most.
    { event: "prop", sequence: 3, actor: "codex", kind: "propose", text: "Adopt it.", timestamp: AT, ratified: true, ratified_by: "ratification", satisfier: admitted },
  ];
  const projection = {
    decisions: statements.map((item) => ({ event: item.event, sequence: item.sequence, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements,
    commitments: [{ request: "req", requester: "codex", addressed_to: ME, status: "open", waiting_on: ME }],
    artifacts: [{ event: "art", path: PATH, commit: COMMIT }],
    actors: {
      [ME]: { name: "hugh", kind: "human", roles, role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    },
    provenance: { art: ["req"], prop: ["art"] },
  };
  return workroomAround(projection, [
    { name: "propose", satisfier: live, render: "proposal" },
    { name: "artifact", satisfier: "none", render: "artifact" },
    { name: "request", satisfier: "none", render: "commitment" },
  ]);
}

async function mount(vite, root, workroom, at) {
  const { Thread } = await vite.ssrLoadModule("/src/components/Thread.tsx");
  const projection = workroom.status.durable.projection;
  await act(async () => {
    root.render(
      React.createElement(Thread, {
        index: buildRecordIndex(projection),
        workroom,
        session: { credential: "browser", actor: "hugh", live: true, setActor() {}, activity: { status: "available", focus: [] }, setActivity() {} },
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
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ branch: "main", commits: [] }) });
  try {
    await body(vite, root);
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => root.unmount());
    await vite.close();
  }
}

// The fold reads `target.definition.Satisfier` — the definition bound to the
// target when it was admitted — so a kind redefined later cannot change the
// verdict on a record already in the log. A screen that re-derives the
// satisfier from the live vocabulary disagrees with the fold in both
// directions the moment a kind evolves, and each direction is its own harm:
// one writes a permanently ineffective row into an append-only log, the other
// silently withholds an act the actor is entitled to and says nothing.
test("a ratification is offered by the satisfier admitted with the statement, not the one in force now", async () => {
  await withPane(async (vite, root) => {
    // The kind has NARROWED since admission: admitted as role:steward, now
    // published as role:rider. The viewer holds steward, so the fold would
    // record their ratification as effective.
    await mount(vite, root, room({ admitted: "role:steward", live: "role:rider", roles: ["participant", "steward"] }), "prop");
    assert.ok(
      labelled("agree"),
      "false denial: the fold would accept this ratification on the satisfier admitted with the statement, but the screen offered no control at all",
    );
    assert.ok(labelled("disagree"), "dissent needs no authority and must stay offered");

    // The mirror. The kind has WIDENED since admission: admitted as
    // role:rider, now published as role:steward. The viewer holds only
    // steward, so the fold would refuse — permanently, in an append-only log.
    await mount(vite, root, room({ admitted: "role:rider", live: "role:steward", roles: ["participant", "steward"] }), "prop");
    assert.equal(
      labelled("agree"),
      null,
      "false offer: the screen offered a ratification the fold refuses on the satisfier admitted with the statement",
    );
    assert.ok(labelled("disagree"), "dissent needs no authority and must stay offered");

    // The two cases where the answer does not depend on the disagreement, kept
    // so the fix cannot be mistaken for "always offer".
    await mount(vite, root, room({ admitted: "role:steward", live: "role:steward", roles: ["participant"] }), "prop");
    assert.equal(labelled("agree"), null, "no role, no ratification");

    // Nothing admitted means nothing proved. A client shown no satisfier for a
    // statement does not know what the fold requires, and guessing is how the
    // ineffective rows got written in the first place.
    await mount(vite, root, room({ admitted: undefined, live: "role:steward", roles: ["participant", "steward"] }), "prop");
    assert.equal(labelled("agree"), null, "no admitted satisfier, no proof of authority");
  });
});

// A second room, for the one satisfier that names a person instead of a role.
//
// `roles` and `retired` are the roster entry the fold publishes for the
// viewer, who is also the requester here. The fold keeps a departed principal
// listed with `retired: true` and no roles at all, because that principal
// signed events that are permanent and dropping it would leave those
// signatures attributed to nothing.
function reportedRoom({ roles, retired }) {
  const statements = [
    { event: "req", sequence: 1, actor: ME, kind: "request", text: "Record the decision.", timestamp: AT, body: { to: "codex" } },
    { event: "prom", sequence: 2, actor: "codex", kind: "promise", text: "I will do it.", timestamp: AT },
    { event: "rep", sequence: 3, actor: "codex", kind: "report", text: "Done.", timestamp: AT, satisfier: "originating-requester" },
  ];
  const projection = {
    decisions: statements.map((item) => ({ event: item.event, sequence: item.sequence, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements,
    commitments: [
      { request: "req", requester: ME, addressed_to: "codex", performer: "codex", promise: "prom", report: "rep", status: "reported", waiting_on: ME },
    ],
    artifacts: [],
    actors: {
      [ME]: { name: "hugh", kind: "human", roles, retired, role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      codex: { name: "codex", kind: "agent", roles: ["participant"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    },
    provenance: { prom: ["req"], rep: ["prom"] },
  };
  return workroomAround(projection, [
    { name: "request", satisfier: "none", render: "commitment" },
    { name: "promise", satisfier: "none", render: "commitment" },
    { name: "report", satisfier: "originating-requester", render: "commitment" },
  ]);
}

// `originating-requester` is the only satisfier that never consults a role, so
// it is the only one a departed principal can still be offered. Every
// `role:` satisfier already fails closed on a principal the fold left with no
// roles; this one asked a different question — is this fingerprint listed? —
// and a departed principal stays listed for ever.
//
// The fold asks `hasActor`, which is the participant role, and refuses a
// departed requester with "requester is not a live participant". A ratified
// report is what closes a commitment and a ratified approval is what `gs
// merge` consumes, so offering this act appends a permanent ineffective row to
// an append-only log, which is exactly the harm the satisfier repair exists to
// stop. The membership question the screen asks has to be the one the fold
// asks, not a weaker one that happens to agree while nobody has left.
test("a departed requester is not offered the ratification the fold refuses them", async () => {
  await withPane(async (vite, root) => {
    // Still a participant: the fold would record this ratification as
    // effective, so withholding it would be the silent denial in the other
    // direction.
    await mount(vite, root, reportedRoom({ roles: ["participant"] }), "req");
    assert.ok(
      labelled("accept"),
      "false denial: the requester is a live participant and the fold would accept this ratification, but the screen offered no control",
    );

    // Departed: listed, because the signatures are permanent, and holding no
    // roles, because the membership grants were all superseded.
    await mount(vite, root, reportedRoom({ roles: [], retired: true }), "req");
    // Compared as a boolean on purpose. Handing a live DOM node to an
    // assertion as its actual value makes the runner try to render a diff of
    // it, and the test hangs instead of printing why it failed.
    assert.ok(
      labelled("accept") === null,
      "false offer: the screen offered a ratification the fold refuses with \"requester is not a live participant\"",
    );
  });
});

// The row builds the citation so that an identifier nobody should retype is
// not retyped. That is a good reason to prefill it and no reason at all to
// hide it: what is signed is what the operator is answerable for, and until
// this test the composer submitted `rests_on` the operator had never seen.
test("the composer shows every citation it will sign before the send control can be used", async () => {
  await withPane(async (vite, root) => {
    await mount(vite, root, room({ admitted: "role:steward", live: "role:steward", roles: ["participant", "steward"] }), "art");

    // The composer's own region, not the whole document: these events also
    // appear in the rail above, so a document-wide match would pass without
    // the person signing ever having been shown anything.
    const cited = () => {
      const region = document.querySelector("[data-citations]");
      return region === null ? null : region.textContent;
    };

    // One citation, and a mode that can actually be sent. This is the plain
    // reading of the condition: the operator can see what they are about to
    // sign at the moment they are able to sign it.
    await click(labelled("propose adoption"));
    const proposing = cited();
    assert.notEqual(proposing, null, "the composer disclosed no citations at all");
    assert.ok(proposing.includes("art"), "the artifact this proposal will cite is signed but never shown");
    const send = labelled("keep reply");
    assert.ok(send, "the composer never entered its durable mode");
    assert.equal(send.disabled, false, "disclosure must precede signing, and the send control is already live");

    // Two citations, from the row that resolves the most on the operator's
    // behalf. Every event it will put in `rests_on` has to be named; showing
    // the first and hiding the rest is the same defect with a smaller number.
    await click(labelled("request review"));
    const reviewing = cited();
    assert.notEqual(reviewing, null, "the review request disclosed no citations at all");
    for (const event of ["art", "prop"]) {
      assert.ok(reviewing.includes(event), `citation ${event} is signed but never shown`);
    }
  });
});
