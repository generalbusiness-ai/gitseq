// Types mirror the Go service; the workroom projection is the fold's output.

export interface Decision {
  event: string;
  verdict: "effective" | "ineffective" | "disputed";
  reason: string;
}

export interface Statement {
  event: string;
  actor: string;
  kind: string;
  text: string;
  body?: Record<string, string>;
  ratified?: boolean;
  retired?: boolean;
  stale?: boolean;
}

export interface Commitment {
  request: string;
  requester: string;
  performer?: string;
  promise?: string;
  report?: string;
  status: string;
  waiting_on?: string;
}

export interface Artifact {
  event: string;
  path: string;
  commit: string;
  stale: boolean;
}

export interface Act {
  event: string;
  actor: string;
  type: "ratify" | "supersede";
  target: string;
  text?: string;
  verdict: "effective" | "ineffective" | "disputed";
  reason: string;
}

export interface Projection {
  decisions: Decision[];
  acts: Act[];
  statements: Statement[];
  commitments: Commitment[];
  artifacts: Artifact[];
  roles: Record<string, string[]>;
  provenance: Record<string, string[]>;
}

export interface DurableSnapshot {
  genesis: string;
  head: string;
  depth: number;
  projection: Projection;
}

export interface LiveCursor {
  generation: string;
  position: number;
}

export interface LiveSnapshot {
  cursor: LiveCursor;
  presence: Record<string, string>;
  conversations: string[];
}

export interface Cursor {
  frontier: { genesis: string; head: string; depth: number }[];
  live: LiveCursor;
}

export interface Status {
  durable: DurableSnapshot;
  live: LiveSnapshot;
  cursor: Cursor;
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

export interface Actor {
  name: string;
  fingerprint: string;
  role: string;
}

export interface Frame {
  Conversation: string;
  Sequence: number;
  Payload: string; // base64 of {"about","text"}
  ActorKey: string;
}

export interface FrameView {
  conversation: string;
  sequence: number;
  about: string;
  text: string;
  actor: string;
}

async function json<T>(response: Response): Promise<T> {
  const value = await response.json();
  if (!response.ok) throw new Error((value as { error?: string }).error ?? response.statusText);
  return value as T;
}

export const api = {
  status: () => fetch("/v0/status", { cache: "no-store" }).then((r) => json<Status>(r)),
  graph: () =>
    fetch("/v0/graph", { cache: "no-store" })
      .then((r) => json<{ commits: GraphCommit[] }>(r))
      .then((graph) => ({
        // Go marshals nil slices as null; the root commit has no parents.
        commits: (graph.commits ?? []).map((commit) => ({
          ...commit,
          parents: commit.parents ?? [],
          rests_on: commit.rests_on ?? undefined,
        })),
      })),
  actors: () => fetch("/v0/actors").then((r) => json<Actor[]>(r)),
  wait: (cursor: Cursor, timeoutMS = 25000) =>
    fetch("/v0/wait", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ cursor, timeout_ms: timeoutMS }),
    }).then((r) => json<{ status: Status; reset?: boolean }>(r)),
  frames: (conversation: string) =>
    fetch(`/v0/conversations/${encodeURIComponent(conversation)}/frames`).then((r) => json<Frame[]>(r)),
  say: (session: string, about: string, text: string, conversation?: string) =>
    fetch("/v0/say", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session, about, text, conversation }),
    }).then((r) => json<unknown>(r)),
  announce: (actor: string, session: string) =>
    fetch("/v0/presence", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ actor, session, ttl_ms: 30000 }),
    }).then((r) => json<unknown>(r)),
  depart: (session: string) => fetch(`/v0/presence/${encodeURIComponent(session)}`, { method: "DELETE" }),
  act: (input: ActInput) =>
    fetch("/v0/act", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }).then((r) => json<{ id: string }>(r)),
};

export interface ActInput {
  session: string;
  act: "state" | "ratify" | "supersede";
  kind?: string;
  text?: string;
  body?: Record<string, string>;
  target?: string;
  rests_on?: string[];
  evidence?: Record<string, string>;
  idempotency_key?: string;
}

export function decodeFrame(frame: Frame, actors: Actor[]): FrameView {
  let about = "";
  let text = "";
  try {
    // atob yields Latin-1 code units; frames are UTF-8 JSON.
    const bytes = Uint8Array.from(atob(frame.Payload), (c) => c.charCodeAt(0));
    const payload = JSON.parse(new TextDecoder().decode(bytes)) as { about?: string; text?: string };
    about = payload.about ?? "";
    text = payload.text ?? "";
  } catch {
    text = "(unreadable frame)";
  }
  return {
    conversation: frame.Conversation,
    sequence: frame.Sequence,
    about,
    text,
    actor: actorNameByKey(frame.ActorKey, actors),
  };
}

// The frame carries the raw actor public key (base64); actors list carries
// sha256 fingerprints. Match by recomputing is overkill for a demo pane, so
// frames display the key tail when no presence name matches.
function actorNameByKey(key: string, _actors: Actor[]): string {
  return key ? key.slice(0, 8) : "unknown";
}

export function shortEvent(id: string): string {
  const hash = id.split("#").pop() ?? id;
  const bare = hash.replace(/^git:sha(1|256):/, "");
  return bare.slice(0, 8);
}

export function shortHash(hash: string): string {
  return hash.slice(0, 8);
}
