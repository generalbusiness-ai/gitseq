import { useEffect, useMemo, useRef, useState } from "react";
import { BadgeCheck, CircleSlash, CornerUpLeft, FileWarning, MessageSquareX, Stamp, Undo2 } from "lucide-react";
import { api, decodeFrame, type Act, type Frame, type FrameView, type Statement } from "../lib/api";
import { shortEvent } from "../lib/api";
import type { Workroom } from "../lib/store";
import type { Selection } from "../lib/store";
import type { Session } from "../lib/session";
import { actorTint, cn, fingerprintOfKey, kindTint, statusTint } from "../lib/util";
import type { ComposerContext } from "./Composer";

interface StreamItem {
  key: string;
  order: number;
  statement?: Statement;
  frame?: FrameView;
}

// One stream: durable acts as set-in-type cards, ephemeral talk as light
// lines, interleaved by arrival. On first load history reads as the agreed
// narrative followed by the live conversation.
export function Stream({
  workroom,
  session,
  highlight,
  selection,
  onSelect,
  composer,
  onComposer,
}: {
  workroom: Workroom;
  session: Session;
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
  composer: ComposerContext;
  onComposer: (context: ComposerContext) => void;
}) {
  const projection = workroom.status?.durable.projection;
  const conversations = workroom.status?.live.conversations ?? [];
  const livePosition = workroom.status?.live.cursor.position ?? 0;
  const [frames, setFrames] = useState<FrameView[]>([]);
  const [supersedeTarget, setSupersedeTarget] = useState<string>();
  const [supersedeReason, setSupersedeReason] = useState("");
  const [actError, setActError] = useState<string>();
  const orderRef = useRef(new Map<string, number>());
  const counterRef = useRef(0);
  const scroller = useRef<HTMLDivElement>(null);

  const byFingerprint = useMemo(
    () => new Map(workroom.actors.map((a) => [a.fingerprint, a.name])),
    [workroom.actors],
  );
  const nameOf = (fingerprint: string) => {
    const named = byFingerprint.get(fingerprint);
    if (named) return named;
    const roster = projection?.statements.find(
      (s) => s.kind === "roster" && s.body?.actor === fingerprint && s.body?.name,
    );
    return roster?.body?.name ?? fingerprint.slice(0, 8);
  };

  useEffect(() => {
    let stopped = false;
    Promise.all(
      conversations.map((conversation) =>
        api
          .frames(conversation)
          .then(async (raw: Frame[]) =>
            Promise.all(
              raw.map(async (frame) => {
                const view = decodeFrame(frame, workroom.actors);
                const fingerprint = await fingerprintOfKey(frame.ActorKey).catch(() => "");
                return { ...view, actor: byFingerprint.get(fingerprint) ?? view.actor };
              }),
            ),
          )
          .catch(() => [] as FrameView[]),
      ),
    ).then((groups) => {
      if (!stopped) setFrames(groups.flat());
    });
    return () => {
      stopped = true;
    };
  }, [conversations.join(","), livePosition, workroom.actors, byFingerprint]);

  const items = useMemo(() => {
    const order = orderRef.current;
    const place = (key: string) => {
      if (!order.has(key)) order.set(key, counterRef.current++);
      return order.get(key)!;
    };
    const list: StreamItem[] = [];
    for (const statement of projection?.statements ?? []) {
      list.push({ key: "e:" + statement.event, order: place("e:" + statement.event), statement });
    }
    for (const frame of frames) {
      const key = `f:${frame.conversation}:${frame.sequence}`;
      list.push({ key, order: place(key), frame });
    }
    return list.sort((a, b) => a.order - b.order);
  }, [projection, frames]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 240;
    if (nearBottom) requestAnimationFrame(() => el.scrollTo({ top: 1e9 }));
  }, [items.length]);

  const annotations = useMemo(() => {
    const map = new Map<string, { act?: Act; dissent?: Statement }[]>();
    const attach = (target: string, note: { act?: Act; dissent?: Statement }) =>
      map.set(target, [...(map.get(target) ?? []), note]);
    for (const act of projection?.acts ?? []) attach(act.target, { act });
    for (const statement of projection?.statements ?? []) {
      if (statement.kind !== "dissent") continue;
      const target = projection?.provenance[statement.event]?.[0];
      if (target) attach(target, { dissent: statement });
    }
    return map;
  }, [projection]);

  const doAct = async (input: Parameters<typeof api.act>[0]) => {
    setActError(undefined);
    try {
      await api.act(input);
    } catch (error) {
      setActError(error instanceof Error ? error.message : String(error));
    }
  };

  const commitments = projection?.commitments ?? [];
  const statementText = (event: string) =>
    projection?.statements.find((s) => s.event === event)?.text ?? shortEvent(event);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {commitments.length > 0 && (
        <div className="flex gap-2 overflow-x-auto border-b border-border/60 px-5 py-2">
          {commitments.map((commitment) => (
            <button
              key={commitment.request + (commitment.promise ?? "")}
              onClick={() =>
                onSelect({ kind: "event", id: commitment.report ?? commitment.promise ?? commitment.request })
              }
              title={statementText(commitment.request)}
              className="flex shrink-0 items-center gap-1.5 rounded-md border border-border bg-surface px-2 py-1 text-[11px] hover:border-input"
            >
              <span className={cn("font-semibold", statusTint[commitment.status] ?? "text-muted")}>
                {commitment.status}
              </span>
              <span className="max-w-44 truncate text-muted">{statementText(commitment.request)}</span>
              {commitment.waiting_on && <span className="text-faint">⏳ {nameOf(commitment.waiting_on)}</span>}
            </button>
          ))}
        </div>
      )}
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="mx-auto max-w-3xl space-y-1">
          {items.length === 0 && (
            <p className="py-10 text-center font-serif text-[15px] italic text-faint">
              A quiet room. Say something ephemeral, or set something down for the record.
            </p>
          )}
          {items.map((item) =>
            item.frame ? (
              <MessageLine
                key={item.key}
                frame={item.frame}
                selected={composer.frames.some((f) => f.conversation === item.frame!.conversation && f.sequence === item.frame!.sequence)}
                onToggle={() => {
                  const exists = composer.frames.some(
                    (f) => f.conversation === item.frame!.conversation && f.sequence === item.frame!.sequence,
                  );
                  onComposer({
                    ...composer,
                    mode: composer.mode === "say" && !exists ? "propose" : composer.mode,
                    frames: exists
                      ? composer.frames.filter(
                          (f) => !(f.conversation === item.frame!.conversation && f.sequence === item.frame!.sequence),
                        )
                      : [...composer.frames, item.frame!],
                  });
                }}
              />
            ) : item.statement ? (
              <ActCard
                key={item.key}
                statement={item.statement}
                notes={annotations.get(item.statement.event) ?? []}
                nameOf={nameOf}
                bright={highlight.events.has(item.statement.event)}
                selected={selection?.kind === "event" && selection.id === item.statement.event}
                onSelect={() => onSelect({ kind: "event", id: item.statement!.event })}
                onReply={() =>
                  onComposer({
                    ...composer,
                    mode: composer.mode === "say" ? "assert" : composer.mode,
                    restsOn: composer.restsOn.includes(item.statement!.event)
                      ? composer.restsOn
                      : [...composer.restsOn, item.statement!.event],
                  })
                }
                onDissent={() =>
                  onComposer({ ...composer, mode: "dissent", restsOn: [item.statement!.event] })
                }
                onRatify={() => void doAct({ session: session.id, act: "ratify", target: item.statement!.event })}
                superseding={supersedeTarget === item.statement.event}
                onSupersede={() => {
                  setSupersedeTarget(supersedeTarget === item.statement!.event ? undefined : item.statement!.event);
                  setSupersedeReason("");
                }}
                supersedeReason={supersedeReason}
                onSupersedeReason={setSupersedeReason}
                onSupersedeCommit={() => {
                  void doAct({
                    session: session.id,
                    act: "supersede",
                    target: item.statement!.event,
                    text: supersedeReason || "superseded",
                  });
                  setSupersedeTarget(undefined);
                }}
              />
            ) : null,
          )}
        </div>
      </div>
      {actError && (
        <div className="border-t border-danger/30 bg-danger/10 px-5 py-1.5 text-xs text-danger">
          {actError} <span className="text-faint">— the attempt, if admitted, stays visible in the record.</span>
        </div>
      )}
    </div>
  );
}

function MessageLine({
  frame,
  selected,
  onToggle,
}: {
  frame: FrameView;
  selected: boolean;
  onToggle: () => void;
}) {
  return (
    <div className={cn("group flex gap-3 rounded-md px-2 py-1", selected && "bg-accent/10")}>
      <button
        onClick={onToggle}
        title={selected ? "remove from promotion evidence" : "select as promotion evidence"}
        className={cn(
          "mt-1 h-3.5 w-3.5 shrink-0 rounded-sm border transition-colors",
          selected ? "border-accent bg-accent/70" : "border-border opacity-0 group-hover:opacity-100",
        )}
      />
      <div className="min-w-0">
        <span className={cn("mr-2 text-xs font-semibold", actorTint(frame.actor))}>{frame.actor}</span>
        <span className="font-serif text-[15px] leading-relaxed text-foreground/90">{frame.text}</span>
      </div>
    </div>
  );
}

function ActCard({
  statement,
  notes,
  nameOf,
  bright,
  selected,
  onSelect,
  onReply,
  onDissent,
  onRatify,
  superseding,
  onSupersede,
  supersedeReason,
  onSupersedeReason,
  onSupersedeCommit,
}: {
  statement: Statement;
  notes: { act?: Act; dissent?: Statement }[];
  nameOf: (fingerprint: string) => string;
  bright: boolean;
  selected: boolean;
  onSelect: () => void;
  onReply: () => void;
  onDissent: () => void;
  onRatify: () => void;
  superseding: boolean;
  onSupersede: () => void;
  supersedeReason: string;
  onSupersedeReason: (reason: string) => void;
  onSupersedeCommit: () => void;
}) {
  const governance = ["roster", "infra-key", "seal"].includes(statement.kind);
  if (governance) {
    return (
      <div className="px-2 py-0.5 text-center text-[11px] text-faint">
        — {statement.text} {statement.ratified && "· ratified"} —
      </div>
    );
  }
  const dead = statement.retired;
  return (
    <div
      className={cn(
        "group rise my-1.5 rounded-lg border bg-card px-4 py-3 transition-colors",
        selected ? "border-accent/70" : bright ? "border-accent/40" : "border-border",
        statement.stale && !dead && "border-l-2 border-l-danger",
      )}
    >
      <div className="flex items-center gap-2">
        <button onClick={onSelect} className={cn("shrink-0 rounded border px-1.5 py-px text-[10px] font-medium uppercase tracking-wide", kindTint[statement.kind] ?? "border-border text-muted")}>
          {statement.kind}
        </button>
        <span className="text-xs text-faint">
          {nameOf(statement.actor)}
          {statement.body?.to && <> → {nameOf(statement.body.to)}</>}
        </span>
        {statement.ratified && <BadgeCheck className="h-3.5 w-3.5 text-ok" />}
        {statement.stale && !dead && (
          <span className="flex items-center gap-1 text-[10px] font-medium uppercase text-danger">
            <FileWarning className="h-3 w-3" /> stale
          </span>
        )}
        <span className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <CardAction title="reply — new act resting on this" onClick={onReply}>
            <CornerUpLeft className="h-3.5 w-3.5" />
          </CardAction>
          <CardAction title="ratify" onClick={onRatify}>
            <Stamp className="h-3.5 w-3.5" />
          </CardAction>
          <CardAction title="dissent" onClick={onDissent}>
            <MessageSquareX className="h-3.5 w-3.5" />
          </CardAction>
          <CardAction title="supersede" onClick={onSupersede}>
            <Undo2 className="h-3.5 w-3.5" />
          </CardAction>
        </span>
      </div>
      <p className={cn("mt-1.5 font-serif text-[16px] leading-relaxed", dead ? "text-faint line-through" : "text-foreground")}>
        {statement.text}
      </p>
      <div className="mt-1 flex items-center gap-3 text-[11px] text-faint">
        {statement.body?.conditions && <span>satisfied when: {statement.body.conditions}</span>}
        {statement.body?.path && (
          <span className="text-teal">
            {statement.body.path}@{statement.body.commit?.slice(0, 8)}
          </span>
        )}
        <code className="opacity-0 transition-opacity group-hover:opacity-100">{shortEvent(statement.event)}</code>
      </div>
      {notes.length > 0 && (
        <div className="mt-2 space-y-1 border-l border-border pl-3">
          {notes.map((note, index) => (
            <Note key={index} note={note} nameOf={nameOf} />
          ))}
        </div>
      )}
      {superseding && (
        <div className="mt-2 flex gap-2">
          <input
            autoFocus
            value={supersedeReason}
            onChange={(event) => onSupersedeReason(event.target.value)}
            onKeyDown={(event) => event.key === "Enter" && onSupersedeCommit()}
            placeholder="why this is retired — visible forever"
            className="min-w-0 flex-1 rounded-md border border-input bg-surface px-2.5 py-1.5 text-xs outline-none placeholder:text-faint focus:border-danger/60"
          />
          <button onClick={onSupersedeCommit} className="rounded-md border border-danger/50 px-3 text-xs text-danger hover:bg-danger/10">
            supersede
          </button>
        </div>
      )}
    </div>
  );
}

function CardAction({ title, onClick, children }: { title: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      title={title}
      onClick={onClick}
      className="rounded p-1 text-faint transition-colors hover:bg-elevated hover:text-foreground"
    >
      {children}
    </button>
  );
}

function Note({ note, nameOf }: { note: { act?: Act; dissent?: Statement }; nameOf: (f: string) => string }) {
  if (note.dissent) {
    return (
      <div className="flex items-start gap-1.5 text-xs text-danger">
        <MessageSquareX className="mt-0.5 h-3 w-3 shrink-0" />
        <span>
          {nameOf(note.dissent.actor)} dissents: <span className="text-muted">{note.dissent.text}</span>
        </span>
      </div>
    );
  }
  const act = note.act!;
  if (act.verdict === "effective" && act.type === "ratify") {
    return (
      <div className="flex items-center gap-1.5 text-xs text-ok">
        <BadgeCheck className="h-3 w-3 shrink-0" /> ratified by {nameOf(act.actor)}
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
