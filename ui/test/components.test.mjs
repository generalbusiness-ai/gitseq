import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

function workroom(presence, suppliedProjection) {
  const projection = suppliedProjection ?? {
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

test("durable threads expose a railway whose rendered edges distinguish citations", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const [{ ThreadPane }, { ThreadRailway }] = await Promise.all([
      vite.ssrLoadModule("/src/components/ThreadPane.tsx"),
      vite.ssrLoadModule("/src/components/ThreadRailway.tsx"),
    ]);
    const statement = (event, actor, kind, text) => ({ event, actor, kind, text, timestamp: 1_700_000_000 });
    const statements = [
      statement("root", "codex", "request", "Build the railway"),
      statement("first", "claude", "promise", "I will review it"),
      statement("branch", "hugh", "assert", "Keep the thread local"),
      statement("join", "codex", "report", "Railway ready"),
    ];
    const act = { event: "agree", actor: "hugh", type: "ratify", target: "join", verdict: "effective", reason: "authorized", timestamp: 1_700_000_001 };
    const projection = {
      decisions: [...statements.map((item) => ({ event: item.event, verdict: "effective", reason: "recorded" })), { event: act.event, verdict: "effective", reason: "authorized" }],
      acts: [act],
      statements,
      commitments: [],
      artifacts: [],
      actors: {},
      provenance: { root: [], first: ["root"], branch: ["root"], join: ["branch", "first"], agree: ["join"] },
    };
    const thread = { statements: statements.slice(1), acts: [act], events: ["first", "branch", "join", "agree"] };
    const tickets = new Map(projection.decisions.map((decision, index) => [decision.event, index + 1]));
    const railway = renderToStaticMarkup(
      React.createElement(ThreadRailway, {
        root: statements[0],
        thread,
        projection,
        tickets,
        nameOf: (actor) => actor,
        onJumpTo() {},
      }),
    );
    assert.match(railway, /data-thread-railway="true"/);
    assert.equal((railway.match(/data-thread-rail-event=/g) ?? []).length, 5);
    assert.match(railway, /stroke-dasharray="3 3"/);
    assert.match(railway, /cites #2/);

    const panel = renderToStaticMarkup(
      React.createElement(ThreadPane, {
        workroom: workroom({}, projection),
        session: { ...session, actor: "codex" },
        frames: [],
        target: { kind: "event", event: "root" },
        pending: [],
        composer: { restsOn: [], frames: [], type: "assert" },
        onComposer() {},
        onClose() {},
        onJumpTo() {},
        onOpenProfile() {},
        onRoute() {},
        doAct() {},
        onSay() { return "pending"; },
        onSayFailed() {},
      }),
    );
    assert.match(panel, /aria-label="Durable thread view"/);
    assert.match(panel, />Thread<\/button>/);
    assert.match(panel, / Railway<\/button>/);
  } finally {
    await vite.close();
  }
});
