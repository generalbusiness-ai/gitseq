export type ThreadIdentity =
  | { kind: "event"; event: string }
  | { kind: "frame"; conversation: string; sequence: number };

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
