import { useMemo, useState } from "react";
import { Columns3, GitBranch, List, Search } from "lucide-react";
import type { Projection, WorktreeView } from "../lib/api";
import type { Session } from "../lib/session";
import { ticketsOf, type Selection, type Workroom } from "../lib/store";
import {
  buildWorkProjection,
  filterWorkProjection,
  type WorkFilters,
  type WorkAttentionItem,
  type WorkItem,
  type WorkLane,
  type WorkTopic,
} from "../lib/work";
import { worktreesForCommitment, type WorktreeAssociation } from "../lib/worktrees";
import { cn, commitmentRelationship, statusLabel, statusTint } from "../lib/util";
import { EventTime } from "./EventTime";
import { Railway } from "./Railway";

type Presentation = "list" | "board";

// Work is a retrieval view over the durable ledger. It never mutates status:
// lifecycle transitions still happen through signed semantic acts.
export function WorkView({
  workroom,
  session,
  highlight,
  selection,
  onSelect,
  onOpenThread,
}: {
  workroom: Workroom;
  session: Session;
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
  onOpenThread: (event: string) => void;
}) {
  const projection = workroom.status?.durable.projection;
  const [presentation, setPresentation] = useState<Presentation>("list");
  const [filters, setFilters] = useState<WorkFilters>({ open: true, attention: true, closed: false });
  const work = useMemo(() => (projection ? buildWorkProjection(projection) : undefined), [projection]);
  const visible = useMemo(() => (work ? filterWorkProjection(work, filters) : undefined), [work, filters]);
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((actor) => [actor.fingerprint, actor.name]));
    return (fingerprint: string) =>
      byFingerprint.get(fingerprint) ??
      projection?.statements.find((statement) => statement.kind === "roster" && statement.body?.actor === fingerprint)?.body?.name ??
      fingerprint.slice(0, 8);
  }, [projection, workroom.actors]);
  const me = workroom.actors.find((actor) => actor.name === session.actor)?.fingerprint;
  const itemCount = visible?.topics.reduce((sum, topic) => sum + topic.items.length, 0) ?? 0;

  const updateFlag = (key: "open" | "attention" | "closed", value: boolean) =>
    setFilters((current) => ({ ...current, [key]: value }));
  const setAuthor = (author?: string) => setFilters((current) => ({ ...current, author }));

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-border bg-surface/40 px-4 py-3 sm:px-6">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-2.5">
          <div className="relative min-w-[13rem] flex-1 sm:max-w-md">
            <Search aria-hidden className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
            <input
              type="search"
              value={filters.query ?? ""}
              onChange={(event) => setFilters((current) => ({ ...current, query: event.target.value }))}
              placeholder="Search topics and their activity…"
              aria-label="Search work"
              className="h-8 w-full rounded-md border border-input bg-background pl-8 pr-3 text-xs outline-none placeholder:text-faint focus:border-accent/60"
            />
          </div>
          <fieldset className="flex items-center gap-1.5" aria-label="Lifecycle filters">
            <legend className="sr-only">Lifecycle filters</legend>
            <FilterCheck label="Open" checked={filters.open} count={work?.topics.reduce((sum, topic) => sum + topic.openCount, 0)} onChange={(value) => updateFlag("open", value)} />
            <FilterCheck label="Attention" checked={filters.attention} count={(work?.topics.reduce((sum, topic) => sum + topic.attentionCount, 0) ?? 0) + (work?.attention.length ?? 0)} tone="danger" onChange={(value) => updateFlag("attention", value)} />
            <FilterCheck label="Closed" checked={filters.closed} count={work?.topics.reduce((sum, topic) => sum + topic.closedCount, 0)} onChange={(value) => updateFlag("closed", value)} />
          </fieldset>
          <div className="flex items-center gap-1.5">
            {me && (
              <button
                type="button"
                aria-pressed={filters.author === me}
                onClick={() => setAuthor(filters.author === me ? undefined : me)}
                className={controlClass(filters.author === me)}
              >
                Mine
              </button>
            )}
            <select
              value={filters.author ?? ""}
              onChange={(event) => setAuthor(event.target.value || undefined)}
              aria-label="Topic author"
              className="h-8 max-w-40 rounded-md border border-border bg-background px-2 text-xs text-muted outline-none focus:border-accent/60"
            >
              <option value="">All authors</option>
              {work?.authors.map((author) => (
                <option key={author} value={author}>{nameOf(author)}</option>
              ))}
            </select>
          </div>
          <div className="ml-auto flex rounded-md border border-border p-0.5" aria-label="Work presentation">
            <PresentationButton label="Topics" active={presentation === "list"} onClick={() => setPresentation("list")} icon={<List className="h-3.5 w-3.5" />} />
            <PresentationButton label="Board" active={presentation === "board"} onClick={() => setPresentation("board")} icon={<Columns3 className="h-3.5 w-3.5" />} />
          </div>
        </div>
        <p className="mx-auto mt-2 max-w-7xl text-[11px] text-faint" aria-live="polite">
          {visible?.topics.length ?? 0} {(visible?.topics.length ?? 0) === 1 ? "topic" : "topics"} · {itemCount} visible work {itemCount === 1 ? "item" : "items"}
          {filters.author ? ` · written by ${nameOf(filters.author)}` : ""}
        </p>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6">
        {!projection || !visible ? (
          <p className="py-12 text-center text-sm text-faint">Loading work…</p>
        ) : visible.topics.length === 0 && visible.attention.length === 0 ? (
          <div className="mx-auto max-w-xl py-16 text-center">
            <p className="font-serif text-lg text-foreground/90">No work matches this view.</p>
            <p className="mt-1 text-xs text-faint">Try another author, clear the search, or include another lifecycle.</p>
          </div>
        ) : (
          <>
            <OtherAttention items={visible.attention} tickets={tickets} nameOf={nameOf} onSelect={onSelect} onOpenThread={onOpenThread} />
            {visible.topics.length > 0 && (presentation === "list" ? (
              <TopicList topics={visible.topics} projection={projection} tickets={tickets} commits={workroom.commits} worktrees={workroom.worktrees ?? []} nameOf={nameOf} onOpenThread={onOpenThread} />
            ) : (
              <WorkBoard topics={visible.topics} filters={filters} projection={projection} tickets={tickets} commits={workroom.commits} worktrees={workroom.worktrees ?? []} nameOf={nameOf} onOpenThread={onOpenThread} />
            ))}
          </>
        )}

        <details className="mx-auto mt-6 max-w-7xl rounded-lg border border-border bg-surface/30">
          <summary className="cursor-pointer px-3 py-2 text-xs font-medium text-muted focus-visible:outline focus-visible:outline-accent">
            Repository context
          </summary>
          <div className="border-t border-border p-3">
            <div className="mb-3 flex flex-wrap gap-1.5">
              {workroom.localOffline && <span className="text-xs text-warn">Local checkout state unavailable.</span>}
              {!workroom.localOffline && !workroom.worktrees && <span className="text-xs text-faint">Reading local checkouts…</span>}
              {workroom.worktrees?.map((worktree, index) => <WorktreePill key={`${worktree.checkout}:${index}`} worktree={worktree} />)}
            </div>
            {workroom.graphTruncated && <p className="mb-2 text-[11px] text-warn">Showing the newest 80 commits.</p>}
            <div className="h-[26rem] overflow-hidden rounded-lg border border-border">
              <Railway commits={workroom.commits} statements={projection?.statements ?? []} highlight={highlight} selection={selection} onSelect={onSelect} />
            </div>
          </div>
        </details>
      </div>
    </div>
  );
}

function OtherAttention({ items, tickets, nameOf, onSelect, onOpenThread }: {
  items: WorkAttentionItem[];
  tickets: Map<string, number>;
  nameOf: (fingerprint: string) => string;
  onSelect: (selection: Selection) => void;
  onOpenThread: (event: string) => void;
}) {
  if (items.length === 0) return null;
  return (
    <details className="mx-auto mb-4 max-w-5xl rounded-lg border border-danger/35 bg-danger/5">
      <summary className="cursor-pointer px-3 py-2 text-xs font-semibold text-danger focus-visible:outline focus-visible:outline-accent">
        Other attention ({items.length})
        <span className="ml-2 font-normal text-faint">retired or stale artifacts and unlinked promises</span>
      </summary>
      <div className="space-y-0.5 border-t border-danger/20 p-2">
        {items.map((item) => (
          <button
            key={`${item.kind}:${item.event}`}
            type="button"
            onClick={() => item.kind === "artifact" && item.commit ? onSelect({ kind: "commit", id: item.commit }) : onOpenThread(item.event)}
            className="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-elevated/70 focus-visible:outline focus-visible:outline-accent"
          >
            <span className="w-14 shrink-0 font-semibold text-danger">{item.label}</span>
            <span className="min-w-0 flex-1 text-foreground/85">{item.title}</span>
            {item.actor && <span className="shrink-0 text-faint">{nameOf(item.actor)}</span>}
            <EventTime timestamp={item.timestamp} />
            <span className="shrink-0 font-mono text-[11px] text-faint" title={item.event}>#{tickets.get(item.event) ?? "?"}</span>
          </button>
        ))}
      </div>
    </details>
  );
}

function TopicList(props: WorkRenderProps & { topics: WorkTopic[] }) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const toggle = (event: string) => setExpanded((current) => {
    const next = new Set(current);
    if (next.has(event)) next.delete(event); else next.add(event);
    return next;
  });
  return (
    <div className="mx-auto max-w-5xl space-y-2">
      {props.topics.map((topic) => {
        const isExpanded = expanded.has(topic.event);
        const rootItems = topic.items.filter((item) => item.request.event === topic.event);
        const descendants = topic.items.filter((item) => item.request.event !== topic.event);
        const shown = isExpanded ? topic.items : [...rootItems, ...descendants].slice(0, 3);
        return <article key={topic.event} className="rounded-lg border border-border bg-card/55 px-3 py-2.5">
          <button type="button" onClick={() => props.onOpenThread(topic.event)} className="block w-full rounded text-left focus-visible:outline focus-visible:outline-accent">
            <div className="flex flex-wrap items-start gap-x-3 gap-y-1">
              <h2 className="min-w-0 flex-1 font-serif text-[15px] font-semibold leading-5 text-foreground/95">{topic.title}</h2>
              <TopicCounts topic={topic} />
            </div>
            <p className="mt-1 text-[11px] text-faint">
              written by {props.nameOf(topic.author)} · latest activity by {props.nameOf(topic.latestActor)} <EventTime timestamp={topic.latestTimestamp} />
            </p>
          </button>
          <div className="mt-2 space-y-1 border-t border-border/70 pt-2">
            {shown.map((item) => <WorkItemRow key={item.key} item={item} topic={topic} {...props} />)}
            {(topic.items.length > shown.length || isExpanded) && (
              <button type="button" aria-expanded={isExpanded} onClick={() => toggle(topic.event)} className="ml-2 rounded px-1 py-0.5 text-[11px] font-medium text-info hover:underline focus-visible:outline focus-visible:outline-accent">
                {isExpanded ? "Show summary" : `Show all ${topic.items.length} items`}
              </button>
            )}
          </div>
        </article>;
      })}
    </div>
  );
}

function WorkBoard({ topics, filters, ...props }: WorkRenderProps & { topics: WorkTopic[]; filters: WorkFilters }) {
  const cards = topics.flatMap((topic) => topic.items.map((item) => ({ item, topic })));
  const laneEnabled = (lane?: WorkLane) => lane === "closed" ? filters.closed : Boolean(lane && filters.open);
  const attentionOnly = cards.filter(({ item }) => item.attention && !laneEnabled(item.lane));
  const lanes: { id: WorkLane; title: string }[] = [
    ...(filters.open ? [
      { id: "available" as const, title: "Available" },
      { id: "inProgress" as const, title: "In progress" },
      { id: "review" as const, title: "Review" },
    ] : []),
    ...(filters.closed ? [{ id: "closed" as const, title: "Closed" }] : []),
  ];
  return (
    <div className="mx-auto max-w-7xl space-y-4">
      {attentionOnly.length > 0 && (
        <section aria-labelledby="attention-heading">
          <h2 id="attention-heading" className="mb-2 text-xs font-semibold uppercase tracking-[0.12em] text-danger">Needs attention</h2>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {attentionOnly.map(({ item, topic }) => <BoardCard key={item.key} item={item} topic={topic} {...props} />)}
          </div>
        </section>
      )}
      {lanes.length > 0 && (
        <div className="overflow-x-auto pb-2">
          <div className="grid min-w-max gap-3" style={{ gridTemplateColumns: `repeat(${lanes.length}, minmax(16rem, 1fr))` }}>
            {lanes.map((lane) => {
              const items = cards.filter(({ item }) => item.lane === lane.id);
              return (
                <section key={lane.id} aria-labelledby={`lane-${lane.id}`} className="w-[min(21rem,82vw)] rounded-lg bg-surface/55 p-2.5 xl:w-auto">
                  <h2 id={`lane-${lane.id}`} className="mb-2 flex items-center justify-between text-xs font-semibold uppercase tracking-[0.12em] text-faint">
                    {lane.title}<span className="font-mono text-[11px]">{items.length}</span>
                  </h2>
                  <div className="space-y-2">
                    {items.length === 0 && <p className="rounded-md border border-dashed border-border px-2 py-4 text-center text-xs italic text-faint">Nothing here.</p>}
                    {items.map(({ item, topic }) => <BoardCard key={item.key} item={item} topic={topic} {...props} />)}
                  </div>
                </section>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

interface WorkRenderProps {
  projection: Projection;
  tickets: Map<string, number>;
  commits: Workroom["commits"];
  worktrees: WorktreeView[];
  nameOf: (fingerprint: string) => string;
  onOpenThread: (event: string) => void;
}

function WorkItemRow({ item, projection, tickets, commits, worktrees, nameOf, onOpenThread }: WorkRenderProps & { item: WorkItem; topic: WorkTopic }) {
  const associations = worktreesForCommitment(item.commitment, projection, commits, worktrees);
  const relationship = commitmentRelationship(item.commitment, nameOf);
  return (
    <button type="button" onClick={() => onOpenThread(item.request.event)} className="w-full rounded-md px-2 py-1.5 text-left hover:bg-elevated/70 focus-visible:outline focus-visible:outline-accent">
      <div className="flex items-start gap-2 text-xs">
        <StatusBadge item={item} />
        <span className="min-w-0 flex-1 text-foreground/85">{item.request.text}</span>
        {relationship && <span className="shrink-0 text-faint">{relationship}</span>}
        <span className="shrink-0 font-mono text-[11px] text-faint" title={item.request.event}>#{tickets.get(item.request.event) ?? "?"}</span>
      </div>
      <WorktreeAssociations associations={associations} />
    </button>
  );
}

function BoardCard({ item, topic, projection, tickets, commits, worktrees, nameOf, onOpenThread }: WorkRenderProps & { item: WorkItem; topic: WorkTopic }) {
  const associations = worktreesForCommitment(item.commitment, projection, commits, worktrees);
  const relationship = commitmentRelationship(item.commitment, nameOf);
  return (
    <button type="button" onClick={() => onOpenThread(item.request.event)} className="block w-full rounded-md border border-border bg-card px-3 py-2.5 text-left shadow-sm hover:border-accent/40 hover:bg-elevated/70 focus-visible:outline focus-visible:outline-accent">
      <div className="flex items-center justify-between gap-2">
        <StatusBadge item={item} />
        <span className="font-mono text-[11px] text-faint" title={item.request.event}>#{tickets.get(item.request.event) ?? "?"}</span>
      </div>
      <p className="mt-1.5 text-sm leading-5 text-foreground/90">{item.request.text}</p>
      {item.request.event !== topic.event && <p className="mt-1 line-clamp-2 text-[11px] text-faint">in {topic.title}</p>}
      <p className="mt-1.5 text-[11px] text-faint">
        asked by {nameOf(item.request.actor)}{relationship && <> · {relationship}</>}
      </p>
      <WorktreeAssociations associations={associations} />
    </button>
  );
}

function StatusBadge({ item }: { item: WorkItem }) {
  return (
    <span className="flex shrink-0 items-center gap-1">
      <span className={cn("text-[11px] font-semibold", statusTint[item.commitment.status])}>{statusLabel(item.commitment.status)}</span>
      {item.attention && item.commitment.status !== "stale" && item.commitment.status !== "disputed" && (
        <span className="rounded border border-danger/40 px-1 text-[10px] font-semibold uppercase text-danger">attention</span>
      )}
    </span>
  );
}

function TopicCounts({ topic }: { topic: WorkTopic }) {
  return (
    <span className="flex shrink-0 items-center gap-1 text-[10px] font-semibold uppercase tracking-wide">
      {topic.attentionCount > 0 && <span className="rounded border border-danger/40 px-1.5 py-0.5 text-danger">{topic.attentionCount} attention</span>}
      {topic.openCount > 0 && <span className="rounded border border-info/35 px-1.5 py-0.5 text-info">{topic.openCount} open</span>}
      {topic.closedCount > 0 && <span className="rounded border border-border px-1.5 py-0.5 text-faint">{topic.closedCount} closed</span>}
    </span>
  );
}

function FilterCheck({ label, checked, count, tone, onChange }: { label: string; checked: boolean; count?: number; tone?: "danger"; onChange: (checked: boolean) => void }) {
  return (
    <label className={cn("flex h-8 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs", checked ? "border-accent/50 bg-accent/10 text-foreground" : "border-border text-faint hover:text-muted", tone === "danger" && checked && "border-danger/50 bg-danger/10")}>
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} className="h-3.5 w-3.5 accent-[var(--color-accent)]" />
      {label}<span className={cn("font-mono text-[10px]", tone === "danger" && (count ?? 0) > 0 && "text-danger")}>{count ?? 0}</span>
    </label>
  );
}

function PresentationButton({ label, active, icon, onClick }: { label: string; active: boolean; icon: React.ReactNode; onClick: () => void }) {
  return (
    <button type="button" aria-pressed={active} onClick={onClick} className={cn("flex h-7 items-center gap-1.5 rounded px-2 text-xs focus-visible:outline focus-visible:outline-accent", active ? "bg-elevated text-foreground" : "text-faint hover:text-muted")}>
      {icon}{label}
    </button>
  );
}

const controlClass = (active: boolean) => cn("h-8 rounded-md border px-2 text-xs focus-visible:outline focus-visible:outline-accent", active ? "border-accent/50 bg-accent/10 text-foreground" : "border-border text-muted hover:bg-elevated");

function WorktreeAssociations({ associations }: { associations: WorktreeAssociation[] }) {
  if (associations.length === 0) return null;
  return (
    <span className="mt-1.5 flex flex-wrap gap-1 text-[10px] text-faint">
      {associations.map(({ worktree, headMatches, evidence }) => (
        <span key={worktree.checkout} className="rounded border border-border px-1.5 py-0.5" title={worktree.checkout}>
          {worktree.branch ?? worktree.head?.slice(0, 8) ?? "checkout"}
          {headMatches === false ? " · moved" : ""}{worktree.state === "dirty" ? " · dirty" : ""}{evidence === "local-trailer" ? " · local" : ""}
        </span>
      ))}
    </span>
  );
}

function WorktreePill({ worktree }: { worktree: WorktreeView }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-[11px] text-muted" title={worktree.checkout}>
      <GitBranch className="h-3 w-3 text-info" />
      {worktree.branch ?? worktree.head?.slice(0, 8) ?? "checkout"}
      {worktree.current && <span className="text-info">serving</span>}
      {worktree.state !== "clean" && <span className="text-warn">{worktree.state}</span>}
    </span>
  );
}
