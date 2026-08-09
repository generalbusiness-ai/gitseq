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

test("same-name presence stays fingerprint-based at the rendered component boundary", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const [{ TopBar }, { ProfilePane }] = await Promise.all([
      vite.ssrLoadModule("/src/components/TopBar.tsx"),
      vite.ssrLoadModule("/src/components/ProfilePane.tsx"),
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
