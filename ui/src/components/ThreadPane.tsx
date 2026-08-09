import { useEffect, useMemo, useRef, useState } from "react";
import { BadgeCheck, Bookmark, CircleSlash, Link2, SendHorizonal, Undo2, X } from "lucide-react";
import { api, frameKey, type ActInput, type FrameView, type Statement } from "../lib/api";
import { buildThreadIndex, ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { mentionFingerprints } from "../lib/mentions";
import { RetryKeys, threadTargetKey } from "../lib/interaction";
import { actorTint, clock, cn, seenAt } from "../lib/util";
import { Avatar } from "./Avatar";
import { RowToolbar, ToolbarButton, semanticActions, type SemanticReplyMode } from "./Toolbar";
import { toggleLinkEvent, toggleLinkFrame, type ComposerContext } from "./Composer";
import { EventTime } from "./EventTime";
import { MentionText, Ticket } from "./Stream";
import type { PendingSay } from "./Stream";

// What a thread hangs from: a durable act, or a chat line.
export type ThreadTarget =
  | { kind: "event"; event: string }
  | { kind: "frame"; conversation: string; sequence: number };

export interface ThreadRoute {
  id: string;
  mode: SemanticReplyMode;
  prefill: string;
}

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
  route,
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
  route?: ThreadRoute;
  pending: PendingSay[];
  composer: ComposerContext;
  onComposer: (context: ComposerContext) => void;
  onClose: () => void;
  onJumpTo: (event: string) => void;
  onOpenProfile: (fingerprint: string) => void;
  onRoute: (mode: SemanticReplyMode, basis: string, prefill: string) => void;
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
  const threadIndex = useMemo(() => (projection ? buildThreadIndex(projection) : undefined), [projection]);
  const thread = useMemo(
    () => (target.kind === "event" ? threadIndex?.content(target.event) : undefined),
    [target, threadIndex],
  );
  const statementByEvent = useMemo(() => new Map((thread?.statements ?? []).map((statement) => [statement.event, statement])), [thread]);
  const actByEvent = useMemo(() => new Map((thread?.acts ?? []).map((act) => [act.event, act])), [thread]);
  const reKey = target.kind === "frame" ? `${target.conversation}:${target.sequence}` : undefined;
  const replies = useMemo(() => (reKey ? frames.filter((f) => f.re === reKey) : []), [frames, reKey]);
  const pendingHere = pending.filter((p) => p.re === reKey);

  // Follow the tail as replies arrive.
  const scroller = useRef<HTMLDivElement>(null);
  const replyCount = (thread?.statements.length ?? 0) + (thread?.acts.length ?? 0) + replies.length + pendingHere.length;
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
        <h2 className="text-sm font-semibold text-foreground/90">Thread</h2>
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
                onCite={() => toggleLinkEvent(composer, onComposer, root.event)}
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
                      })
                    : []
                }
              />
              <div className="my-2 flex items-center gap-2" aria-hidden>
                <span className="h-px flex-1 bg-border/60" />
                <span className="text-xs text-faint">
                  {(thread?.statements.length ?? 0) + (thread?.acts.length ?? 0)}{" "}
                  {(thread?.statements.length ?? 0) + (thread?.acts.length ?? 0) === 1 ? "reply" : "replies"}
                </span>
                <span className="h-px flex-1 bg-border/60" />
              </div>
              {thread?.events.map((event) => {
                const statement = statementByEvent.get(event);
                if (statement) {
                  return (
                    <ThreadStatement
                      key={event}
                      statement={statement}
                      ticket={tickets.get(event)}
                      nameOf={nameOf}
                      cited={composer.restsOn.includes(event)}
                      onCite={() => toggleLinkEvent(composer, onComposer, event)}
                      onJumpTo={onJumpTo}
                      onOpenProfile={onOpenProfile}
                      actions={semanticActions({
                        statement,
                        commitment: (projection.commitments ?? []).find((c) => [c.request, c.promise, c.report].includes(event)),
                        decision: (projection.decisions ?? []).find((d) => d.event === event),
                        projection,
                        me: myFingerprint,
                        onRoute,
                        doAct,
                      })}
                    />
                  );
                }
                const act = actByEvent.get(event);
                if (!act) return null;
                return (
                  <div key={event} className={cn("ml-9 flex items-start gap-1.5 px-2 py-0.5 text-xs", act.verdict === "effective" ? (act.type === "ratify" ? "text-ok" : "text-danger") : "text-faint")}>
                    {act.type === "ratify" ? <BadgeCheck className="mt-0.5 h-3 w-3 shrink-0" /> : <Undo2 className="mt-0.5 h-3 w-3 shrink-0" />}
                    <span>
                      {act.verdict === "effective"
                        ? `${act.type === "ratify" ? "agreed" : "withdrawn"} by ${nameOf(act.actor)}`
                        : `${nameOf(act.actor)} tried to ${act.type} — ${act.reason}`}
                      {act.text && <span className="text-muted"> — {act.text}</span>}
                    </span>
                    <EventTime timestamp={act.timestamp} className="ml-auto" />
                  </div>
                );
              })}
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
                onCite={() => toggleLinkFrame(composer, onComposer, parentFrame)}
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
                  onCite={() => toggleLinkFrame(composer, onComposer, frame)}
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
        route={route}
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
}: {
  statement: Statement;
  ticket?: number;
  nameOf: (fp: string) => string;
  root?: boolean;
  cited: boolean;
  onCite: () => void;
  onJumpTo: (event: string) => void;
  onOpenProfile: (fingerprint: string) => void;
  actions: { label: string; symbol: string; tone?: "ok" | "danger"; run: () => void }[];
}) {
  const dead = statement.retired;
  return (
    <div tabIndex={-1} className={cn("group relative rounded-md px-2 py-1 outline-none", root && "mb-1")}>
      <div className="flex items-start gap-2.5">
        <Avatar
          fingerprint={statement.actor}
          name={nameOf(statement.actor)}
          size={28}
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
            <span className="flex items-center gap-1 text-[11px] text-faint" title="kept">
              <Bookmark className="h-3 w-3 fill-current" /> kept
            </span>
            {statement.ratified && !dead && <BadgeCheck aria-label="ratified" className="h-3 w-3 shrink-0 text-ok" />}
            {statement.stale && !dead && <span className="text-[11px] text-danger">stale</span>}
            <EventTime timestamp={statement.timestamp} className="ml-auto" />
            <span>
              <Ticket ticket={ticket} event={statement.event} onSelect={() => onJumpTo(statement.event)} />
            </span>
          </div>
          <p className={cn("mt-0.5 text-sm leading-relaxed", dead ? "text-faint line-through" : "text-foreground/90")}>{statement.text}</p>
          {statement.body?.conditions && <p className="text-xs text-faint">when {statement.body.conditions}</p>}
        </div>
      </div>
      <RowToolbar>
        <ToolbarButton icon={<Link2 className="h-3.5 w-3.5" />} label={cited ? "remove link" : "link"} active={cited} onClick={onCite} />
        {actions.length > 0 && <span aria-hidden className="mx-0.5 h-4 w-px bg-border" />}
        {actions.map((action) => (
          <ToolbarButton key={action.label} label={action.label} icon={<span aria-hidden>{action.symbol}</span>} tone={action.tone} onClick={action.run} />
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
    <div tabIndex={-1} className="group relative flex gap-2.5 rounded-md px-2 py-1 outline-none">
      <Avatar fingerprint={frame.fingerprint} name={frame.actor} size={28} onClick={() => onOpenProfile(frame.fingerprint)} />
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
        <ToolbarButton icon={<Link2 className="h-3.5 w-3.5" />} label={cited ? "remove link" : "link"} active={cited} onClick={onCite} />
      </RowToolbar>
    </div>
  );
}

// Thread replies are temporary under temporary chat and kept under a kept
// root. Chat replies can be bookmarked without exposing record kinds.
function ThreadComposer({
  workroom,
  session,
  target,
  route,
  parentFrame,
  boxRef,
  onSay,
  onSayFailed,
}: {
  workroom: Workroom;
  session: Session;
  target: ThreadTarget;
  route?: ThreadRoute;
  parentFrame?: FrameView;
  boxRef: React.RefObject<HTMLTextAreaElement | null>;
  onSay: (text: string, re: string) => string;
  onSayFailed: (id: string) => void;
}) {
  const chat = target.kind === "frame";
  type ReplyType = "say" | "assert" | SemanticReplyMode;
  const defaultType: ReplyType = chat ? "say" : "assert";
  const [activeRoute, setActiveRoute] = useState(route);
  const [type, setType] = useState<ReplyType>(route?.mode ?? defaultType);
  const [text, setText] = useState(route?.prefill ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const retryKeys = useRef(new RetryKeys());
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
      const mentioned = mentionFingerprints(line, workroom.actors);
      if (mentioned.length > 0) body.mentions = mentioned.join(" ");
      // A reply rests on the act it answers; a durable reply in a chat
      // thread instead embeds the parent's complete signed frame as
      // evidence — verifiable after the conversation is forgotten.
      const evidence =
        chat && parentFrame ? { "frames.json": JSON.stringify([parentFrame.raw], null, 2) } : undefined;
      const input: ActInput =
        type === "withdraw"
          ? {
              session: session.id,
              act: "supersede",
              target: target.kind === "event" ? target.event : undefined,
              text: line,
            }
          : {
              session: session.id,
              act: "state",
              kind: type,
              text: line,
              body: Object.keys(body).length ? body : undefined,
              rests_on: target.kind === "event" ? [target.event] : [],
              evidence,
            };
      const scope = `${threadTargetKey(target)}:${type}`;
      const payload = JSON.stringify(input);
      const intentKey = retryKeys.current.forAttempt(scope, payload);
      await api.act({ ...input, idempotency_key: intentKey });
      retryKeys.current.succeeded(scope, intentKey);
      setText("");
      setActiveRoute(undefined);
      setType(defaultType);
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-border px-3 py-2.5">
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
        {activeRoute ? (
          <>
            <span className="rounded-md border border-accent/40 bg-accent/10 px-2 py-0.5 text-xs text-foreground">
              {activeRoute.mode === "promise"
                ? "Accept"
                : activeRoute.mode === "report"
                  ? "Mark done"
                  : activeRoute.mode === "dissent"
                    ? "Disagree"
                    : "Withdraw"}
            </span>
            <button
              onClick={() => {
                setActiveRoute(undefined);
                setType(defaultType);
                setText("");
              }}
              aria-label="cancel reply action"
              className="rounded p-1 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </>
        ) : chat ? (
          <button
            onClick={() => setType(durable ? "say" : "assert")}
            aria-pressed={durable}
            className={cn(
              "flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium focus-visible:outline focus-visible:outline-accent",
              durable ? "bg-accent/12 text-accent-deep" : "text-faint hover:bg-elevated hover:text-muted",
            )}
          >
            <Bookmark className={cn("h-3.5 w-3.5", durable && "fill-current")} /> {durable ? "Kept" : "Temporary"}
          </button>
        ) : (
          <span className="flex items-center gap-1 text-xs text-faint">
            <Bookmark className="h-3 w-3 fill-current" /> kept
          </span>
        )}
      </div>
      <div className="flex items-end gap-2">
        <textarea
          ref={boxRef}
          value={text}
          rows={1}
          placeholder={
            type === "withdraw"
              ? "why — visible forever…"
              : type === "dissent"
                ? "what should be understood differently…"
                : type === "promise"
                  ? "what you undertake…"
                  : type === "report"
                    ? "what was done…"
                    : "Reply…"
          }
          aria-label={type === "withdraw" ? "withdraw reason" : "thread reply"}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
          className={cn(
            "min-w-0 flex-1 resize-none rounded-lg border border-input bg-surface px-3 py-1.5 text-sm outline-none placeholder:text-faint focus:border-accent/60",
          )}
        />
        <button
          onClick={() => void send()}
          disabled={busy || !text.trim() || !session.live}
          aria-label={type === "withdraw" ? "withdraw" : "send reply"}
          title={session.live ? undefined : "not present yet"}
          className={cn(
            "flex h-8 w-8 items-center justify-center rounded-lg transition-colors focus-visible:outline focus-visible:outline-accent disabled:opacity-40",
            "bg-accent text-background hover:bg-accent/90",
          )}
        >
          {type === "withdraw" ? <Undo2 className="h-3.5 w-3.5" /> : <SendHorizonal className="h-3.5 w-3.5" />}
        </button>
      </div>
      {error && (
        <p role="alert" className="mt-1 flex items-center gap-1 text-xs text-danger">
          <CircleSlash className="h-3 w-3" /> {error}
        </p>
      )}
    </div>
  );
}
