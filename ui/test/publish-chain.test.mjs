// The publish dialog, driven against a real workroom.
//
// Everything else in this directory renders components over a projection
// written by hand. That is the right shape for most questions, and it is the
// wrong shape for this one. `aria-modal="true"` is a promise about a live
// document — where focus starts, where Tab may go, what Escape does, where
// focus lands afterwards — and none of it can be observed in a static render
// or against a stubbed submit handler. A test that replaced the seam with a
// spy would pass whether or not the record ever reached a fold.
//
// So this file builds `gs`, initialises a real repository, runs a real
// resident, and mounts the real `App` against it. The button that opens the
// dialog is the real one in the top bar, which is also what focus must return
// to. The submit path is the browser's own `api.act`, over HTTP, to the
// service, into the fold — and the record it writes is then read back out of
// the fold's own projection. The one adaptation is that `fetch` resolves the
// page's relative paths against the resident's address, because a jsdom
// document has no origin to resolve them against; the request, the transport,
// the service and the fold are all the shipped ones.
import test, { after, before } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));
const repoRoot = resolve(uiRoot, "..");

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://127.0.0.1/",
});
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.localStorage = dom.window.localStorage;
dom.window.Element.prototype.scrollTo = function scrollTo() {};
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const { act } = await import("react");
const React = (await import("react")).default;
const { createRoot } = await import("react-dom/client");
const { createServer } = await import("vite");

const OPERATOR = "publisher";
const DECISION_PATH = "docs/decisions/0001-use-the-fold.md";
const DECISION_COMMIT = "0123456789abcdef0123456789abcdef01234567";
const STAMPED_PATH = "docs/decisions/0000-the-decision-it-replaces.md";

let workspace;
let resident;
let base;
let vite;
let mounted;
const directFetch = globalThis.fetch;

function run(command, argv, options = {}) {
  return execFileSync(command, argv, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], ...options });
}

// Wait for the resident to name the port it bound. `--listen 127.0.0.1:0`
// takes any free one, which is what lets this run beside a workroom that is
// already serving on the usual address.
function residentAddress(child) {
  return new Promise((resolve_, reject) => {
    let seen = "";
    const timer = setTimeout(() => reject(new Error(`resident did not announce an address: ${seen}`)), 30_000);
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => {
      seen += chunk;
      const match = seen.match(/gitseq workroom (http:\/\/\S+)/);
      if (!match) return;
      clearTimeout(timer);
      resolve_(match[1]);
    });
    child.on("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`resident exited with ${code}: ${seen}`));
    });
  });
}

async function projection() {
  const response = await directFetch(`${base}/v0/status`, { cache: "no-store" });
  return (await response.json()).durable.projection;
}

before(async () => {
  workspace = mkdtempSync(join(tmpdir(), "gitseq-publish-chain-"));
  const repo = join(workspace, "repo");
  const gs = join(workspace, "gs");
  // Built rather than assumed: `bin/gs` may be absent, or stale against the
  // source this test is checking.
  run("go", ["build", "-o", gs, "./cmd/gs"], { cwd: repoRoot, timeout: 600_000 });
  run("git", ["init", "-q", repo]);
  run(gs, ["init", "--repo", repo, "--operator", OPERATOR]);

  resident = spawn(gs, ["serve", "--repo", repo, "--listen", "127.0.0.1:0"], { stdio: ["ignore", "ignore", "pipe"] });
  base = await residentAddress(resident);

  // The page speaks in paths, as a browser does; this gives them an origin.
  globalThis.fetch = (input, init) => directFetch(new URL(input, base), init);

  vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
});

after(async () => {
  if (mounted) await act(async () => mounted.unmount());
  globalThis.fetch = directFetch;
  if (vite) await vite.close();
  // Asked to stop rather than killed, and waited for: `gs serve` closes its
  // listener on SIGTERM, so a long poll this page still had open returns
  // instead of having its connection torn out from under it.
  if (resident && resident.exitCode === null) {
    await new Promise((resolve_) => {
      resident.once("exit", resolve_);
      resident.kill("SIGTERM");
      setTimeout(() => {
        resident.kill("SIGKILL");
        resolve_();
      }, 10_000).unref?.();
    });
  }
  if (workspace) rmSync(workspace, { recursive: true, force: true });
});

const publishButton = () => [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "publish" && b.type !== "submit");
const dialog = () => document.querySelector('[role="dialog"][aria-labelledby="publish-title"]');
const submitButton = () => dialog()?.querySelector('button[type="submit"]');
const cancelButton = () => [...(dialog()?.querySelectorAll("button") ?? [])].find((b) => b.textContent.trim() === "cancel");
const field = (label) => dialog().querySelector(`input[aria-label="${label}"]`);

const click = (element) =>
  act(async () => {
    element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });

const press = (key, options = {}) =>
  act(async () => {
    document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...options }));
  });

const type = (element, value) =>
  act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value").set;
    setter.call(element, value);
    element.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });

// The page keeps three loops running against the resident — status, presence
// and chat — so a state update can land at any moment. Every wait below
// therefore happens *inside* `act`, including the checking, so React never
// sees an update from outside one.
const settle = (rounds = 6) =>
  act(async () => {
    for (let round = 0; round < rounds; round += 1) {
      await new Promise((resolve_) => setTimeout(resolve_, 60));
    }
  });

async function until(what, predicate, attempts = 80) {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    // The question itself is asked inside `act`, because asking it can mean
    // waiting on the resident, and that is exactly the window in which one of
    // the page's own loops would otherwise update state outside one.
    let answer;
    await act(async () => {
      answer = await predicate();
    });
    if (answer !== undefined && answer !== null && answer !== false) return answer;
    await settle(1);
  }
  throw new Error(`timed out waiting for ${what}`);
}

test("the publish dialog keeps the focus contract and writes both artifacts of a replacement through the real fold", async () => {
  // The actor is chosen before mounting so the join gate is already answered;
  // presence, and therefore the live lease, come from the real resident.
  localStorage.setItem("workroom.actor", OPERATOR);
  const { default: App } = await vite.ssrLoadModule("/src/App.tsx");
  mounted = createRoot(document.getElementById("root"));
  try {
    await act(async () => {
      mounted.render(React.createElement(App));
      await new Promise((resolve_) => setTimeout(resolve_, 250));
    });
    await until("the top bar to offer publishing", () => publishButton() && !publishButton().disabled);

    // 1. INITIAL FOCUS. The opener is focused first, exactly as clicking it
    //    leaves it, so restoration at the end has somewhere real to return to.
    const opener = publishButton();
    opener.focus();
    assert.equal(document.activeElement, opener);
    await click(opener);
    await until("the dialog", () => dialog());
    const firstField = field("what this artifact records");
    assert.equal(document.activeElement, firstField, "focus starts on the dialog's first control, not wherever the page left it");

    // 2. CONTAINMENT. jsdom moves focus for nobody, so nothing here can wrap it
    //    except the dialog's own handler — which is exactly the point: without
    //    one, focus walks out of the dialog into the page a modal declares
    //    inert. Both endpoints are named rather than computed, so this cannot
    //    quietly agree with a broken implementation.
    //
    //    While the form is empty its submit button is disabled, so the last
    //    control in the ring is "cancel". A disabled control is not focusable
    //    and must not be a wrap target.
    assert.equal(submitButton().disabled, true, "an empty form cannot be submitted");
    cancelButton().focus();
    await press("Tab");
    assert.equal(document.activeElement, firstField, "Tab past the last control wraps to the first");
    await press("Tab", { shiftKey: true });
    assert.equal(document.activeElement, cancelButton(), "Shift+Tab before the first control wraps to the last, skipping the disabled submit");

    // The ring is read when the key is pressed, not captured when the dialog
    // opened: filling the form enables submit, and the wrap target moves.
    await type(firstField, "Decision record drafted for adoption");
    await type(field("path"), DECISION_PATH);
    await type(field("commit"), DECISION_COMMIT);
    assert.equal(submitButton().disabled, false);
    field("what this artifact records").focus();
    await press("Tab", { shiftKey: true });
    assert.equal(document.activeElement, submitButton(), "the newly enabled control joins the ring");

    // 3. ESCAPE dismisses, and 4. FOCUS RETURNS to what opened the dialog.
    await press("Escape");
    await until("the dialog to close", () => !dialog());
    assert.equal(document.activeElement, opener, "closing returns focus to the control that opened it");

    // 5. THE REAL CHAIN. Nothing below is stubbed: the browser's own act call,
    //    the resident's /v0/act route, the workroom fold, and the projection the
    //    fold publishes. A dialog wired to a spy would pass every assertion
    //    above and none of these.
    const before_ = await until("the current projection", async () => await projection());
    assert.equal(
      before_.artifacts.filter((artifact) => artifact.path === DECISION_PATH).length,
      0,
      "the workroom has no such artifact until the dialog files one",
    );

    await click(publishButton());
    await until("the dialog again", () => dialog());
    await type(field("what this artifact records"), "Decision record drafted for adoption");
    await type(field("path"), DECISION_PATH);
    await type(field("commit"), DECISION_COMMIT);
    await act(async () => {
      dialog().dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
    });

    const landed = await until(
      "the fold to project the record the dialog signed",
      async () => (await projection()).artifacts.find((artifact) => artifact.path === DECISION_PATH),
      100,
    );
    assert.equal(landed.commit, DECISION_COMMIT, "and carries the exact commit that was typed");

    const decided = await until(
      "the fold's verdict on it",
      async () => (await projection()).decisions.find((decision) => decision.event === landed.event),
    );
    assert.equal(decided.verdict, "effective", "the fold admitted it rather than refusing it");

    // The dialog closes itself only once the projection carries the record, so
    // the thread it opens can never report a record it just filed as missing.
    await until("the dialog to close on its own", () => !dialog(), 200);

    // 6. THE REPLACEMENT'S SECOND ARTIFACT. Step 6 of
    //    docs/how-to/keep-decision-records.md records a replacement as two
    //    artifacts: the new decision, and the stamped predecessor *resting on
    //    it*, so a reader of either file reaches the other through the log as
    //    well as through the front matter. The dialog's own checkbox is the
    //    only way the browser can say that, and until this nothing proved the
    //    box reached `rests_on` at all: it could have been read and dropped,
    //    and every assertion above would still pass.
    //
    //    Filing the first artifact left the page on that record's thread,
    //    which is where the offered basis comes from.
    await click(publishButton());
    await until("the dialog for the stamped predecessor", () => dialog());
    const cite = dialog().querySelector('input[type="checkbox"]');
    assert.ok(cite, "the thread offered no record to rest the stamped predecessor on");
    await act(async () => {
      cite.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    await type(field("what this artifact records"), "The superseded decision, stamped with its replacement");
    await type(field("path"), STAMPED_PATH);
    await type(field("commit"), DECISION_COMMIT);
    await act(async () => {
      dialog().dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
    });

    const stamped = await until(
      "the fold to project the stamped predecessor",
      async () => (await projection()).artifacts.find((artifact) => artifact.path === STAMPED_PATH),
      100,
    );
    const provenance = (await projection()).provenance[stamped.event] ?? [];
    assert.deepEqual(
      provenance,
      [landed.event],
      "the fold recorded no edge from the stamped predecessor to its replacement: the checkbox was shown and dropped",
    );
  } finally {
    // Unmounted here rather than in teardown, and on the failing path too, so
    // the page's parting call — the lease it departs — is always made while
    // the resident is still answering.
    await act(async () => mounted.unmount());
    mounted = undefined;
    await settle(3);
  }
});
