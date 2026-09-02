import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { projectName, tabTitle } from "../src/lib/title.ts";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

test("the tab title is the served checkout's folder name plus workroom", () => {
  assert.equal(projectName({ repo: "/Users/hugh/play/gitseq" }), "gitseq");
  assert.equal(tabTitle(projectName({ repo: "/Users/hugh/play/gitseq" })), "gitseq workroom");
  assert.equal(projectName({ repo: "/srv/tailapp/" }), "tailapp");
  assert.equal(projectName({ repo: "C:\\work\\tailapp.git" }), "tailapp");
});

test("a service started from a linked worktree is named after that worktree", () => {
  // The service sorts the current checkout first and marks no main tree, so
  // the served path is the only honest name; see the note in title.ts.
  assert.equal(tabTitle(projectName({ repo: "/Users/hugh/play/gitseq-worktrees/ui-title-favicon" })), "ui-title-favicon workroom");
});

test("the title falls back to workroom before the service answers or when it names nothing", () => {
  assert.equal(tabTitle(undefined), "workroom");
  assert.equal(tabTitle(projectName(undefined)), "workroom");
  assert.equal(tabTitle(projectName({ repo: "" })), "workroom");
  assert.equal(tabTitle(projectName({ repo: "/" })), "workroom");
});

test("index.html ships the static fallback title and references the favicon", () => {
  const html = readFileSync(`${uiRoot}index.html`, "utf8");
  assert.match(html, /<title>workroom<\/title>/);
  assert.match(html, /<link rel="icon" type="image\/svg\+xml" href="\/favicon\.svg" \/>/);
  const svg = readFileSync(`${uiRoot}public/favicon.svg`, "utf8");
  assert.match(svg, /viewBox="0 0 32 32"/);
});
