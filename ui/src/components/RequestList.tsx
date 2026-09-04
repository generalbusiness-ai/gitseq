import { useMemo } from "react";
import { Search } from "lucide-react";
import { ticketsOf, type Workroom } from "../lib/store";
import { buildOutcomeMap } from "../lib/outcomeMap.ts";
import { age, matchingRows, POPULATIONS, ratificationRows, sortAfterClick, sortRows, workRows, type Population, type Sort, type SortKey, type WorkRow } from "../lib/rows";
import { cn } from "../lib/util";
import { OutcomeMap } from "./OutcomeMap";
import { RebuildNotice } from "./RebuildNotice";


// What the operator chose on this screen. Owned by the caller so it survives
// opening a thread and coming back: the list is not the place the operator
// was, it is the question they were asking, and the question should still be
// on the screen when they return to it.
export interface ListView {
  sort?: Sort;
  query: string;
  population: Population;
  presentation?: "table" | "graph";
}
export const defaultListView: ListView = { query: "", population: "live", presentation: "table" };

const COLUMNS: { key: SortKey; label: string; className: string }[] = [
  { key: "state", label: "state", className: "w-[9rem]" },
  { key: "waits", label: "waits on", className: "w-[7rem]" },
  { key: "age", label: "age", className: "w-[3.5rem] text-right" },
  { key: "title", label: "title", className: "" },
  { key: "ticket", label: "#", className: "w-[5rem] text-right" },
];

// The table and graph are two presentations of one selected, search-filtered
// population. Sorting belongs to the table; it never changes graph membership.
export function RequestList({
  workroom,
  onOpenThread,
  view,
  onView,
}: {
  workroom: Workroom;
  onOpenThread: (event: string) => void;
  view: ListView;
  onView: (view: ListView) => void;
}) {
  const projection = workroom.status?.durable.projection;
  const vocabulary = workroom.status?.durable.vocabulary;
  const { sort, query, population } = view;
  const presentation = view.presentation ?? "table";
  const setSort = (update: (current?: Sort) => Sort | undefined) => onView({ ...view, sort: update(view.sort) });
  const setQuery = (query: string) => onView({ ...view, query });
  const setPopulation = (population: Population) => onView({ ...view, population });
  const setPresentation = (presentation: "table" | "graph") => onView({ ...view, presentation });
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((actor) => [actor.fingerprint, actor.name]));
    return (fingerprint: string) =>
      byFingerprint.get(fingerprint) ??
      projection?.actors?.[fingerprint]?.name ??
      fingerprint.slice(0, 8);
  }, [projection, workroom.actors]);

  const context = useMemo(() => ({ nameOf, tickets, actors: projection?.actors ?? {} }), [nameOf, tickets, projection]);
  // Beside the commitments, not added to them: a different duty, owed by a
  // different person. ratificationRows reads the vocabulary and returns
  // ratifiers alongside its rows, so it is dispatched into the map below
  // rather than mapped through workRows with the other five. Handing
  // "ratification" to workRows would return nothing and the tab would count
  // zero forever, which is the shape this avoids.
  const ratification = useMemo(
    () =>
      projection && vocabulary
        ? ratificationRows(projection, vocabulary, context)
        : { rows: [], ratifiers: [] },
    [projection, vocabulary, context],
  );
  const populations = useMemo(
    () =>
      Object.fromEntries(
        POPULATIONS.map(({ key }) => [
          key,
          key === "ratification" ? ratification.rows : projection ? workRows(projection, context, key) : [],
        ]),
      ) as Record<Population, WorkRow[]>,
    [projection, context, ratification],
  );
  // Search defines the population before a tab selects one slice. Keeping the
  // filtered map beside the unfiltered map makes every tab count a preview of
  // exactly what that tab will render under the current query.
  const filteredPopulations = useMemo(
    () =>
      Object.fromEntries(
        POPULATIONS.map(({ key }) => [key, matchingRows(populations[key], query)]),
      ) as Record<Population, WorkRow[]>,
    [populations, query],
  );
  const rows = useMemo(
    () => sortRows(filteredPopulations[population], sort),
    [filteredPopulations, population, sort],
  );
  // The graph receives exactly the rows selected by the population and search.
  // It intentionally ignores table order, so sorting cannot change layout or
  // which lifecycle represents a collapsed thread card.
  const graphRows = filteredPopulations[population];
  const graph = useMemo(
    () => (projection ? buildOutcomeMap(projection, graphRows) : undefined),
    [projection, graphRows],
  );

  if (!projection) return <RebuildNotice />;

  // The lifecycle-stale line says what its rows are and nothing more. An
  // earlier head called them "requests nobody claimed", which is false for
  // every one that carries a durable promise — 17 of 110 on the board this was
  // written against. The population is deliberately not filtered to the
  // unclaimed ones: filters were deleted because they hide work and because
  // the operator cannot tell, from a filtered screen, what they are not
  // seeing, and hiding 17 stalled claims to rescue a phrase would be that
  // mistake in miniature. The count still opens to exactly the rows it counts.
  const headline = {
    live: `${rows.length} open ${rows.length === 1 ? "request" : "requests"}`,
    moved: `${rows.length} resting on reasoning that has moved`,
    stale: `${rows.length} stale ${rows.length === 1 ? "request" : "requests"}, not in flight`,
    done: `${rows.length} completed`,
    closed: `${rows.length} closed, not completed`,
    ratification: `${rows.length} ${rows.length === 1 ? "act awaits" : "acts await"} ratification`,
  }[population];

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
          {/* One tab per population. Every count is one click from exactly
              the rows it counts, and the tab strip is the only place the
              operator chooses which rows exist. */}
          <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-1 border-b border-border px-1">
            <div role="tablist" aria-label="Request populations" className="flex flex-wrap gap-x-1">
              {POPULATIONS.map(({ key, label }) => (
                <button
                  key={key}
                  type="button"
                  role="tab"
                  aria-selected={population === key}
                  onClick={() => setPopulation(key)}
                  className={cn(
                    "-mb-px flex items-baseline gap-1.5 border-b-2 px-2 py-1.5 text-[11px] focus-visible:outline focus-visible:outline-accent",
                    population === key ? "border-accent font-medium text-foreground" : "border-transparent text-faint hover:text-muted",
                  )}
                >
                  {label}
                  <span className="font-mono text-faint">{filteredPopulations[key].length}</span>
                </button>
              ))}
            </div>
            <div role="tablist" aria-label="Board presentation" className="mb-1 flex rounded border border-border p-0.5 text-[11px]">
              {(["table", "graph"] as const).map((choice) => (
                <button
                  key={choice}
                  type="button"
                  role="tab"
                  aria-selected={presentation === choice}
                  onClick={() => setPresentation(choice)}
                  className={cn(
                    "rounded px-2 py-1 capitalize focus-visible:outline focus-visible:outline-accent",
                    presentation === choice ? "bg-accent text-background" : "text-faint hover:text-foreground",
                  )}
                >
                  {choice === "table" ? "Table" : "Graph"}
                </button>
              ))}
            </div>
          </div>

          <div className="mt-2 flex flex-wrap items-baseline gap-3 px-1">
            <h2 className="font-serif text-base font-semibold" aria-live="polite">
              {headline}
            </h2>
            {presentation === "table" ? (
              <span className="text-[11px] text-faint">
                sorted by <b className="font-semibold text-accent">{sort ? COLUMNS.find((c) => c.key === sort.key)?.label : "priority"}</b>
                {" — click any column to re-sort"}
              </span>
            ) : (
              <span className="text-[11px] text-faint">same selected rows as the table, with bounded direct context</span>
            )}
          </div>

          {rows.length === 0 ? (
            <p className="py-12 text-center text-xs text-faint">
              {query ? "Nothing here matches that." : "Nothing is waiting."}
            </p>
          ) : presentation === "table" ? (
            <table data-board-presentation="table" className="mt-2 w-full table-fixed border-collapse text-xs">
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
                  <Row key={row.key} row={row} onOpen={() => onOpenThread(row.event)} />
                ))}
              </tbody>
            </table>
          ) : graph ? (
            <OutcomeMap graph={graph} nameOf={nameOf} onOpenThread={onOpenThread} />
          ) : (
            <RebuildNotice />
          )}
          {/* Who is expected to act, when the rows cannot say it themselves.
              A queue with no ratifier is not a discharged queue, and an empty
              screen reading "nothing is waiting" would say the opposite of
              what is true. */}
          {population === "ratification" && ratification.ratifiers.length !== 1 && (
            <p className="mt-2 px-1 text-[11px] text-danger">
              {ratification.ratifiers.length === 0
                ? "Nobody in this room holds `ratifier`, so nothing here can be discharged."
                : `${ratification.ratifiers.length} actors hold \`ratifier\`, so no single actor is named as the next mover.`}
            </p>
          )}
        </div>
      </div>
    </div>
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
      data-row-key={row.key}
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
