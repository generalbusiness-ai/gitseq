// A record's own text names its own evidence. Writing
// `[current reader baseline](rollout-baseline.json)` in a record whose signed
// attachments include `rollout-baseline.json` used to open a source-file
// preview and report the file absent at the commit, while the very same name
// in the record's evidence row opened the attachment. The detail panel
// rendered its text through ReferenceText without the attachment list, and
// fetched that list separately for the evidence row.
//
// Which target a name opens is only observable once the listing has been
// fetched and the link has been clicked, so this file drives RecordDetail in a
// DOM against a resident stand-in and asserts the address each link produces.
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
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const head = "c".repeat(40);
const eventOf = (tag) => `git:sha1:${"a".repeat(40)}#git:sha1:${tag.repeat(40)}`;

const click = (element) => act(async () => {
  element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true, button: 0 }));
});
const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });

// A projection holding one record whose text is what the reader sees.
function projectionFor(event, text, extra = {}) {
  return {
    decisions: [{ event, sequence: 1, verdict: "effective" }],
    acts: [],
    statements: [{ event, sequence: 1, actor: "actor", kind: "artifact", text, timestamp: 1_786_500_000, ...extra.statement }],
    commitments: [],
    reviews: [],
    artifacts: extra.artifacts ?? [],
    actors: { actor: { name: "an actor", kind: "agent", roles: [] } },
    provenance: {},
  };
}

// The resident's listing branch: `{ event }` alone answers with that record's
// own attachment names. Every other input is a file read this file does not
// need, and asking for one is itself a failure the tests catch.
function listing(attachments) {
  return async (url, init) => {
    const input = JSON.parse(init.body);
    assert.equal(url, "/v0/preview");
    assert.deepEqual(Object.keys(input), ["event"], "the listing is one bounded per-record call");
    return { ok: true, statusText: "OK", json: async () => ({ repo: "/repo", event: input.event, status: "ready", limit: 4194304, attachments: attachments[input.event] ?? [] }) };
  };
}

// Render one record's detail under a preview context that records the target
// each click opens, and return the hrefs the links carry.
async function open({ vite, mounted, projection, event, tickets = new Map() }) {
  const { RecordDetail } = await vite.ssrLoadModule("/src/components/RecordDetail.tsx");
  const { buildRecordIndex } = await vite.ssrLoadModule("/src/lib/records.ts");
  const { PreviewContext } = await vite.ssrLoadModule("/src/components/Preview.tsx");
  const opened = [];
  await act(async () => {
    mounted.render(React.createElement(PreviewContext.Provider,
      { value: { address: { kind: "thread", event }, open(target) { opened.push(target); } } },
      React.createElement(RecordDetail, {
        event,
        index: buildRecordIndex(projection),
        actors: projection.actors,
        tickets,
        nameOf: (fingerprint) => fingerprint,
        onOpenThread() {},
      })));
  });
  return opened;
}

const linkNamed = (label) => [...document.querySelectorAll("a")].find((anchor) => anchor.textContent === label);

async function withUI(run) {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const mounted = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  try {
    await run({ vite, mounted });
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => { mounted.unmount(); });
    await vite.close();
  }
}

test("a record's prose link to its own evidence opens that attachment, not a source file", () => withUI(async ({ vite, mounted }) => {
  const event = eventOf("1");
  globalThis.fetch = listing({ [event]: ["proof.json"] });
  const opened = await open({
    vite, mounted, event,
    projection: projectionFor(event, "The baseline is [current reader baseline](proof.json)."),
  });
  await settle();

  const link = linkNamed("current reader baseline");
  assert.ok(link, "the Markdown link is on screen");
  // The address is the whole answer: evidence=, and no file= to read at a
  // commit where this name is not a repository path.
  assert.match(link.getAttribute("href"), /evidence=proof\.json/);
  assert.doesNotMatch(link.getAttribute("href"), /file=/);
  await click(link);
  assert.deepEqual(opened, [{ event, attachment: "proof.json" }]);

  // The evidence row opens the same content under the same name, from the
  // same single listing.
  const row = linkNamed("proof.json");
  assert.ok(row, "the evidence row lists the attachment");
  assert.match(row.getAttribute("href"), /evidence=proof\.json/);
}));

test("a name that is both an attachment and a source path: the bare name is evidence, a line or revision is source", () => withUI(async ({ vite, mounted }) => {
  const event = eventOf("2");
  globalThis.fetch = listing({ [event]: ["notes.md"] });
  const opened = await open({
    vite, mounted, event,
    projection: projectionFor(event,
      `Read [the note](notes.md), then [line twelve](notes.md:12) and [as filed](notes.md@${head}).`,
      { artifacts: [{ event, path: "notes.md", commit: head, stale: false }] }),
  });
  await settle();

  await click(linkNamed("the note"));
  await click(linkNamed("line twelve"));
  await click(linkNamed("as filed"));
  assert.deepEqual(opened, [
    { event, attachment: "notes.md" },
    { event, path: "notes.md", commit: undefined, line: 12 },
    { event, path: "notes.md", commit: head, line: undefined },
  ], "a bare name is the record's evidence; an explicit line or revision keeps the source reachable");

  // The artifact path row is not a reference in prose and never consults the
  // listing: it names the destination the record published, at its commit.
  const pathRow = [...document.querySelectorAll("a")].find((anchor) => anchor.textContent === "notes.md" && /file=/.test(anchor.getAttribute("href")));
  assert.ok(pathRow, "the path row still opens source");
  assert.match(pathRow.getAttribute("href"), new RegExp(`file=notes.md&at=${head}`));
}));

test("a name absent from the listing keeps its source behaviour", () => withUI(async ({ vite, mounted }) => {
  const event = eventOf("3");
  globalThis.fetch = listing({ [event]: ["proof.json"] });
  const opened = await open({
    vite, mounted, event,
    projection: projectionFor(event, "See [the design](docs/design.md) and `internal/service/preview.go:42`."),
  });
  await settle();

  await click(linkNamed("the design"));
  await click(linkNamed("internal/service/preview.go:42"));
  assert.deepEqual(opened, [
    { event, path: "docs/design.md", commit: undefined, line: undefined },
    { event, path: "internal/service/preview.go", commit: undefined, line: 42 },
  ], "an unlisted name resolves exactly as it did before attachments were consulted");
}));

test("a source line reference still opens source at its line", () => withUI(async ({ vite, mounted }) => {
  const event = eventOf("4");
  globalThis.fetch = listing({ [event]: ["proof.json"] });
  const opened = await open({ vite, mounted, event, projection: projectionFor(event, "Broken at docs/x.md:5 today.") });
  await settle();

  const link = linkNamed("docs/x.md:5");
  assert.ok(link, "the bare path in prose is a link");
  assert.match(link.getAttribute("href"), /file=docs%2Fx.md&line=5/);
  await click(link);
  assert.deepEqual(opened, [{ event, path: "docs/x.md", commit: undefined, line: 5 }]);
}));

test("a listing that arrives after the reader moved on binds to nothing", () => withUI(async ({ vite, mounted }) => {
  const first = eventOf("5");
  const second = eventOf("6");
  // The stand-in holds each answer until this test releases it, so the reader
  // can leave the first record while its listing is still in flight.
  const held = [];
  globalThis.fetch = async (_url, init) => {
    const input = JSON.parse(init.body);
    return new Promise((resolve) => held.push({ event: input.event, resolve }));
  };

  const projections = {
    [first]: projectionFor(first, "Evidence: [the first](a.json)."),
    [second]: projectionFor(second, "Evidence: [the second](a.json)."),
  };
  await open({ vite, mounted, event: first, projection: projections[first] });
  await settle();
  assert.equal(held.length, 1);
  assert.equal(held[0].event, first);

  // The reader opens another record before the first listing answers.
  const opened = await open({ vite, mounted, event: second, projection: projections[second] });
  await settle();

  // The first record's answer lands late, naming an attachment both records'
  // texts mention by the same name.
  const answer = (event) => ({ ok: true, statusText: "OK", json: async () => ({ repo: "/repo", event, status: "ready", limit: 4194304, attachments: ["a.json"] }) });
  await act(async () => { held[0].resolve(answer(first)); });
  await settle();

  // Nothing of the first record's listing reaches the second record: its
  // prose still resolves as source, and its evidence row is still waiting.
  const link = linkNamed("the second");
  assert.doesNotMatch(link.getAttribute("href"), /evidence=/, "a listing never binds to another record's event");
  await click(link);
  assert.deepEqual(opened, [{ event: second, path: "a.json", commit: undefined, line: undefined }]);
  assert.ok(document.body.textContent.includes("Checking evidence"), "the evidence row is still waiting on the second record's own listing");

  // When the second record's own listing arrives, the same name becomes its
  // own evidence.
  const late = held.find((request) => request.event === second);
  assert.ok(late, "the second record asked for its own listing");
  await act(async () => { late.resolve(answer(second)); });
  await settle();
  assert.match(linkNamed("the second").getAttribute("href"), /evidence=a\.json/);
}));

test("a failed listing leaves prose as source and says the evidence could not be checked", () => withUI(async ({ vite, mounted }) => {
  const event = eventOf("7");
  globalThis.fetch = async () => { throw new Error("resident unreachable"); };
  const opened = await open({ vite, mounted, event, projection: projectionFor(event, "Evidence: [the proof](proof.json).") });
  await settle();

  const link = linkNamed("the proof");
  assert.doesNotMatch(link.getAttribute("href"), /evidence=/);
  await click(link);
  assert.deepEqual(opened, [{ event, path: "proof.json", commit: undefined, line: undefined }]);
  assert.ok(document.body.textContent.includes("Evidence could not be checked"), "the evidence row keeps its own failure text");
}));

test("body values resolve their names against the same listing as the text", () => withUI(async ({ vite, mounted }) => {
  const event = eventOf("8");
  globalThis.fetch = listing({ [event]: ["measurement.json"] });
  const opened = await open({
    vite, mounted, event,
    projection: projectionFor(event, "Measured.", { statement: { body: { evidence: "measurement.json" } } }),
  });
  await settle();

  const value = [...document.querySelectorAll("a")].filter((anchor) => anchor.textContent === "measurement.json");
  assert.equal(value.length, 2, "the body row and the evidence row both name it");
  for (const anchor of value) assert.match(anchor.getAttribute("href"), /evidence=measurement\.json/);
  await click(value[0]);
  assert.deepEqual(opened, [{ event, attachment: "measurement.json" }]);
}));
