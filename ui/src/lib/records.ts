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
  /**
   * The request whose thread this record belongs to: the record itself when
   * it is a request, else the nearest request up its first-basis chain, with
   * an act resolved through its target. A record with no request above it
   * returns itself, which the thread screen reports as not a thread rather
   * than inventing one.
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
    for (const basis of bases) restedOnBy.set(basis, [...(restedOnBy.get(basis) ?? []), dependent]);
  }
  const threadRoot = (event: string): string => {
    const seen = new Set<string>();
    let cursor = event;
    while (!seen.has(cursor)) {
      seen.add(cursor);
      if (statements.get(cursor)?.kind === "request") return cursor;
      const act = acts.get(cursor);
      const next = act ? act.target : (projection.provenance[cursor] ?? [])[0];
      if (!next) break;
      cursor = next;
    }
    return event;
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
    threadRoot,
  };
}
