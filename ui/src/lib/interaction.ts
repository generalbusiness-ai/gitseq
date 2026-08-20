export type ThreadIdentity =
  | { kind: "event"; event: string }
  | { kind: "frame"; conversation: string; sequence: number };

export interface ThreadFrame {
  about: string;
  re?: string;
}

export interface ConversationFrame extends ThreadFrame {
  conversation: string;
  sequence: number;
}

export interface ThreadPending {
  about?: string;
  re?: string;
}

export interface PendingEcho extends ThreadPending {
  id: string;
  text: string;
  at: number;
}

export interface DeliveredFrame extends ThreadFrame {
  actor: string;
  text: string;
  seen: number;
}

export interface DiscussionEntry<T extends ConversationFrame> {
  frame: T;
  depth: number;
}

export interface TemporaryDelivery {
  about: string;
  conversation?: string;
  re?: string;
}

// Retry identity is scoped to both the UI intention and the exact payload.
// A transport failure retains the key; editing the payload or completing the
// attempt creates a new identity.
export class RetryKeys {
  private readonly entries = new Map<string, { payload: string; key: string }>();
  private readonly create: () => string;

  constructor(create: () => string = () => crypto.randomUUID()) {
    this.create = create;
  }

  forAttempt(scope: string, payload: string): string {
    const current = this.entries.get(scope);
    if (current?.payload === payload) return current.key;
    const key = this.create();
    this.entries.set(scope, { payload, key });
    return key;
  }

  succeeded(scope: string, key: string): void {
    if (this.entries.get(scope)?.key === key) this.entries.delete(scope);
  }
}

export function threadTargetKey(target: ThreadIdentity): string {
  return target.kind === "event"
    ? `event:${target.event}`
    : `frame:${target.conversation}:${target.sequence}`;
}

// Keep the whole event conversation in its event pane. Direct frames are
// roots; replies follow their exact conversation:sequence parent. Hostile or
// expired parent references remain visible as roots instead of disappearing.
export function eventDiscussionEntries<T extends ConversationFrame>(event: string, frames: T[]): DiscussionEntry<T>[] {
  const relevant = frames.filter((frame) => frame.about === event);
  const known = new Set(relevant.map((frame) => `${frame.conversation}:${frame.sequence}`));
  const children = new Map<string, T[]>();
  const roots: T[] = [];
  for (const frame of relevant) {
    if (!frame.re || !known.has(frame.re)) {
      roots.push(frame);
      continue;
    }
    children.set(frame.re, [...(children.get(frame.re) ?? []), frame]);
  }
  const entries: DiscussionEntry<T>[] = [];
  const visited = new Set<string>();
  const append = (frame: T, depth: number) => {
    const key = `${frame.conversation}:${frame.sequence}`;
    if (visited.has(key)) return;
    visited.add(key);
    entries.push({ frame, depth });
    for (const child of children.get(key) ?? []) append(child, depth + 1);
  };
  for (const root of roots) append(root, 0);
  // A cycle is invalid nexus shape, but rendering it once is safer than
  // silently hiding signed talk.
  for (const frame of relevant) append(frame, 0);
  return entries;
}

export function pendingForThread<T extends ThreadPending>(target: ThreadIdentity, pending: T[]): T[] {
  if (target.kind === "event") {
    return pending.filter((item) => item.about === target.event && !item.re);
  }
  const re = `${target.conversation}:${target.sequence}`;
  return pending.filter((item) => item.re === re);
}

// Optimistic echoes reconcile only within the conversation scope that sent
// them. Equal words in two event threads must not make either echo disappear.
export function pendingMatchesFrame(pending: ThreadPending, frame: ThreadFrame): boolean {
  if (pending.re) return pending.re === frame.re;
  if (pending.about) return !frame.re && pending.about === frame.about;
  return !frame.re && frame.about === "the workroom";
}

// Reconcile once per delivered frame, after the pending echo was created.
// This prevents an old equal-text frame from clearing a retry and prevents one
// delivered frame from clearing two identical sends.
export function reconciledPendingIDs(
  pending: PendingEcho[],
  frames: DeliveredFrame[],
  actor: string | undefined,
): string[] {
  if (!actor) return [];
  const available = new Set(frames.map((_, index) => index));
  const matched: string[] = [];
  for (const echo of pending) {
    const index = frames.findIndex(
      (frame, candidate) =>
        available.has(candidate) &&
        frame.actor === actor &&
        frame.text === echo.text &&
        frame.seen >= echo.at &&
        pendingMatchesFrame(echo, frame),
    );
    if (index < 0) continue;
    available.delete(index);
    matched.push(echo.id);
  }
  return matched;
}

export function temporaryReplyDelivery(
  target: ThreadIdentity,
  parent?: ConversationFrame,
): TemporaryDelivery | undefined {
  if (target.kind === "event") return { about: target.event };
  if (!parent) return undefined;
  return {
    about: parent.about || "the workroom",
    conversation: parent.conversation,
    re: `${parent.conversation}:${parent.sequence}`,
  };
}

// Keep optimistic delivery honest at one boundary: a failed publish removes
// the echo before the error returns to the composer, which can then restore
// the author's text and expose the transport error.
export async function sendTemporaryReply(
  text: string,
  delivery: TemporaryDelivery,
  callbacks: {
    optimistic: (text: string, re?: string, about?: string) => string;
    publish: (delivery: TemporaryDelivery, text: string) => Promise<unknown>;
    failed: (pendingID: string) => void;
  },
): Promise<string> {
  const pendingID = callbacks.optimistic(text, delivery.re, delivery.re ? undefined : delivery.about);
  try {
    await callbacks.publish(delivery, text);
    return pendingID;
  } catch (error) {
    callbacks.failed(pendingID);
    throw error;
  }
}

export function parsePresenceLabel(value: string): { name: string; fingerprint: string } {
  const match = /^(.*) \(([^()]*)\)$/.exec(value);
  return match ? { name: match[1], fingerprint: match[2] } : { name: value, fingerprint: "" };
}

// Presence is leased per session, and one person runs several: a browser tab,
// an MCP server per agent session. "Who is here" is a count of people, so the
// leases fold onto the identity label they all carry, keeping the session
// count rather than discarding it.
export interface PresentActor {
  label: string;
  name: string;
  fingerprint: string; // the short fingerprint the label carries
  sessions: number;
  status: import("./api").ActivityStatus;
  focus: string[];
  note?: string;
}

interface KnownActor {
  name: string;
  fingerprint: string;
}

// Presence publishes a short fingerprint while durable actor records carry
// the full one. Resolve by that identity prefix, never by display name when a
// fingerprint is present: names are deliberately not unique.
export function fingerprintOfPresentActor(person: PresentActor, actors: KnownActor[]): string {
  if (person.fingerprint) {
    const matches = actors.filter(
      (actor) =>
        actor.fingerprint === person.fingerprint ||
        actor.fingerprint.startsWith(person.fingerprint) ||
        person.fingerprint.startsWith(actor.fingerprint),
    );
    return matches.length === 1 ? matches[0].fingerprint : person.fingerprint;
  }
  const matches = actors.filter((actor) => actor.name === person.name);
  return matches.length === 1 ? matches[0].fingerprint : "";
}

export function fingerprintsIdentifySameActor(left: string, right: string): boolean {
  return Boolean(left && right) &&
    (left === right || left.startsWith(right) || right.startsWith(left));
}

export function toggleActivityFocus(focus: string[], event: string): string[] {
  const next = new Set(focus);
  if (next.has(event)) next.delete(event); else next.add(event);
  return [...next].sort().slice(0, 8);
}

const activityRank: Record<import("./api").ActivityStatus, number> = {
  available: 0,
  busy: 1,
  waiting: 2,
  blocked: 3,
};

// Multiple leases for one identity aggregate deterministically: the most
// urgent status wins, focus is a sorted union capped to the same eight-event
// bound as one session, and the lexicographically first note is displayed.
export function presentActors(
  presence: Record<string, string> | undefined,
  activity: Record<string, import("./api").Activity> | undefined = undefined,
): PresentActor[] {
  const people = new Map<string, PresentActor>();
  for (const [handle, label] of Object.entries(presence ?? {})) {
    const session = activity?.[handle] ?? { status: "available" as const, focus: [] };
    const known = people.get(label);
    if (known) {
      known.sessions += 1;
      if (activityRank[session.status] > activityRank[known.status]) known.status = session.status;
      known.focus = [...new Set([...known.focus, ...(session.focus ?? [])])].sort().slice(0, 8);
      if (session.note && (!known.note || session.note.localeCompare(known.note) < 0)) known.note = session.note;
      continue;
    }
    people.set(label, {
      label,
      ...parsePresenceLabel(label),
      sessions: 1,
      status: session.status,
      focus: [...new Set(session.focus ?? [])].sort().slice(0, 8),
      note: session.note || undefined,
    });
  }
  return [...people.values()].sort((a, b) => a.label.localeCompare(b.label));
}
