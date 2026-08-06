import { useEffect, useMemo, useRef, useState } from "react";
import { BadgeCheck, CircleSlash, CornerUpLeft, Feather, FileWarning, SendHorizonal, Undo2, X } from "lucide-react";
import { api, frameKey, type ActInput, type FrameView, type Statement } from "../lib/api";
import { threadChildren, ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { mentionFingerprints } from "../lib/mentions";
import { actorTint, clock, cn, kindLabel, kindTint, seenAt } from "../lib/util";
import { Avatar } from "./Avatar";
import { RowToolbar, ToolbarButton, WithdrawInput, semanticActions } from "./Toolbar";
import { toggleCiteEvent, toggleCiteFrame, type ComposerContext, type ComposerMode } from "./Composer";
import { MentionText, Ticket } from "./Stream";
import type { PendingSay } from "./Stream";

// What a thread hangs from: a durable act, or a chat line.
export type ThreadTarget =
  | { kind: "event"; event: string }
  | { kind: "frame"; conversation: string; sequence: number };

const durableTypes = [
  { type: "assert" as const, label: "Note" },
  { type: "propose" as const, label: "Proposal" },
  { type: "request" as const, label: "Request" },
];

// The Slack-style thread pane: parent at top, everything resting on it below,
// composer at the bottom. For a durable act the replies are the provenance
// children — the folded promise/report chain, dissents, follow-ons. For a
// chat line they are the frames whose `re` names it. Docked beside the
// stream on desktop; a full-width sheet on phones. Escape closes.
export function ThreadPane({
  workroom,
  session,
  frames,
  target,
  pending,
  composer,
  onComposer,
  onClose,
  onJumpTo,
  onOpenProfile,
  onRoute,
  doAct,
  onSay,
  onSayFailed,
}: {
  workroom: Workroom;
  session: Session;
  frames: FrameView[];
  target: ThreadTarget;
  pending: PendingSay[];
  composer: ComposerContext;
  onComposer: (context: ComposerContext) => void;
  onClose: () => void;
  onJumpTo: (event: string) => void;
  onOpenProfile: (fingerprint: string) => void;
  onRoute: (mode: ComposerMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
  onSay: (text: string, re: string) => string;
  onSayFailed: (id: string) => void;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const projection = workroom.status?.durable.projection;
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const actorNames = useMemo(() => new Set(workroom.actors.map((a) => a.name.toLowerCase())), [workroom.actors]);
  const byFingerprint = useMemo(() => new Map(workroom.actors.map((a) => [a.fingerprint, a.name])), [workroom.actors]);
  const nameOf = (fp: string) =>
    byFingerprint.get(fp) ??
    projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fp)?.body?.name ??
    fp.slice(0, 8);
  const myFingerprint = workroom.actors.find((a) => a.name === session.actor)?.fingerprint;

  // Focus discipline: the pane takes focus on open (into its composer) and
  // hands it back on close; Escape closes from anywhere inside.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    box.current?.focus();
    return () => opener?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(target)]);
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
  };

  const root = target.kind === "event" ? projection?.statements.find((s) => s.event === target.event) : undefined;
  const parentFrame = target.kind === "frame" ? frames.find((f) => f.conversation === target.conversation && f.sequence === target.sequence) : undefined;
  const thread = useMemo(
    () => (target.kind === "event" && projection ? threadChildren(target.event, projection) : undefined),
    [target, projection],
  );
  const reKey = target.kind === "frame" ? `${target.conversation}:${target.sequence}` : undefined;
  const replies = useMemo(() => (reKey ? frames.filter((f) => f.re === reKey) : []), [frames, reKey]);
  const pendingHere = pending.filter((p) => p.re === reKey);

  // Follow the tail as replies arrive.
  const scroller = useRef<HTMLDivElement>(null);
  const replyCount = (thread?.statements.length ?? 0) + replies.length + pendingHere.length;
  useEffect(() => {
    requestAnimationFrame(() => scroller.current?.scrollTo({ top: 1e9 }));
  }, [replyCount]);

  return (
    <div
      ref={panel}
      role="complementary"
      aria-label="Thread"
      tabIndex={-1}
      onKeyDown={onKeyDown}
      className="fixed inset-0 z-40 flex flex-col border-border bg-background outline-none sm:static sm:z-auto sm:w-[24rem] sm:shrink-0 sm:border-l"
    >
      <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
        <h2 className="text-xs font-medium uppercase tracking-[0.16em] text-muted">Thread</h2>
        <button onClick={onClose} aria-label="close thread" className="rounded p-1 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
          <X className="h-4 w-4" />
        </button>
      </div>
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {target.kind === "event" &&
          (root && projection ? (
            <>
              <ThreadStatement
                statement={root}
                ticket={tickets.get(root.event)}
                nameOf={nameOf}
                root
                cited={composer.restsOn.includes(root.event)}
                onCite={() => toggleCiteEvent(composer, onComposer, root.event)}
                onJumpTo={onJumpTo}
                onOpenProfile={onOpenProfile}
                actions={
                  projection
                    ? semanticActions({
                        statement: root,
                        commitment: (projection.commitments ?? []).find((c) => [c.request, c.promise, c.report].includes(root.event)),
                        decision: (projection.decisions ?? []).find((d) => d.event === root.event),
                        projection,
                        me: myFingerprint,
                        onRoute,
                        doAct,
                        onWithdraw: () => {},
                      })
                    : []
                }
                doAct={doAct}
              />
              <div className="my-2 flex items-center gap-2" aria-hidden>
                <span className="h-px flex-1 bg-border/60" />
                <span className="text-xs text-faint">
                  {thread?.statements.length ?? 0} {thread?.statements.length === 1 ? "reply" : "replies"}
                </span>
                <span className="h-px flex-1 bg-border/60" />
              </div>
              {thread?.statements.map((statement) => (
                <ThreadStatement
                  key={statement.event}
                  statement={statement}
                  ticket={tickets.get(statement.event)}
                  nameOf={nameOf}
                  cited={composer.restsOn.includes(statement.event)}
                  onCite={() => toggleCiteEvent(composer, onComposer, statement.event)}
                  onJumpTo={onJumpTo}
                  onOpenProfile={onOpenProfile}
                  actions={
                    projection
                      ? semanticActions({
                          statement,
                          commitment: (projection.commitments ?? []).find((c) => [c.request, c.promise, c.report].includes(statement.event)),
                          decision: (projection.decisions ?? []).find((d) => d.event === statement.event),
                          projection,
                          me: myFingerprint,
                          onRoute,
                          doAct,
                          onWithdraw: () => {},
                        })
                      : []
                  }
                  doAct={doAct}
                />
              ))}
              {thread?.acts.map((act) => (
                <div key={act.event} className={cn("ml-9 flex items-start gap-1.5 px-2 py-0.5 text-xs", act.verdict === "effective" ? (act.type === "ratify" ? "text-ok" : "text-danger") : "text-faint")}>
                  {act.type === "ratify" ? <BadgeCheck className="mt-0.5 h-3 w-3 shrink-0" /> : <Undo2 className="mt-0.5 h-3 w-3 shrink-0" />}
                  <span>
                    {act.verdict === "effective"
                      ? `${act.type === "ratify" ? "agreed" : "withdrawn"} by ${nameOf(act.actor)}`
                      : `${nameOf(act.actor)} tried to ${act.type} — ${act.reason}`}
                    {act.text && <span className="text-muted"> — {act.text}</span>}
                  </span>
                </div>
              ))}
            </>
          ) : (
            <p className="px-2 py-4 text-xs text-faint">Gone.</p>
          ))}
        {target.kind === "frame" &&
          (parentFrame ? (
            <>
              <ThreadMessage
                frame={parentFrame}
                known={actorNames}
                myName={session.actor}
                cited={composer.frames.some((f) => frameKey(f) === frameKey(parentFrame))}
                onCite={() => toggleCiteFrame(composer, onComposer, parentFrame)}
                onOpenProfile={onOpenProfile}
              />
              <div className="my-2 flex items-center gap-2" aria-hidden>
                <span className="h-px flex-1 bg-border/60" />
                <span className="text-xs text-faint">
                  {replies.length} {replies.length === 1 ? "reply" : "replies"}
                </span>
                <span className="h-px flex-1 bg-border/60" />
              </div>
              {replies.map((frame) => (
                <ThreadMessage
                  key={frameKey(frame)}
                  frame={frame}
                  known={actorNames}
                  myName={session.actor}
                  cited={composer.frames.some((f) => frameKey(f) === frameKey(frame))}
                  onCite={() => toggleCiteFrame(composer, onComposer, frame)}
                  onOpenProfile={onOpenProfile}
                />
              ))}
              {pendingHere.map((say) => (
                <div key={say.id} className="flex gap-2 rounded-md px-2 py-1 opacity-50">
                  <span className="w-6 shrink-0" />
                  <span className="text-sm italic leading-relaxed text-foreground/90">{say.text}</span>
                </div>
              ))}
            </>
          ) : (
            <p className="px-2 py-4 text-xs text-faint">Gone — the room forgot this conversation.</p>
          ))}
      </div>
      <ThreadComposer
        workroom={workroom}
        session={session}
        target={target}
        parentFrame={parentFrame}
        boxRef={box}
        onSay={onSay}
        onSayFailed={onSayFailed}
      />
    </div>
  );
}

// A durable act inside the thread: avatar, author, kind, text, ticket — with
// the same hover toolbar grammar as the stream (cite + semantic shortcuts).
function ThreadStatement({
  statement,
  ticket,
  nameOf,
  root,
  cited,
  onCite,
  onJumpTo,
  onOpenProfile,
  actions,
  doAct,
}: {
  statement: Statement;
  ticket?: number;
  nameOf: (fp: string) => string;
  root?: boolean;
  cited: boolean;
  onCite: () => void;
  onJumpTo: (event: string) => void;
  onOpenProfile: (fingerprint: string) => void;
  actions: { label: string; tone?: "ok" | "danger"; run: () => void }[];
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
}) {
  const [withdrawing, setWithdrawing] = useState(false);
  const dead = statement.retired;
  const dissent = statement.kind === "dissent";
  // Withdraw arrives via the shared semanticActions when we wire onWithdraw.
  const rowActions = actions.map((a) => (a.label === "Withdraw" ? { ...a, run: () => setWithdrawing(true) } : a));
  return (
    <div tabIndex={-1} className={cn("group relative rounded-md px-2 py-1.5 outline-none", root && "rounded-lg border border-border bg-card")}>
      <div className="flex items-start gap-2">
        <Avatar
          fingerprint={statement.actor}
          name={nameOf(statement.actor)}
          size={root ? 28 : 24}
          onClick={() => onOpenProfile(statement.actor)}
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <button
              onClick={() => onOpenProfile(statement.actor)}
              className={cn("text-xs font-semibold hover:underline focus-visible:outline focus-visible:outline-accent", actorTint(nameOf(statement.actor)))}
            >
              {nameOf(statement.actor)}
            </button>
            <span className={cn("shrink-0 rounded border px-1 text-[10px] font-medium uppercase leading-4 tracking-wide", dissent ? "border-danger/40 text-danger" : kindTint[statement.kind] ?? "border-border text-muted")}>
              {kindLabel[statement.kind] ?? statement.kind}
            </span>
            {statement.ratified && !dead && <BadgeCheck aria-label="ratified" className="h-3 w-3 shrink-0 text-ok" />}
            {statement.stale && !dead && (
              <span className="flex shrink-0 items-center gap-0.5 text-[10px] font-medium uppercase text-danger">
                <FileWarning className="h-3 w-3" /> stale
              </span>
            )}
            <span className="ml-auto">
              <Ticket ticket={ticket} event={statement.event} onSelect={() => onJumpTo(statement.event)} />
            </span>
          </div>
          <p className={cn("mt-0.5 font-serif text-sm leading-relaxed", dead ? "text-faint line-through" : "text-foreground/90")}>{statement.text}</p>
          {statement.body?.conditions && <p className="text-xs text-faint">satisfied when: {statement.body.conditions}</p>}
          {withdrawing && <WithdrawInput statement={statement} doAct={doAct} onDone={() => setWithdrawing(false)} />}
        </div>
      </div>
      <RowToolbar>
        <ToolbarButton icon={<CornerUpLeft className="h-3.5 w-3.5" />} label={cited ? "remove citation" : "cite"} active={cited} onClick={onCite} />
        {rowActions.length > 0 && <span aria-hidden className="mx-0.5 h-4 w-px bg-border" />}
        {rowActions.map((action) => (
          <ToolbarButton key={action.label} label={action.label} showLabel tone={action.tone} onClick={action.run} />
        ))}
      </RowToolbar>
    </div>
  );
}

// A chat line inside the thread — Slack layout at thread scale.
function ThreadMessage({
  frame,
  known,
  myName,
  cited,
  onCite,
  onOpenProfile,
}: {
  frame: FrameView;
  known: Set<string>;
  myName?: string;
  cited: boolean;
  onCite: () => void;
  onOpenProfile: (fingerprint: string) => void;
}) {
  return (
    <div tabIndex={-1} className="group relative flex gap-2 rounded-md px-2 py-1 outline-none">
      <Avatar fingerprint={frame.fingerprint} name={frame.actor} size={24} onClick={() => onOpenProfile(frame.fingerprint)} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <button
            onClick={() => onOpenProfile(frame.fingerprint)}
            className={cn("text-xs font-semibold hover:underline focus-visible:outline focus-visible:outline-accent", actorTint(frame.actor))}
          >
            {frame.actor}
          </button>
          <span className="text-xs text-faint" title={seenAt(frame.seen)}>
            {clock(frame.seen)}
          </span>
        </div>
        <MentionText text={frame.text} known={known} myName={myName} className="block text-sm leading-relaxed text-foreground/90" />
      </div>
      <RowToolbar>
        <ToolbarButton icon={<CornerUpLeft className="h-3.5 w-3.5" />} label={cited ? "remove citation" : "cite"} active={cited} onClick={onCite} />
      </RowToolbar>
    </div>
  );
}

// The thread's own composer. A chat thread replies with say-with-re by
// default and can flip to a durable type — the durable reply then cites the
// parent frame as evidence automatically. An act thread replies durably,
// resting on the act; Note is the default.
function ThreadComposer({
  workroom,
  session,
  target,
  parentFrame,
  boxRef,
  onSay,
  onSayFailed,
}: {
  workroom: Workroom;
  session: Session;
  target: ThreadTarget;
  parentFrame?: FrameView;
  boxRef: React.RefObject<HTMLTextAreaElement | null>;
  onSay: (text: string, re: string) => string;
  onSayFailed: (id: string) => void;
}) {
  const chat = target.kind === "frame";
  const [type, setType] = useState<"say" | "assert" | "propose" | "request">(chat ? "say" : "assert");
  const [text, setText] = useState("");
  const [to, setTo] = useState("");
  const [conditions, setConditions] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // One idempotency key per user intention, held across retries.
  const intentKey = useMemo(() => crypto.randomUUID(), [type, busy === false && text]);
  const durable = type !== "say";

  const send = async () => {
    if (!text.trim() || busy || !session.actor) return;
    setBusy(true);
    setError(undefined);
    const line = text.trim();
    if (!durable && chat && parentFrame) {
      const re = frameKey(parentFrame);
      const pendingID = onSay(line, re); // optimistic: it appears before the round trip
      setText("");
      try {
        await api.say(session.id, parentFrame.about || "the workroom", line, parentFrame.conversation, re);
      } catch (thrown) {
        onSayFailed(pendingID);
        setText(line); // give the words back rather than losing them
        setError(thrown instanceof Error ? thrown.message : String(thrown));
      } finally {
        setBusy(false);
      }
      return;
    }
    try {
      const body: Record<string, string> = {};
      if (type === "request") {
        if (!to || !conditions.trim()) throw new Error("a request names its performer and conditions");
        body.to = "@" + to;
        body.conditions = conditions.trim();
      }
      const mentioned = mentionFingerprints(line, workroom.actors);
      if (mentioned.length > 0) body.mentions = mentioned.join(" ");
      // A reply rests on the act it answers; a durable reply in a chat
      // thread instead embeds the parent's complete signed frame as
      // evidence — verifiable after the conversation is forgotten.
      const evidence =
        chat && parentFrame ? { "frames.json": JSON.stringify([parentFrame.raw], null, 2) } : undefined;
      await api.act({
        session: session.id,
        act: "state",
        kind: type,
        text: line,
        body: Object.keys(body).length ? body : undefined,
        rests_on: target.kind === "event" ? [target.event] : [],
        evidence,
        idempotency_key: intentKey,
      });
      setText("");
      setTo("");
      setConditions("");
      if (chat) setType("say");
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-border px-3 py-2.5">
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
        {chat && (
          <TypePill label="Reply" active={type === "say"} onClick={() => setType("say")} />
        )}
        {durableTypes.map((pill) => (
          <TypePill key={pill.type} label={pill.label} active={type === pill.type} onClick={() => setType(pill.type)} />
        ))}
      </div>
      {type === "request" && (
        <div className="mb-1.5 flex flex-wrap items-center gap-2 text-xs">
          <label className="text-faint">to</label>
          <select value={to} onChange={(e) => setTo(e.target.value)} className="rounded-md border border-input bg-surface px-2 py-1 outline-none focus:border-accent/60">
            <option value="">choose…</option>
            {workroom.actors
              .filter((a) => a.name !== session.actor)
              .map((a) => (
                <option key={a.name} value={a.name}>
                  {a.name}
                </option>
              ))}
          </select>
          <input
            value={conditions}
            onChange={(e) => setConditions(e.target.value)}
            placeholder="satisfied when…"
            className="min-w-0 flex-1 rounded-md border border-input bg-surface px-2 py-1 outline-none placeholder:text-faint focus:border-accent/60"
          />
        </div>
      )}
      <div className="flex items-end gap-2">
        <textarea
          ref={boxRef}
          value={text}
          rows={1}
          placeholder={durable ? "reply for the record…" : "reply…"}
          aria-label="thread reply"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
          className={cn(
            "min-w-0 flex-1 resize-none rounded-lg border border-input bg-surface px-3 py-1.5 text-sm outline-none placeholder:text-faint",
            durable ? "font-serif focus:border-accent/60" : "focus:border-input",
          )}
        />
        <button
          onClick={() => void send()}
          disabled={busy || !text.trim() || !session.live}
          aria-label={durable ? "set it down" : "reply"}
          title={session.live ? undefined : "not present yet"}
          className={cn(
            "flex h-8 w-8 items-center justify-center rounded-lg transition-colors focus-visible:outline focus-visible:outline-accent disabled:opacity-40",
            durable ? "bg-accent text-background hover:bg-accent/90" : "border border-border text-muted hover:bg-elevated hover:text-foreground",
          )}
        >
          {durable ? <Feather className="h-3.5 w-3.5" /> : <SendHorizonal className="h-3.5 w-3.5" />}
        </button>
      </div>
      {error && (
        <p role="alert" className="mt-1 flex items-center gap-1 text-xs text-danger">
          <CircleSlash className="h-3 w-3" /> {error}
        </p>
      )}
      {chat && durable && parentFrame && <p className="mt-1 text-xs text-faint">cites the parent message</p>}
    </div>
  );
}

function TypePill({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "rounded-md px-2 py-0.5 text-xs font-medium focus-visible:outline focus-visible:outline-accent",
        active ? "border border-accent/50 bg-accent/10 text-foreground" : "text-faint hover:text-muted",
      )}
    >
      {label}
    </button>
  );
}
