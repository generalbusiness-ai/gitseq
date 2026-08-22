import type { ActorState, Commitment, KindDefinition, Projection, Statement, Vocabulary } from "./api.ts";
import { firstLine } from "./util.ts";

// The words a row may say about itself. The first four are what a live row
// can say, and no fifth: whether a reported row is already approved and
// waiting on a merge is a fact about *who* it waits on, and belongs in the
// next column.
//
// "stale" is not a fifth live state: it belongs only to the lifecycle-stale
// population, which is a different list reached by its own count. A row there
// must not borrow a live word — a commitment that was claimed and then went
// stale is not "in progress", and saying so under a heading that calls the
// whole population not-in-flight would have the one screen contradict itself
// about one row.
//
// The closed words and "awaiting ratification" name populations a live row is
// never in, so they extend this union without weakening the rule above.
export type RowState =
  | "needs attention"
  | "unclaimed"
  | "in progress"
  | "reported"
  | "stale"
  | "satisfied"
  | "cancelled"
  | "reneged"
  | "withdrawn"
  | "awaiting ratification";

// Which population the list is showing. Each is one tab, and each replaces
// the headline with its own count, so no number on the screen is more than
// one click from exactly the rows it counts. "moved" is the open requests
// whose reasoning moved underneath them — a subset of "live", not a separate
// lifecycle state.
//
// "ratification" is the one population that is not a slice of the commitments:
// it is built by ratificationRows from the vocabulary, takes different inputs
// and returns a different shape. It is a tab here because a reader looking for
// what is owed should find it where the other counts are, not because it is
// another workRows population — the caller dispatches on it rather than
// passing it through.
export type Population = "live" | "moved" | "stale" | "done" | "closed" | "ratification";
export const POPULATIONS: { key: Population; label: string }[] = [
  { key: "live", label: "open" },
  { key: "moved", label: "reasoning moved" },
  { key: "stale", label: "stale, not in flight" },
  { key: "done", label: "completed" },
  { key: "closed", label: "closed, not completed" },
  { key: "ratification", label: "awaiting ratification" },
];

// The priority rule, as the default sort rather than as prose. Groups 0 to 2
// cannot move without a person, so age is the signal; group 3 is running and
// only needs watching, so recency is right there.
export const PRIORITY_GROUPS = ["needs attention", "unclaimed", "waiting on a human", "running"] as const;

export interface WorkRow {
  /** The request this row is about, and the thread a click opens. */
  event: string;
  ticket?: number;
  state: RowState;
  /** Only `needs attention` is coloured. Ordinary staleness never sets it. */
  attention: boolean;
  /** The one actor who must act next. Empty when nobody is named. */
  waitsOn: string;
  waitsOnName: string;
  waitsOnHuman: boolean;
  /** Seconds since the epoch of the last durable event anywhere in the thread. */
  moved: number;
  title: string;
  /** Ordinary staleness: counted below the list, never on the row. */
  stale: boolean;
  group: number;
  search: string;
}

// The lifecycle states that are work in flight. Everything else is history or
// a queue that is no longer moving.
const LIVE_STATUSES = ["open", "promised", "reported"];
const CLOSED_STATUSES = ["cancelled", "reneged", "withdrawn"];

function inPopulation(commitment: Commitment, population: Population): boolean {
  switch (population) {
    case "live":
      return LIVE_STATUSES.includes(commitment.status);
    case "moved":
      return LIVE_STATUSES.includes(commitment.status) && commitment.stale === true;
    case "stale":
      return commitment.status === "stale";
    case "done":
      return commitment.status === "satisfied";
    case "closed":
      return CLOSED_STATUSES.includes(commitment.status);
    // Not a slice of the commitments at all: ratificationRows builds it from
    // the vocabulary. The caller dispatches before reaching workRows, so this
    // is unreachable rather than empty — it is here because the switch is
    // exhaustive over Population, which is what makes adding a seventh
    // population a compile error rather than a silently empty tab.
    case "ratification":
      return false;
  }
}

// Age, in the shortest form that is still true: `3m`, `6h`, `7d`.
export function age(seconds: number, now = Date.now() / 1000): string {
  const delta = Math.max(0, now - seconds);
  if (!Number.isFinite(delta) || seconds <= 0) return "—";
  if (delta < 3600) return `${Math.max(1, Math.round(delta / 60))}m`;
  if (delta < 86400) return `${Math.round(delta / 3600)}h`;
  return `${Math.round(delta / 86400)}d`;
}

// The last durable event anywhere in the thread, for every event at once. A
// three-week-old request that moved an hour ago is not neglected work, so the
// column cannot read the request's own timestamp.
//
// One pass, backwards. A record's basis is always earlier in the log than the
// record, so walking the log in reverse and pushing each event's movement up
// to its conversational parent settles every ancestor before it is read. The
// alternative — walking each request's descendants — is quadratic on a log
// whose spine four fifths of the records hang from.
export function lastMovement(projection: Projection): Map<string, number> {
  const timestamps = new Map<string, number>();
  for (const statement of projection.statements) timestamps.set(statement.event, statement.timestamp ?? 0);
  for (const act of projection.acts) timestamps.set(act.event, act.timestamp ?? 0);
  const order = projection.decisions.length > 0
    ? projection.decisions.map((decision) => decision.event)
    : [...timestamps.keys()];
  const moved = new Map<string, number>();
  for (let index = order.length - 1; index >= 0; index--) {
    const event = order[index];
    const own = Math.max(timestamps.get(event) ?? 0, moved.get(event) ?? 0);
    moved.set(event, own);
    const parent = (projection.provenance[event] ?? [])[0];
    if (parent && own > (moved.get(parent) ?? 0)) moved.set(parent, own);
  }
  return moved;
}

// A row needs attention when a merge would actually refuse it, when the room
// is in dispute about it, or when a promise rests on no request so nobody is
// positioned to close it. Ordinary staleness is none of those: 27 of 42 live
// commitments carry it, so a row state fed from it would colour nearly two
// rows in three and say nothing.
function needsAttention(commitment: Commitment, records: (Statement | undefined)[]): boolean {
  if (commitment.status === "disputed") return true;
  return records.some((record) => record?.describes_superseded_world === true);
}

function rowState(commitment: Commitment, attention: boolean, population: Population): RowState {
  // Outside the live populations the fold has already said this commitment
  // is not in flight, and the only true word for it is the one the fold used:
  // stale, satisfied, cancelled, reneged or withdrawn. No live lifecycle word
  // applies, whether or not somebody once claimed it. The row can still be
  // loud — attention is carried in its own field, not in this word — so a
  // world-stale stale row reads "stale" and is coloured, which is both facts
  // at once rather than either one swallowing the other.
  if (population === "stale" || population === "done" || population === "closed") return commitment.status as RowState;
  if (attention) return "needs attention";
  if (commitment.status === "reported") return "reported";
  if (commitment.promise) return "in progress";
  return "unclaimed";
}

export interface RowContext {
  nameOf: (fingerprint: string) => string;
  tickets: Map<string, number>;
  actors: Record<string, ActorState>;
}

// One row per commitment in the named population. The default is the live
// commitments — open, promised or reported. Lifecycle-stale, completed and
// closed commitments are their own tabs: they are not work in flight, and
// without that separation 110 stale rows would sort above the 42 that are
// actually moving.
//
// The stale population is every commitment the fold marked lifecycle-stale,
// claimed or not. Some carry a durable promise — work that was taken up and
// then stalled — so it is not a queue of unclaimed requests and must not be
// described as one.
export function workRows(projection: Projection, context: RowContext, population: Population = "live"): WorkRow[] {
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const moved = lastMovement(projection);
  const rows: WorkRow[] = [];
  for (const commitment of projection.commitments) {
    if (!inPopulation(commitment, population)) continue;
    const request = statements.get(commitment.request);
    if (!request) continue;
    // Computed for every population. World-staleness and retirement stay loud
    // wherever they appear: a lifecycle-stale row whose approval or artifact
    // describes a superseded world is still the thing a merge will refuse, and
    // quieting it because its lifecycle already stopped would hide the louder
    // fact behind the duller one.
    const attention = needsAttention(commitment, [
      request,
      commitment.promise ? statements.get(commitment.promise) : undefined,
      commitment.report ? statements.get(commitment.report) : undefined,
    ]);
    const waitsOn = commitment.waiting_on ?? (commitment.promise ? "" : commitment.addressed_to ?? "");
    const title = firstLine(request.text);
    rows.push({
      event: commitment.request,
      ticket: context.tickets.get(commitment.request),
      state: rowState(commitment, attention, population),
      attention,
      waitsOn,
      waitsOnName: waitsOn ? context.nameOf(waitsOn) : "unassigned",
      waitsOnHuman: waitsOn ? context.actors[waitsOn]?.kind === "human" : false,
      moved: moved.get(commitment.request) ?? request.timestamp ?? 0,
      title,
      stale: commitment.stale === true,
      group: 0,
      search: [title, request.text, ...Object.values(request.body ?? {})].join("\n").toLowerCase(),
    });
  }
  for (const row of rows) row.group = priorityGroup(row);
  return rows;
}

function priorityGroup(row: WorkRow): number {
  if (row.attention) return 0;
  // Every lifecycle-stale row shares one state, so the group only breaks ties.
  // It sits with the oldest-first groups because age is the only signal left
  // in a queue that stopped moving.
  if (row.state !== "in progress" && row.state !== "reported") return 1;
  if (row.waitsOnHuman) return 2;
  return 3;
}

// Every column header sorts. Third click on the same column returns to
// priority order, which is how the default stays reachable without a reset
// control of its own.
export type SortKey = "state" | "waits" | "age" | "title" | "ticket";
export interface Sort {
  key: SortKey;
  descending: boolean;
}

export function sortAfterClick(current: Sort | undefined, key: SortKey): Sort | undefined {
  if (!current || current.key !== key) return { key, descending: false };
  if (!current.descending) return { key, descending: true };
  return undefined;
}

const STATE_ORDER: Record<RowState, number> = {
  "needs attention": 0,
  "awaiting ratification": 0,
  unclaimed: 1,
  "in progress": 2,
  reported: 3,
  stale: 4,
  satisfied: 5,
  cancelled: 6,
  reneged: 6,
  withdrawn: 6,
};

// A sort reorders the rows that are there; a filter decides which rows exist.
// This function only ever reorders: the array it returns has exactly the
// members it was given.
export function sortRows(rows: readonly WorkRow[], sort?: Sort): WorkRow[] {
  const ordered = [...rows];
  if (!sort) {
    return ordered.sort(
      (a, b) => a.group - b.group || (a.group === 3 ? b.moved - a.moved : a.moved - b.moved) || compareTitle(a, b),
    );
  }
  const direction = sort.descending ? -1 : 1;
  return ordered.sort((a, b) => {
    const value =
      sort.key === "state"
        ? STATE_ORDER[a.state] - STATE_ORDER[b.state]
        : sort.key === "waits"
          ? a.waitsOnName.localeCompare(b.waitsOnName)
          : sort.key === "age"
            ? b.moved - a.moved
            : sort.key === "ticket"
              ? (a.ticket ?? 0) - (b.ticket ?? 0)
              : compareTitle(a, b);
    // Priority order breaks every tie, so a sorted column never scrambles the
    // rows it says nothing about.
    return (value || a.group - b.group || a.moved - b.moved) * direction;
  });
}

function compareTitle(a: WorkRow, b: WorkRow): number {
  return a.title.localeCompare(b.title);
}

export function matchingRows(rows: readonly WorkRow[], query: string): WorkRow[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [...rows];
  return rows.filter((row) => row.search.includes(needle) || `#${row.ticket}`.includes(needle));
}

// Ratification is not a commitment, so the list above can never show it. That
// matters more than it sounds. Conferring force is the principal duty of
// whoever holds `ratifier`, and it was the one population the board was
// structurally blind to: it was found by being told in conversation, which is
// not a queue.
//
// It is a population beside the commitments and not a total with them. They
// are owed by different people for different reasons, and one figure covering
// both would be a number nobody could act on.
export interface RatificationView {
  rows: WorkRow[];
  /** Who can discharge these. Empty when the room has no ratifier. */
  ratifiers: string[];
}

// Which kinds are owed is read from the vocabulary rather than listed here, so
// a kind declared later is classified by its own definition instead of by
// whoever remembers to edit this file.
//
// Two rules, and the second is the one that decides whether this is a signal
// or noise. A kind is a candidate when its satisfier names the ratifier role.
// But `assert` satisfies that test and renders as a note: an assert stands on
// its author's signature and has already done its job unratified. Counting
// those put 403 rows in front of the 15 that are genuinely inert without
// force, and a queue nobody can read is a queue nobody reads. What is actually
// owed is a kind that does nothing until ratified — a proposal, a governance
// act — so the render is the discriminator.
export function awaitsRatification(definition: KindDefinition): boolean {
  return definition.satisfier === "role:ratifier" && definition.render !== "note";
}

export function ratificationRows(
  projection: Projection,
  vocabulary: Vocabulary,
  context: RowContext,
): RatificationView {
  const owed = new Set(
    vocabulary.definitions.filter(awaitsRatification).map((definition) => definition.name),
  );
  const effective = new Set(
    projection.decisions
      .filter((decision) => decision.verdict === "effective")
      .map((decision) => decision.event),
  );
  const ratifiers = Object.entries(context.actors)
    .filter(([, actor]) => actor.roles?.includes("ratifier"))
    .map(([fingerprint]) => fingerprint);
  // Only when exactly one actor can act is there one actor to name. With none
  // the rows are owed to nobody in the room, and with several the next mover
  // is not determined — in both cases the column stays empty rather than
  // picking one, and the caller says which case it is.
  const waitsOn = ratifiers.length === 1 ? ratifiers[0] : "";
  const rows: WorkRow[] = [];
  for (const statement of projection.statements) {
    if (!owed.has(statement.kind)) continue;
    // A retired statement asks for nothing and an ineffective one never took
    // hold, so neither is waiting on anybody.
    if (statement.ratified || statement.retired) continue;
    if (!effective.has(statement.event)) continue;
    const title = firstLine(statement.text);
    rows.push({
      event: statement.event,
      ticket: context.tickets.get(statement.event),
      state: "awaiting ratification",
      attention: false,
      waitsOn,
      waitsOnName: waitsOn ? context.nameOf(waitsOn) : "",
      waitsOnHuman: waitsOn ? context.actors[waitsOn]?.kind !== "agent" : false,
      moved: statement.timestamp ?? 0,
      title,
      stale: statement.stale === true,
      group: 0,
      search: `${title} ${statement.kind} ${context.nameOf(statement.actor)}`.toLowerCase(),
    });
  }
  return { rows, ratifiers };
}
