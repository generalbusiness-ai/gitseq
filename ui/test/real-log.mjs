// Seven records from this workroom's own log, as the fold projected them.
//
// Not a fixture invented to make a rule pass: it is the exact chain the two
// defects were found on, read out of a running resident's `/v0/status` at
// depth 13800 and pasted here unchanged. Event identifiers, sequences, kinds,
// lifecycles, satisfiers, actors, `provenance`, the commitment row and the
// fold's own decisions are verbatim. The only edits are to prose nothing here
// reads: three long texts and one very long `conditions` field are cut at a
// sentence and marked with an ellipsis, because a page of review conditions in
// a test file hides the seven lines that matter.
//
// Why these seven. #1 is the founding roster seed every workroom opens with.
// #6 is a proposal resting on it — the only kind of record that does. #11867
// and #11868 are two artifacts resting on that proposal. #11869 is a review
// request that opens by citing the first of those artifacts, and #11870 and
// #11872 are its promise and its approval. That first citation is the edge the
// thread walk used to read as a reply, and reading it that way put a review
// approval, its promise and its request inside the thread of "hugh begins the
// workroom".
//
// To refresh it: `curl -s http://127.0.0.1:7777/v0/status` and read
// `.durable.projection`, whose `statements`, `decisions`, `provenance`,
// `commitments` and `actors` are the five collections below.

const PREFIX = "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:";
const id = (suffix) => PREFIX + suffix;

export const SEED = id("efde1edc2e6757185845689cf1b68f6b820884b8");
export const PROPOSAL = id("b15de2f8788788a1afe970d6d077f7843862ebf2");
export const INTERFACE_NOTE = id("64cd3ab2b3b8f71d282e22dec10dfcccb5d4a3bb");
export const COMPANION_NOTE = id("fe61fd8a99ef6e18be5d5c3adac2ec10d8c73e60");
export const REVIEW_REQUEST = id("dd84764e34c4948c0beb00b75c7a33ea62272948");
export const REVIEW_PROMISE = id("7dd3374f9f75ace576ad066ef9d8ed615f05cbf8");
export const REVIEW_APPROVAL = id("234e4389438463a3b48043b56a7528062b9f64b2");

export const HUGH = "7fbc80f1ba06266cc375cb00507b94a631e5bed989180ad849fcd63175938275";
export const CODEX = "5f12e916d136b0c1ed941aa99f4c47af94021451e31d08159d8df368e9946123";
export const OX_CHECKER = "663e99d1a7876f383b8430669cfcd5633512bd45e545c790c65b60b87adc994a";

const STATEMENTS = [
  {
    event: SEED,
    sequence: 1,
    timestamp: 1786030594,
    actor: HUGH,
    kind: "roster",
    lifecycle: "none",
    satisfier: "role:ratifier",
    text: "hugh begins the workroom",
    body: { actor: HUGH, name: "hugh", role: "operator" },
  },
  {
    event: PROPOSAL,
    sequence: 6,
    timestamp: 1786030652,
    actor: CODEX,
    kind: "propose",
    lifecycle: "none",
    satisfier: "role:ratifier",
    text: "Keep the fold pure and total; unknown kinds remain opaque and all services translate verified records only.",
    ratified: true,
    ratified_by: id("3d1b1b2d375d616901f3b0f4e48bc1b21beb9236"),
  },
  {
    event: INTERFACE_NOTE,
    sequence: 11867,
    timestamp: 1787744699,
    actor: CODEX,
    kind: "artifact",
    lifecycle: "none",
    satisfier: "none",
    text: "Current implementation report for the JSONata/DDL application interface at exact clean pushed head cb994c6345830f34e29002fe66b0ad260de7a954. …",
    body: {
      branch: "request/jsonata-ddl-application-interface",
      commit: "cb994c6345830f34e29002fe66b0ad260de7a954",
      head: "cb994c6345830f34e29002fe66b0ad260de7a954",
      path: "notes/2026-08-26-jsonata-ddl-application-interface.md",
    },
    retired: true,
  },
  {
    event: COMPANION_NOTE,
    sequence: 11868,
    timestamp: 1787744699,
    actor: CODEX,
    kind: "artifact",
    lifecycle: "none",
    satisfier: "none",
    text: "Current implementation report for the JSONata/DDL companion design at exact clean pushed head cb994c6345830f34e29002fe66b0ad260de7a954. …",
    body: {
      branch: "request/jsonata-ddl-application-interface",
      commit: "cb994c6345830f34e29002fe66b0ad260de7a954",
      head: "cb994c6345830f34e29002fe66b0ad260de7a954",
      path: "notes/2026-08-26-jsonata-ddl-application-implementation.md",
    },
    retired: true,
  },
  {
    event: REVIEW_REQUEST,
    sequence: 11869,
    timestamp: 1787744723,
    actor: CODEX,
    kind: "request",
    lifecycle: "request",
    satisfier: "none",
    text: "Ox-checker: independently review exact clean pushed JSONata/DDL head cb994c6345830f34e29002fe66b0ad260de7a954 using current non-stale artifacts 64cd3ab2 and fe61fd8a.",
    body: {
      artifact: INTERFACE_NOTE,
      branch: "request/jsonata-ddl-application-interface",
      conditions: "Review only exact pushed head cb994c6345830f34e29002fe66b0ad260de7a954 and both same-head current artifacts: 64cd3ab2 for the interface note and fe61fd8a for the companion implementation-design note. …",
      head: "cb994c6345830f34e29002fe66b0ad260de7a954",
      to: OX_CHECKER,
    },
  },
  {
    event: REVIEW_PROMISE,
    sequence: 11870,
    timestamp: 1787744790,
    actor: OX_CHECKER,
    kind: "promise",
    lifecycle: "promise",
    satisfier: "none",
    text: "I take this review: exactly pushed head cb994c6345830f34e29002fe66b0ad260de7a954 against current artifacts 64cd3ab2 and fe61fd8a. …",
  },
  {
    event: REVIEW_APPROVAL,
    sequence: 11872,
    timestamp: 1787745061,
    actor: OX_CHECKER,
    kind: "report",
    lifecycle: "report",
    satisfier: "originating-requester",
    text: "APPROVED at exact head cb994c6345830f34e29002fe66b0ad260de7a954, reviewing current artifacts 64cd3ab2 (interface) and fe61fd8a (companion). …",
    ratified: true,
  },
];

// Verbatim, including the order the fold emitted each list in: #11869 opens on
// the interface note and only then names the proposal, and #11872 opens on the
// promise it answers. Which reference comes first is the whole question here,
// so the order is a fact and not a formatting choice.
const PROVENANCE = {
  [SEED]: [],
  [PROPOSAL]: [SEED],
  [INTERFACE_NOTE]: [PROPOSAL],
  [COMPANION_NOTE]: [PROPOSAL],
  [REVIEW_REQUEST]: [INTERFACE_NOTE, COMPANION_NOTE, PROPOSAL],
  [REVIEW_PROMISE]: [REVIEW_REQUEST],
  [REVIEW_APPROVAL]: [REVIEW_PROMISE, REVIEW_REQUEST, INTERFACE_NOTE, COMPANION_NOTE],
};

const DECISIONS = [
  { event: SEED, sequence: 1, verdict: "effective", reason: "operator roster seed" },
  { event: PROPOSAL, sequence: 6, verdict: "effective", reason: "statement recorded" },
  { event: INTERFACE_NOTE, sequence: 11867, verdict: "effective", reason: "statement recorded" },
  { event: COMPANION_NOTE, sequence: 11868, verdict: "effective", reason: "statement recorded" },
  { event: REVIEW_REQUEST, sequence: 11869, verdict: "effective", reason: "statement recorded" },
  { event: REVIEW_PROMISE, sequence: 11870, verdict: "effective", reason: "statement recorded" },
  { event: REVIEW_APPROVAL, sequence: 11872, verdict: "effective", reason: "statement recorded" },
];

const ACTORS = {
  [HUGH]: {
    name: "hugh",
    kind: "human",
    roles: ["operator", "participant", "ratifier"],
    membership_event: SEED,
    role_sources: { operator: [SEED], participant: [SEED] },
    dormant_role_sources: {},
    retired_role_sources: {},
  },
  [CODEX]: {
    name: "codex",
    kind: "agent",
    roles: ["participant"],
    membership_event: id("38c58f73e2c08ee95781917f6a178c56f8087535"),
    role_sources: { participant: [id("38c58f73e2c08ee95781917f6a178c56f8087535")] },
    dormant_role_sources: {},
    retired_role_sources: {},
  },
  [OX_CHECKER]: {
    name: "ox-checker",
    kind: "agent",
    roles: ["participant"],
    membership_event: id("0e6f6436f3625a49c9889bb068ad269a5d299f68"),
    role_sources: { participant: [id("0e6f6436f3625a49c9889bb068ad269a5d299f68")] },
    dormant_role_sources: {},
    retired_role_sources: {},
  },
};

const COMMITMENTS = [
  {
    request: REVIEW_REQUEST,
    requester: CODEX,
    performer: OX_CHECKER,
    promise: REVIEW_PROMISE,
    report: REVIEW_APPROVAL,
    status: "satisfied",
  },
];

// The six kinds this workroom's live vocabulary satisfies with `role:ratifier`,
// plus the three commitment kinds, each with the render class the fold
// publishes. Copied from the same `/v0/status`, under `.durable.vocabulary`.
export const VOCABULARY = {
  definitions: [
    { name: "admission-profile", satisfier: "role:ratifier", render: "governance", lifecycle: "none" },
    { name: "artifact", satisfier: "none", render: "artifact", lifecycle: "none" },
    { name: "assert", satisfier: "role:ratifier", render: "note", lifecycle: "none" },
    { name: "infra-key", satisfier: "role:ratifier", render: "governance", lifecycle: "none" },
    { name: "kind-def", satisfier: "role:ratifier", render: "governance", lifecycle: "none" },
    { name: "promise", satisfier: "none", render: "commitment", lifecycle: "promise" },
    { name: "propose", satisfier: "role:ratifier", render: "proposal", lifecycle: "none" },
    { name: "report", satisfier: "originating-requester", render: "result", lifecycle: "report" },
    { name: "request", satisfier: "none", render: "commitment", lifecycle: "request" },
    { name: "roster", satisfier: "role:ratifier", render: "governance", lifecycle: "none" },
    { name: "seal", satisfier: "role:ratifier", render: "governance", lifecycle: "none" },
  ],
};

// A fresh copy each call, so a test that edits one record to drive a variation
// cannot reach the next test through a shared object.
export function realProjection() {
  return structuredClone({
    decisions: DECISIONS,
    acts: [],
    statements: STATEMENTS,
    commitments: COMMITMENTS,
    reviews: [],
    artifacts: [],
    actors: ACTORS,
    provenance: PROVENANCE,
  });
}
