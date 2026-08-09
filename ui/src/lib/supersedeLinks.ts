import type { Verdict } from "./api";

interface SupersedeAct {
  event: string;
  type: "ratify" | "supersede";
  target: string;
  verdict: Verdict;
}

interface SupersedeLinkProjection {
  decisions: { event: string; verdict: Verdict }[];
  statements: { event: string; retired?: boolean; stale?: boolean }[];
  provenance: Record<string, string[]>;
}

// Additional supersede provenance may name a replacement, evidence, or a
// governing basis; the projection does not type that role. It can establish
// only that exactly one additional current statement is linked. The signed
// act text remains responsible for describing what the link means.
export function soleCurrentSupersedeBasis(act: SupersedeAct, projection: SupersedeLinkProjection): string | undefined {
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
