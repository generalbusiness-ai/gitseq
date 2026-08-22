// "For you" read position: which durable acts addressed to this identity have
// already been looked at — keyed by room genesis AND actor fingerprint, so
// switching identity (or room) keeps separate positions.
//
// This is the only browser-local read position left. Follows and per-topic
// watermarks are gone: a read position that does not sync is true on one
// machine and false on the next, which is a promise the UI cannot keep. This
// one survives because the list it hides is one click away and shown in full,
// so nothing addressed to you is concealed by a number.
//
// Stored as a watermark plus any tickets read out of order above it. Earlier
// versions stored a bare number, which still loads: it is read as a watermark
// with nothing above it.

import { NOTHING_READ, type ForYouRead } from "./store";

function forYouKey(genesis: string, fingerprint: string): string {
  return `workroom.foryou.${genesis}.${fingerprint}`;
}

export function loadForYouRead(genesis: string, fingerprint: string): ForYouRead {
  if (!genesis || !fingerprint) return NOTHING_READ;
  try {
    const raw = localStorage.getItem(forYouKey(genesis, fingerprint));
    if (!raw) return NOTHING_READ;
    // The old format: one number, the watermark.
    const asNumber = Number(raw);
    if (Number.isFinite(asNumber) && String(asNumber) === raw.trim()) {
      return { watermark: asNumber || 0, read: [] };
    }
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return NOTHING_READ;
    const value = parsed as { watermark?: unknown; read?: unknown };
    const watermark = typeof value.watermark === "number" && Number.isFinite(value.watermark) ? value.watermark : 0;
    const read = Array.isArray(value.read) ? value.read.filter((t): t is number => typeof t === "number" && Number.isFinite(t)) : [];
    return { watermark, read };
  } catch {
    // Unreadable storage is the same as never having read anything: showing a
    // notification twice is recoverable, hiding one is not.
    return NOTHING_READ;
  }
}

export function saveForYouRead(genesis: string, fingerprint: string, value: ForYouRead): void {
  if (!genesis || !fingerprint) return;
  try {
    localStorage.setItem(forYouKey(genesis, fingerprint), JSON.stringify(value));
  } catch {
    /* best-effort */
  }
}
