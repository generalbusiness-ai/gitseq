import { useEffect, useState } from "react";
import { api, type Actor, type Cursor, type Projection, type Status } from "./api";
export { buildThreadIndex, threadChildren } from "./threads";
export type { ThreadContent, ThreadIndex, ThreadSummary } from "./threads";

export interface Workroom {
  status?: Status;
  repo?: string; // absolute path of the checkout this service is serving
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
      api.worktrees().then((local) => !stopped && setRepo(local.repo || undefined)).catch(() => {});
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

  return { status, repo, actors, offline };
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

// Folded promise/report events belong to their request's thread; opening one
// opens the thread it lives in.
export function foldAnchor(event: string, projection?: Projection): string {
  for (const commitment of projection?.commitments ?? []) {
    if (commitment.promise === event || commitment.report === event) return commitment.request;
  }
  return event;
}

// "For you": durable effective acts addressed to me — a mention in
// body.mentions or a request whose body.to is my fingerprint — newer than
// the given watermark ticket, oldest first. My own acts are not news to me.
export interface ForYouItem {
  event: string;
  ticket: number;
}

export function forYouItems(projection: Projection | undefined, me: string | undefined, watermark: number): ForYouItem[] {
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
    if (!ticket || ticket <= watermark) continue;
    items.push({ event: statement.event, ticket });
  }
  return items.sort((a, b) => a.ticket - b.ticket);
}
