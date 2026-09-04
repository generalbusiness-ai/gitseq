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
  /**
   * The ratified proposals resting **directly** on this record, newest first,
   * bounded by {@link CITED_PROPOSAL_LIMIT}.
   *
   * This is a citation prefill and nothing more. It is not an adoption fact,
   * it is not consulted to decide what the screen offers, and it says nothing
   * about whether any decision is adopted — the fold projects no such
   * relation, and the browser may not invent one. See "Layer 5 and layer 7:
   * what the browser may derive" in docs/reference/architecture.md.
   *
   * Every input is a projected field — the citation edge, the kind, the fold's
   * verdict, and the fold's own `ratified` and `retired` flags — and nothing
   * is joined across records. The operator sees the result before signing.
   */
  citableProposals: (event: string) => string[];
}

/**
 * How many proposals a prefill may cite, and why the number is one.
 *
 * Without a bound the browser could put every qualifying proposal into one
 * record's causal references. Two things go wrong at once. A large enough set
 * reaches the kernel's own ceiling on causal references and the record cannot
 * be admitted at all. Long before that, several proposals that contradict each
 * other all appear to govern the same review, and a reader has no way to tell
 * which one the signer meant.
 *
 * One is the answer rather than some larger round number because only one
 * proposal can be the current one. Citing the others adds no information and
 * creates exactly the ambiguity the bound exists to remove.
 */
export const CITED_PROPOSAL_LIMIT = 1;

/**
 * How many records one composed reply may cite in total, prefilled and
 * operator-named together.
 *
 * The kernel admits 4,096 causal references in one intent, so this is not
 * that ceiling; it is the number a person can read before signing. The
 * disclosure list above the send control is the whole point of resolving
 * citations at all, and a list nobody finishes reading discloses nothing. The
 * bound is applied to the list as a whole rather than to the operator's
 * additions alone, because what has to stay readable is what will be signed.
 */
export const COMPOSED_CITATION_LIMIT = 8;

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
  // Which proposal is picked may not depend on the order the projection
  // happened to list things in. `provenance` arrives as a JSON object and its
  // dependents as a JSON array; both preserve whatever order the fold emitted,
  // and neither is a contract. The fold's sequence is a contract, so that is
  // what orders this: newest first, ties broken by the event identifier, which
  // is unique. The same set of records therefore yields the same citation
  // whichever way it was serialised.
  const citableProposals = (event: string): string[] =>
    (restedOnBy.get(event) ?? [])
      .map((dependent) => statements.get(dependent))
      .filter(
        (candidate): candidate is Statement =>
          candidate !== undefined &&
          candidate.kind === "propose" &&
          candidate.ratified === true &&
          candidate.retired !== true &&
          decisions.get(candidate.event)?.verdict === "effective",
      )
      .sort((left, right) => right.sequence - left.sequence || (left.event < right.event ? -1 : 1))
      .slice(0, CITED_PROPOSAL_LIMIT)
      .map((candidate) => candidate.event);
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
    citableProposals,
  };
}
