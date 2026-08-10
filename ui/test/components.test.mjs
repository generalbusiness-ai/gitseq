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
      live: { cursor: { generation: "generation", position: 1 }, presence, activity: {}, conversations: [] },
      cursor: { frontier: [], live: { generation: "generation", position: 1 } },
    },
  };
}

const session = { id: "browser", live: true, setActor() {} };

test("every durable Stream presentation renders advisory focus behaviorally", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  const previousDocument = globalThis.document;
  globalThis.document = { title: "gitseq", hasFocus: () => true };
  try {
    const { Stream, CompactRow, Card } = await vite.ssrLoadModule("/src/components/Stream.tsx");
    const statement = { event: "focused-event", actor: "codex-fingerprint", kind: "assert", text: "Focused work", timestamp: 1_700_000_000 };
    const projection = {
      decisions: [{ event: statement.event, verdict: "effective", reason: "recorded" }],
      acts: [], artifacts: [], actors: {}, statements: [statement], commitments: [], provenance: { [statement.event]: [] },
    };
    const focused = [{
      label: "codex (codex-finger)", name: "codex", fingerprint: "codex-finger", sessions: 1,
      status: "blocked", focus: [statement.event], note: "waiting on review",
    }];
    const room = workroom({ "handle:codex": "codex (codex-finger)" }, projection);
    room.actors = [{ name: "codex", fingerprint: "codex-fingerprint", roles: [], custody: true }];
    room.status.live.activity = { "handle:codex": { status: "blocked", focus: [statement.event], note: "waiting on review" } };
    const noop = () => {};
    const rowProps = {
      statement, ticket: 1, decision: projection.decisions[0], projection, tickets: new Map([[statement.event, 1]]),
      notes: new Map(), nameOf: () => "codex", bright: false, selected: false, cited: false, focused,
      onSelect: noop, onJumpTo: noop, onCite: noop, onOpenThread: noop, onOpenProfile: noop, onRoute: noop, doAct: noop,
    };
    const streamMarkup = renderToStaticMarkup(React.createElement(Stream, {
      workroom: room,
      session: { ...session, actor: "codex", activity: { status: "available", focus: [] }, setActivity: noop },
      frames: [], deliveries: 0, highlight: { events: new Set(), commits: new Set() }, onSelect: noop, onJump: noop,
      composer: { type: "say", restsOn: [], frames: [] }, onComposer: noop, pending: [], onReconcile: noop,
      onOpenThread: noop, onRoute: noop, onOpenProfile: noop, doAct: noop,
    }));
    const compactMarkup = renderToStaticMarkup(React.createElement(CompactRow, rowProps));
    const cardMarkup = renderToStaticMarkup(React.createElement(Card, rowProps));
    for (const [presentation, markup] of [["recorded", streamMarkup], ["compact", compactMarkup], ["card", cardMarkup]]) {
      assert.match(markup, /aria-label="Focused here: codex \(blocked\)"/, `${presentation} presentation omitted focused actor`);
      assert.match(markup, /title="codex — blocked — waiting on review"/, `${presentation} presentation omitted focus detail`);
      assert.match(markup, /codex · blocked/, `${presentation} presentation omitted focus status`);
    }
  } finally {
    globalThis.document = previousDocument;
    await vite.close();
  }
});

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
    personalRoom.status.live.presence = { "handle:codex": "codex (codex-finger)" };
    personalRoom.status.live.activity = { "handle:codex": { status: "blocked", focus: ["request"], note: "waiting on review" } };
    const activeSession = { ...session, actor: "codex", activity: { status: "blocked", focus: ["request"] }, setActivity() {} };
    const activeTopBar = renderToStaticMarkup(
      React.createElement(TopBar, {
        workroom: personalRoom,
        session: activeSession,
        selection: { kind: "event", id: "request" },
        mainView: "work",
        onShowWork() {}, onShowActivity() {}, onJumpEvent() {}, onOpenProfile() {},
      }),
    );
    assert.match(activeTopBar, /aria-label="Activity status"/);
    assert.match(activeTopBar, /aria-pressed="true"[^>]*>unfocus</);
    assert.match(activeTopBar, />clear<\/button>/);
    assert.match(activeTopBar, /codex — blocked — waiting on review/);
    const markup = renderToStaticMarkup(
      React.createElement(WorkView, {
        workroom: personalRoom,
        session: activeSession,
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
    assert.match(markup, /Focused here: codex \(blocked\)/);
    assert.match(markup, /codex · blocked/);

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
    assert.match(visible, /recorded without force/, "the consequence of the refusal is not stated");
    assert.doesNotMatch(visible, /citation/i, "claims a citation never formed, but provenance is projected for every record");

    // A standing interpretive limit is not work waiting on anyone.
    assert.equal(workSummary(projection).stale, 0, "an unreadable act is counted as attention owed");
  } finally {
    await vite.close();
  }
});

test("Vocabulary copy distinguishes an unbound room from an uninterpretable fold transition", async () => {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  try {
    const { WorkView } = await vite.ssrLoadModule("/src/components/WorkDrawer.tsx");
    const definition = (name, source) => ({
      name, fields: [], basis: [], satisfier: "none", render: "note", staleness: "exempt",
      lifecycle: "none", guidance: `${name} guidance`, source,
    });
    const renderVocabulary = (definitions, binding) => {
      const room = workroom({});
      room.status.durable.vocabulary = { definitions, binding };
      return renderToStaticMarkup(
        React.createElement(WorkView, {
          workroom: room,
          session,
          highlight: { events: new Set(), commits: new Set() },
          onSelect() {},
          onOpenThread() {},
        }),
      );
    };

    const unbound = renderVocabulary(
      [definition("request", "starter")],
      { status: "unbound", reason: "no ratified seed or prefix binding", transitions: [] },
    );
    assert.match(unbound, /starter kinds only/);
    assert.match(unbound, /no declared vocabulary extends it yet/);
    assert.match(unbound, /no ratified seed or prefix binding/);

    const uninterpretable = renderVocabulary(
      [definition("request", "starter"), definition("finding", "declared")],
      {
        status: "uninterpretable",
        reason: "activated interpreter execution is not held",
        transitions: [{
          activation: "activation", ratification: "ratification", fold: "example/fold@abc123",
          entry: "example/fold", interface: "workroom-fold@1", toolchain: "go1.25.0", prefix: true,
        }],
      },
    );
    assert.match(uninterpretable, /interpretation stopped after 1 fold transition/);
    assert.match(uninterpretable, /reached 1 activated fold transition but cannot\s+interpret records beyond it/);
    assert.match(uninterpretable, /activated interpreter execution is not held/);
    assert.match(uninterpretable, /definitions this reader established before that transition; declared kinds remain declared/);
    assert.match(uninterpretable, /finding[\s\S]*note · declared/);
    assert.doesNotMatch(uninterpretable, /starter kinds only|no declared vocabulary extends it yet/);
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

    // The rail reads from the root toward the present, and the rows are in
    // that order in the document, not merely positioned in it.
    assert.deepEqual(
      [...railway.matchAll(/data-thread-rail-event="([^"]+)"/g)].map((match) => match[1]),
      ["root", "first", "branch", "join", "agree"],
    );

    // The root's own basis lies outside this thread. Every event carries a
    // ticket, so only the laid-out rows can say what is on the thread.
    const rootOutside = renderToStaticMarkup(
      React.createElement(ThreadRailway, {
        root: statements[0],
        thread,
        projection: { ...projection, provenance: { ...projection.provenance, root: ["elsewhere"] } },
        tickets: new Map([...tickets, ["elsewhere", 99]]),
        nameOf: (actor) => actor,
        onJumpTo() {},
      }),
    );
    assert.match(rootOutside, /reply to #99 \(outside this thread\)/);
    assert.match(rootOutside, /reply to #1</); // an on-thread reply says only its ticket

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
    // The pane opens on the conversation; the rail waits behind its own tab.
    assert.doesNotMatch(panel, /data-thread-railway/);
  } finally {
    await vite.close();
  }
});

test("the railway carries the same status marks as the conversation it draws", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const { ThreadRailway } = await vite.ssrLoadModule("/src/components/ThreadRailway.tsx");
    const statements = [
      { event: "root", actor: "codex", kind: "request", text: "Draw the rail", timestamp: 1 },
      { event: "agreed", actor: "claude", kind: "promise", text: "Agreed work", ratified: true, timestamp: 2 },
      { event: "moved", actor: "claude", kind: "report", text: "Work the world moved under", stale: true, timestamp: 3 },
      { event: "gone", actor: "claude", kind: "assert", text: "Taken back", retired: true, timestamp: 4 },
    ];
    const acts = [
      { event: "refused", actor: "hugh", type: "ratify", target: "agreed", verdict: "unauthorized", reason: "not yours to agree", timestamp: 5 },
      { event: "undone", actor: "hugh", type: "supersede", target: "gone", verdict: "effective", reason: "authorized", timestamp: 6 },
    ];
    const projection = {
      decisions: [...statements, ...acts].map((item) => ({ event: item.event, verdict: "effective", reason: "recorded" })),
      acts,
      statements,
      commitments: [],
      artifacts: [],
      actors: {},
      provenance: { root: [], agreed: ["root"], moved: ["agreed"], gone: ["root"], refused: ["agreed"], undone: ["gone"] },
    };
    const markup = renderToStaticMarkup(
      React.createElement(ThreadRailway, {
        root: statements[0],
        thread: { statements: statements.slice(1), acts, events: ["agreed", "moved", "gone", "refused", "undone"] },
        projection,
        tickets: new Map(projection.decisions.map((decision, index) => [decision.event, index + 1])),
        nameOf: (actor) => actor,
        onJumpTo() {},
      }),
    );
    assert.match(markup, /aria-label="ratified"/);
    assert.match(markup, />stale</);
    assert.match(markup, /line-through/);
    assert.match(markup, /aria-label="unauthorized"/);
    assert.match(markup, /aria-label="withdrawn"/);
  } finally {
    await vite.close();
  }
});

test("a rail too wide for the pane stops at ten lanes and says what it folded", async () => {
  const vite = await createServer({
    root: uiRoot,
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  try {
    const { ThreadRailway } = await vite.ssrLoadModule("/src/components/ThreadRailway.tsx");
    // Fifteen branches of the root that all stay open to the end.
    const children = Array.from({ length: 15 }, (_, index) => `child-${index + 1}`);
    const events = [...children, ...children.map((child) => `${child}-leaf`)];
    const statements = [
      { event: "root", actor: "codex", kind: "request", text: "Wide thread", timestamp: 1 },
      ...events.map((event, index) => ({ event, actor: "claude", kind: "assert", text: event, timestamp: index + 2 })),
    ];
    const provenance = { root: [] };
    for (const child of children) {
      provenance[child] = ["root"];
      provenance[`${child}-leaf`] = [child];
    }
    const projection = {
      decisions: statements.map((statement) => ({ event: statement.event, verdict: "effective", reason: "recorded" })),
      acts: [],
      statements,
      commitments: [],
      artifacts: [],
      actors: {},
      provenance,
    };
    const markup = renderToStaticMarkup(
      React.createElement(ThreadRailway, {
        root: statements[0],
        thread: { statements: statements.slice(1), acts: [], events },
        projection,
        tickets: new Map(projection.decisions.map((decision, index) => [decision.event, index + 1])),
        nameOf: (actor) => actor,
        onJumpTo() {},
      }),
    );
    // Ten lanes at 22px, 14px of left margin and 10px of right: 244px, well
    // inside the 24rem pane. An unbounded rail would be several times that.
    assert.match(markup, /<svg width="244"/);
    assert.match(markup, /data-thread-rail-folded="12"/);
    assert.match(markup, /12 of 31 events did not fit/);
    assert.match(markup, /The rail stops at 10 lanes/);
    // Folded rows are marked on the rail and in their own text, so a reader
    // can tell a shared lane from a lane of its own.
    assert.equal((markup.match(/data-thread-rail-folded-row/g) ?? []).length, 12);
    assert.equal((markup.match(/<rect /g) ?? []).length, 12);
  } finally {
    await vite.close();
  }
});
