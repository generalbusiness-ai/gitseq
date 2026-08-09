import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Commitment } from "./api";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// One color language across the whole surface. Kind tags are deliberately
// neutral: green is reserved for satisfied/ratified/current, red for
// stale/reneged/disputed/dissent, amber for selection/focus only.
export const neutralKind = "text-muted border-border";
export const kindTint: Record<string, string> = {
  request: neutralKind,
  promise: neutralKind,
  report: neutralKind,
  propose: neutralKind,
  assert: neutralKind,
  dissent: "text-danger border-danger/40",
  artifact: neutralKind,
  roster: neutralKind,
  "infra-key": neutralKind,
  seal: neutralKind,
};

// Statement kinds wear their plain names in the UI; "assert" reads as jargon.
export const kindLabel: Record<string, string> = { assert: "note", propose: "proposal" };

export const statusTint: Record<string, string> = {
  satisfied: "text-ok",
  reported: "text-muted",
  promised: "text-muted",
  open: "text-muted",
  withdrawn: "text-faint",
  cancelled: "text-faint",
  reneged: "text-danger",
  stale: "text-danger",
  disputed: "text-danger",
};

const familiarStatus: Record<string, string> = {
  open: "open",
  promised: "in progress",
  reported: "ready",
  satisfied: "done",
  withdrawn: "closed",
  cancelled: "closed",
  reneged: "stopped",
  stale: "stale",
  disputed: "disputed",
};

export function statusLabel(status: string): string {
  return familiarStatus[status] ?? status;
}

export function commitmentRelationship(commitment: Commitment, nameOf: (fingerprint: string) => string): string | undefined {
  if (!commitment.promise && commitment.addressed_to) return `addressed to ${nameOf(commitment.addressed_to)} · unclaimed`;
  if (commitment.waiting_on) return `waiting on ${nameOf(commitment.waiting_on)}`;
  return undefined;
}

const workOnlyKinds = new Set(["roster", "infra-key", "seal", "artifact"]);

export function belongsInRoom(kind: string): boolean {
  return !workOnlyKinds.has(kind);
}

// Stable per-actor hues for chat authorship — none of the semantic ok/danger
// hues, so an author's color never reads as a verdict.
const actorHues = ["text-info", "text-violet", "text-teal", "text-foreground/80"];
export function actorTint(name: string): string {
  let hash = 0;
  for (const c of name) hash = (hash * 31 + c.charCodeAt(0)) | 0;
  return actorHues[Math.abs(hash) % actorHues.length];
}

const fingerprintCache = new Map<string, string>();
export async function fingerprintOfKey(base64Key: string): Promise<string> {
  const cached = fingerprintCache.get(base64Key);
  if (cached) return cached;
  const raw = Uint8Array.from(atob(base64Key), (c) => c.charCodeAt(0));
  const digest = await crypto.subtle.digest("SHA-256", raw);
  const hex = Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  fingerprintCache.set(base64Key, hex);
  return hex;
}

// Honest label for a client-side arrival time: this is when *we* first saw
// the frame, not when it was said — the room keeps no clock for chatter.
// The word "seen" carries that honesty; clock() is the bare Slack-style time
// for message headers, with the same caveat in its hover title upstream.
export function clock(ms: number): string {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

export function seenAt(ms: number): string {
  return `seen ${clock(ms)}`;
}

// Durable events use the sequencer's signed Git commit time. Keep this
// visually and semantically distinct from the client-side "seen" clock above.
export function eventTimestamp(seconds: number): string {
  const date = new Date(seconds * 1000);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const hours = String(date.getHours()).padStart(2, "0");
  const minutes = String(date.getMinutes()).padStart(2, "0");
  const secondsPart = String(date.getSeconds()).padStart(2, "0");
  return `${year}-${month}-${day} ${hours}:${minutes}:${secondsPart}`;
}

export function timeAgo(seconds: number): string {
  const delta = Math.max(0, Date.now() / 1000 - seconds);
  if (delta < 90) return `${Math.round(delta)}s ago`;
  if (delta < 5400) return `${Math.round(delta / 60)}m ago`;
  if (delta < 129600) return `${Math.round(delta / 3600)}h ago`;
  return `${Math.round(delta / 86400)}d ago`;
}
