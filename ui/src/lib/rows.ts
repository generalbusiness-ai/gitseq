import type { ActorState, Commitment, Projection, Statement } from "./api.ts";
import { firstLine } from "./util.ts";

// The four words a row may say about itself, and no fifth. Whether a reported
// row is already approved and waiting on a merge is a fact about *who* it
// waits on, and belongs in the next column.
export type RowState = "needs attention" | "unclaimed" | "in progress" | "reported";

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
// a queue nobody claimed.
const LIVE_STATUSES = ["open", "promised", "reported"];

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

function rowState(commitment: Commitment, attention: boolean): RowState {
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

// The list is the live commitments — open, promised or reported — one row
// each. Lifecycle-stale commitments are counted below the list instead: they
// are not work in flight, and without that separation 110 of them would sort
// above the 42 that are actually moving.
export function workRows(projection: Projection, context: RowContext, stale = false): WorkRow[] {
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const moved = lastMovement(projection);
  const rows: WorkRow[] = [];
  for (const commitment of projection.commitments) {
    const live = LIVE_STATUSES.includes(commitment.status);
    const lifecycleStale = commitment.status === "stale";
    if (stale ? !lifecycleStale : !live) continue;
    const request = statements.get(commitment.request);
    if (!request) continue;
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
      state: rowState(commitment, attention),
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
  if (row.state === "unclaimed") return 1;
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
  unclaimed: 1,
  "in progress": 2,
  reported: 3,
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
