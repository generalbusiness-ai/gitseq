// The citation the row cannot resolve, and the operator can.
//
// docs/how-to/keep-decision-records.md step 5 revises an adopted decision at
// the same path. Its review request rests on two records: the new artifact,
// and the proposal that adopted the decision in the first place. The fold
// projects no edge between them — the proposal rests on the *earlier* artifact
// at that path — and docs/reference/architecture.md forbids the browser to
// join them, because "the decision at this path is adopted, and stays adopted"
// is exactly the relation layer 5 does not project. So the prefill cannot
// carry it, and until this the browser could not file that request at all.
//
// The repair is not a derivation. The operator names the record, the browser
// resolves that name against the projection and no further, and the result is
// shown in the same list that already discloses everything a reply will sign.
//
// Everything below renders the real composer in a DOM and counts POSTs to
// `/v0/act`. A helper that returned the right array would satisfy nothing
// here: what is asserted is the rendered list and the bytes on the wire.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
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

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");
const { buildRecordIndex, COMPOSED_CITATION_LIMIT } = await import("../src/lib/records.ts");

const AT = 1_700_000_000;
const ME = "hugh-fingerprint";
const RAE = "rae-fingerprint";
const PATH = "docs/decisions/0001-use-postgres.md";
const FIRST = "0123456789abcdef0123456789abcdef01234567";
const SECOND = "89abcdef0123456789abcdef0123456789abcdef";

const click = (element) =>
  act(async () => {
    element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });

const setValue = (element, value, prototype, eventName = "input") =>
  act(async () => {
    Object.getOwnPropertyDescriptor(prototype, "value").set.call(element, value);
    element.dispatchEvent(new dom.window.Event(eventName, { bubbles: true }));
  });

const typeInput = (element, value) => setValue(element, value, dom.window.HTMLInputElement.prototype);
const typeArea = (element, value) => setValue(element, value, dom.window.HTMLTextAreaElement.prototype);
const choose = (element, value) => setValue(element, value, dom.window.HTMLSelectElement.prototype, "change");

const labelled = (label) => document.querySelector(`[aria-label="${label}"]`);
const citations = () => document.querySelector("[data-citations]")?.textContent ?? null;

// Every POST to /v0/act, in order: the wire, not a module spy.
let filed = [];

// The workroom of the how-to's step 5, at the moment the revision is ready to
// be reviewed. `prop` adopted the decision and rests on `art1`; `art2` is the
// revision at the same path and rests on nothing, exactly as the page writes
// it. Nothing joins `prop` to `art2` because nothing in the fold does.
function revisionRoom(extra = []) {
  const statements = [
    { event: "art1", sequence: 1, actor: ME, kind: "artifact", text: "Decision record drafted for adoption", timestamp: AT, body: { path: PATH, commit: FIRST } },
    { event: "prop", sequence: 2, actor: ME, kind: "propose", text: "Adopt the decision recorded at this path", timestamp: AT, ratified: true, satisfier: "role:ratifier" },
    { event: "art2", sequence: 3, actor: ME, kind: "artifact", text: "Revised wording of the decision", timestamp: AT, body: { path: PATH, commit: SECOND } },
    ...extra,
  ];
  const projection = {
    decisions: statements.map((item) => ({ event: item.event, sequence: item.sequence, verdict: "effective", reason: "recorded" })),
    acts: [],
    statements,
    commitments: [],
    artifacts: [
      { event: "art1", path: PATH, commit: FIRST },
      { event: "art2", path: PATH, commit: SECOND },
    ],
    actors: {
      [ME]: { name: "hugh", kind: "human", roles: ["participant", "ratifier"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
      [RAE]: { name: "rae", kind: "human", roles: ["participant"], role_sources: {}, dormant_role_sources: {}, retired_role_sources: {} },
    },
    provenance: { prop: ["art1"] },
  };
  return {
    actors: [
      { name: "hugh", fingerprint: ME, roles: [], custody: true },
      { name: "rae", fingerprint: RAE, roles: [], custody: false },
    ],
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
            { name: "artifact", satisfier: "none", render: "artifact" },
            { name: "propose", satisfier: "role:ratifier", render: "proposal" },
            { name: "request", satisfier: "none", render: "commitment" },
          ],
        },
      },
      live: { cursor: { generation: "generation", position: 1 }, presence: {}, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
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
  filed = [];
  globalThis.fetch = async (url, options) => {
    const path = typeof url === "string" ? url : String(url);
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

// Open the review request the revision needs, and answer the two fields a
// request cannot be filed without.
async function requestReview(vite, root, workroom) {
  await mount(vite, root, workroom, "art2");
  const opener = labelled("request review");
  assert.ok(opener, "the artifact row never offered the review request");
  await click(opener);
  await typeArea(labelled("thread reply"), "Review the revised wording at its exact head");
  await choose(labelled("addressed to"), RAE);
  await typeInput(labelled("conditions of satisfaction"), "same decision, clearer scope");
}

// The whole point, end to end: the gap is shown first, then closed, then the
// wire is read. Asserting the wire is what makes this more than a screenshot.
test("a revision's review request can rest on the adoption the row cannot resolve", async () => {
  await withPane(async (vite, root) => {
    await requestReview(vite, root, revisionRoom());

    // The gap, stated rather than assumed. The prefill follows the fold's
    // citation edge, and no edge reaches the proposal from this artifact.
    const prefilled = citations();
    assert.notEqual(prefilled, null, "the composer disclosed no citations at all");
    assert.ok(prefilled.includes("art2"), "the revision artifact is signed but never shown");
    assert.equal(
      prefilled.includes("prop"),
      false,
      "the prefill reached across a shared path, which is the adoption relation the fold does not project",
    );

    // The operator names the proposal by the number the screen shows them.
    await typeInput(labelled("cite another record"), "#2");
    await click(labelled("add citation"));
    const disclosed = citations();
    assert.ok(disclosed.includes("prop"), "the named citation was accepted but never shown before signing");
    assert.ok(disclosed.includes("art2"), "naming a citation must not displace the one the row resolved");

    await click(labelled("keep reply"));
    assert.equal(filed.length, 1, "the composer filed nothing at all");
    assert.deepEqual(
      filed[0].rests_on,
      ["art2", "prop"],
      "what reached the wire is not what the composer disclosed",
    );
    assert.equal(filed[0].body.artifact, "art2");
    assert.equal(filed[0].body.head, SECOND, "the request carries the artifact's own commit as its head");
    assert.equal(filed[0].body.to, RAE);
    assert.equal(filed[0].body.conditions, "same decision, clearer scope");
  });
});

// A whole event identifier is the other name the operator has: a record's
// detail renders it select-all for exactly this.
test("a citation may be named by its whole event identifier", async () => {
  await withPane(async (vite, root) => {
    await requestReview(vite, root, revisionRoom());
    await typeInput(labelled("cite another record"), "  prop  ");
    await click(labelled("add citation"));
    assert.ok(citations().includes("prop"), "a whole identifier was not resolved");
    await click(labelled("keep reply"));
    assert.deepEqual(filed[0].rests_on, ["art2", "prop"]);
  });
});

// Resolution stops at the projection. A name it does not carry is refused
// where the operator can still fix it, rather than filed as a reference to
// nothing and judged afterwards.
test("a name the projection does not carry is refused, not passed through", async () => {
  await withPane(async (vite, root) => {
    await requestReview(vite, root, revisionRoom());

    await typeInput(labelled("cite another record"), "#99");
    await click(labelled("add citation"));
    assert.ok(labelled("citation refused"), "an unresolvable ticket was accepted in silence");
    assert.equal(citations().includes("99"), false, "an unresolvable ticket reached the citation list");

    await typeInput(labelled("cite another record"), "git:sha1:nothing#git:sha1:nothing");
    await click(labelled("add citation"));
    assert.ok(labelled("citation refused"), "an unresolvable identifier was accepted in silence");

    // The positive control: the same field still resolves a real record, so
    // the two refusals above are the guard and not a broken control.
    await typeInput(labelled("cite another record"), "#2");
    await click(labelled("add citation"));
    assert.ok(citations().includes("prop"));

    // And citing it twice is refused too: a repeated reference says nothing
    // the first one did not.
    await typeInput(labelled("cite another record"), "#2");
    await click(labelled("add citation"));
    assert.ok(labelled("citation refused"));

    await click(labelled("keep reply"));
    assert.deepEqual(filed[0].rests_on, ["art2", "prop"], "a refused name still reached the wire");
  });
});

// The bound covers the whole list, prefill included, because what has to stay
// readable is what will be signed.
test("the citation list is bounded however many records the operator names", async () => {
  await withPane(async (vite, root) => {
    const spare = Array.from({ length: COMPOSED_CITATION_LIMIT + 4 }, (_, position) => ({
      event: `spare-${position}`,
      sequence: 100 + position,
      actor: ME,
      kind: "assert",
      text: `a spare record ${position}`,
      timestamp: AT,
    }));
    await requestReview(vite, root, revisionRoom(spare));

    for (const record of spare) {
      await typeInput(labelled("cite another record"), record.event);
      await click(labelled("add citation"));
    }

    await click(labelled("keep reply"));
    assert.equal(
      filed[0].rests_on.length,
      COMPOSED_CITATION_LIMIT,
      "the operator drove the citation list past the bound the composer publishes",
    );
    assert.equal(filed[0].rests_on[0], "art2", "the bound dropped the row's own citation instead of the surplus");
  });
});

// Added by hand, so removable by hand — before anything is signed.
test("an operator's citation can be taken out again before signing", async () => {
  await withPane(async (vite, root) => {
    await requestReview(vite, root, revisionRoom());
    await typeInput(labelled("cite another record"), "#2");
    await click(labelled("add citation"));
    assert.ok(citations().includes("prop"));

    const remove = labelled("stop citing prop");
    assert.ok(remove, "a citation the operator added carries no way to take it out");
    await click(remove);
    assert.equal(citations().includes("prop"), false, "the citation stayed in the list after being removed");

    assert.ok(
      labelled("stop citing art2") === null,
      "the row's own citation must not be removable: it is what the reply is a reply to",
    );

    await click(labelled("keep reply"));
    assert.deepEqual(filed[0].rests_on, ["art2"], "a removed citation still reached the wire");
  });
});

// A withdrawal names the record it retires in its target and files no causal
// references at all, so there is nothing an operator citation could mean there.
test("a withdrawal takes no operator citation", async () => {
  await withPane(async (vite, root) => {
    await mount(vite, root, revisionRoom(), "art2");
    // The row's control and the composer's send control share this label, and
    // the row's comes first in the document.
    const withdrawals = () => [...document.querySelectorAll('[aria-label="withdraw"]')];
    await click(withdrawals()[0]);
    await typeArea(labelled("thread reply"), "Filed at the wrong commit.");
    assert.ok(labelled("the record this will retire"), "the withdrawal did not open");
    // Compared as a boolean: handing a live DOM node to an assertion as its
    // actual value makes the runner try to render a diff of it, and the test
    // hangs instead of printing why it failed.
    assert.ok(labelled("cite another record") === null, "a withdrawal offered a citation it cannot sign");

    const send = withdrawals().at(-1);
    assert.ok(send !== withdrawals()[0], "the composer never entered its withdrawal mode");
    await click(send);
    assert.equal(filed.length, 1);
    assert.equal(filed[0].act, "supersede");
    assert.equal(filed[0].target, "art2");
    assert.deepEqual(filed[0].rests_on, []);
  });
});
