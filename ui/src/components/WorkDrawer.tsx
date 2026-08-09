import { useEffect, useMemo, useRef } from "react";
import { BadgeCheck, FileWarning, GitBranch, X } from "lucide-react";
import type { Artifact, Projection, WorktreeView } from "../lib/api";
import { ATTENTION_COMMITMENT_STATUSES, OPEN_COMMITMENT_STATUSES, danglingPromises, ticketsOf, type Selection, type Workroom } from "../lib/store";
import { cn, statusLabel, statusTint } from "../lib/util";
import { groupOpenWork, worktreesForCommitment, type WorktreeAssociation } from "../lib/worktrees";
import { Railway } from "./Railway";
import { EventTime } from "./EventTime";
import { Ticket, WhyStale } from "./Stream";

// Work state and the Git history live behind the header chip, separate from
// the conversational room.
export function WorkDrawer({
  workroom,
  highlight,
  selection,
  onSelect,
  onJump,
  onClose,
}: {
  workroom: Workroom;
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
  onJump: (selection: Selection) => void;
  onClose: () => void;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const projection = workroom.status?.durable.projection;
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((a) => [a.fingerprint, a.name]));
    return (fp: string) =>
      byFingerprint.get(fp) ??
      projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fp)?.body?.name ??
      fp.slice(0, 8);
  }, [workroom.actors, projection]);

  // Focus discipline: the drawer takes focus on open and hands it back on
  // close; Escape closes from anywhere inside.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    panel.current?.focus();
    return () => opener?.focus();
  }, []);
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
  };

  const jumpEvent = (event: string) => onJump({ kind: "event", id: event });

  return (
    <div className="fixed inset-0 z-40" role="dialog" aria-modal="true" aria-label="Work" onKeyDown={onKeyDown}>
      <div className="absolute inset-0 bg-background/60 backdrop-blur-[2px]" onClick={onClose} aria-hidden />
      <div
        ref={panel}
        tabIndex={-1}
        className="absolute inset-y-0 right-0 flex w-[min(26rem,92vw)] flex-col border-l border-border bg-background shadow-2xl outline-none"
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
          <h2 className="text-sm font-semibold text-foreground/90">Work</h2>
          <button onClick={onClose} aria-label="close" className="rounded p-1 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {!projection ? (
            <p className="p-4 text-sm text-faint">Loading…</p>
          ) : (
            <WorkSections
              projection={projection}
              commits={workroom.commits}
              worktrees={workroom.worktrees}
              localOffline={workroom.localOffline}
              tickets={tickets}
              nameOf={nameOf}
              onSelect={onSelect}
              onJumpEvent={jumpEvent}
            />
          )}
          <section className="border-t border-border px-4 py-3">
            <h3 className="mb-2 text-xs font-medium text-faint">History</h3>
            {workroom.graphTruncated && <p className="mb-2 text-[11px] text-warn">Showing the newest 80 commits; older local trailer associations may be absent.</p>}
            <div className="h-[26rem] overflow-hidden rounded-lg border border-border">
              <Railway
                commits={workroom.commits}
                statements={projection?.statements ?? []}
                highlight={highlight}
                selection={selection}
                onSelect={onSelect}
              />
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

// The Work sections proper: needs attention first, then what's open, what
// stands, and what's completed. Human labels; hashes ride in hover titles.
function WorkSections({
  projection,
  commits,
  worktrees,
  localOffline,
  tickets,
  nameOf,
  onSelect,
  onJumpEvent,
}: {
  projection: Projection;
  commits: Workroom["commits"];
  worktrees?: WorktreeView[];
  localOffline: boolean;
  tickets: Map<string, number>;
  nameOf: (fp: string) => string;
  onSelect: (selection: Selection) => void;
  onJumpEvent: (event: string) => void;
}) {
  const staleArtifacts = projection.artifacts.filter((a) => a.stale);
  const attention = projection.commitments.filter((c) => ATTENTION_COMMITMENT_STATUSES.includes(c.status));
  const dangling = danglingPromises(projection);
  const open = projection.commitments.filter((c) => OPEN_COMMITMENT_STATUSES.includes(c.status));
  const workGroups = groupOpenWork(open);
  const standing = projection.statements.filter((s) => s.kind === "propose" && s.ratified && !s.retired && !s.stale);
  const done = projection.commitments.filter((c) => c.status === "satisfied");
  const currentGroups = groupArtifacts(projection.artifacts.filter((a) => !a.stale));
  const requestText = (event: string) => projection.statements.find((s) => s.event === event)?.text ?? event;
  const timestampByEvent = new Map([
    ...projection.statements.map((event) => [event.event, event.timestamp] as const),
    ...projection.acts.map((event) => [event.event, event.timestamp] as const),
  ]);
  const needsAttention = staleArtifacts.length + attention.length + dangling.length > 0;

  return (
    <div className="space-y-5 px-4 py-4">
      <section>
        <SectionTitle icon={<GitBranch className="h-3.5 w-3.5 text-info" />} title="Local checkouts" />
        <p className="mb-2 text-[11px] leading-4 text-faint">Read-only state from this machine; it is not part of the durable workroom.</p>
        {localOffline && <Empty>Local checkout state unavailable. Durable work is unchanged.</Empty>}
        {!localOffline && !worktrees && <Empty>Reading local checkout state…</Empty>}
        {worktrees?.length === 0 && <Empty>No checkouts found.</Empty>}
        {worktrees?.map((worktree, index) => <WorktreePill key={`${worktree.checkout}:${worktree.head}:${index}`} worktree={worktree} />)}
      </section>
      <section>
        <SectionTitle icon={<FileWarning className="h-3.5 w-3.5 text-danger" />} title="Needs attention" />
        {!needsAttention && <Empty>All clear.</Empty>}
        {staleArtifacts.map((artifact) => (
          <div key={artifact.event} className="rounded-md px-2 py-1.5 hover:bg-elevated/60">
            <Row onClick={() => onSelect({ kind: "commit", id: artifact.commit })} bare>
              <span className="w-14 shrink-0 text-xs font-semibold uppercase text-danger">stale</span>
              <span className="truncate" title={`${artifact.path}@${artifact.commit}`}>
                {artifactLabel(artifact.path)}
              </span>
              <span className="ml-auto flex shrink-0 items-center gap-2">
                <EventTime timestamp={timestampByEvent.get(artifact.event)} />
                <Ticket ticket={tickets.get(artifact.event)} event={artifact.event} onSelect={() => onJumpEvent(artifact.event)} />
              </span>
            </Row>
            <WhyStale event={artifact.event} projection={projection} tickets={tickets} nameOf={nameOf} onJumpTo={onJumpEvent} />
          </div>
        ))}
        {attention.map((commitment) => {
          const anchor = commitment.report ?? commitment.promise ?? commitment.request;
          const associations = worktreesForCommitment(commitment, projection, commits, worktrees ?? []);
          return (
            <div key={commitment.request + (commitment.promise ?? "")} className="rounded-md px-2 py-1.5 hover:bg-elevated/60">
              <Row onClick={() => onJumpEvent(commitment.request)} bare>
                <span className={cn("w-16 shrink-0 text-xs font-semibold", statusTint[commitment.status])}>{statusLabel(commitment.status)}</span>
                <span className="truncate text-muted">{requestText(commitment.request)}</span>
                <span className="ml-auto flex shrink-0 items-center gap-2">
                  <EventTime timestamp={timestampByEvent.get(commitment.request)} />
                  <Ticket ticket={tickets.get(commitment.request)} event={commitment.request} onSelect={() => onJumpEvent(commitment.request)} />
                </span>
              </Row>
              <WorktreeAssociations associations={associations} />
              {commitment.status === "stale" && (
                <WhyStale event={anchor} projection={projection} tickets={tickets} nameOf={nameOf} onJumpTo={onJumpEvent} />
              )}
            </div>
          );
        })}
        {dangling.map((promise) => (
          <Row key={promise.event} onClick={() => onJumpEvent(promise.event)}>
            <span className="w-16 shrink-0 text-xs font-semibold text-danger">unlinked</span>
            <span className="truncate text-muted" title="unlinked work item">
              {promise.text}
            </span>
            <span className="ml-auto flex shrink-0 items-center gap-2">
              <EventTime timestamp={promise.timestamp} />
              <Ticket ticket={tickets.get(promise.event)} event={promise.event} onSelect={() => onJumpEvent(promise.event)} />
            </span>
          </Row>
        ))}
      </section>
      {[
        { title: "Available", items: workGroups.available, empty: "Nothing available." },
        { title: "In progress", items: workGroups.inProgress, empty: "Nothing in progress." },
        { title: "Review", items: workGroups.review, empty: "Nothing ready for review." },
      ].map((group) => (
        <section key={group.title}>
          <SectionTitle title={`${group.title} (${group.items.length})`} />
          {group.items.length === 0 && <Empty>{group.empty}</Empty>}
          {group.items.map((commitment) => {
            const associations = worktreesForCommitment(commitment, projection, commits, worktrees ?? []);
            return (
              <div key={commitment.request + (commitment.promise ?? "")} className="rounded-md px-2 py-1 hover:bg-elevated/60">
                <Row onClick={() => onJumpEvent(commitment.request)} bare>
                  <span className={cn("w-16 shrink-0 text-xs font-semibold", statusTint[commitment.status])}>{statusLabel(commitment.status)}</span>
                  <span className="truncate text-muted">{requestText(commitment.request)}</span>
                  <span className="ml-auto flex shrink-0 items-center gap-2">
                    {commitment.waiting_on && <span className="text-xs text-faint">⏳ {nameOf(commitment.waiting_on)}</span>}
                    <EventTime timestamp={timestampByEvent.get(commitment.request)} />
                    <Ticket ticket={tickets.get(commitment.request)} event={commitment.request} onSelect={() => onJumpEvent(commitment.request)} />
                  </span>
                </Row>
                <WorktreeAssociations associations={associations} />
              </div>
            );
          })}
        </section>
      ))}
      <section>
        <SectionTitle icon={<BadgeCheck className="h-3.5 w-3.5 text-ok" />} title="Decisions" />
        {standing.length === 0 && <Empty>Nothing standing.</Empty>}
        {standing.map((decision) => (
          <Row key={decision.event} onClick={() => onJumpEvent(decision.event)}>
            <span className="truncate font-serif text-[13px]">{decision.text}</span>
            <span className="ml-auto flex shrink-0 items-center gap-2">
              <EventTime timestamp={decision.timestamp} />
              <Ticket ticket={tickets.get(decision.event)} event={decision.event} onSelect={() => onJumpEvent(decision.event)} />
            </span>
          </Row>
        ))}
      </section>
      <section>
        <SectionTitle title="Completed" />
        {done.length === 0 && currentGroups.length === 0 && <Empty>Nothing yet.</Empty>}
        {done.map((commitment) => (
          <Row key={commitment.request} onClick={() => onJumpEvent(commitment.report ?? commitment.request)}>
            <BadgeCheck className="h-3.5 w-3.5 shrink-0 text-ok" />
            <span className="truncate text-muted">{requestText(commitment.request)}</span>
            <span className="ml-auto flex shrink-0 items-center gap-2">
              <EventTime timestamp={timestampByEvent.get(commitment.request)} />
              <Ticket ticket={tickets.get(commitment.request)} event={commitment.request} onSelect={() => onJumpEvent(commitment.request)} />
            </span>
          </Row>
        ))}
        {currentGroups.map((group) => (
          <div key={group.path}>
            {group.artifacts.length > 1 && (
              <p className="px-2 pt-1 text-xs font-semibold text-danger">unresolved: {group.artifacts.length} current</p>
            )}
            {group.artifacts.map((artifact) => (
              <Row key={artifact.event} onClick={() => onSelect({ kind: "commit", id: artifact.commit })}>
                <span className="w-14 shrink-0 text-xs font-semibold uppercase text-ok">current</span>
                <span className="truncate" title={`${artifact.path}@${artifact.commit}`}>
                  {artifactLabel(artifact.path)}
                </span>
                <span className="ml-auto flex shrink-0 items-center gap-2">
                  <EventTime timestamp={timestampByEvent.get(artifact.event)} />
                  <Ticket ticket={tickets.get(artifact.event)} event={artifact.event} onSelect={() => onJumpEvent(artifact.event)} />
                </span>
              </Row>
            ))}
          </div>
        ))}
      </section>
    </div>
  );
}

function WorktreeAssociations({ associations }: { associations: WorktreeAssociation[] }) {
  if (associations.length === 0) return null;
  return (
    <div className="ml-16 mt-1 space-y-1 border-l border-border pl-2">
      {associations.map((association, index) => (
        <WorktreePill
          key={`${association.worktree.checkout}:${association.worktree.head}:${index}`}
          worktree={association.worktree}
          expectedHead={association.expectedHead}
          expectedHeads={association.expectedHeads}
          headMatches={association.headMatches}
          evidence={association.evidence}
          compact
        />
      ))}
    </div>
  );
}

function WorktreePill({
  worktree,
  expectedHead,
  expectedHeads,
  headMatches,
  evidence,
  compact,
}: {
  worktree: WorktreeView;
  expectedHead?: string;
  expectedHeads?: string[];
  headMatches?: boolean;
  evidence?: WorktreeAssociation["evidence"];
  compact?: boolean;
}) {
  const branch = worktree.detached ? "detached" : worktree.state === "bare" ? "bare" : worktree.branch || "unborn";
  const head = worktree.head?.slice(0, 8) || "no HEAD";
  const moved = headMatches === false;
  const title = [
    `local checkout ${worktree.checkout}`,
    `branch ${branch}`,
    `HEAD ${worktree.head || "unborn"}`,
    worktree.state,
    expectedHead ? `review head ${expectedHead}` : "",
    expectedHeads && expectedHeads.length > 1 ? `review heads ${expectedHeads.join(", ")}` : "",
    "local only — not durable workroom state",
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <div
      title={title}
      className={cn(
        "flex min-w-0 items-center gap-1.5 rounded border border-border bg-surface/70 text-[11px] text-muted",
        compact ? "px-1.5 py-0.5" : "mb-1 px-2 py-1",
      )}
    >
      <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", worktree.state === "clean" ? "bg-ok" : worktree.state === "dirty" ? "bg-warn" : "bg-faint")} />
      <span className="truncate font-medium text-foreground/80">{worktree.checkout}</span>
      <span className="truncate">{branch}</span>
      <code className="shrink-0 text-faint">@{head}</code>
      <span className="shrink-0 text-faint">local</span>
      {evidence === "local-trailer" && <span className="shrink-0 text-warn">unverified trailer</span>}
      {worktree.current && <span className="shrink-0 text-info">serving</span>}
      {worktree.state === "dirty" && <span className="shrink-0 text-warn">dirty</span>}
      {!["clean", "dirty"].includes(worktree.state) && <span className="shrink-0 text-warn">{worktree.state}</span>}
      {moved && <span className="shrink-0 text-danger">moved</span>}
    </div>
  );
}

// Human label for an artifact path; "." means the repository itself.
function artifactLabel(path: string): string {
  return path === "." ? "this repository" : path;
}

function groupArtifacts(artifacts: Artifact[]): { path: string; artifacts: Artifact[] }[] {
  const byPath = new Map<string, Artifact[]>();
  for (const artifact of artifacts) byPath.set(artifact.path, [...(byPath.get(artifact.path) ?? []), artifact]);
  return [...byPath.entries()].map(([path, list]) => ({ path, artifacts: list }));
}

function SectionTitle({ icon, title }: { icon?: React.ReactNode; title: string }) {
  return (
    <h3 className="mb-1.5 flex items-center gap-1.5 text-xs font-medium uppercase tracking-[0.14em] text-faint">
      {icon}
      {title}
    </h3>
  );
}

// Rows contain inner buttons (tickets), so the row itself is a keyboard-
// activatable div rather than a nested <button>.
function Row({ children, onClick, bare }: { children: React.ReactNode; onClick: () => void; bare?: boolean }) {
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      className={cn(
        "flex w-full cursor-pointer items-center gap-2 rounded-md text-left text-xs focus-visible:outline focus-visible:outline-accent",
        bare ? "px-0 py-0" : "px-2 py-1.5 hover:bg-elevated/60",
      )}
    >
      {children}
    </div>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="px-2 py-1 text-xs italic text-faint">{children}</p>;
}
