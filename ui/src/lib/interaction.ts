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
