import type { Act, Artifact, Decision, Projection, Review, Statement } from "./api.ts";

// Every record the projection holds, keyed once. A detail panel that scanned
// the whole projection for each of its fields, and again for each reference,
// did the same work hundreds of times on one screen; this does it once per
// projection and every lookup after that is a map read.
export interface RecordIndex {
  statement: (event: string) => Statement | undefined;
  act: (event: string) => Act | undefined;
  decision: (event: string) => Decision | undefined;
  artifact: (event: string) => Artifact | undefined;
  review: (event: string) => Review | undefined;
  /** The fold's position for any record, statement or act. */
  sequence: (event: string) => number | undefined;
  restsOn: (event: string) => string[];
  restedOnBy: (event: string) => string[];
  /** True when the projection has a statement or act with this id. */
  has: (event: string) => boolean;
  /** The commitment a request, promise or report event belongs to. */
  commitment: (event: string) => Projection["commitments"][number] | undefined;
  /**
   * The thread a record opens into: a promise or report opens the request it
   * answers, an act opens where its target lives, and everything else — a
   * proposal, a free-standing assert — opens as itself. Walking further up
   * the citation chain filed a proposal under whatever request its cited
   * artifact happened to answer, however unrelated.
   */
  threadRoot: (event: string) => string;
}

export function buildRecordIndex(projection: Projection): RecordIndex {
  const statements = new Map(projection.statements.map((s) => [s.event, s]));
  const acts = new Map(projection.acts.map((a) => [a.event, a]));
  const decisions = new Map(projection.decisions.map((d) => [d.event, d]));
  const artifacts = new Map(projection.artifacts.map((a) => [a.event, a]));
  const reviews = new Map((projection.reviews ?? []).map((r) => [r.report, r]));
  const restedOnBy = new Map<string, string[]>();
  for (const [dependent, bases] of Object.entries(projection.provenance)) {
    for (const basis of bases ?? []) {
      const bucket = restedOnBy.get(basis);
      if (bucket) bucket.push(dependent);
      else restedOnBy.set(basis, [dependent]);
    }
  }
  const commitments = new Map<string, Projection["commitments"][number]>();
  for (const commitment of projection.commitments) {
    for (const event of [commitment.request, commitment.promise, commitment.report]) {
      if (event && !commitments.has(event)) commitments.set(event, commitment);
    }
  }
  const threadRoot = (event: string): string => {
    let cursor = event;
    const act = acts.get(cursor);
    if (act) cursor = act.target;
    const commitment = commitments.get(cursor);
    return commitment ? commitment.request : cursor;
  };
  return {
    statement: (event) => statements.get(event),
    act: (event) => acts.get(event),
    decision: (event) => decisions.get(event),
    artifact: (event) => artifacts.get(event),
    review: (event) => reviews.get(event),
    sequence: (event) => statements.get(event)?.sequence ?? decisions.get(event)?.sequence,
    restsOn: (event) => projection.provenance[event] ?? [],
    restedOnBy: (event) => restedOnBy.get(event) ?? [],
    has: (event) => statements.has(event) || acts.has(event),
    commitment: (event) => commitments.get(event),
    threadRoot,
  };
}
