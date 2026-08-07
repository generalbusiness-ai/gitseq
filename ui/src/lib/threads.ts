import type { Act, Projection, Statement } from "./api.ts";

export interface ThreadContent {
  statements: Statement[];
  acts: Act[];
  events: string[];
}

export interface ThreadSummary {
  count: number;
  people: string[];
}

export interface ThreadIndex {
  summary: (event: string) => ThreadSummary;
  content: (event: string) => ThreadContent;
}

// Build the provenance inversion and conversational reply tree once per
// projection. The first statement basis is the reply target; later bases are
// citations. Stream summaries are then O(1), while opening a thread walks only
// that thread's descendants.
export function buildThreadIndex(projection: Projection): ThreadIndex {
  const statements = projection.statements ?? [];
  const acts = projection.acts ?? [];
  const actorByEvent = new Map([
    ...statements.map((statement) => [statement.event, statement.actor] as const),
    ...acts.map((act) => [act.event, act.actor] as const),
  ]);
  const replyChildren = new Map<string, string[]>();
  for (const event of actorByEvent.keys()) {
    const parent = (projection.provenance[event] ?? [])[0];
    if (parent) replyChildren.set(parent, [...(replyChildren.get(parent) ?? []), event]);
  }
  const summaries = new Map<string, ThreadSummary>();
  const orderedEvents = projection.decisions.length > 0
    ? projection.decisions.map((decision) => decision.event)
    : [...statements.map((statement) => statement.event), ...acts.map((act) => act.event)];
  for (let index = orderedEvents.length - 1; index >= 0; index--) {
    const event = orderedEvents[index];
    let count = 0;
    const people: string[] = [];
    for (const child of replyChildren.get(event) ?? []) {
      count += 1 + (summaries.get(child)?.count ?? 0);
      const actor = actorByEvent.get(child);
      if (actor && !people.includes(actor)) people.push(actor);
    }
    summaries.set(event, { count, people });
  }

  return {
    summary: (event) => summaries.get(event) ?? { count: 0, people: [] },
    content: (event) => {
      const descendants = new Set<string>();
      const queue = [event];
      for (let cursor = 0; cursor < queue.length; cursor++) {
        for (const child of replyChildren.get(queue[cursor]) ?? []) {
          if (descendants.has(child)) continue;
          descendants.add(child);
          queue.push(child);
        }
      }
      return {
        statements: statements.filter((statement) => statement.event !== event && descendants.has(statement.event)),
        acts: acts.filter((act) => descendants.has(act.event)),
        events: orderedEvents.filter((candidate) => candidate !== event && descendants.has(candidate)),
      };
    },
  };
}

export function threadChildren(event: string, projection: Projection): ThreadContent {
  return buildThreadIndex(projection).content(event);
}
