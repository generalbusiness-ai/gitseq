import { useMemo, useState } from "react";
import { Search } from "lucide-react";
import { ticketsOf, type Workroom } from "../lib/store";
import { age, matchingRows, sortAfterClick, sortRows, workRows, type Sort, type SortKey, type WorkRow } from "../lib/rows";
import { cn } from "../lib/util";
import { RebuildNotice } from "./RebuildNotice";

// Which population the list is showing. Each is reached from the number that
// counts it, and each replaces the headline with its own count, so no number
// on this screen is more than one click from exactly the rows it counts.
type Population = "live" | "moved" | "stale";

const COLUMNS: { key: SortKey; label: string; className: string }[] = [
  { key: "state", label: "state", className: "w-[9rem]" },
  { key: "waits", label: "waits on", className: "w-[7rem]" },
  { key: "age", label: "age", className: "w-[3.5rem] text-right" },
  { key: "title", label: "title", className: "" },
  { key: "ticket", label: "#", className: "w-[5rem] text-right" },
];

// The default screen: one line per open request, in priority order. No filter
// controls, no presentation switch, no grouping. A sort reorders the rows that
// are there; a filter would decide which rows exist, and the operator cannot
// tell from a filtered screen what they are not seeing.
export function RequestList({
  workroom,
  onOpenThread,
}: {
  workroom: Workroom;
  onOpenThread: (event: string) => void;
}) {
  const projection = workroom.status?.durable.projection;
  const [sort, setSort] = useState<Sort>();
  const [query, setQuery] = useState("");
  const [population, setPopulation] = useState<Population>("live");
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((actor) => [actor.fingerprint, actor.name]));
    return (fingerprint: string) =>
      byFingerprint.get(fingerprint) ??
      projection?.actors?.[fingerprint]?.name ??
      fingerprint.slice(0, 8);
  }, [projection, workroom.actors]);

  const live = useMemo(
    () => (projection ? workRows(projection, { nameOf, tickets, actors: projection.actors ?? {} }) : []),
    [projection, nameOf, tickets],
  );
  const lifecycleStale = useMemo(
    () => (projection ? workRows(projection, { nameOf, tickets, actors: projection.actors ?? {} }, true) : []),
    [projection, nameOf, tickets],
  );
  const moved = useMemo(() => live.filter((row) => row.stale), [live]);

  const shown = population === "stale" ? lifecycleStale : population === "moved" ? moved : live;
  const rows = useMemo(
    () => sortRows(matchingRows(shown, query), sort),
    [shown, query, sort],
  );

  if (!projection) return <RebuildNotice />;

  const headline =
    population === "stale"
      ? `${rows.length} stale ${rows.length === 1 ? "request" : "requests"} nobody claimed`
      : population === "moved"
        ? `${rows.length} resting on reasoning that has moved`
        : `${rows.length} open ${rows.length === 1 ? "request" : "requests"}`;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-border px-4 py-2 sm:px-6">
        <div className="relative mx-auto max-w-5xl">
          <Search aria-hidden className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search…"
            aria-label="Search requests"
            className="h-8 w-full max-w-md rounded-md border border-input bg-background pl-8 pr-3 text-xs outline-none placeholder:text-faint focus:border-accent/60"
          />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3 sm:px-6">
        <div className="mx-auto max-w-5xl">
          <div className="flex flex-wrap items-baseline gap-3 px-1">
            <h2 className="font-serif text-base font-semibold" aria-live="polite">
              {headline}
            </h2>
            <span className="text-[11px] text-faint">
              sorted by <b className="font-semibold text-accent">{sort ? COLUMNS.find((c) => c.key === sort.key)?.label : "priority"}</b>
              {" — click any column to re-sort"}
            </span>
          </div>

          {/* Two quiet lines, each opening to exactly the rows it counts. The
              second is why the list is 42 rows rather than 152, with the 110
              sorting above the work that is actually moving. */}
          <p className="mt-1 flex flex-wrap gap-x-3 px-1 text-[11px] text-faint">
            <SummaryLink
              active={population === "moved"}
              onClick={() => setPopulation(population === "moved" ? "live" : "moved")}
              label={`${moved.length} of these rest on reasoning that has moved.`}
            />
            <SummaryLink
              active={population === "stale"}
              onClick={() => setPopulation(population === "stale" ? "live" : "stale")}
              label={`${lifecycleStale.length} stale requests nobody claimed.`}
            />
          </p>

          <table className="mt-2 w-full table-fixed border-collapse text-xs">
            <thead>
              <tr>
                {COLUMNS.map((column) => (
                  <th
                    key={column.key}
                    scope="col"
                    aria-sort={sort?.key === column.key ? (sort.descending ? "descending" : "ascending") : "none"}
                    className={cn("border-y border-border bg-surface/40 p-0", column.className)}
                  >
                    <button
                      type="button"
                      onClick={() => setSort((current) => sortAfterClick(current, column.key))}
                      className={cn(
                        "block w-full cursor-pointer px-2 py-1 text-left text-[11px] font-semibold uppercase tracking-[0.08em] focus-visible:outline focus-visible:outline-accent",
                        column.className.includes("text-right") && "text-right",
                        sort?.key === column.key ? "text-accent" : "text-faint hover:text-foreground",
                      )}
                    >
                      {column.label}
                      {sort?.key === column.key && <span aria-hidden className="ml-1 font-mono font-normal opacity-60">{sort.descending ? "↓" : "↑"}</span>}
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <Row key={row.event} row={row} onOpen={() => onOpenThread(row.event)} />
              ))}
            </tbody>
          </table>
          {rows.length === 0 && (
            <p className="py-12 text-center text-xs text-faint">
              {query ? "Nothing here matches that." : "Nothing is waiting."}
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

function SummaryLink({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "underline decoration-dotted underline-offset-2 focus-visible:outline focus-visible:outline-accent",
        active ? "font-medium text-accent" : "text-faint hover:text-muted",
      )}
    >
      {label}
    </button>
  );
}

// One line, fixed columns, no wrapping. Not the author, the topic, worktree
// pills, focus badges, ratified badges, or per-row staleness.
function Row({ row, onOpen }: { row: WorkRow; onOpen: () => void }) {
  return (
    <tr
      tabIndex={0}
      role="link"
      aria-label={`${row.state}, waits on ${row.waitsOnName}, ${row.title}`}
      data-state={row.state}
      data-waits={row.waitsOnName}
      data-group={row.group}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
      className="cursor-pointer border-b border-border/70 hover:bg-accent/[0.07] focus-visible:outline focus-visible:outline-accent"
    >
      <td className={cn("truncate px-2 py-1 font-semibold", row.attention ? "text-danger" : "text-muted")}>{row.state}</td>
      <td className={cn("truncate px-2 py-1", row.waitsOnHuman ? "font-semibold text-foreground" : "text-muted")}>{row.waitsOnName}</td>
      <td className="truncate px-2 py-1 text-right font-mono text-faint">{age(row.moved)}</td>
      <td className="truncate px-2 py-1 text-foreground/90">{row.title}</td>
      <td className="truncate px-2 py-1 text-right font-mono text-faint">{row.ticket ? `#${row.ticket}` : "—"}</td>
    </tr>
  );
}
