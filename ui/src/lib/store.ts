import { useEffect, useState } from "react";
import { api, type Actor, type Cursor, type Projection, type Status } from "./api";
import { projectName } from "./title";
export { buildThreadIndex, threadChildren } from "./threads";
export type { ThreadContent, ThreadIndex, ThreadSummary } from "./threads";

export interface Workroom {
  status?: Status;
  repo?: string; // absolute path of the checkout this service is serving
  project?: string; // the repository's folder name, for the tab title
  repoRemote?: string; // that repository's remote, when one is safe to link
  actors: Actor[];
  offline: boolean;
}

// One wait-loop drives the whole page: the composite cursor is the only
// client state the server ever needs back, per the stateless contract.
//
// Two polls that used to run beside it are gone with the surfaces that read
// them. The commit graph fed a drawing of main's newest eighty commits, which
// answered nothing about what waits; the periodic worktree read fed pills on
// rows that no longer carry them. The served path is read once, because it
// identifies the server rather than describing mutable checkout state.
export function useWorkroom(): Workroom {
  const [status, setStatus] = useState<Status>();
  const [repo, setRepo] = useState<string>();
  const [project, setProject] = useState<string>();
  const [repoRemote, setRepoRemote] = useState<string>();
  const [actors, setActors] = useState<Actor[]>([]);
  const [offline, setOffline] = useState(false);

  useEffect(() => {
    let stopped = false;
    let cursor: Cursor | undefined;

    const apply = (next: Status) => {
      if (stopped) return;
      setStatus(next);
      setOffline(false);
      cursor = next.cursor;
    };

    const loop = async () => {
      api.actors().then((list) => !stopped && setActors(list)).catch(() => {});
      api
        .worktrees()
        .then((local) => {
          if (stopped) return;
          setRepo(local.repo || undefined);
          setProject(projectName(local));
          setRepoRemote(local.remote || undefined);
        })
        .catch(() => {});
      while (!stopped) {
        try {
          if (!cursor) {
            apply(await api.status());
          } else {
            const wait = await api.wait(cursor);
            apply(wait.status);
          }
        } catch {
          if (!stopped) setOffline(true);
          await new Promise((resolve) => setTimeout(resolve, 2000));
        }
      }
    };
    void loop();
    return () => {
      stopped = true;
    };
  }, []);

  return { status, repo, project, repoRemote, actors, offline };
}

// Ticket numbers: every durable event's 1-based position in log order.
// #N is the human handle; the hex event id stays one hover behind.
// Ticket numbers come from the fold, not from where a decision happened to
// land in this array. The two agree — the fold counts the same records in the
// same order — but only one of them is a fact about the log.
export function ticketsOf(projection?: Projection): Map<string, number> {
  const map = new Map<string, number>();
  projection?.decisions.forEach((decision, index) => map.set(decision.event, decision.sequence || index + 1));
  return map;
}

export interface ForYouItem {
  event: string;
  ticket: number;
  /** Who addressed you, as a fingerprint; the caller knows how to name it. */
  actor: string;
  kind: string;
  text: string;
  /** Why this is yours: a request put to you, or a mention of you. */
  reason: "request" | "mention";
}

// "For you": every effective durable act addressed to me, a request whose
// body.to is my fingerprint or an act whose body.mentions names me, newest
// first. My own acts are not news to me. Every such act is returned, read or
// not: read position is a browser-local opinion and belongs to whoever is
// displaying the list, so one function answers "what was addressed to me"
// and another answers "what have I looked at".
export function forYouItems(projection: Projection | undefined, me: string | undefined): ForYouItem[] {
  if (!projection || !me) return [];
  const effective = new Set(projection.decisions.filter((d) => d.verdict === "effective").map((d) => d.event));
  const tickets = ticketsOf(projection);
  const items: ForYouItem[] = [];
  for (const statement of projection.statements) {
    if (statement.actor === me || !effective.has(statement.event)) continue;
    const mentioned = (statement.body?.mentions ?? "").split(/\s+/).includes(me);
    const requested = statement.kind === "request" && statement.body?.to === me;
    if (!mentioned && !requested) continue;
    const ticket = tickets.get(statement.event);
    if (!ticket) continue;
    items.push({
      event: statement.event,
      ticket,
      actor: statement.actor,
      kind: statement.kind,
      text: statement.text,
      // A request addressed to you is owed work; a mention is only a pointer.
      // When an act is both, the heavier reading is the true one.
      reason: requested ? "request" : "mention",
    });
  }
  return items.sort((a, b) => b.ticket - a.ticket);
}

// A read position is a watermark plus the tickets read out of order above
// it. A single watermark cannot say "I opened the third one and not the first
// two" without silently burying the first two, and a list the reader can see
// is exactly where that would be a lie. Storage stays small because any
// contiguous run of read tickets collapses back into the watermark.
export interface ForYouRead {
  watermark: number;
  read: number[];
}

export const NOTHING_READ: ForYouRead = { watermark: 0, read: [] };

// What is still unread, given a read position. Kept next to forYouItems so
// the two cannot drift about what a ticket means.
export function unreadForYou(items: ForYouItem[], read: ForYouRead): ForYouItem[] {
  const individually = new Set(read.read);
  return items.filter((item) => item.ticket > read.watermark && !individually.has(item.ticket));
}

function collapse(all: number[], watermark: number, read: Set<number>): ForYouRead {
  let mark = watermark;
  for (const ticket of [...all].sort((a, b) => a - b)) {
    if (ticket <= mark) continue;
    if (!read.has(ticket)) break;
    mark = ticket;
    read.delete(ticket);
  }
  return { watermark: mark, read: [...read].sort((a, b) => a - b) };
}

export function markForYouRead(all: number[], read: ForYouRead, ticket: number): ForYouRead {
  if (ticket <= read.watermark) return read;
  return collapse(all, read.watermark, new Set([...read.read, ticket]));
}

export function markAllForYouRead(all: number[], read: ForYouRead): ForYouRead {
  const highest = all.reduce((max, ticket) => Math.max(max, ticket), read.watermark);
  return { watermark: highest, read: [] };
}
