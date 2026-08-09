interface SupersedeAct {
  event: string;
  type: "ratify" | "supersede";
  target: string;
  verdict: "effective" | "ineffective" | "disputed";
}

interface ReplacementProjection {
  decisions: { event: string; verdict: "effective" | "ineffective" | "disputed" }[];
  statements: { event: string; retired?: boolean; stale?: boolean }[];
  provenance: Record<string, string[]>;
}

// A supersession may cite the event that stands in for its target after the
// target itself. Treat that as a replacement only when the signed provenance
// names exactly one effective statement that is neither retired nor stale.
// Additional evidence and ambiguous candidates must not become a guessed UI
// link.
export function replacementForSupersede(act: SupersedeAct, projection: ReplacementProjection): string | undefined {
  if (act.type !== "supersede" || act.verdict !== "effective") return undefined;
  const effective = new Set(projection.decisions.filter((decision) => decision.verdict === "effective").map((decision) => decision.event));
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const candidates = new Set(
    (projection.provenance[act.event] ?? []).filter((event) => {
      const statement = statements.get(event);
      return event !== act.target && effective.has(event) && statement !== undefined && !statement.retired && !statement.stale;
    }),
  );
  return candidates.size === 1 ? candidates.values().next().value : undefined;
}
