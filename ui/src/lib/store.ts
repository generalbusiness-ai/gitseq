import { useEffect, useState } from "react";
import { api, type Act, type Actor, type Commitment, type Cursor, type GraphCommit, type Projection, type Statement, type Status, type Vocabulary, type WorktreeView } from "./api";
import { ATTENTION_WORK_STATUSES, CLOSED_WORK_STATUSES, OPEN_WORK_STATUSES, workAttentionCount, workItemState } from "./work";
import { definitionOf, interpretationGaps } from "./util";
export { buildThreadIndex, threadChildren } from "./threads";
export type { ThreadContent, ThreadIndex, ThreadSummary } from "./threads";

export interface Workroom {
  status?: Status;
  commits: GraphCommit[];
  graphTruncated: boolean;
  repo?: string; // absolute path of the checkout this service is serving
  worktrees?: WorktreeView[];
  actors: Actor[];
  offline: boolean;
  localOffline: boolean;
}

// One wait-loop drives the whole page: the composite cursor is the only
// client state the server ever needs back, per the stateless contract.
export function useWorkroom(): Workroom {
  const [status, setStatus] = useState<Status>();
  const [commits, setCommits] = useState<GraphCommit[]>([]);
  const [graphTruncated, setGraphTruncated] = useState(false);
  const [repo, setRepo] = useState<string>();
  const [worktrees, setWorktrees] = useState<WorktreeView[]>();
  const [actors, setActors] = useState<Actor[]>([]);
  const [offline, setOffline] = useState(false);
  const [localOffline, setLocalOffline] = useState(false);

  useEffect(() => {
    let stopped = false;
    let cursor: Cursor | undefined;

    // Refreshed every cycle, decoupled from the workroom head: ordinary git
    // activity changes the railway too, and a failed fetch simply retries.
    const refreshGraph = async () => {
      try {
        const graph = await api.graph();
        if (!stopped) {
          setCommits(graph.commits);
          setGraphTruncated(graph.truncated);
        }
      } catch {
        /* keep the previous railway; retry next cycle */
      }
    };

    // Local checkout state changes independently of both the durable frontier
    // and the live nexus cursor, so it has its own small read-only poll. A
    // failed read clears the projection instead of presenting stale dirtiness.
    let refreshingWorktrees = false;
    const refreshWorktrees = async () => {
      if (refreshingWorktrees) return;
      refreshingWorktrees = true;
      try {
        const next = await api.worktrees();
        if (!stopped) {
          // The served path identifies the server rather than describing
          // mutable checkout state, so a failed read leaves it standing.
          setRepo(next.repo || undefined);
          setWorktrees(next.worktrees);
          setLocalOffline(false);
        }
      } catch {
        if (!stopped) {
          setWorktrees(undefined);
          setLocalOffline(true);
        }
      } finally {
        refreshingWorktrees = false;
      }
    };

    const apply = (next: Status) => {
      if (stopped) return;
      setStatus(next);
      setOffline(false);
      cursor = next.cursor;
      void refreshGraph();
    };

    const loop = async () => {
      api.actors().then((list) => !stopped && setActors(list)).catch(() => {});
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
    void refreshWorktrees();
    const worktreeTimer = window.setInterval(() => void refreshWorktrees(), 10000);
    void loop();
    return () => {
      stopped = true;
      window.clearInterval(worktreeTimer);
    };
  }, []);

  return { status, commits, graphTruncated, repo, worktrees, actors, offline, localOffline };
}

export interface Selection {
  kind: "event" | "commit";
  id: string;
}

// The provenance closure of a selection, for cross-pane highlighting.
export function provenanceClosure(
  selection: Selection | undefined,
  provenance: Record<string, string[]>,
  commits: GraphCommit[],
  statements: Statement[] = [],
): { events: Set<string>; commits: Set<string> } {
  const events = new Set<string>();
  const commitSet = new Set<string>();
  if (!selection) return { events, commits: commitSet };

  const walk = (id: string) => {
    if (events.has(id)) return;
    events.add(id);
    for (const basis of provenance[id] ?? []) walk(basis);
  };

  if (selection.kind === "event") {
    walk(selection.id);
  } else {
    commitSet.add(selection.id);
    const commit = commits.find((c) => c.hash === selection.id);
    for (const cited of commit?.rests_on ?? []) walk(cited);
    // Artifact statements citing this commit join the association.
    for (const statement of statements) {
      if (statement.kind === "artifact" && statement.body?.commit === selection.id) walk(statement.event);
    }
  }
  // Any commit citing a highlighted event joins the highlight.
  for (const commit of commits) {
    if (commit.rests_on?.some((cited) => events.has(cited))) commitSet.add(commit.hash);
  }
  return { events, commits: commitSet };
}

// Ticket numbers: every durable event's 1-based position in log order.
// #N is the human handle; the hex event id stays one hover behind.
export function ticketsOf(projection?: Projection): Map<string, number> {
  const map = new Map<string, number>();
  projection?.decisions.forEach((decision, index) => map.set(decision.event, index + 1));
  return map;
}

// Why is this item stale? Walk provenance down to every retired ancestor and
// name the live supersession that retired it. The item itself is excluded:
// being superseded yourself is "retired", not "stale".
export interface StaleCause {
  act: Act; // the effective supersede
  target: string; // the retired ancestor it replaced
}

export function staleCauses(event: string, projection: Projection): StaleCause[] {
  const supersedes = new Map<string, Act>();
  for (const act of projection.acts) {
    if (act.type === "supersede" && act.verdict === "effective") supersedes.set(act.target, act);
  }
  const causes: StaleCause[] = [];
  const seen = new Set<string>();
  const walk = (id: string) => {
    if (seen.has(id)) return;
    seen.add(id);
    const act = supersedes.get(id);
    if (act && id !== event) causes.push({ act, target: id });
    for (const basis of projection.provenance[id] ?? []) walk(basis);
  };
  walk(event);
  return causes;
}

// A free-standing promise the fold judged dangling: it landed, but nobody is
// structurally positioned to declare it satisfied.
export { danglingPromises } from "./work";

// Folded promise/report events render inside their request's card; jumping
// to one lands on the request row instead.
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

export const OPEN_COMMITMENT_STATUSES: string[] = [...OPEN_WORK_STATUSES];
export const ATTENTION_COMMITMENT_STATUSES: string[] = [...ATTENTION_WORK_STATUSES];

// The header chip's summary of the Work drawer, computed from the projection.
export function workSummary(projection?: Projection, vocabulary?: Vocabulary): { stale: number; open: number; done: number } {
  if (!projection) return { stale: 0, open: 0, done: 0 };
  // A vocabulary the room cannot interpret is work too: each distinct refusal
  // counts once, and an unbound interpreter counts once however many acts it
  // refuses.
  const bindingGap = vocabulary && vocabulary.binding.status !== "bound" ? 1 : 0;
  return {
    stale: workAttentionCount(projection) + interpretationGaps(projection).length + bindingGap,
    open: projection.commitments.filter((c) => OPEN_COMMITMENT_STATUSES.includes(c.status)).length,
    done: projection.commitments.filter((c) => CLOSED_WORK_STATUSES.includes(c.status as (typeof CLOSED_WORK_STATUSES)[number])).length,
  };
}

// The stream's three weights: plain talk renders as light message rows;
// settled record entries as compact one-line rows; only what still awaits a
// response — active commitments and open proposals — earns card chrome.
export function statementWeight(
  statement: Statement,
  projection: Projection,
  commitment?: Commitment,
  vocabulary?: Vocabulary,
): "card" | "compact" {
  if (statement.retired || statement.stale) {
    // Stale commitments still need someone's attention: keep the card.
    if (commitment && workItemState(commitment).attention) return "card";
    return "compact";
  }
  const definition = definitionOf(statement.kind, vocabulary);
  if (definition?.lifecycle === "request" || (!vocabulary && statement.kind === "request")) {
    // Staleness is a qualifier here, not a status, so the attention predicate
    // has to be asked rather than matching a status list: a stale commitment
    // still reads "reported" or "satisfied" and would otherwise drop out.
    return commitment && (OPEN_COMMITMENT_STATUSES.includes(commitment.status) || workItemState(commitment).attention)
      ? "card"
      : "compact";
  }
  if (definition?.render === "proposal" || (!vocabulary && statement.kind === "propose")) {
    const verdict = projection.decisions.find((d) => d.event === statement.event)?.verdict;
    return !statement.ratified && verdict === "effective" ? "card" : "compact";
  }
  return "compact";
}
