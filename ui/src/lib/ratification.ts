import type { Act, Projection, Statement } from "./api.ts";

// The act that ratifies a statement now, or undefined if none does.
//
// This reads the fold's answer rather than working one out. `ratified_by`
// names the ratification in force, chosen by the same rule that sets
// `ratified`. Searching `projection.acts` here cannot be made correct:
// acts carry no retirement, so the first effective ratify of a target may be
// one that was withdrawn, and so may the last — withdraw the newer of two and
// the older stands again. Separating those cases in the browser would mean
// rebuilding this layer's retirement rule, including restore, which is a
// second implementation of authority in the place least able to hold one.
//
// Both call sites already scanned the acts, so resolving one event by id here
// costs what they cost before.
export function activeRatification(projection: Projection, statement: Statement | undefined): Act | undefined {
  const event = statement?.ratified_by;
  return event ? projection.acts.find((act) => act.event === event) : undefined;
}
