import type { Projection, Vocabulary } from "./api";

// Notes is a reading view under the room's declared render vocabulary. It
// does not create a lifecycle or change request/ratification populations.
export function noteRows(projection: Projection, vocabulary: Vocabulary | undefined, query: string) {
  const kinds = new Set((vocabulary?.definitions ?? []).filter((kind) => kind.render === "note").map((kind) => kind.name));
  const needle = query.trim().toLocaleLowerCase();
  return projection.statements.filter((statement) => kinds.has(statement.kind) &&
    (!needle || `#${statement.sequence} ${statement.text} ${projection.actors[statement.actor]?.name ?? statement.actor}`.toLocaleLowerCase().includes(needle)))
    .sort((a, b) => b.sequence - a.sequence);
}

export function exactRecord(projection: Projection | undefined, query: string) {
  const value = query.trim();
  if (!projection) return undefined;
  const number = /^#([1-9]\d*)$/.exec(value);
  return projection.decisions.find((decision) => number ? decision.sequence === Number(number[1]) : decision.event === value);
}
