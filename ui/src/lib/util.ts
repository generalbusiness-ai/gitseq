import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Commitment, Vocabulary } from "./api";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// One color language across the whole surface. Kind tags are deliberately
// neutral: green is reserved for satisfied/ratified/current, red for
// stale/reneged/disputed/dissent, amber for selection/focus only.
export const neutralKind = "text-muted border-border";
const legacyKindTint: Record<string, string> = {
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

export function kindTint(kind: string, vocabulary?: Vocabulary): string {
  const render = definitionOf(kind, vocabulary)?.render;
  return render === "dissent" ? "text-danger border-danger/40" : legacyKindTint[kind] ?? neutralKind;
}

// A badge says which kind an act is, so it wears that kind's own name. Only
// the two founding kinds whose names read as jargon are translated; a room
// that declares "finding" and "review-note" gets "finding" and "review-note",
// not one indistinguishable "note" for every note-class kind.
const familiarKind: Record<string, string> = { assert: "note", propose: "proposal" };

export function kindLabel(kind: string): string {
  return familiarKind[kind] ?? kind;
}

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

const legacyWorkOnlyKinds = new Set(["roster", "infra-key", "seal", "artifact"]);

export function belongsInRoom(kind: string, vocabulary?: Vocabulary): boolean {
  const render = definitionOf(kind, vocabulary)?.render;
  if (render) return render !== "governance" && render !== "artifact";
  return !legacyWorkOnlyKinds.has(kind);
}

// An act the room cannot read is explained on that act, not counted in a
// panel somewhere else. The count was the wrong unit: it told a reader how
// many acts were unreadable without telling them what any one of them meant
// or whether anything was owed, and read as a fault when the acts were inert.
const interpretationVerdicts = new Set(["undefined-kind", "uninterpretable"]);

export function isInterpretationGap(verdict?: string): boolean {
  return Boolean(verdict && interpretationVerdicts.has(verdict));
}

// What to say beside the act. The reason is the fold's own words; the
// consequence says only what the verdict actually settles.
//
// Two things this must not overclaim, both learned by getting them wrong.
// Citations are not among the things that fail to form: the fold projects
// rests_on provenance for every record before it judges any of them, so an
// act the room cannot read still cites what it cited. And an uninterpretable
// verdict does not always mean "wait for a binding" — the same verdict covers
// kind definitions that are permanently invalid, which no later interpreter
// can rescue. Exactly one reason in that channel is remediable, so key on that
// reason rather than on the verdict alone.
//
// Key on the whole reason, never on a phrase inside it. The fold quotes a
// rejected operand back verbatim, so a permanently invalid definition can put
// any words at all into this channel, the words below included: a field typed
// `interpreter execution is not held` is refused as `uninterpretable kind
// definition: unsupported type "interpreter execution is not held"`, and no
// binding makes that type supported. A search inside the reason would offer
// that definition a rescue that does not exist. The string below is the fold's
// own, pinned by TestFoldActivationRecordsPrefixBoundaryThenNamesExecutionGap;
// the collision is pinned by TestInvalidConstraintAlgebraIsTypedUninterpretable.
const unboundInterpreterReason = "uninterpretable: activated interpreter execution is not held";

export function interpretationNotice(verdict?: string, reason?: string): { verdict: string; reason: string; consequence: string } | undefined {
  if (!isInterpretationGap(verdict)) return undefined;
  const awaitingBinding = reason === unboundInterpreterReason;
  return {
    verdict: verdict!,
    reason: reason ?? "",
    consequence:
      verdict === "undefined-kind"
        ? "No rule in this room reads a kind by this name, so the act is recorded without force: whatever its text undertakes, nothing here acts on it."
        : awaitingBinding
          ? "The room holds no interpreter that can read this act, so it is recorded without force unless one is bound."
          : "The room cannot interpret this act, so it is recorded without force; the reason above is not something a later interpreter would resolve.",
  };
}

export function definitionOf(kind: string, vocabulary?: Vocabulary) {
  return vocabulary?.definitions.find((definition) => definition.name === kind);
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
// A durable record carries the committer date as an unbounded int64, so a
// corrupt or hostile one can sit outside the range JavaScript Date can
// represent. Rendering "NaN-NaN-NaN" would be a quieter lie than throwing, so
// this says plainly that the time is unreadable.
export function isRenderableTimestamp(seconds: number | undefined): seconds is number {
  return typeof seconds === "number" && Number.isFinite(seconds) && !Number.isNaN(new Date(seconds * 1000).getTime());
}

export function eventTimestamp(seconds: number): string {
  if (!isRenderableTimestamp(seconds)) return "unreadable time";
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
