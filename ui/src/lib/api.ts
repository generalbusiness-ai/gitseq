// Types mirror the Go service; the workroom projection is the fold's output.

// One list of verdicts, named once. Declared kinds add refusals the fold can
// reach, and a second copy of this union is a place for them to go missing.
export type Verdict = "effective" | "ineffective" | "disputed" | "undefined-kind" | "uninterpretable";

export interface Decision {
  sequence: number;
  event: string;
  verdict: Verdict;
  reason: string;
}

export interface Statement {
  event: string;
  // The event's position in this workroom's log, counting the founding seed as
  // 1. Meaningful only within one genesis: two workrooms both have a #17.
  sequence: number;
  timestamp?: number;
  actor: string;
  kind: string;
  // The commitment role this record was decided under: the definition in force
  // at its own position, not whichever definition of that kind stands now.
  // Classifying a historical record by the current vocabulary gives a different
  // answer than the fold gave.
  lifecycle?: string;
  // Who may ratify this statement, from the same captured definition
  // `lifecycle` comes from: the one in force when this record was admitted.
  // The fold decides ratifications against exactly that, so reading it here
  // rather than looking the kind up in the live vocabulary is what keeps the
  // screen and the fold from disagreeing after a kind is redefined. Absent
  // means the fold bound no definition, and nothing may be ratified on it.
  satisfier?: string;
  text: string;
  body?: Record<string, string>;
  ratified?: boolean;
  // The act that ratifies this statement now: the latest ratification of it
  // that is neither retired nor ineffective. `ratified` is this being present.
  // Read it rather than searching the acts — acts carry no retirement, so
  // neither the first nor the last effective ratification of a target is the
  // answer, and telling them apart here would mean rebuilding the fold's
  // retirement rule in the browser.
  ratified_by?: string;
  retired?: boolean;
  stale?: boolean;
  // Narrows stale: what moved was the world this statement describes.
  describes_superseded_world?: boolean;
  world_superseded_at?: number;
  merge_left_live?: LeftLiveAccounting[];
  // The nearest retired basis reached only through edges that actually made
  // this row stale, plus its artifact path and bounded-walk exhaustion flag.
  stale_because?: string;
  stale_because_path?: string;
  stale_because_truncated?: boolean;
}

export interface LeftLiveAccounting {
  artifact: string;
  class: string;
  commitment?: string;
  verified: boolean;
  reason?: string;
}

export interface Commitment {
  request: string;
  requester: string;
  addressed_to?: string;
  performer?: string;
  promise?: string;
  report?: string;
  status: string;
  successor_request?: string;
  waiting_on?: string;
  // Staleness/dispute can qualify a lifecycle state. These optional fields
  // let clients preserve the underlying
  // open/promised/reported/awaiting-review/awaiting-authorization/awaiting-landing/terminal
  // state when the projection supplies the richer shape.
  stale?: boolean;
  // The landing obligation, as the fold projects it. target_repo and
  // target_ref name where this commitment owes its result; legacy says the
  // fold read that from the commitment's own history rather than from a stated
  // choice. Absent means the request owes no Git artifact.
  target_repo?: string;
  target_ref?: string;
  legacy?: boolean;
  // hold_owner is the one actor who may release a held landing, and release
  // names the authorization that did. approval and candidate name the ratified
  // approval and the exact head it approved.
  hold_owner?: string;
  release?: string;
  approval?: string;
  candidate?: string;
  // Nonterminal evidence beside the commitment: a report that says where the
  // work stands without closing it.
  latest_resolution?: string;
  // How a closed commitment closed: landed, reported, or abandoned.
  terminal?: string;
  // Measured against the target ref, never against main.
  approved_not_landed?: boolean;
}

export interface Artifact {
  event: string;
  path: string;
  commit: string;
  // Retired and stale are separate facts: the pointer was withdrawn, or a
  // basis under it moved while the commit it names stayed exactly what it was.
  retired?: boolean;
  // Narrows retired: the act that withdrew this pointer also rested on an
  // artifact covering the same path, so it says where the behaviour went. A
  // retirement naming nothing is a condemnation, and the two mean opposite
  // things to whoever stood on the artifact.
  succeeded?: boolean;
  stale: boolean;
  // Narrows stale: the retired basis underneath is itself an artifact, so the
  // world moved rather than a pointer being withdrawn.
  describes_superseded_world?: boolean;
  world_superseded_at?: number;
  // The fold's bounded causal explanation; this is not a second provenance
  // graph in the client.
  stale_because?: string;
  stale_because_path?: string;
  stale_because_truncated?: boolean;
  // No basis under this artifact can ever be retired, so no supersession can
  // make it stale. Its silence is not evidence that it is current.
  unable_to_flare?: boolean;
  // An earlier artifact at the identical path is still live: a probable
  // forgotten supersession. A warning about practice, not a verdict.
  succession_unrecorded?: boolean;
  // How many earlier live artifacts share this exact path. Per-row, and not
  // summable across rows — with A, B and C at one path, B counts A and C
  // counts both, so a total would double-count A.
  live_predecessors?: number;
  merge_left_live?: LeftLiveAccounting[];
}

// A review verdict as the fold computed it. The thread rail reads its
// salience from here rather than scanning report text: three of the four
// verdicts in one acceptance thread are superseded rounds, two of them are
// ratified approvals, and only the latest names the head that landed.
export interface Review {
  report: string;
  timestamp?: number;
  reviewer: string;
  verdict: string;
  head?: string;
  artifact?: string;
  implementer?: string;
  resolved_by?: string;
  independence: string;
  ratified?: boolean;
  retired?: boolean;
  stale?: boolean;
}

export interface Act {
  event: string;
  timestamp?: number;
  actor: string;
  type: "ratify" | "supersede";
  target: string;
  text?: string;
  verdict: Verdict;
  reason: string;
}

export interface Projection {
  decisions: Decision[];
  acts: Act[];
  statements: Statement[];
  commitments: Commitment[];
  reviews?: Review[];
  artifacts: Artifact[];
  actors: Record<string, ActorState>;
  provenance: Record<string, string[]>;
}

export interface ActorState {
  name: string;
  kind?: string;
  roles: string[];
  membership_event?: string;
  role_sources: Record<string, string[]>;
  dormant_role_sources: Record<string, string[]>;
  retired_role_sources: Record<string, string[]>;
  /**
   * A principal whose every membership grant has been superseded. It stays
   * listed because it signed events that are permanent, and it holds no roles.
   * Listed is therefore not the same as present, and no authority question may
   * be answered by the entry existing: ask for the role the fold asks for.
   */
  retired?: boolean;
}

export interface DurableSnapshot {
  genesis: string;
  head: string;
  depth: number;
  projection: Projection;
  vocabulary: Vocabulary;
}

export interface FieldConstraint {
  op: "present" | "type" | "matches" | "one-of";
  name: string;
  type?: "string" | "event-id" | "actor-ref" | "path-commit";
  pattern?: string;
  values?: string[];
}

export interface BasisConstraint {
  kinds: string[];
  min: number;
  max: number;
}

export interface KindDefinition {
  name: string;
  fields: FieldConstraint[];
  basis: BasisConstraint[];
  satisfier: string;
  render: "note" | "proposal" | "commitment" | "result" | "dissent" | "artifact" | "governance";
  staleness: "propagates" | "terminal" | "exempt";
  lifecycle: "none" | "request" | "promise" | "report";
  guidance: string;
  source: string;
  ratified_by?: string;
}

export interface FoldTransition {
  activation: string;
  ratification: string;
  fold: string;
  entry: string;
  interface: string;
  toolchain: string;
  prefix: boolean;
}

export interface Vocabulary {
  definitions: KindDefinition[];
  binding: {
    status: "unbound" | "uninterpretable" | "bound";
    reason?: string;
    transitions: FoldTransition[];
  };
}

export interface LiveCursor {
  generation: string;
  position: number;
}

export type ActivityStatus = "available" | "busy" | "waiting" | "blocked";

export interface Activity {
  status: ActivityStatus;
  focus: string[];
  note?: string;
}

export interface ActivityUpdate {
  status?: ActivityStatus;
  focus?: string[];
  note?: string;
}

export interface LiveSnapshot {
  cursor: LiveCursor;
  presence: Record<string, string>;
  activity: Record<string, Activity>;
  conversations: string[];
}

export interface Cursor {
  frontier: { genesis: string; head: string; depth: number }[];
  live: LiveCursor;
}

// Rebuild is what the resident says about a cold verified rebuild. Running is
// false in the ordinary warm case.
export interface Rebuild {
  running: boolean;
  verified?: number;
  total?: number;
}

export interface Status {
  durable: DurableSnapshot;
  live: LiveSnapshot;
  cursor: Cursor;
  trust_boundary: string;
}

// What git says about one commit, asked at render time and never stored.
// "absent" and "unknown" are different answers: a check that fails must not
// read as a negative, which is the mistake that left a false sentence
// standing in a design note for seven days.
export interface Landing {
  commit: string;
  status: "landed" | "absent" | "unknown";
  merge?: string;
  time?: number;
  reason?: string;
}

export interface GraphCommit {
  hash: string;
  parents: string[];
  refs?: string[];
  subject: string;
  author: string;
  time: number;
  body?: string;
  rests_on?: string[];
}

// Ephemeral repository state from /v0/worktrees. It is deliberately separate
// from Status: none of these fields belong to the durable workroom projection.
// `repo` is the absolute path of the checkout the service is serving — the one
// path it discloses, and only because it refuses to listen off loopback.
// `remote` is the repository's own remote when the service judged it safe to
// link, and absent otherwise. It is never trusted here: repoRemoteHref applies
// the same allowlist again before any of it reaches an href.
export interface LocalRepo {
  repo: string;
  remote?: string;
  worktrees: WorktreeView[];
}

export interface WorktreeView {
  checkout: string;
  branch?: string;
  head?: string;
  state: "clean" | "dirty" | "unavailable" | "bare" | "locked" | "prunable";
  current?: boolean;
  detached?: boolean;
}

export interface Actor {
  name: string;
  fingerprint: string;
  kind?: string;
  roles: string[];
  custody: boolean;
}

export interface Frame {
  Conversation: string;
  Sequence: number;
  Payload: string; // base64 of {"about","text","re"?,"recipients"?}
  ActorKey: string;
}

export interface FrameView {
  conversation: string;
  sequence: number;
  about: string;
  text: string;
  re?: string; // "<conversation>:<sequence>" of the frame this replies to
  actor: string;
  fingerprint: string; // sha256 of the actor key — avatars and profiles key on this
  seen: number; // when this browser first saw the frame (ms)
  raw: Frame; // the complete signed frame — promotion embeds THIS
}

// The thread handle of a frame: replies carry this value in their `re`.
export function frameKey(frame: { conversation: string; sequence: number }): string {
  return `${frame.conversation}:${frame.sequence}`;
}

async function json<T>(response: Response): Promise<T> {
  const value = await response.json();
  if (!response.ok) throw new Error((value as { error?: string }).error ?? response.statusText);
  return value as T;
}

export const api = {
  status: () => fetch("/v0/status", { cache: "no-store" }).then((r) => json<Status>(r)),
  // Deliberately a separate call from status. /v0/status queues behind the
  // rebuild it would be reporting on, which is the whole reason a verifying
  // resident looked like a broken page; this one answers while that waits.
  rebuild: () => fetch("/v0/rebuild", { cache: "no-store" }).then((r) => json<Rebuild>(r)),
  graph: () =>
    fetch("/v0/graph", { cache: "no-store" })
      .then((r) => json<{ commits: GraphCommit[]; truncated?: boolean }>(r))
      .then((graph) => ({
        // Go marshals nil slices as null; the root commit has no parents.
        commits: (graph.commits ?? []).map((commit) => ({
          ...commit,
          parents: commit.parents ?? [],
          rests_on: commit.rests_on ?? undefined,
        })),
        truncated: graph.truncated ?? false,
      })),
  worktrees: () =>
    fetch("/v0/worktrees", { cache: "no-store" })
      .then((r) => json<LocalRepo>(r))
      .then((local) => ({ repo: local.repo ?? "", remote: local.remote ?? "", worktrees: local.worktrees ?? [] })),
  actors: () => fetch("/v0/actors").then((r) => json<Actor[]>(r)),
  // The merge station's second source. The browser names commits; the branch
  // is a ref the service resolves, never one a page may choose.
  landed: (commits: string[]) =>
    fetch("/v0/landed", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ commits }),
    })
      .then((r) => json<{ branch: string; commits: Landing[] }>(r))
      .then((answer) => ({ branch: answer.branch ?? "", commits: answer.commits ?? [] })),
  wait: (cursor: Cursor, timeoutMS = 25000) =>
    fetch("/v0/wait", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ cursor, timeout_ms: timeoutMS }),
    }).then((r) => json<{ status: Status; reset?: boolean }>(r)),
  frames: (conversation: string) =>
    fetch(`/v0/conversations/${encodeURIComponent(conversation)}/frames`).then((r) => json<Frame[]>(r)),
  say: (credential: string, about: string, text: string, conversation?: string, re?: string) =>
    fetch("/v0/say", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ credential, about, text, conversation, re }),
    }).then((r) => json<unknown>(r)),
  announce: (actor: string, credential: string, activity?: ActivityUpdate) =>
    fetch("/v0/presence", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ actor, credential: credential || undefined, ttl_ms: 30000, ...activity }),
    }).then((r) => json<{ credential?: string; change: unknown }>(r)),
  depart: (credential: string) =>
    fetch("/v0/presence/depart", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ credential }),
    }),
  act: (input: ActInput) =>
    fetch("/v0/act", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }).then((r) => json<{ id: string }>(r)),
};

export interface ActInput {
  credential: string;
  act: "state" | "ratify" | "supersede";
  kind?: string;
  text?: string;
  body?: Record<string, string>;
  target?: string;
  rests_on?: string[];
  evidence?: Record<string, string>;
  idempotency_key?: string;
}

// Decode a frame's payload; actor name and fingerprint are resolved by the
// caller (useFrames), which recomputes the sha256 of the actor key.
export function decodeFrame(frame: Frame): Omit<FrameView, "fingerprint" | "seen"> {
  let about = "";
  let text = "";
  let re: string | undefined;
  try {
    // atob yields Latin-1 code units; frames are UTF-8 JSON.
    const bytes = Uint8Array.from(atob(frame.Payload), (c) => c.charCodeAt(0));
    const payload = JSON.parse(new TextDecoder().decode(bytes)) as { about?: string; text?: string; re?: string; recipients?: string[] };
    about = payload.about ?? "";
    text = payload.text ?? "";
    re = payload.re || undefined;
  } catch {
    text = "(unreadable frame)";
  }
  return {
    conversation: frame.Conversation,
    sequence: frame.Sequence,
    about,
    text,
    re,
    actor: frame.ActorKey ? frame.ActorKey.slice(0, 8) : "unknown",
    raw: frame,
  };
}

// An event is named by its number, or by its identifier in full. There is no
// third option, and the third option is what used to be here: eight characters
// of the hash, which looks like a name, cannot be resolved back to one, and
// taught everyone who read it to speak in a form that does not round-trip.
export function eventName(id: string, tickets?: Map<string, number>): string {
  const ticket = tickets?.get(id);
  return ticket ? `#${ticket}` : id;
}

// For git objects only — a commit abbreviates safely because git itself will
// resolve the prefix back. An event identifier will not, which is why there is
// no shortEvent beside this.
export function shortHash(hash: string): string {
  return hash.slice(0, 8);
}
