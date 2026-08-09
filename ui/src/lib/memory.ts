// Personal memory: the witness's floor. The room forgets conversations when
// everyone leaves; each participant may keep their own copy. This transcript
// lives only in this browser's localStorage, keyed by room genesis, and is
// never sent anywhere — it is your memory, not the room's, and not citable.

export interface MemoryEntry {
  key: string; // conversation:sequence — dedupe handle
  actor: string;
  text: string;
  at: number; // when this browser first saw it (ms)
}

const CAP = 500;

function memoryKey(genesis: string): string {
  return `workroom.memory.${genesis}`;
}

export function loadMemory(genesis: string): MemoryEntry[] {
  if (!genesis) return [];
  try {
    const raw = localStorage.getItem(memoryKey(genesis));
    return raw ? (JSON.parse(raw) as MemoryEntry[]) : [];
  } catch {
    return [];
  }
}

// Append newly seen frames, dedupe by key, keep the most recent CAP entries.
export function rememberFrames(genesis: string, entries: MemoryEntry[]): void {
  if (!genesis || entries.length === 0) return;
  try {
    const existing = loadMemory(genesis);
    const known = new Set(existing.map((e) => e.key));
    const fresh = entries.filter((e) => !known.has(e.key));
    if (fresh.length === 0) return;
    const merged = [...existing, ...fresh].slice(-CAP);
    localStorage.setItem(memoryKey(genesis), JSON.stringify(merged));
  } catch {
    /* a full or unavailable localStorage silently drops memory — it is best-effort */
  }
}

// "For you" watermark: the highest ticket already seen among durable acts
// addressed to this identity — keyed by room genesis AND actor fingerprint,
// so switching identity (or room) keeps separate read positions.
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

// Personal Work state follows the same privacy boundary as the For-you
// watermark: it is best-effort state for one exact actor in one room, stored
// only in this browser profile. It is never sent to the workroom, never
// signed, and deliberately does not synchronize across devices.
export interface PersonalWorkMemory {
  followed: string[];
  viewed: Record<string, number>;
}

export const emptyPersonalWorkMemory = (): PersonalWorkMemory => ({ followed: [], viewed: {} });

function personalWorkKey(genesis: string, fingerprint: string): string {
  return `workroom.personal-work.${genesis}.${fingerprint}`;
}

export function loadPersonalWorkMemory(genesis: string, fingerprint: string): PersonalWorkMemory {
  if (!genesis || !fingerprint) return emptyPersonalWorkMemory();
  try {
    const raw = localStorage.getItem(personalWorkKey(genesis, fingerprint));
    if (!raw) return emptyPersonalWorkMemory();
    const parsed = JSON.parse(raw) as { followed?: unknown; viewed?: unknown };
    const followed = Array.isArray(parsed.followed)
      ? [...new Set(parsed.followed.filter((event): event is string => typeof event === "string" && event.length > 0))]
      : [];
    const viewed: Record<string, number> = {};
    if (parsed.viewed && typeof parsed.viewed === "object") {
      for (const [event, order] of Object.entries(parsed.viewed)) {
        if (event && typeof order === "number" && Number.isFinite(order) && order >= 0) viewed[event] = order;
      }
    }
    return { followed, viewed };
  } catch {
    return emptyPersonalWorkMemory();
  }
}

export function savePersonalWorkMemory(genesis: string, fingerprint: string, memory: PersonalWorkMemory): void {
  if (!genesis || !fingerprint) return;
  try {
    localStorage.setItem(personalWorkKey(genesis, fingerprint), JSON.stringify(memory));
  } catch {
    /* best-effort */
  }
}

export function viewWorkTopic(memory: PersonalWorkMemory, event: string, order: number): PersonalWorkMemory {
  if (!event || order <= (memory.viewed[event] ?? -1)) return memory;
  return { ...memory, viewed: { ...memory.viewed, [event]: order } };
}

export function followWorkTopic(
  memory: PersonalWorkMemory,
  event: string,
  following: boolean,
  currentOrder: number,
): PersonalWorkMemory {
  const followed = new Set(memory.followed);
  if (following) followed.add(event); else followed.delete(event);
  const next = { ...memory, followed: [...followed] };
  // Following starts at "now": historical activity does not become unread
  // merely because the reader chose to follow the topic.
  return following ? viewWorkTopic(next, event, currentOrder) : next;
}

// Composer draft: survives a refresh, cleared on send.
const DRAFT_KEY = "workroom.draft";

export function loadDraft(): string {
  try {
    return localStorage.getItem(DRAFT_KEY) ?? "";
  } catch {
    return "";
  }
}

export function saveDraft(text: string): void {
  try {
    if (text) localStorage.setItem(DRAFT_KEY, text);
    else localStorage.removeItem(DRAFT_KEY);
  } catch {
    /* best-effort */
  }
}
