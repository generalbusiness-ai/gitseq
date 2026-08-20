// "For you" watermark: the highest ticket already seen among durable acts
// addressed to this identity — keyed by room genesis AND actor fingerprint,
// so switching identity (or room) keeps separate read positions.
//
// This is the only browser-local read position left. Follows and per-topic
// watermarks are gone: a read position that does not sync is true on one
// machine and false on the next, which is a promise the UI cannot keep. The
// counter survives because clicking it steps to the oldest unseen record, so
// the number is one click from exactly what it counts and nothing is hidden
// by it.

function forYouKey(genesis: string, fingerprint: string): string {
  return `workroom.foryou.${genesis}.${fingerprint}`;
}

export function loadForYouWatermark(genesis: string, fingerprint: string): number {
  if (!genesis || !fingerprint) return 0;
  try {
    return Number(localStorage.getItem(forYouKey(genesis, fingerprint))) || 0;
  } catch {
    return 0;
  }
}

export function saveForYouWatermark(genesis: string, fingerprint: string, ticket: number): void {
  if (!genesis || !fingerprint) return;
  try {
    localStorage.setItem(forYouKey(genesis, fingerprint), String(ticket));
  } catch {
    /* best-effort */
  }
}
