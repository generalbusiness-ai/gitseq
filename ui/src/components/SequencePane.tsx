import { useMemo } from "react";
import { BadgeCheck, CircleSlash, FileWarning, MessageSquareX, Scale, Undo2 } from "lucide-react";
import type { Act, Actor, Projection, Statement } from "../lib/api";
import { shortEvent } from "../lib/api";
import { ticketsOf, type Selection } from "../lib/store";
import { cn, kindLabel, kindTint, statusTint } from "../lib/util";
import { PaneTitle } from "./Railway";

// The sequence renders statements only; ratifications, supersessions,
// dissents, and failed attempts attach to their target as annotations, so
// the log reads as a narrative while every attempt stays visible.
export function SequencePane({
  projection,
  actors,
  highlight,
  selection,
  onSelect,
}: {
  projection?: Projection;
  actors: Actor[];
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
}) {
  const nameOf = useMemo(() => makeNamer(actors, projection), [actors, projection]);
  const statements = projection?.statements ?? [];
  const byEvent = useMemo(() => new Map(statements.map((s) => [s.event, s])), [statements]);
  const tickets = useMemo(() => ticketsOf(projection), [projection]);

  const annotations = useMemo(() => {
    const map = new Map<string, Annotation[]>();
    const attach = (target: string, note: Annotation) => {
      map.set(target, [...(map.get(target) ?? []), note]);
    };
    for (const act of projection?.acts ?? []) {
      attach(act.target, { key: act.event, act });
    }
    for (const statement of statements) {
      if (statement.kind !== "dissent") continue;
      const target = projection?.provenance[statement.event]?.[0];
      if (target && byEvent.has(target)) attach(target, { key: statement.event, dissent: statement });
    }
    return map;
  }, [projection, statements, byEvent]);

  // Dissents shown as annotations don't need their own row.
  const annotatedDissents = useMemo(() => {
    const set = new Set<string>();
    for (const notes of annotations.values())
      for (const note of notes) if (note.dissent) set.add(note.dissent.event);
    return set;
  }, [annotations]);

  const orphanActs = (projection?.acts ?? []).filter((act) => !byEvent.has(act.target));

  return (
    <section className="flex h-full min-h-0 flex-col bg-background">
      <PaneTitle icon={<Scale className="h-3.5 w-3.5" />} title="durable record" hint="signed, ordered, every attempt visible — force is the fold's judgment" />
      {projection && projection.commitments.length > 0 && (
        <div className="border-b border-border/60 px-4 py-3">
          <div className="mb-2 text-xs uppercase tracking-[0.16em] text-faint">who waits on whom</div>
          <div className="space-y-1.5">
            {projection.commitments.map((commitment) => (
              <button
                key={commitment.request + (commitment.promise ?? "")}
                onClick={() => onSelect({ kind: "event", id: commitment.report ?? commitment.promise ?? commitment.request })}
                className="flex w-full items-center gap-2 text-left text-[12px] hover:bg-elevated/60"
              >
                <span className={cn("w-20 shrink-0 font-semibold", statusTint[commitment.status] ?? "text-muted")}>
                  {commitment.status}
                </span>
                <span className="truncate text-muted">
                  {byEvent.get(commitment.request)?.text ?? shortEvent(commitment.request)}
                </span>
                {commitment.waiting_on && (
                  <span className="ml-auto shrink-0 text-xs text-faint">⏳ {nameOf(commitment.waiting_on)}</span>
                )}
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto px-2 py-2">
        {statements.length === 0 && <p className="p-4 text-sm text-faint">No agreed events yet.</p>}
        <ol className="space-y-1.5">
          {[...statements]
            .reverse()
            .filter((statement) => !annotatedDissents.has(statement.event))
            .map((statement) => (
              <StatementCard
                key={statement.event}
                statement={statement}
                ticket={tickets.get(statement.event)}
                annotations={annotations.get(statement.event) ?? []}
                nameOf={nameOf}
                bright={highlight.events.has(statement.event)}
                selected={selection?.kind === "event" && selection.id === statement.event}
                onSelect={onSelect}
              />
            ))}
          {orphanActs.map((act) => (
            <li key={act.event} className="px-2 py-1 text-xs text-faint">
              <CircleSlash className="mr-1 inline h-3 w-3" />
              {nameOf(act.actor)} — {act.reason}
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

interface Annotation {
  key: string;
  act?: Act;
  dissent?: Statement;
}

function StatementCard({
  statement,
  ticket,
  annotations,
  nameOf,
  bright,
  selected,
  onSelect,
}: {
  statement: Statement;
  ticket?: number;
  annotations: Annotation[];
  nameOf: (fingerprint: string) => string;
  bright: boolean;
  selected: boolean;
  onSelect: (selection: Selection) => void;
}) {
  const dead = statement.retired;
  return (
    <li>
      <button
        onClick={() => onSelect({ kind: "event", id: statement.event })}
        className={cn(
          "group w-full border-l-2 border-transparent px-2.5 py-1.5 text-left transition-colors hover:bg-elevated/60",
          selected && "border-accent bg-elevated/60",
          bright && !selected && "border-accent/40 bg-accent/5",
        )}
      >
        <div className="flex items-center gap-2">
          <span className={cn("shrink-0 border px-1.5 text-xs uppercase leading-4 tracking-wide", kindTint[statement.kind] ?? "text-muted border-border")}>
            {kindLabel[statement.kind] ?? statement.kind}
          </span>
          <span className={cn("truncate text-[13px]", dead ? "text-faint line-through" : "text-foreground")}>
            {statement.text}
          </span>
          {statement.ratified && <BadgeCheck className="h-3.5 w-3.5 shrink-0 text-ok" />}
          {statement.stale && !dead && (
            <span className="flex shrink-0 items-center gap-1 text-xs uppercase text-danger">
              <FileWarning className="h-3 w-3" /> stale
            </span>
          )}
          <span className="ml-auto shrink-0 font-mono text-xs text-faint" title={statement.event}>
            {ticket ? `#${ticket}` : shortEvent(statement.event)}
          </span>
        </div>
        <div className="mt-0.5 flex items-center gap-2 text-xs text-faint">
          <span>by {nameOf(statement.actor)}</span>
          {statement.body?.to && <span>→ {nameOf(statement.body.to)}</span>}
          {statement.body?.path && (
            <span className="text-teal">
              {statement.body.path}@{statement.body.commit?.slice(0, 8)}
            </span>
          )}
          {statement.body?.conditions && (
            <span className="hidden truncate group-hover:inline">✓ when: {statement.body.conditions}</span>
          )}
        </div>
        {annotations.length > 0 && (
          <div className="mt-1 space-y-0.5 border-l border-border/60 pl-2.5">
            {annotations.map((note) => (
              <AnnotationLine key={note.key} note={note} nameOf={nameOf} />
            ))}
          </div>
        )}
      </button>
    </li>
  );
}

function AnnotationLine({ note, nameOf }: { note: Annotation; nameOf: (f: string) => string }) {
  if (note.dissent) {
    return (
      <div className="flex items-start gap-1.5 text-xs text-danger">
        <MessageSquareX className="mt-0.5 h-3 w-3 shrink-0" />
        <span>
          dissent, {nameOf(note.dissent.actor)}: <span className="text-muted">{note.dissent.text}</span>
        </span>
      </div>
    );
  }
  const act = note.act!;
  if (act.verdict === "effective" && act.type === "ratify") {
    return (
      <div className="flex items-center gap-1.5 text-xs text-ok">
        <BadgeCheck className="h-3 w-3 shrink-0" />
        ratified by {nameOf(act.actor)}
      </div>
    );
  }
  if (act.verdict === "effective" && act.type === "supersede") {
    return (
      <div className="flex items-start gap-1.5 text-xs text-danger">
        <Undo2 className="mt-0.5 h-3 w-3 shrink-0" />
        <span>
          superseded by {nameOf(act.actor)}
          {act.text && <span className="text-muted"> — {act.text}</span>}
        </span>
      </div>
    );
  }
  return (
    <div className={cn("flex items-start gap-1.5 text-xs", act.verdict === "disputed" ? "text-danger" : "text-accent-deep")}>
      <CircleSlash className="mt-0.5 h-3 w-3 shrink-0" />
      <span>
        {nameOf(act.actor)} tried to {act.type} — {act.reason}
      </span>
    </div>
  );
}

function makeNamer(actors: Actor[], projection?: Projection): (fingerprint: string) => string {
  const names = new Map<string, string>(actors.map((a) => [a.fingerprint, a.name]));
  for (const statement of projection?.statements ?? []) {
    if (statement.kind === "roster" && statement.body?.actor && statement.body?.name) {
      if (!names.has(statement.body.actor)) names.set(statement.body.actor, statement.body.name);
    }
  }
  return (fingerprint: string) => names.get(fingerprint) ?? fingerprint.slice(0, 8);
}
