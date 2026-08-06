import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// One color language across the whole surface.
export const kindTint: Record<string, string> = {
  request: "text-warn border-warn/30 bg-warn/10",
  promise: "text-ok border-ok/30 bg-ok/10",
  report: "text-info border-info/30 bg-info/10",
  propose: "text-violet border-violet/30 bg-violet/10",
  assert: "text-foreground border-border bg-elevated",
  dissent: "text-danger border-danger/30 bg-danger/10",
  artifact: "text-teal border-teal/30 bg-teal/10",
  roster: "text-muted border-border bg-elevated",
  "infra-key": "text-muted border-border bg-elevated",
  seal: "text-muted border-border bg-elevated",
};

export const statusTint: Record<string, string> = {
  satisfied: "text-ok",
  reported: "text-info",
  promised: "text-accent",
  requested: "text-muted",
  withdrawn: "text-faint",
  cancelled: "text-faint",
  reneged: "text-danger",
  stale: "text-danger",
  disputed: "text-danger",
};

// Stable per-actor hues for chat authorship.
const actorHues = ["text-ok", "text-info", "text-violet", "text-warn", "text-teal", "text-danger"];
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

export function timeAgo(seconds: number): string {
  const delta = Math.max(0, Date.now() / 1000 - seconds);
  if (delta < 90) return `${Math.round(delta)}s ago`;
  if (delta < 5400) return `${Math.round(delta / 60)}m ago`;
  if (delta < 129600) return `${Math.round(delta / 3600)}h ago`;
  return `${Math.round(delta / 86400)}d ago`;
}
