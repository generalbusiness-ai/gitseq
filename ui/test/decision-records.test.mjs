// The decision-records loop, as the browser offers it.
//
// Two things are pinned here and they are the two the browser can get wrong on
// its own. Which records a prefilled citation names, and whether the screen
// decides something the fold never said.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

import { createServer } from "vite";

const uiRoot = fileURLToPath(new URL("..", import.meta.url));

const ARTIFACT = "artifact-a";
const OTHER_ARTIFACT = "artifact-b";
const PATH = "docs/decisions/0001-use-postgres.md";
const COMMIT = "0123456789abcdef0123456789abcdef01234567";

function artifact(event, sequence) {
  return { event, sequence, actor: "codex", kind: "artifact", text: `pointer ${event}`, body: { path: PATH, commit: COMMIT }, timestamp: 1 };
}

function proposal(event, sequence, extra = {}) {
  return { event, sequence, actor: "codex", kind: "propose", text: `adopt ${event}`, ratified: true, timestamp: 1, ...extra };
}

// `statements`, `decisions` and `provenance` are all built from one list, so a
// test can reorder that list and change nothing about what the fold said.
function projectionOf(records, provenance, refused = new Set()) {
  return {
    decisions: records.map((record) => ({
      event: record.event,
      sequence: record.sequence,
      verdict: refused.has(record.event) ? "ineffective" : "effective",
      reason: "recorded",
    })),
    acts: [],
    statements: records,
    commitments: [],
    artifacts: records.filter((record) => record.kind === "artifact").map((record) => ({ event: record.event, path: PATH, commit: COMMIT, stale: false })),
    actors: {},
    provenance,
  };
}

async function load() {
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const records = await vite.ssrLoadModule("/src/lib/records.ts");
  const toolbar = await vite.ssrLoadModule("/src/components/Toolbar.tsx");
  return [{ ...records, ...toolbar }, () => vite.close()];
}

// The defect this pins: with nothing choosing between them, which proposal a
// review request cited came down to the order the projection happened to
// arrive in. `provenance` is a JSON object and its dependents a JSON array;
// both preserve emission order and neither is a contract. The fold's sequence
// is a contract, so the same records must give the same citation however they
// were serialised.
test("the cited proposal is chosen by the fold's sequence, not by serialisation order", async () => {
  const [{ buildRecordIndex }, close] = await load();
  try {
    const records = [artifact(ARTIFACT, 1), proposal("older", 5), proposal("newest", 9), proposal("middle", 7)];
    const dependents = ["older", "newest", "middle"];

    const forwards = buildRecordIndex(projectionOf(records, { [ARTIFACT]: [], ...Object.fromEntries(dependents.map((event) => [event, [ARTIFACT]])) }));
    assert.deepEqual(forwards.citableProposals(ARTIFACT), ["newest"]);

    // The same records, every list reversed. A reader who believed the first
    // or the last entry was "the" proposal would now get a different answer.
    const backwards = buildRecordIndex(
      projectionOf([...records].reverse(), Object.fromEntries([...dependents].reverse().map((event) => [event, [ARTIFACT]]).concat([[ARTIFACT, []]]))),
    );
    assert.deepEqual(backwards.citableProposals(ARTIFACT), ["newest"], "reordering the projection must not reorder the citation");
  } finally {
    await close();
  }
});

// The other half of the same defect: every qualifying proposal went into one
// record's causal references. Enough of them and the kernel will not admit the
// record at all; long before that, contradictory proposals all appear to
// govern one review and a reader cannot tell which the signer meant.
test("the citation prefill is bounded however many proposals qualify", async () => {
  const [{ buildRecordIndex, CITED_PROPOSAL_LIMIT }, close] = await load();
  try {
    const many = Array.from({ length: 500 }, (_, position) => proposal(`p-${position}`, position + 2));
    const index = buildRecordIndex(
      projectionOf([artifact(ARTIFACT, 1), ...many], { [ARTIFACT]: [], ...Object.fromEntries(many.map((p) => [p.event, [ARTIFACT]])) }),
    );
    const cited = index.citableProposals(ARTIFACT);
    assert.equal(cited.length, CITED_PROPOSAL_LIMIT);
    assert.ok(cited.length < many.length);
    assert.deepEqual(cited, ["p-499"], "and the bound keeps the newest, not whichever arrived first");
  } finally {
    await close();
  }
});

test("only what the fold says is ratified, standing and effective is cited", async () => {
  const [{ buildRecordIndex }, close] = await load();
  try {
    const records = [
      artifact(ARTIFACT, 1),
      proposal("unratified", 9, { ratified: false }),
      proposal("withdrawn", 8, { retired: true }),
      proposal("refused", 7),
      proposal("standing", 6),
      { event: "an-assert", sequence: 5, actor: "codex", kind: "assert", text: "not a proposal", ratified: true, timestamp: 1 },
    ];
    const dependents = ["unratified", "withdrawn", "refused", "standing", "an-assert"];
    const index = buildRecordIndex(
      projectionOf(records, { [ARTIFACT]: [], ...Object.fromEntries(dependents.map((event) => [event, [ARTIFACT]])) }, new Set(["refused"])),
    );
    assert.deepEqual(index.citableProposals(ARTIFACT), ["standing"]);
  } finally {
    await close();
  }
});

// The architecture defect, in one assertion. The fold projects no relation
// between a proposal and a decision record, so a proposal that adopted a
// *different* artifact at the same path is not evidence about this one. The
// browser follows the citation edge the fold does project and joins nothing.
test("the prefill follows the fold's citation edge and never a shared path", async () => {
  const [{ buildRecordIndex }, close] = await load();
  try {
    const index = buildRecordIndex(
      projectionOf([artifact(ARTIFACT, 1), artifact(OTHER_ARTIFACT, 2), proposal("adopts-the-other", 3)], {
        [ARTIFACT]: [],
        [OTHER_ARTIFACT]: [],
        "adopts-the-other": [OTHER_ARTIFACT],
      }),
    );
    assert.deepEqual(index.citableProposals(OTHER_ARTIFACT), ["adopts-the-other"]);
    assert.deepEqual(
      index.citableProposals(ARTIFACT),
      [],
      "two artifacts sharing a path are two records; the fold relates neither to the other",
    );
  } finally {
    await close();
  }
});

// Which acts the row offers, and which records it resolves into the citation.
// Whether a decision is adopted is not a fact the fold projects, so the row
// does not decide it: both acts are offered and the operator picks. Gating one
// of them would mean inventing the fact here.
//
// This is about what the row resolves. That the operator can then read those
// resolved citations in the composer before signing them is a separate
// condition, and it is asserted on the rendered composer in
// before-signing.test.mjs — an array a spy caught is not a disclosure.
test("an artifact row offers both acts and gates neither on an adoption it cannot know", async () => {
  const [{ buildRecordIndex, semanticActions }, close] = await load();
  try {
    const routed = [];
    const labels = (records, provenance) => {
      routed.length = 0;
      const projection = {
        ...projectionOf(records, provenance),
        actors: { hugh: { name: "hugh", kind: "human", roles: ["participant"] } },
      };
      return semanticActions({
        statement: records[0],
        projection,
        index: buildRecordIndex(projection),
        me: "hugh",
        onRoute: (...args) => routed.push(args),
        doAct() {},
      });
    };

    const unadopted = labels([artifact(ARTIFACT, 1)], { [ARTIFACT]: [] });
    assert.deepEqual(unadopted.map(({ label }) => label), ["propose adoption", "request review"]);

    const many = Array.from({ length: 40 }, (_, position) => proposal(`p-${position}`, position + 2));
    const adopted = labels([artifact(ARTIFACT, 1), ...many], {
      [ARTIFACT]: [],
      ...Object.fromEntries(many.map((p) => [p.event, [ARTIFACT]])),
    });
    assert.deepEqual(
      adopted.map(({ label }) => label),
      ["propose adoption", "request review"],
      "a standing proposal does not withdraw the offer to make another",
    );

    adopted.find(({ label }) => label === "request review").run();
    const [[mode, bases, prefill, body]] = routed;
    assert.equal(mode, "request");
    assert.deepEqual(bases, [ARTIFACT, "p-39"], "the artifact and one determinate proposal, and nothing else");
    assert.match(prefill, /Review docs\/decisions\/0001-use-postgres\.md at its exact head/);
    assert.deepEqual(body, { artifact: ARTIFACT, head: COMMIT }, "the identifiers the row already holds are never retyped");

    routed.length = 0;
    unadopted.find(({ label }) => label === "propose adoption").run();
    assert.deepEqual(routed[0][1], [ARTIFACT]);
  } finally {
    await close();
  }
});
