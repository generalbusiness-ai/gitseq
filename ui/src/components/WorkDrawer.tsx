import { useCallback, useEffect, useMemo, useState } from "react";
import { Bell, BellOff, BookOpen, Columns3, GitBranch, List, Search } from "lucide-react";
import type { FrameView, Projection, Vocabulary, WorktreeView } from "../lib/api";
import type { Session } from "../lib/session";
import { emptyPersonalWorkMemory, followWorkTopic, loadPersonalWorkMemory, savePersonalWorkMemory, viewWorkTopic, type PersonalWorkMemory } from "../lib/memory";
import { ticketsOf, type Selection, type Workroom } from "../lib/store";
import {
  attentionItemCounts,
  buildWorkProjection,
  filterPersonalWorkProjection,
  filterWorkProjection,
  otherWorkAttentionClause,
  topicChangeSince,
  workCommitmentCounts,
  workItemNeedsAction,
  type WorkFilters,
  type WorkAttentionItem,
  type WorkItem,
  type WorkLane,
  type PersonalWorkView,
  type WorkTopic,
  type WorkTopicChange,
} from "../lib/work";
import { worktreesForCommitment, type WorktreeAssociation } from "../lib/worktrees";
import { RebuildNotice } from "./RebuildNotice";
import { temporaryDiscussionCounts, temporaryDiscussionLabel, type TemporaryDiscussionCount } from "../lib/interaction";
import { cn, commitmentRelationship, statusLabel, statusTint } from "../lib/util";
import { EventTime } from "./EventTime";
import { Railway } from "./Railway";
import { presentActors, type PresentActor } from "../lib/interaction";

type Presentation = "list" | "board";

// Work is a retrieval view over the durable ledger. It never mutates status:
// lifecycle transitions still happen through signed semantic acts.
export function WorkView({
  workroom,
  session,
  frames = [],
  highlight,
  selection,
  onSelect,
  onOpenThread,
  initialPresentation = "list",
  initialPersonalView = "all",
  initialPersonalMemory,
}: {
  workroom: Workroom;
  session: Session;
  frames?: FrameView[];
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
  onOpenThread: (event: string) => void;
  initialPresentation?: Presentation;
  initialPersonalView?: PersonalWorkView;
  initialPersonalMemory?: PersonalWorkMemory;
}) {
  const projection = workroom.status?.durable.projection;
  const vocabulary = workroom.status?.durable.vocabulary;
  const [presentation, setPresentation] = useState<Presentation>(initialPresentation);
  const [filters, setFilters] = useState<WorkFilters>({ active: true, attention: true, closed: false });
  const [personalView, setPersonalView] = useState<PersonalWorkView>(initialPersonalView);
  const work = useMemo(() => (projection ? buildWorkProjection(projection) : undefined), [projection]);
  const counts = useMemo(() => workCommitmentCounts(projection), [projection]);
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const durableEvents = useMemo(
    () => new Set((projection?.statements ?? []).map((statement) => statement.event)),
    [projection],
  );
  const temporaryCounts = useMemo(
    () => temporaryDiscussionCounts(frames, durableEvents),
    [frames, durableEvents],
  );
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((actor) => [actor.fingerprint, actor.name]));
    return (fingerprint: string) =>
      byFingerprint.get(fingerprint) ??
      projection?.statements.find((statement) => statement.kind === "roster" && statement.body?.actor === fingerprint)?.body?.name ??
      fingerprint.slice(0, 8);
  }, [projection, workroom.actors]);
  // A name that resolves to more than one configured actor is not enough to
  // open either actor's personal state. This fails closed instead of showing
  // another identity's follows or watermarks.
  const matchingActors = workroom.actors.filter((actor) => actor.name === session.actor);
  const me = matchingActors.length === 1 ? matchingActors[0].fingerprint : undefined;
  const genesis = workroom.status?.durable.genesis ?? "";
  const personalScope = genesis && me ? `${genesis}\u0000${me}` : "";
  const [personalState, setPersonalState] = useState<{ scope: string; memory: PersonalWorkMemory }>(() =>
    initialPersonalMemory && personalScope
      ? { scope: personalScope, memory: initialPersonalMemory }
      : { scope: "", memory: emptyPersonalWorkMemory() },
  );
  useEffect(() => {
    setPersonalState({ scope: personalScope, memory: loadPersonalWorkMemory(genesis, me ?? "") });
  }, [genesis, me, personalScope]);
  const personalMemory = personalState.scope === personalScope ? personalState.memory : emptyPersonalWorkMemory();
  const remember = useCallback((update: (current: PersonalWorkMemory) => PersonalWorkMemory) => {
    if (!personalScope) return;
    setPersonalState((current) => {
      const memory = current.scope === personalScope ? current.memory : loadPersonalWorkMemory(genesis, me ?? "");
      const next = update(memory);
      savePersonalWorkMemory(genesis, me ?? "", next);
      return { scope: personalScope, memory: next };
    });
  }, [genesis, me, personalScope]);
  const followed = useMemo(() => new Set(personalMemory.followed), [personalMemory.followed]);
  const lifecycleVisible = useMemo(() => (work ? filterWorkProjection(work, filters) : undefined), [work, filters]);
  // Personal views select from the complete Work projection. Lifecycle
  // filters remain intact and resume when "All work" is selected; a closed
  // unread change or stale responsibility must not disappear merely because
  // the default lifecycle view omits it.
  const visible = useMemo(
    () => work ? filterPersonalWorkProjection(personalView === "all" ? lifecycleVisible! : work, personalView, me, followed, personalMemory.viewed) : undefined,
    [work, lifecycleVisible, personalView, me, followed, personalMemory.viewed],
  );
  const changes = useMemo(() => new Map(
    (work?.topics ?? []).flatMap((topic) => {
      const change = topicChangeSince(topic, me, followed, personalMemory.viewed[topic.event] ?? -1);
      return change ? [[topic.event, change] as const] : [];
    }),
  ), [work, me, followed, personalMemory.viewed]);
  const needsCount = work?.topics.reduce((sum, topic) => sum + topic.items.filter((item) => workItemNeedsAction(item, me)).length, 0) ?? 0;
  const unreadCount = changes.size;
  const followingCount = work?.topics.filter((topic) => followed.has(topic.event)).length ?? 0;
  const itemCount = visible?.topics.reduce((sum, topic) => sum + topic.items.length, 0) ?? 0;
  const focusedActors = useMemo(
    () => presentActors(workroom.status?.live.presence, workroom.status?.live.activity),
    [workroom.status?.live.presence, workroom.status?.live.activity],
  );

  const openWorkItem = (event: string, topic: WorkTopic) => {
    remember((current) => viewWorkTopic(current, topic.event, topic.latestOrder));
    onSelect({ kind: "event", id: event });
    onOpenThread(event);
  };
  const toggleFollowing = (topic: WorkTopic) =>
    remember((current) => followWorkTopic(current, topic.event, !current.followed.includes(topic.event), topic.latestOrder));

  const updateFlag = (key: "active" | "attention" | "closed", value: boolean) =>
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
            <FilterCheck
              label="Active"
              description="All actors: available (open), in progress (promised), and review (reported)."
              checked={filters.active}
              count={counts.active}
              onChange={(value) => updateFlag("active", value)}
            />
            <FilterCheck
              label="Attention"
              description={`Stale or disputed, which is a qualifier rather than a status: ${counts.attention} of ${counts.total} commitments, most of them also counted as active or closed. Separately, ${otherWorkAttentionClause(attentionItemCounts(work?.attention ?? []))}.`}
              checked={filters.attention}
              count={counts.attention}
              tone="danger"
              onChange={(value) => updateFlag("attention", value)}
            />
            <FilterCheck
              label="Closed"
              description="All actors: satisfied, withdrawn, cancelled or reneged."
              checked={filters.closed}
              count={counts.closed}
              onChange={(value) => updateFlag("closed", value)}
            />
          </fieldset>
          {me && <fieldset className="flex items-center gap-1.5" aria-label="Personal work filters">
            <legend className="sr-only">Personal work filters</legend>
            <PersonalFilter label="Needs my action" count={needsCount} active={personalView === "needs"} onClick={() => setPersonalView(personalView === "needs" ? "all" : "needs")} />
            <PersonalFilter label="Unread" count={unreadCount} active={personalView === "unread"} onClick={() => setPersonalView(personalView === "unread" ? "all" : "unread")} />
            <PersonalFilter label="Following" count={followingCount} active={personalView === "following"} onClick={() => setPersonalView(personalView === "following" ? "all" : "following")} />
          </fieldset>}
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
          {personalView !== "all" ? ` · ${personalView === "needs" ? "needs my action" : personalView}` : ""}
        </p>
        <p className="mx-auto mt-1 max-w-7xl text-[10px] text-faint">
          Active covers every actor's available (open), in-progress (promised), and review (reported) work. Read positions and follows are private to this browser and actor; they do not sync across devices. Needs my action comes only from active durable responsibility: lifecycle-stale rows are excluded, while stale-qualified reports still wait on their requester.
        </p>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6">
        {!projection || !visible ? (
          // RebuildNotice owns both mutually exclusive waiting states. A cold
          // rebuild replaces the generic loading message; a warm brief read
          // keeps it.
          <RebuildNotice />
        ) : visible.topics.length === 0 && visible.attention.length === 0 ? (
          <div className="mx-auto max-w-xl py-16 text-center">
            <p className="font-serif text-lg text-foreground/90">No work matches this view.</p>
            <p className="mt-1 text-xs text-faint">Try another author, clear the search, or include another lifecycle.</p>
          </div>
        ) : (
          <>
            <OtherAttention items={visible.attention} tickets={tickets} nameOf={nameOf} onSelect={onSelect} onOpenThread={onOpenThread} />
            {visible.topics.length > 0 && (presentation === "list" ? (
              <TopicList topics={visible.topics} projection={projection} tickets={tickets} temporaryCounts={temporaryCounts} commits={workroom.commits} worktrees={workroom.worktrees ?? []} nameOf={nameOf} canPersonalize={Boolean(me)} followed={followed} changes={changes} focusedActors={focusedActors} onToggleFollowing={toggleFollowing} onOpenWorkItem={openWorkItem} />
            ) : (
              <WorkBoard topics={visible.topics} filters={personalView === "all" ? filters : { active: true, attention: true, closed: true }} projection={projection} tickets={tickets} temporaryCounts={temporaryCounts} commits={workroom.commits} worktrees={workroom.worktrees ?? []} nameOf={nameOf} canPersonalize={Boolean(me)} followed={followed} changes={changes} focusedActors={focusedActors} onToggleFollowing={toggleFollowing} onOpenWorkItem={openWorkItem} />
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

        <VocabularyPanel vocabulary={vocabulary} />
      </div>
    </div>
  );
}

// The kinds the room currently interprets, each with the rules it is judged
// against, and whether the definition came with the room or was declared in
// the log.
function VocabularyPanel({ vocabulary }: { vocabulary?: Vocabulary }) {
  if (!vocabulary) return null;
  const unbound = vocabulary.binding.status === "unbound";
  const uninterpretable = vocabulary.binding.status === "uninterpretable";
  const transitionCount = vocabulary.binding.transitions.length;
  const transitionLabel = `${transitionCount} fold ${transitionCount === 1 ? "transition" : "transitions"}`;
  return (
    <details className="mx-auto mt-3 max-w-7xl rounded-lg border border-border bg-surface/30">
      <summary className="cursor-pointer px-3 py-2 text-xs font-medium text-muted focus-visible:outline focus-visible:outline-accent">
        <BookOpen aria-hidden className="mr-1.5 inline h-3.5 w-3.5" />
        Vocabulary ({vocabulary.definitions.length})
        {unbound && <span className="ml-2 font-normal text-faint">starter kinds only</span>}
        {uninterpretable && <span className="ml-2 font-normal text-faint">interpretation stopped after {transitionLabel}</span>}
      </summary>
      <div className="space-y-1 border-t border-border p-3">
        {/* The reach of this room's vocabulary is a standing property, stated
            in the same register as any other limit — not an incident. Until a
            seed is ratified and a prefix bound, these definitions are all the
            room reads, and an act naming anything else says so on itself. */}
        {unbound && (
          <p className="rounded-md border border-border/70 bg-surface/40 px-2 py-1.5 leading-relaxed text-muted">
            This room reads only the kinds listed here. Its fold is{" "}
            <span className="font-medium text-foreground/90">{vocabulary.binding.status}</span>
            {vocabulary.binding.reason ? <> — {vocabulary.binding.reason}</> : null}, so no declared vocabulary extends
            it yet. An act naming a kind outside this list is kept and marked on the act itself.
          </p>
        )}
        {uninterpretable && (
          <p className="rounded-md border border-border/70 bg-surface/40 px-2 py-1.5 leading-relaxed text-muted">
            This reader reached {transitionCount === 1 ? "1 activated fold transition" : `${transitionCount} activated fold transitions`} but cannot
            interpret records beyond it.
            {vocabulary.binding.reason ? <> Reason: <span className="font-medium text-foreground/90">{vocabulary.binding.reason}</span>.</> : null}
            {" "}The kinds listed here are the definitions this reader established before that transition; declared kinds remain declared.
          </p>
        )}
        {vocabulary.definitions.map((definition) => (
          <details key={definition.name} className="rounded-md border border-border/70 px-2 py-1.5 text-xs">
            <summary className="cursor-pointer list-none font-medium text-foreground/90">
              <span>{definition.name}</span>
              <span className="ml-2 font-normal text-faint">{definition.render} · {definition.source === "starter" ? "starter" : "declared"}</span>
            </summary>
            <p className="mt-1.5 leading-relaxed text-muted">{definition.guidance}</p>
            <dl className="mt-1 grid grid-cols-[5rem_1fr] gap-x-2 text-faint">
              <dt>satisfier</dt><dd>{definition.satisfier}</dd>
              <dt>staleness</dt><dd>{definition.staleness}</dd>
              <dt>lifecycle</dt><dd>{definition.lifecycle}</dd>
            </dl>
          </details>
        ))}
      </div>
    </details>
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
          <div className="flex items-start gap-2">
          <button type="button" onClick={() => props.onOpenWorkItem(topic.event, topic)} className="block min-w-0 flex-1 rounded text-left focus-visible:outline focus-visible:outline-accent">
            <div className="flex flex-wrap items-start gap-x-3 gap-y-1">
              <h2 className="min-w-0 flex-1 font-serif text-[15px] font-semibold leading-5 text-foreground/95">{topic.title}</h2>
              <TopicCounts topic={topic} />
            </div>
            <p className="mt-1 text-[11px] text-faint">
              written by {props.nameOf(topic.author)}
              {topic.titleLabel && <> · title by {props.nameOf(topic.titleLabel.actor)}</>}
              {" · "}latest activity by {props.nameOf(topic.latestActor)} <EventTime timestamp={topic.latestTimestamp} />
            </p>
            {topic.aliases.length > 0 && <p className="mt-1 flex flex-wrap gap-1" aria-label="topic aliases">
              {topic.aliases.map((alias) => <span key={alias.event} data-topic-alias={alias.value} title={`named by ${props.nameOf(alias.actor)}`} className="rounded border border-border px-1.5 py-0.5 font-mono text-[10px] text-muted">{alias.value}</span>)}
            </p>}
            <TopicChange change={props.changes.get(topic.event)} nameOf={props.nameOf} />
          </button>
          {props.canPersonalize && <TopicFollowButton following={props.followed.has(topic.event)} onClick={() => props.onToggleFollowing(topic)} />}
          </div>
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
  const laneEnabled = (lane?: WorkLane) => lane === "closed" ? filters.closed : Boolean(lane && filters.active);
  const attentionOnly = cards.filter(({ item }) => item.attention && !laneEnabled(item.lane));
  const lanes: { id: WorkLane; title: string }[] = [
    ...(filters.active ? [
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
  temporaryCounts: Map<string, TemporaryDiscussionCount>;
  commits: Workroom["commits"];
  worktrees: WorktreeView[];
  nameOf: (fingerprint: string) => string;
  canPersonalize: boolean;
  followed: ReadonlySet<string>;
  changes: ReadonlyMap<string, WorkTopicChange>;
  focusedActors: PresentActor[];
  onToggleFollowing: (topic: WorkTopic) => void;
  onOpenWorkItem: (event: string, topic: WorkTopic) => void;
}

function WorkItemRow({ item, topic, projection, tickets, temporaryCounts, commits, worktrees, nameOf, focusedActors, onOpenWorkItem }: WorkRenderProps & { item: WorkItem; topic: WorkTopic }) {
  const associations = worktreesForCommitment(item.commitment, projection, commits, worktrees);
  const relationship = commitmentRelationship(item.commitment, nameOf);
  return (
    <button type="button" onClick={() => onOpenWorkItem(item.request.event, topic)} className="w-full rounded-md px-2 py-1.5 text-left hover:bg-elevated/70 focus-visible:outline focus-visible:outline-accent">
      <div className="flex items-start gap-2 text-xs">
        <StatusBadge item={item} />
        <FocusActors actors={focusedActors} event={item.request.event} />
        <span className="min-w-0 flex-1 text-foreground/85">{item.request.text}</span>
        {relationship && <span className="shrink-0 text-faint">{relationship}</span>}
        <TemporaryDiscussionSignal summary={temporaryCounts.get(item.request.event)} />
        <span className="shrink-0 font-mono text-[11px] text-faint" title={item.request.event}>#{tickets.get(item.request.event) ?? "?"}</span>
      </div>
      <WorktreeAssociations associations={associations} />
    </button>
  );
}

function BoardCard({ item, topic, projection, tickets, temporaryCounts, commits, worktrees, nameOf, canPersonalize, followed, changes, focusedActors, onToggleFollowing, onOpenWorkItem }: WorkRenderProps & { item: WorkItem; topic: WorkTopic }) {
  const associations = worktreesForCommitment(item.commitment, projection, commits, worktrees);
  const relationship = commitmentRelationship(item.commitment, nameOf);
  return (
    <article className="rounded-md border border-border bg-card px-3 py-2.5 shadow-sm hover:border-accent/40 hover:bg-elevated/70">
      <div className="flex items-center justify-between gap-2">
        <StatusBadge item={item} />
        <FocusActors actors={focusedActors} event={item.request.event} />
        {canPersonalize && <TopicFollowButton following={followed.has(topic.event)} onClick={() => onToggleFollowing(topic)} compact />}
        <span className="font-mono text-[11px] text-faint" title={item.request.event}>#{tickets.get(item.request.event) ?? "?"}</span>
      </div>
      <button type="button" onClick={() => onOpenWorkItem(item.request.event, topic)} className="block w-full rounded text-left focus-visible:outline focus-visible:outline-accent">
      <p className="mt-1.5 text-sm leading-5 text-foreground/90">{item.request.text}</p>
      {item.request.event !== topic.event && <p className="mt-1 line-clamp-2 text-[11px] text-faint">in {topic.title}</p>}
      <p className="mt-1.5 text-[11px] text-faint">
        asked by {nameOf(item.request.actor)}{relationship && <> · {relationship}</>}
      </p>
      <TopicChange change={changes.get(topic.event)} nameOf={nameOf} />
      <TemporaryDiscussionSignal summary={temporaryCounts.get(item.request.event)} />
      <WorktreeAssociations associations={associations} />
      </button>
    </article>
  );
}

function FocusActors({ actors, event }: { actors: PresentActor[]; event: string }) {
  const focused = actors.filter((actor) => actor.focus.includes(event));
  if (focused.length === 0) return null;
  return (
    <span className="flex shrink-0 items-center gap-1" aria-label={`Focused here: ${focused.map((actor) => `${actor.name} (${actor.status})`).join(", ")}`}>
      {focused.map((actor) => (
        <span key={actor.label} title={`${actor.name} — ${actor.status}${actor.note ? ` — ${actor.note}` : ""}`} className={cn("rounded border px-1 text-[10px] font-medium", actor.status === "blocked" ? "border-danger/50 text-danger" : actor.status === "waiting" ? "border-warn/50 text-warn" : "border-info/40 text-info")}>
          {actor.name} · {actor.status}
        </span>
      ))}
    </span>
  );
}

function TopicChange({ change, nameOf }: { change?: WorkTopicChange; nameOf: (fingerprint: string) => string }) {
  if (!change) return null;
  return (
    <p className="mt-1 text-[11px] font-medium text-accent" aria-label={`Changed since viewed by ${nameOf(change.actor)}, status ${statusLabel(change.status)}`}>
      changed · {nameOf(change.actor)} <EventTime timestamp={change.timestamp} /> · {statusLabel(change.status)}
    </p>
  );
}

function TopicFollowButton({ following, onClick, compact = false }: { following: boolean; onClick: () => void; compact?: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={following}
      aria-label={following ? "unfollow topic" : "follow topic"}
      title={following ? "Unfollow topic" : "Follow topic"}
      onClick={onClick}
      className={cn("shrink-0 rounded-md border border-border text-xs focus-visible:outline focus-visible:outline-accent", compact ? "p-1" : "flex h-7 items-center gap-1 px-2", following ? "bg-accent/10 text-accent" : "text-faint hover:bg-elevated hover:text-muted")}
    >
      {following ? <BellOff className="h-3 w-3" /> : <Bell className="h-3 w-3" />}
      {!compact && (following ? "Following" : "Follow")}
    </button>
  );
}

function TemporaryDiscussionSignal({ summary }: { summary?: TemporaryDiscussionCount }) {
  const label = temporaryDiscussionLabel(summary);
  if (!label) return null;
  return (
    <span aria-label={`${label} messages in temporary discussion`} className="mt-1 inline-flex rounded border border-info/30 px-1.5 py-0.5 text-[10px] font-medium text-info">
      {label}
    </span>
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
      {topic.activeCount > 0 && <span className="rounded border border-info/35 px-1.5 py-0.5 text-info">{topic.activeCount} active</span>}
      {topic.closedCount > 0 && <span className="rounded border border-border px-1.5 py-0.5 text-faint">{topic.closedCount} closed</span>}
    </span>
  );
}

function FilterCheck({ label, description, checked, count, tone, onChange }: { label: string; description?: string; checked: boolean; count?: number; tone?: "danger"; onChange: (checked: boolean) => void }) {
  return (
    <label title={description} className={cn("flex h-8 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs", checked ? "border-accent/50 bg-accent/10 text-foreground" : "border-border text-faint hover:text-muted", tone === "danger" && checked && "border-danger/50 bg-danger/10")}>
      <input type="checkbox" aria-label={description ? `${label}. ${description}` : label} checked={checked} onChange={(event) => onChange(event.target.checked)} className="h-3.5 w-3.5 accent-[var(--color-accent)]" />
      {label}<span className={cn("font-mono text-[10px]", tone === "danger" && (count ?? 0) > 0 && "text-danger")}>{count ?? 0}</span>
    </label>
  );
}

function PersonalFilter({ label, count, active, onClick }: { label: string; count: number; active: boolean; onClick: () => void }) {
  return (
    <button type="button" aria-pressed={active} onClick={onClick} className={cn(controlClass(active), "flex items-center gap-1.5")}>
      {label}<span className="font-mono text-[10px]">{count}</span>
    </button>
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
