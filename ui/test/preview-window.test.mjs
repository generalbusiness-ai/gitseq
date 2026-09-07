// A large file is read in windows, and three things about that are only
// observable in a browser: that a review link opens at the window holding
// the cited line with the file's own line numbers, that Next and Previous
// move between windows until the last one, and that an answer for a target
// the reader has already left is dropped rather than shown under the new
// one. Everything the resident decides about windows is proven in Go; this
// file drives the dialog against a resident stand-in that follows the same
// rules.
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
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const React = (await import("react")).default;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const event = `git:sha1:${"a".repeat(40)}#git:sha1:${"b".repeat(40)}`;
const head = "c".repeat(40);
const TOTAL = 5611;
const WINDOW = 400;

// The stand-in answers like the resident: an explicit start wins, a cited
// line selects its aligned window, and a whole small file is unwindowed.
function answer(input) {
  const tag = input.commit === head ? "first" : "second";
  let start = input.start || (input.line ? Math.floor((input.line - 1) / WINDOW) * WINDOW + 1 : 1);
  const end = Math.min(start + WINDOW - 1, TOTAL);
  const lines = [];
  for (let i = start; i <= end; i++) lines.push(`${tag} line ${i}`);
  return {
    repo: "/repo", event: input.event, commit: input.commit, path: input.path, status: "ready", limit: 4194304, size: 123456,
    content: lines.join("\n"),
    window: { start, end, total: TOTAL, partial: true, previous: start > 1 ? Math.max(1, start - WINDOW) : undefined, next: end < TOTAL ? end + 1 : undefined },
  };
}

const click = (element) => act(async () => { element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true, button: 0 })); });
const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

test("a review link opens the cited line's window and Next/Previous walk to the final window", async () => {
  const vite = await createServer({ root: uiRoot, server: { middlewareMode: true }, appType: "custom" });
  const mounted = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (url, init) => {
    const input = JSON.parse(init.body);
    requests.push(input);
    return { ok: true, statusText: "OK", json: async () => answer(input) };
  };
  try {
    const { Preview, PreviewContext } = await vite.ssrLoadModule("/src/components/Preview.tsx");
    let target = { event, path: "cmd/gs/main_test.go", commit: head, line: 726 };
    const render = () => act(async () => {
      mounted.render(React.createElement(PreviewContext.Provider, { value: { address: { kind: "thread", event }, open(next) { target = next; } } },
        React.createElement(Preview, { target, onClose() {} })));
    });
    await render();
    await settle();

    // The cited line went to the resident and its window came back: real
    // line numbers from 401, line 726 highlighted, and the range stated.
    assert.deepEqual(requests.at(-1), { event, path: "cmd/gs/main_test.go", commit: head, line: 726 });
    const header = document.querySelector('[aria-label="Window"]');
    assert.match(header.textContent, /Lines 401–800 of 5,611/);
    assert.match(header.textContent, /partial/);
    const rows = [...document.querySelectorAll('[aria-label="Source with line numbers"] > div')];
    assert.equal(rows[0].firstChild.textContent, "401", "line numbers must be the file's, not the window's");
    const highlighted = rows.filter((row) => row.className.includes("bg-accent"));
    assert.equal(highlighted.length, 1);
    assert.equal(highlighted[0].firstChild.textContent, "726");
    assert.match(highlighted[0].textContent, /first line 726/);

    // Next asks for the following window by its first line and keeps the
    // cited line in the address, so the highlight returns with it.
    const next = [...document.querySelectorAll("a")].find((a) => a.textContent === "Next lines");
    assert.ok(next, "no Next on a middle window");
    assert.match(next.getAttribute("href"), /from=801/);
    assert.match(next.getAttribute("href"), /line=726/);
    await click(next);
    await render();
    await settle();
    assert.equal(requests.at(-1).start, 801);
    assert.match(document.querySelector('[aria-label="Window"]').textContent, /Lines 801–1,200 of 5,611/);
    assert.equal(document.querySelectorAll('[aria-label="Source with line numbers"] > div')[0].firstChild.textContent, "801");
    assert.equal(document.querySelectorAll(".bg-accent\\/20").length, 0, "line 726 is not in this window");

    // Previous is a real link back to the adjacent window: its address
    // round-trips (so history holds it) and following it asks the resident
    // for start 401 again.
    const previous = [...document.querySelectorAll("a")].find((a) => a.textContent === "Previous lines");
    assert.match(previous.getAttribute("href"), /from=401/);
    const { parseAddress } = await vite.ssrLoadModule("/src/lib/address.ts");
    assert.deepEqual(parseAddress(previous.getAttribute("href")).preview, { event, path: "cmd/gs/main_test.go", commit: head, attachment: undefined, line: 726, start: 401 });
    await click(previous);
    await render();
    await settle();
    assert.equal(requests.at(-1).start, 401);
    assert.match(document.querySelector('[aria-label="Window"]').textContent, /Lines 401–800 of 5,611/);
    assert.equal(document.querySelectorAll(".bg-accent\\/20").length, 1, "line 726 is highlighted again on return");
    // Then jump to the final window, which offers no Next and ends on the
    // file's last line.
    target = { ...target, start: 5601 };
    await render();
    await settle();
    const final = document.querySelector('[aria-label="Window"]');
    assert.match(final.textContent, /Lines 5,601–5,611 of 5,611/);
    assert.equal([...document.querySelectorAll("a")].some((a) => a.textContent === "Next lines"), false);
    assert.ok([...document.querySelectorAll("a")].some((a) => a.textContent === "Previous lines"));
    const lastRows = document.querySelectorAll('[aria-label="Source with line numbers"] > div');
    assert.equal(lastRows[lastRows.length - 1].firstChild.textContent, "5611");
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => mounted.unmount());
    await vite.close();
  }
});

test("an answer for a target the reader already left is dropped, and the neighbour revision never shows", async () => {
  const vite = await createServer({ root: uiRoot, server: { middlewareMode: true }, appType: "custom" });
  const mounted = createRoot(document.getElementById("root"));
  const previousFetch = globalThis.fetch;
  const pending = [];
  globalThis.fetch = (url, init) => new Promise((resolve, reject) => {
    const input = JSON.parse(init.body);
    init.signal?.addEventListener("abort", () => reject(new Error("aborted")));
    pending.push(() => resolve({ ok: true, statusText: "OK", json: async () => answer(input) }));
  });
  try {
    const { Preview, PreviewContext } = await vite.ssrLoadModule("/src/components/Preview.tsx");
    const other = "d".repeat(40);
    const render = (target) => act(async () => {
      mounted.render(React.createElement(PreviewContext.Provider, { value: { address: { kind: "thread", event }, open() {} } },
        React.createElement(Preview, { target, onClose() {} })));
    });
    await render({ event, path: "cmd/gs/main_test.go", commit: head, line: 726 });
    await render({ event, path: "cmd/gs/main_test.go", commit: other, line: 726 });
    assert.equal(pending.length, 2);
    // Answer the first (abandoned) request first, then the current one.
    pending[0]();
    await settle();
    assert.equal(document.querySelector('[aria-label="Source with line numbers"]'), null, "content from an abandoned target was shown");
    pending[1]();
    await settle();
    const text = document.querySelector('[aria-label="Source with line numbers"]').textContent;
    assert.match(text, /second line 726/);
    assert.doesNotMatch(text, /first line/, "neighbouring revision content shown under the chosen one");
    assert.match(document.querySelector("header").textContent, new RegExp(`Exact revision: ${other}`));
  } finally {
    globalThis.fetch = previousFetch;
    await act(async () => mounted.unmount());
    await vite.close();
  }
});

// Review 318bec67: the rows must agree with the window metadata for an empty
// file (no rows) and for a whole file ending in a newline (no phantom last
// row), and an empty out-of-range window shows only its message.
for (const [label, content, total, window] of [
  ["an empty file", "", 0, { start: 0, end: 0, total: 0, partial: false }],
  ["a whole file ending in a newline", "a\nb\n", 2, { start: 1, end: 2, total: 2, partial: false }],
  ["an out-of-range window", "", 0, { start: 9000, end: 8999, total: 2, partial: true, previous: 1 }],
  ["a partial window holding one blank line", "", 1, { start: 401, end: 401, total: 401, partial: true, previous: 1 }],
]) {
  test(`source rows agree with the window for ${label}`, async () => {
    const vite = await createServer({ root: uiRoot, server: { middlewareMode: true }, appType: "custom" });
    const mounted = createRoot(document.getElementById("root"));
    const previousFetch = globalThis.fetch;
    globalThis.fetch = async () => ({ ok: true, statusText: "OK", json: async () => ({ repo: "/repo", event, commit: head, path: "small.txt", status: "ready", limit: 4194304, content, window, message: window.start > window.total ? "Window start 9000 is outside this file, which has 2 lines." : undefined }) });
    try {
      const { Preview, PreviewContext } = await vite.ssrLoadModule("/src/components/Preview.tsx");
      await act(async () => mounted.render(React.createElement(PreviewContext.Provider, { value: { address: { kind: "thread", event }, open() {} } }, React.createElement(Preview, { target: { event, path: "small.txt", commit: head }, onClose() {} }))));
      await settle();
      const rows = document.querySelectorAll('[aria-label="Source with line numbers"] > div');
      assert.equal(rows.length, window.end >= window.start ? total : 0, `rendered line numbers: ${[...rows].map((r) => r.firstChild.textContent)}`);
      if (window.start > window.total) assert.match(document.body.textContent, /outside this file/);
    } finally {
      globalThis.fetch = previousFetch;
      await act(async () => mounted.unmount());
      await vite.close();
    }
  });
}
