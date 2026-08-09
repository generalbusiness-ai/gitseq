import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

function workroom(presence) {
  const projection = {
    decisions: [],
    acts: [],
    statements: [],
    commitments: [],
    artifacts: [],
    actors: {},
    provenance: {},
  };
  return {
    actors: [
      { name: "claude", fingerprint: "a5d35aa7e4799472", roles: [], custody: true },
      { name: "claude", fingerprint: "0011223344ff5566", roles: [], custody: true },
    ],
    commits: [],
    graphTruncated: false,
    offline: false,
    localOffline: false,
    status: {
      durable: { genesis: "genesis", head: "head", depth: 1, projection },
      live: { cursor: { generation: "generation", position: 1 }, presence, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

const session = { id: "browser", live: true, setActor() {} };

test("identity and personal Work state stay honest at rendered component boundaries", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const [{ TopBar }, { ProfilePane }, { WorkView }] = await Promise.all([
      vite.ssrLoadModule("/src/components/TopBar.tsx"),
      vite.ssrLoadModule("/src/components/ProfilePane.tsx"),
      vite.ssrLoadModule("/src/components/WorkDrawer.tsx"),
    ]);
    const room = workroom({
      "handle:first": "claude (a5d35aa7e479)",
      "handle:second": "claude (0011223344ff)",
    });
    const topBar = renderToStaticMarkup(
      React.createElement(TopBar, {
        workroom: room,
        session,
        mainView: "work",
        onShowWork() {},
        onShowActivity() {},
        onJumpEvent() {},
        onOpenProfile() {},
      }),
    );

    // Avatar rendering and its click target receive the full fingerprint,
    // rather than both same-name entries resolving to the first actor.
    assert.match(topBar, /data-fingerprint="0011223344ff5566"/);
    assert.match(topBar, /data-fingerprint="a5d35aa7e4799472"/);

    const onlyFirstPresent = workroom({ "handle:first": "claude (a5d35aa7e479)" });
    const profile = (fingerprint) =>
      renderToStaticMarkup(
        React.createElement(ProfilePane, {
          workroom: onlyFirstPresent,
          session,
          fingerprint,
          onClose() {},
          onJumpTo() {},
        }),
    );
    assert.match(profile("a5d35aa7e4799472"), /aria-label="online"/);
    assert.match(profile("0011223344ff5566"), /aria-label="away"/);
    const ambiguousPersonal = renderToStaticMarkup(
      React.createElement(WorkView, {
        workroom: room,
        session: { ...session, actor: "claude" },
        highlight: { events: new Set(), commits: new Set() },
        onSelect() {},
        onOpenThread() {},
      }),
    );
    assert.doesNotMatch(ambiguousPersonal, /aria-label="Personal work filters"|aria-label="follow topic"/);

    const personalRoom = workroom({});
    personalRoom.actors = [
      { name: "codex", fingerprint: "codex-fingerprint", roles: [], custody: true },
      { name: "claude", fingerprint: "claude-fingerprint", roles: [], custody: true },
    ];
    personalRoom.status.durable.projection = {
      decisions: ["request", "change"].map((event) => ({ event, verdict: "effective", reason: "ok" })),
      acts: [], artifacts: [], actors: {},
      statements: [
        { event: "request", actor: "codex-fingerprint", kind: "request", text: "Review the release", body: { to: "codex-fingerprint" }, timestamp: 10 },
        { event: "change", actor: "claude-fingerprint", kind: "assert", text: "Release changed", timestamp: 20 },
      ],
      commitments: [{ request: "request", requester: "codex-fingerprint", addressed_to: "codex-fingerprint", status: "open" }],
      provenance: { request: [], change: ["request"] },
    };
    const markup = renderToStaticMarkup(
      React.createElement(WorkView, {
        workroom: personalRoom,
        session: { ...session, actor: "codex" },
        highlight: { events: new Set(), commits: new Set() },
        onSelect() {},
        onOpenThread() {},
      }),
    );

    assert.match(markup, /Needs my action<span[^>]*>1<\/span>/);
    assert.match(markup, /Unread<span[^>]*>1<\/span>/);
    assert.match(markup, /Following<span[^>]*>0<\/span>/);
    assert.match(markup, /private to this browser and actor; they do not sync across devices/);
    assert.match(markup, /Needs my action comes only from unresolved durable responsibility/);
    assert.match(markup, /aria-label="follow topic"/);
    assert.match(markup, /Changed since viewed by claude, status open/);

    const multiRoom = workroom({});
    multiRoom.actors = personalRoom.actors;
    multiRoom.status.durable.projection = {
      decisions: ["open-a", "change-a", "open-b", "change-b", "closed-c", "change-c"].map((event) => ({ event, verdict: "effective", reason: "ok" })),
      acts: [], artifacts: [], actors: {},
      statements: [
        { event: "open-a", actor: "codex-fingerprint", kind: "request", text: "Open A", body: { to: "codex-fingerprint" }, timestamp: 10 },
        { event: "change-a", actor: "claude-fingerprint", kind: "assert", text: "Changed A", timestamp: 11 },
        { event: "open-b", actor: "codex-fingerprint", kind: "request", text: "Open B", body: { to: "claude-fingerprint" }, timestamp: 20 },
        { event: "change-b", actor: "claude-fingerprint", kind: "assert", text: "Changed B", timestamp: 21 },
        { event: "closed-c", actor: "codex-fingerprint", kind: "request", text: "Closed C", body: { to: "claude-fingerprint" }, timestamp: 30 },
        { event: "change-c", actor: "claude-fingerprint", kind: "assert", text: "Changed C", timestamp: 31 },
      ],
      commitments: [
        { request: "open-a", requester: "codex-fingerprint", addressed_to: "codex-fingerprint", status: "open" },
        { request: "open-b", requester: "codex-fingerprint", addressed_to: "claude-fingerprint", status: "open" },
        { request: "closed-c", requester: "codex-fingerprint", addressed_to: "claude-fingerprint", status: "satisfied" },
      ],
      provenance: { "open-a": [], "change-a": ["open-a"], "open-b": [], "change-b": ["open-b"], "closed-c": [], "change-c": ["closed-c"] },
    };
    const renderWork = (initial) => renderToStaticMarkup(
      React.createElement(WorkView, {
        workroom: multiRoom,
        session: { ...session, actor: "codex" },
        highlight: { events: new Set(), commits: new Set() },
        onSelect() {},
        onOpenThread() {},
        ...initial,
      }),
    );

    const needsAfterViewing = renderWork({
      initialPersonalView: "needs",
      initialPersonalMemory: { followed: [], viewed: { "open-a": 999 } },
    });
    assert.match(needsAfterViewing, />Open A</);

    const unread = renderWork({
      initialPersonalView: "unread",
      initialPersonalMemory: { followed: ["open-b"], viewed: { "open-a": 999 } },
    });
    assert.doesNotMatch(unread, />Open A</);
    assert.match(unread, />Open B</);
    assert.match(unread, />Closed C</);

    const board = renderWork({
      initialPresentation: "board",
      initialPersonalMemory: { followed: ["open-b"], viewed: { "open-a": 999 } },
    });
    assert.match(board, /id="lane-available"/);
    assert.match(board, />Open B</);
    assert.match(board, /Changed since viewed by claude, status open/);
    assert.match(board, /aria-pressed="true" aria-label="unfollow topic"/);
  } finally {
    await vite.close();
  }
});

// The reason an act carries no force belongs beside that act. This is the
// regression that matters: the previous surface counted unreadable acts in a
// panel and a header badge while the explanation lived only in a hover title,
// so the room looked broken and no reader could learn why.
test("an unreadable act explains itself where it is rendered", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { Stream } = await vite.ssrLoadModule("/src/components/Stream.tsx");
    const { workSummary } = await vite.ssrLoadModule("/src/lib/store.ts");

    const event = "git:sha1:genesis#git:sha1:unreadable";
    const projection = {
      decisions: [{ event, verdict: "undefined-kind", reason: 'undefined kind "commit"' }],
      acts: [],
      statements: [{
        event, timestamp: 1786200000, actor: "a5d35aa7e4799472", kind: "commit",
        text: "I will re-review task/docs at exact head 212820ca and report concrete approval or findings.",
      }],
      commitments: [], artifacts: [], actors: {}, provenance: {},
    };
    const room = {
      actors: [{ name: "codex", fingerprint: "a5d35aa7e4799472", roles: [], custody: true }],
      commits: [], graphTruncated: false, offline: false, localOffline: false,
      status: {
        durable: { genesis: "genesis", head: "head", depth: 1, projection },
        live: { cursor: { generation: "g", position: 1 }, presence: {}, conversations: [] },
        cursor: { frontier: [], live: { generation: "g", position: 1 } },
      },
    };
    const html = renderToStaticMarkup(
      React.createElement(Stream, {
        workroom: room, session, frames: [], deliveries: 0,
        highlight: { events: new Set(), commits: new Set() },
        composer: { type: "say", restsOn: [], frames: [] }, pending: [],
        onSelect() {}, onJump() {}, onComposer() {}, onReconcile() {},
        onOpenThread() {}, onRoute() {}, onOpenProfile() {}, doAct() {},
      }),
    );

    // The fold's own reason, rendered as text rather than hidden in a title.
    assert.match(html, /undefined kind/, "the reason the room could not read the act is not rendered");
    const visible = html.replace(/title="[^"]*"/g, "");
    assert.match(visible, /undefined kind/, "the reason appears only in a hover title");
    // And what it cost: the promise this act was written to be never formed.
    assert.match(visible, /never formed/, "the consequence of the refusal is not stated");

    // A standing interpretive limit is not work waiting on anyone.
    assert.equal(workSummary(projection).stale, 0, "an unreadable act is counted as attention owed");
  } finally {
    await vite.close();
  }
});
