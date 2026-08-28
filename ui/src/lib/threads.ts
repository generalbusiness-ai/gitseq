import type { Act, Commitment, Projection, Statement } from "./api.ts";

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
//
// The first basis is a reply only inside one commitment, and that qualifier is
// the whole of {@link crossesCommitment} below. Without it the walk followed
// the first basis wherever it went, and a first basis goes wherever its author
// pointed it: a request may open by citing the artifact whose review suggested
// the work, and that artifact answers somebody else's commitment. Treating that
// citation as a reply made the new commitment a descendant of the old one, and
// then of whatever the old one cited, all the way back. On the workroom's own
// log that put 3040 of 13800 records — 431 whole commitments — inside the
// thread of the founding roster seed, whose own text is "hugh begins the
// workroom" and which nothing in the log answers. A review approval landed
// under that thread's "Earlier rounds — superseded review verdicts" expander,
// which is a statement about a review round that thread never had.
//
// Every edge dropped here is real provenance; what the walk may not do is read
// a citation as containment. Nothing is re-parented in its place. A record
// belonging to another commitment simply does not appear in this thread — it
// appears in its own, which is where `threadRoot` in records.ts already sends a
// click on it. The two answers were contradicting each other on the same
// record: the thread said "this belongs to me" and the row inside it navigated
// somewhere else.
export function buildThreadIndex(projection: Projection): ThreadIndex {
  const statements = projection.statements ?? [];
  const acts = projection.acts ?? [];
  const actorByEvent = new Map([
    ...statements.map((statement) => [statement.event, statement.actor] as const),
    ...acts.map((act) => [act.event, act.actor] as const),
  ]);
  // Which commitment each record belongs to, as the fold says it rather than
  // as anything here infers it: the projection names a commitment's request,
  // promise and report, and those three events are what the commitment is.
  const commitmentOf = new Map<string, Commitment>();
  for (const commitment of projection.commitments ?? []) {
    for (const event of [commitment.request, commitment.promise, commitment.report]) {
      if (event && !commitmentOf.has(event)) commitmentOf.set(event, commitment);
    }
  }
  // Does this basis edge leave the commitment the child belongs to? A child
  // that belongs to no commitment — a note, a proposal, an artifact — is a
  // reply to whatever it rests on, so only a child the fold placed in a
  // commitment can be cut, and only when its parent is not in that same
  // commitment. Asked of the child's own commitment rather than of the thread
  // being walked, so one tree serves every root and the summaries below stay
  // one bottom-up pass.
  const crossesCommitment = (event: string, parent: string): boolean => {
    const commitment = commitmentOf.get(event);
    return commitment !== undefined && commitment !== commitmentOf.get(parent);
  };
  const replyChildren = new Map<string, string[]>();
  for (const event of actorByEvent.keys()) {
    const parent = (projection.provenance[event] ?? [])[0];
    if (!parent || crossesCommitment(event, parent)) continue;
    replyChildren.set(parent, [...(replyChildren.get(parent) ?? []), event]);
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
