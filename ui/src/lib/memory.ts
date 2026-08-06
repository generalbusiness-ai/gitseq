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
