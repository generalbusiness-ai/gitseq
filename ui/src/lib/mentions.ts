import type { Actor } from "./api";

// Mentions: "@name" tokens in free text. One grammar for chat and durable
// statements — the author just writes; the system resolves who was addressed
// (to roster fingerprints at set-down time) and files it.
export const MENTION_PATTERN = /@(?:"([^"]+)"|([A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?))/g;

export function mentionNames(text: string): string[] {
  const names: string[] = [];
  for (const match of text.matchAll(MENTION_PATTERN)) names.push(match[1] ?? match[2]);
  return names;
}

// Resolve @name tokens to roster fingerprints, dropping unknown names and
// duplicates. Name comparison is case-insensitive; the fingerprint is exact.
export function mentionFingerprints(text: string, actors: Actor[]): string[] {
  const byName = new Map(actors.map((a) => [a.name.toLowerCase(), a.fingerprint]));
  const fingerprints: string[] = [];
  for (const name of mentionNames(text)) {
    const fingerprint = byName.get(name.toLowerCase());
    if (fingerprint && !fingerprints.includes(fingerprint)) fingerprints.push(fingerprint);
  }
  return fingerprints;
}

export function mentionsActor(text: string, name?: string): boolean {
  if (!name) return false;
  const lowered = name.toLowerCase();
  return mentionNames(text).some((candidate) => candidate.toLowerCase() === lowered);
}

// The mention token being typed at the caret, if any — drives autocomplete.
export function mentionAt(text: string, caret: number): { start: number; partial: string; quoted: boolean } | undefined {
  const head = text.slice(0, caret);
  const quoted = /(^|[\s(])@"([^"]*)$/.exec(head);
  if (quoted) return { start: caret - quoted[2].length - 2, partial: quoted[2], quoted: true };
  const simple = /(^|[\s(])@([A-Za-z0-9_.-]*)$/.exec(head);
  if (!simple) return undefined;
  return { start: caret - simple[2].length - 1, partial: simple[2], quoted: false };
}

// Split text into plain and mention tokens for highlighted rendering.
export function mentionTokens(text: string): { text: string; mention?: string }[] {
  const tokens: { text: string; mention?: string }[] = [];
  let last = 0;
  for (const match of text.matchAll(MENTION_PATTERN)) {
    const index = match.index ?? 0;
    if (index > last) tokens.push({ text: text.slice(last, index) });
    tokens.push({ text: match[0], mention: match[1] ?? match[2] });
    last = index + match[0].length;
  }
  if (last < text.length) tokens.push({ text: text.slice(last) });
  return tokens;
}

// body.mentions carries space-separated fingerprints; parse defensively.
export function mentionedFingerprints(body?: Record<string, string>): string[] {
  return (body?.mentions ?? "").split(/\s+/).filter(Boolean);
}
