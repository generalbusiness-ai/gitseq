import { useEffect, useMemo, useRef, useState } from "react";
import { BadgeCheck, Bookmark, BookmarkPlus, CircleSlash, FileWarning, Link2, MessageSquareText, MessageSquareX, ThumbsUp } from "lucide-react";
import { frameKey, type ActInput, type Commitment, type Decision, type FrameView, type Projection, type Statement, type Vocabulary } from "../lib/api";
import { buildThreadIndex, staleCauses, ticketsOf, type ThreadSummary, type Workroom, type Selection } from "../lib/store";
import type { Session } from "../lib/session";
import { mentionedFingerprints, mentionsActor, mentionTokens } from "../lib/mentions";
import { actorTint, belongsInRoom, clock, cn, definitionOf, kindLabel, kindTint, seenAt, statusLabel, statusTint } from "../lib/util";
import { Avatar } from "./Avatar";
import { RowToolbar, ToolbarButton, semanticActions, type SemanticReplyMode } from "./Toolbar";
import { toggleLinkEvent, toggleLinkFrame, type ComposerContext } from "./Composer";
import type { ThreadTarget } from "./ThreadPane";

export interface PendingSay {
  id: string;
  text: string;
  at: number;
  re?: string; // thread replies echo in the thread pane, not the stream
}

// One room, one message shape. Temporary chat and kept items differ by a
// bookmark, not by typography or card chrome.
export function Stream({
  workroom,
  session,
  frames,
  deliveries,
  highlight,
  selection,
  onSelect,
  onJump,
  composer,
  onComposer,
  pending,
  onReconcile,
  onOpenThread,
  onRoute,
  onOpenProfile,
  doAct,
  actError,
}: {
  workroom: Workroom;
  session: Session;
  frames: FrameView[];
  deliveries: number;
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
  onJump: (selection: Selection) => void; // select without toggling — basis links
  composer: ComposerContext;
  onComposer: (context: ComposerContext) => void;
  pending: PendingSay[];
  onReconcile: (ids: string[]) => void;
  onOpenThread: (target: ThreadTarget) => void;
  onRoute: (mode: SemanticReplyMode, basis: string, prefill: string) => void;
  onOpenProfile: (fingerprint: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
  actError?: string;
}) {
  const projection = workroom.status?.durable.projection;
  const vocabulary = workroom.status?.durable.vocabulary;
  const orderRef = useRef(new Map<string, number>());
  const counterRef = useRef(0);
  const scroller = useRef<HTMLDivElement>(null);
  // Aging is a function of time, not of data: re-render each minute so
  // unpromoted talk visibly fades even in a quiet room.
  const [, setAgeTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setAgeTick((t) => t + 1), 60000);
    return () => clearInterval(timer);
  }, []);

  const actorNames = useMemo(() => new Set(workroom.actors.map((a) => a.name.toLowerCase())), [workroom.actors]);
  const myFingerprint = workroom.actors.find((a) => a.name === session.actor)?.fingerprint;
  const byFingerprint = useMemo(() => new Map(workroom.actors.map((a) => [a.fingerprint, a.name])), [workroom.actors]);
  const nameOf = (fp: string) =>
    byFingerprint.get(fp) ??
    projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fp)?.body?.name ??
    fp.slice(0, 8);

  // Optimistic-echo reconciliation: a pending line is replaced by its real
  // frame when one with the same author and text arrives.
  useEffect(() => {
    if (pending.length === 0) return;
    const matched = pending
      .filter((p) => frames.some((f) => f.actor === session.actor && f.text === p.text))
      .map((p) => p.id);
    if (matched.length > 0) onReconcile(matched);
  }, [frames, pending, session.actor, onReconcile]);

  // Being addressed is worth a knock: when someone else's chat line mentions
  // my actor while this tab is unfocused, flash the title until focus returns.
  // Frames already in the room when the page opened (the first delivery)
  // don't knock.
  const flashSeen = useRef(new Set<string>());
  const flashTimer = useRef<number | undefined>(undefined);
  const baseTitle = useRef(document.title);
  useEffect(() => {
    const fresh = frames.filter((f) => !flashSeen.current.has(frameKey(f)));
    for (const f of fresh) flashSeen.current.add(frameKey(f));
    if (deliveries <= 1) return;
    const addressed = fresh.some((f) => f.actor !== session.actor && mentionsActor(f.text, session.actor));
    if (addressed && !document.hasFocus() && flashTimer.current === undefined) {
      let on = false;
      flashTimer.current = window.setInterval(() => {
        on = !on;
        document.title = on ? `● @${session.actor} — you're mentioned` : baseTitle.current;
      }, 1000);
    }
  }, [frames, deliveries, session.actor]);
  useEffect(() => {
    const stop = () => {
      if (flashTimer.current !== undefined) {
        clearInterval(flashTimer.current);
        flashTimer.current = undefined;
        document.title = baseTitle.current;
      }
    };
    window.addEventListener("focus", stop);
    return () => {
      window.removeEventListener("focus", stop);
      stop();
    };
  }, []);

  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const decisions = useMemo(() => new Map((projection?.decisions ?? []).map((d) => [d.event, d])), [projection]);
  const commitmentByEvent = useMemo(() => {
    const map = new Map<string, Commitment>();
    for (const c of projection?.commitments ?? []) {
      map.set(c.request, c);
      if (c.promise) map.set(c.promise, c);
      if (c.report) map.set(c.report, c);
    }
    return map;
  }, [projection]);
  // Folded promise/report events anchor at their request's card.
  const foldedTo = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of projection?.commitments ?? []) {
      if (c.promise) map.set(c.promise, c.request);
      if (c.report) map.set(c.report, c.request);
    }
    return map;
  }, [projection]);
  const threadIndex = useMemo(() => (projection ? buildThreadIndex(projection) : undefined), [projection]);
  // Chat replies keyed by the frame they rest under.
  const frameReplies = useMemo(() => {
    const map = new Map<string, FrameView[]>();
    for (const frame of frames) {
      if (!frame.re) continue;
      map.set(frame.re, [...(map.get(frame.re) ?? []), frame]);
    }
    return map;
  }, [frames]);

  // Jump the selection to a basis and bring its row into view.
  const jumpTo = (event: string) => {
    onJump({ kind: "event", id: event });
    const anchor = foldedTo.get(event) ?? event;
    requestAnimationFrame(() => document.getElementById("evt-" + anchor)?.scrollIntoView({ block: "center" }));
  };

  // The unified cite gesture, shared with the thread pane (see Composer).
  const linkEvent = (event: string) => toggleLinkEvent(composer, onComposer, event);
  const linkFrame = (frame: FrameView) => toggleLinkFrame(composer, onComposer, frame);

  const items = useMemo(() => {
    const order = orderRef.current;
    const place = (key: string) => {
      if (!order.has(key)) order.set(key, counterRef.current++);
      return order.get(key)!;
    };
    const list: { key: string; order: number; statement?: Statement; frame?: FrameView }[] = [];
    for (const statement of projection?.statements ?? []) {
      if (!belongsInRoom(statement.kind, vocabulary)) continue;
      // promise/report cards fold into their request's card
      const commitment = commitmentByEvent.get(statement.event);
      if (commitment && statement.event !== commitment.request && decisions.get(statement.event)?.verdict === "effective") continue;
      list.push({ key: "e:" + statement.event, order: place("e:" + statement.event), statement });
    }
    for (const frame of frames) {
      // Slack behavior: a threaded reply renders only in its thread pane.
      if (frame.re) continue;
      const key = "f:" + frameKey(frame);
      list.push({ key, order: place(key), frame });
    }
    return list.sort((a, b) => a.order - b.order);
  }, [projection, frames, commitmentByEvent, decisions, vocabulary]);

  // The room opens on the present: jump to the bottom once when content
  // first arrives, then only follow if the reader is already near the end.
  const didInitialScroll = useRef(false);
  useEffect(() => {
    const el = scroller.current;
    if (!el || items.length + pending.length === 0) return;
    if (!didInitialScroll.current) {
      didInitialScroll.current = true;
      requestAnimationFrame(() => el.scrollTo({ top: 1e9 }));
      return;
    }
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 240) requestAnimationFrame(() => el.scrollTo({ top: 1e9 }));
  }, [items.length, pending.length]);

  const annotations = useMemo(() => {
    const map = new Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>();
    const attach = (t: string, n: { act?: Projection["acts"][number]; dissent?: Statement }) => map.set(t, [...(map.get(t) ?? []), n]);
    for (const act of projection?.acts ?? []) attach(act.target, { act });
    for (const s of projection?.statements ?? []) {
      if (definitionOf(s.kind, vocabulary)?.render !== "dissent" && s.kind !== "dissent") continue;
      const target = projection?.provenance[s.event]?.[0];
      if (target) attach(target, { dissent: s });
    }
    return map;
  }, [projection, vocabulary]);

  // Build the rendered sequence with client-side "seen" dividers between
  // gaps in arrival time, and consecutive same-actor messages grouped under
  // one avatar, Slack-style.
  const now = Date.now();
  const rendered: React.ReactNode[] = [];
  let previousFrame: { actor: string; seen: number } | undefined;
  for (const item of items) {
    if (item.frame) {
      const key = "f:" + frameKey(item.frame);
      const seen = item.frame.seen;
      const gap = !previousFrame || seen - previousFrame.seen > 5 * 60000;
      if (gap) {
        rendered.push(
          <div key={"seen:" + key} className="flex items-center gap-3 py-1.5" aria-hidden>
            <span className="h-px flex-1 bg-border/50" />
            <span className="text-xs text-faint">{seenAt(seen)}</span>
            <span className="h-px flex-1 bg-border/50" />
          </div>,
        );
      }
      const showActor = gap || previousFrame?.actor !== item.frame.actor;
      const age = now - seen;
      rendered.push(
        <MessageLine
          key={item.key}
          frame={item.frame}
          showActor={showActor}
          ageClass={age > 30 * 60000 ? "opacity-60" : age > 10 * 60000 ? "opacity-80" : undefined}
          known={actorNames}
          myName={session.actor}
          selected={composer.frames.some((f) => frameKey(f) === frameKey(item.frame!))}
          onToggle={() => linkFrame(item.frame!)}
          onKeep={() =>
            onComposer({
              type: "assert",
              restsOn: composer.restsOn,
              frames: composer.frames.some((frame) => frameKey(frame) === frameKey(item.frame!))
                ? composer.frames
                : [...composer.frames, item.frame!],
              prefill: item.frame!.text,
              prefillID: crypto.randomUUID(),
            })
          }
          replies={frameReplies.get(frameKey(item.frame)) ?? []}
          onOpenThread={() => onOpenThread({ kind: "frame", conversation: item.frame!.conversation, sequence: item.frame!.sequence })}
          onOpenProfile={onOpenProfile}
        />,
      );
      previousFrame = { actor: item.frame.actor, seen };
      continue;
    }
    if (!item.statement || !projection) continue;
    previousFrame = undefined;
    const statement = item.statement;
    const commitment = commitmentByEvent.get(statement.event);
    const common = {
      statement,
      ticket: tickets.get(statement.event),
      decision: decisions.get(statement.event),
      commitment,
      projection,
      vocabulary,
      tickets,
      nameOf,
      me: myFingerprint,
      bright: highlight.events.has(statement.event),
      selected: selection?.kind === "event" && selection.id === statement.event,
      cited: composer.restsOn.includes(statement.event),
      thread: threadIndex?.summary(statement.event),
      onSelect: () => onSelect({ kind: "event", id: statement.event }),
      onJumpTo: jumpTo,
      onCite: () => linkEvent(statement.event),
      onOpenThread: () => onOpenThread({ kind: "event", event: statement.event }),
      onOpenProfile,
      onRoute,
      doAct,
    };
    rendered.push(<RecordedMessage key={item.key} {...common} notes={annotations} />);
  }
  for (const say of pending) {
    if (say.re) continue; // thread replies echo in the pane
    rendered.push(
      <div key={"p:" + say.id} className="flex gap-2.5 rounded-md px-2 py-1 opacity-50">
        <span className="w-9 shrink-0" />
        <div className="min-w-0">
          <span className="text-sm italic leading-relaxed text-foreground/90">{say.text}</span>
        </div>
      </div>,
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-5">
        <div className="mx-auto max-w-3xl space-y-1">
          {items.length === 0 && pending.length === 0 && (
            <p className="py-10 text-center font-serif text-[15px] italic text-faint">A quiet room.</p>
          )}
          {rendered}
        </div>
      </div>
      {actError && (
        <div role="alert" className="border-t border-danger/30 bg-danger/10 px-5 py-1.5 text-xs text-danger">
          {actError}
        </div>
      )}
    </div>
  );
}

// Free text with @name tokens highlighted; my own name glows warmer.
export function MentionText({ text, known, myName, className }: { text: string; known: Set<string>; myName?: string; className?: string }) {
  const tokens = mentionTokens(text);
  return (
    <span className={className}>
      {tokens.map((token, index) =>
        token.mention && known.has(token.mention.toLowerCase()) ? (
          <span
            key={index}
            className={cn(
              "font-medium",
              myName && token.mention.toLowerCase() === myName.toLowerCase() ? "text-accent" : "text-info",
            )}
          >
            {token.text}
          </span>
        ) : (
          <span key={index}>{token.text}</span>
        ),
      )}
    </span>
  );
}

// "→ @ada" badges: whom a durable act addresses, resolved from the
// space-separated fingerprints the composer filed in body.mentions.
function MentionBadges({ body, nameOf, me }: { body?: Record<string, string>; nameOf: (fp: string) => string; me?: string }) {
  const fingerprints = mentionedFingerprints(body);
  if (fingerprints.length === 0) return null;
  return (
    <>
      {fingerprints.map((fp) => (
        <span
          key={fp}
          title={fp}
          className={cn(
            "shrink-0 self-center rounded border px-1 text-[11px] leading-4",
            me && fp === me ? "border-accent/50 text-accent" : "border-border text-muted",
          )}
        >
          → @{nameOf(fp)}
        </span>
      ))}
    </>
  );
}

// The thread indicator, Slack-shaped: a mini avatar row and the reply count.
// Clicking opens the pane. Used under chat parents and on act cards.
export function ThreadIndicator({
  people,
  count,
  onOpen,
  compact,
}: {
  people: { fingerprint: string; name: string }[];
  count: number;
  onOpen: () => void;
  compact?: boolean;
}) {
  if (count === 0) return null;
  const label = `${count} ${count === 1 ? "reply" : "replies"}`;
  if (compact) {
    return (
      <button
        onClick={(e) => {
          e.stopPropagation();
          onOpen();
        }}
        className="shrink-0 self-center text-xs font-medium text-info hover:underline focus-visible:outline focus-visible:outline-accent"
      >
        {label}
      </button>
    );
  }
  return (
    <button
      onClick={(e) => {
        e.stopPropagation();
        onOpen();
      }}
      className="mt-1 flex w-fit items-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-xs font-medium text-info hover:border-border hover:bg-card focus-visible:outline focus-visible:outline-accent"
    >
      <span className="flex -space-x-1">
        {people.slice(0, 3).map((person) => (
          <Avatar key={person.fingerprint} fingerprint={person.fingerprint} name={person.name} size={16} />
        ))}
      </span>
      {label}
    </button>
  );
}

function MessageLine({
  frame,
  showActor,
  ageClass,
  known,
  myName,
  selected,
  onToggle,
  onKeep,
  replies,
  onOpenThread,
  onOpenProfile,
}: {
  frame: FrameView;
  showActor: boolean;
  ageClass?: string;
  known: Set<string>;
  myName?: string;
  selected: boolean;
  onToggle: () => void;
  onKeep: () => void;
  replies: FrameView[];
  onOpenThread: () => void;
  onOpenProfile: (fingerprint: string) => void;
}) {
  // Being addressed lights the whole line — someone else said my name.
  const addressed = frame.actor !== myName && mentionsActor(frame.text, myName);
  const repliers = [...new Map(replies.map((r) => [r.fingerprint, { fingerprint: r.fingerprint, name: r.actor }])).values()];
  return (
    <div
      tabIndex={-1}
      className={cn("group relative rounded-md px-2 py-0.5 outline-none", selected ? "bg-accent/10" : addressed && "bg-info/10", ageClass)}
    >
      <div className="flex gap-2.5">
        <div className="w-9 shrink-0 pt-1">
          {showActor && (
            <Avatar fingerprint={frame.fingerprint} name={frame.actor} size={36} onClick={() => onOpenProfile(frame.fingerprint)} />
          )}
        </div>
        <div className="min-w-0 flex-1">
          {showActor && (
            <div className="flex items-baseline gap-2 pt-0.5">
              <button
                onClick={() => onOpenProfile(frame.fingerprint)}
                className={cn("text-sm font-semibold hover:underline focus-visible:outline focus-visible:outline-accent", actorTint(frame.actor))}
              >
                {frame.actor}
              </button>
              <span className="text-xs text-faint" title={seenAt(frame.seen)}>
                {clock(frame.seen)}
              </span>
            </div>
          )}
          <MentionText text={frame.text} known={known} myName={myName} className="block text-sm leading-relaxed text-foreground/90" />
          <ThreadIndicator people={repliers} count={replies.length} onOpen={onOpenThread} />
        </div>
      </div>
      <RowToolbar>
        <ToolbarButton
          icon={<Link2 className="h-3.5 w-3.5" />}
          label={selected ? "remove link" : "link"}
          active={selected}
          onClick={onToggle}
        />
        <ToolbarButton icon={<BookmarkPlus className="h-3.5 w-3.5" />} label="keep this message" onClick={onKeep} />
        <ToolbarButton icon={<MessageSquareText className="h-3.5 w-3.5" />} label="reply in thread" onClick={onOpenThread} />
      </RowToolbar>
    </div>
  );
}

// The ticket: the human handle for a durable event. The hex id hides in the
// hover title; clicking selects the event for cross-pane inspection.
export function Ticket({ ticket, event, onSelect, className }: { ticket?: number; event: string; onSelect: () => void; className?: string }) {
  if (!ticket) return null;
  return (
    <button
      onClick={(e) => {
        // Tickets often sit inside clickable rows; the ticket wins.
        e.stopPropagation();
        onSelect();
      }}
      title={event}
      className={cn("shrink-0 font-mono text-xs text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent", className)}
    >
      #{ticket}
    </button>
  );
}

// Ordinary links to other kept messages. The full graph stays in Work.
function RestsOn({ event, projection, tickets, onJumpTo, className }: { event: string; projection: Projection; tickets: Map<string, number>; onJumpTo: (event: string) => void; className?: string }) {
  const bases = (projection.provenance[event] ?? []).filter((basis) => tickets.has(basis));
  if (bases.length === 0) return null;
  return (
    <span className={cn("text-xs text-faint", className)}>
      linked to{" "}
      {bases.map((basis, index) => (
        <span key={basis}>
          {index > 0 && ", "}
          <button onClick={() => onJumpTo(basis)} className="hover:text-muted hover:underline focus-visible:outline focus-visible:outline-accent">
            #{tickets.get(basis)}
          </button>
        </span>
      ))}
    </span>
  );
}

// The staleness explanation: walk provenance to the retired ancestors and
// name the supersession that killed each one.
export function WhyStale({ event, projection, tickets, nameOf, onJumpTo }: { event: string; projection: Projection; tickets: Map<string, number>; nameOf: (fp: string) => string; onJumpTo: (event: string) => void }) {
  const [open, setOpen] = useState(false);
  const causes = useMemo(() => staleCauses(event, projection), [event, projection]);
  if (causes.length === 0) return null;
  return (
    <div className="mt-1">
      <button onClick={() => setOpen((o) => !o)} aria-expanded={open} className="text-xs text-danger/80 hover:text-danger focus-visible:outline focus-visible:outline-accent">
        {open ? "why stale −" : "why stale?"}
      </button>
      {open &&
        causes.map((cause) => (
          <p key={cause.act.event} className="mt-0.5 text-xs text-muted">
            stale because{" "}
            <button onClick={() => onJumpTo(cause.act.event)} title={cause.act.event} className="text-foreground/80 hover:underline">
              #{tickets.get(cause.act.event) ?? "?"}
            </button>{" "}
            replaced{" "}
            <button onClick={() => onJumpTo(cause.target)} title={cause.target} className="text-foreground/80 hover:underline">
              #{tickets.get(cause.target) ?? "?"}
            </button>{" "}
            — {nameOf(cause.act.actor)}
            {cause.act.text && <>: “{cause.act.text}”</>}
          </p>
        ))}
    </div>
  );
}

// The shared toolbar for recorded acts: cite, thread, then the row's
// semantic shortcuts as compact labeled buttons.
function ActToolbar({
  statement,
  commitment,
  decision,
  projection,
  me,
  cited,
  onCite,
  onOpenThread,
  onRoute,
  doAct,
}: {
  statement: Statement;
  commitment?: Commitment;
  decision?: Decision;
  projection: Projection;
  me?: string;
  cited: boolean;
  onCite: () => void;
  onOpenThread: () => void;
  onRoute: (mode: SemanticReplyMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
}) {
  const actions = semanticActions({ statement, commitment, decision, projection, me, onRoute, doAct });
  return (
    <RowToolbar>
      <ToolbarButton
        icon={<Link2 className="h-3.5 w-3.5" />}
        label={cited ? "remove link" : "link"}
        active={cited}
        onClick={onCite}
      />
      <ToolbarButton icon={<MessageSquareText className="h-3.5 w-3.5" />} label="open thread" onClick={onOpenThread} />
      {actions.length > 0 && <span aria-hidden className="mx-0.5 h-4 w-px bg-border" />}
      {actions.map((action) => (
        <ToolbarButton key={action.label} label={action.label} icon={<span aria-hidden>{action.symbol}</span>} tone={action.tone} onClick={action.run} />
      ))}
    </RowToolbar>
  );
}

interface RowProps {
  statement: Statement;
  ticket?: number;
  decision?: Decision;
  commitment?: Commitment;
  projection: Projection;
  vocabulary?: Vocabulary;
  tickets: Map<string, number>;
  notes: Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>;
  nameOf: (fp: string) => string;
  me?: string;
  bright: boolean;
  selected: boolean;
  cited: boolean;
  thread?: ThreadSummary;
  onSelect: () => void;
  onJumpTo: (event: string) => void;
  onCite: () => void;
  onOpenThread: () => void;
  onOpenProfile: (fingerprint: string) => void;
  onRoute: (mode: SemanticReplyMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
}

// Kept items use the same author/text/thread grammar as temporary messages.
// A bookmark and ticket carry permanence without turning the row into a card.
function RecordedMessage({
  statement,
  ticket,
  decision,
  commitment,
  projection,
  vocabulary,
  tickets,
  notes,
  nameOf,
  me,
  bright,
  selected,
  cited,
  thread,
  onSelect,
  onJumpTo,
  onCite,
  onOpenThread,
  onOpenProfile,
  onRoute,
  doAct,
}: RowProps) {
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  const tallies = definitionOf(statement.kind, vocabulary)?.render === "proposal" || (!vocabulary && statement.kind === "propose") ? tallyOf(statement.event, notes) : undefined;
  const repliers = (thread?.people ?? []).map((fingerprint) => ({ fingerprint, name: nameOf(fingerprint) }));
  const ratified = statement.ratified || (notes.get(statement.event) ?? []).some((note) => note.act?.type === "ratify" && note.act.verdict === "effective");

  return (
    <div
      id={"evt-" + statement.event}
      tabIndex={-1}
      className={cn(
        "group relative rounded-md px-2 py-1 outline-none",
        selected ? "bg-accent/10" : bright && "bg-accent/5",
        ineffective && "opacity-75",
      )}
    >
      <div className="flex gap-2.5">
        <div className="w-9 shrink-0 pt-1">
          <Avatar fingerprint={statement.actor} name={nameOf(statement.actor)} size={36} onClick={() => onOpenProfile(statement.actor)} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 pt-0.5">
            <button
              onClick={() => onOpenProfile(statement.actor)}
              className={cn("text-sm font-semibold hover:underline focus-visible:outline focus-visible:outline-accent", actorTint(nameOf(statement.actor)))}
            >
              {nameOf(statement.actor)}
            </button>
            <span className="flex items-center gap-1 text-xs text-faint" title="kept">
              <Bookmark className="h-3 w-3 fill-current" /> kept
            </span>
            {statement.body?.to && <span className="text-xs text-faint">for {nameOf(statement.body.to)}</span>}
            {commitment && <span className={cn("text-xs", statusTint[commitment.status])}>{statusLabel(commitment.status)}</span>}
            {ratified && !dead && <BadgeCheck aria-label="agreed" className="h-3.5 w-3.5 text-ok" />}
            {statement.stale && !dead && <span className="text-xs text-danger">stale</span>}
            {ineffective && !dead && <span className="text-xs text-faint" title={decision!.reason}>not active</span>}
            <span className="ml-auto"><Ticket ticket={ticket} event={statement.event} onSelect={onSelect} /></span>
          </div>
          <p className={cn("text-sm leading-relaxed", dead ? "text-faint line-through" : "text-foreground/90")}>{statement.text}</p>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-faint">
            {statement.body?.conditions && <span>when {statement.body.conditions}</span>}
            <MentionBadges body={statement.body} nameOf={nameOf} me={me} />
            <RestsOn event={statement.event} projection={projection} tickets={tickets} onJumpTo={onJumpTo} />
          </div>
          {statement.stale && !dead && <WhyStale event={statement.event} projection={projection} tickets={tickets} nameOf={nameOf} onJumpTo={onJumpTo} />}
          {tallies && (tallies.up > 0 || tallies.down > 0) && (
            <div className="mt-1 flex items-center gap-3 text-xs">
              {tallies.up > 0 && <span className="text-ok">👍 {tallies.up}</span>}
              {tallies.down > 0 && <span className="text-danger">👎 {tallies.down}</span>}
            </div>
          )}
          <ThreadIndicator people={repliers} count={thread?.count ?? 0} onOpen={onOpenThread} />
        </div>
      </div>
      <ActToolbar
        statement={statement}
        commitment={commitment}
        decision={decision}
        projection={projection}
        me={me}
        cited={cited}
        onCite={onCite}
        onOpenThread={onOpenThread}
        onRoute={onRoute}
        doAct={doAct}
      />
    </div>
  );
}

// Weight (b): a settled record entry — small avatar, kind tag, text, ticket.
// One line, no card chrome; every affordance lives in the hover toolbar.
export function CompactRow({
  statement,
  ticket,
  decision,
  commitment,
  projection,
  vocabulary,
  tickets,
  notes,
  nameOf,
  me,
  bright,
  selected,
  cited,
  thread,
  onSelect,
  onJumpTo,
  onCite,
  onOpenThread,
  onOpenProfile,
  onRoute,
  doAct,
}: RowProps) {
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  const ratifiedNote = (notes.get(statement.event) ?? []).some((n) => n.act?.type === "ratify" && n.act.verdict === "effective");
  const replies = thread?.count ?? 0;
  return (
    <div
      id={"evt-" + statement.event}
      tabIndex={-1}
      className={cn("group relative rounded-md px-2 py-1 outline-none", selected ? "bg-accent/10" : bright && "bg-accent/5")}
    >
      <div className="flex items-baseline gap-2">
        <Avatar
          fingerprint={statement.actor}
          name={nameOf(statement.actor)}
          size={18}
          onClick={() => onOpenProfile(statement.actor)}
          className="self-center"
        />
        <button
          onClick={onSelect}
          className={cn("shrink-0 rounded border px-1.5 py-px text-xs font-medium uppercase tracking-wide focus-visible:outline focus-visible:outline-accent", kindTint(statement.kind, vocabulary))}
        >
          {kindLabel(statement.kind, vocabulary)}
        </button>
        <span className={cn("min-w-0 truncate font-serif text-sm", dead ? "text-faint line-through" : "text-foreground/90")} title={statement.text}>
          {statement.text}
        </span>
        <MentionBadges body={statement.body} nameOf={nameOf} me={me} />
        {(statement.ratified || ratifiedNote) && !dead && <BadgeCheck aria-label="ratified" className="h-3.5 w-3.5 shrink-0 self-center text-ok" />}
        {statement.stale && !dead && (
          <span className="flex shrink-0 items-center gap-1 self-center text-xs font-medium uppercase text-danger">
            <FileWarning className="h-3 w-3" /> stale
          </span>
        )}
        {ineffective && !dead && (
          <span className="flex shrink-0 items-center gap-1 self-center text-xs text-faint" title={decision!.reason}>
            <CircleSlash className="h-3 w-3" /> not in force
          </span>
        )}
        {statement.body?.path && (
          <span className="shrink-0 text-xs text-muted" title={`${statement.body.path}@${statement.body.commit}`}>
            {statement.body.path === "." ? "this repository" : statement.body.path}
          </span>
        )}
        <span className="ml-auto flex shrink-0 items-center gap-2 self-center">
          <ThreadIndicator people={[]} count={replies} onOpen={onOpenThread} compact />
          <RestsOn event={statement.event} projection={projection} tickets={tickets} onJumpTo={onJumpTo} className="hidden sm:inline" />
          <Ticket ticket={ticket} event={statement.event} onSelect={onSelect} />
        </span>
      </div>
      {statement.stale && !dead && <WhyStale event={statement.event} projection={projection} tickets={tickets} nameOf={nameOf} onJumpTo={onJumpTo} />}
      <ActToolbar
        statement={statement}
        commitment={commitment}
        decision={decision}
        projection={projection}
        me={me}
        cited={cited}
        onCite={onCite}
        onOpenThread={onOpenThread}
        onRoute={onRoute}
        doAct={doAct}
      />
    </div>
  );
}

// Weight (c): the rich card — only for what still awaits a response.
// Semantic next actions live in the hover toolbar; the fold still judges
// everything. The promise/report chain lives in the thread pane, reached by
// the reply indicator.
export function Card({
  statement,
  ticket,
  decision,
  commitment,
  projection,
  vocabulary,
  tickets,
  notes,
  nameOf,
  me,
  bright,
  selected,
  cited,
  thread,
  onSelect,
  onJumpTo,
  onCite,
  onOpenThread,
  onOpenProfile,
  onRoute,
  doAct,
}: RowProps) {
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  const tallies = definitionOf(statement.kind, vocabulary)?.render === "proposal" || (!vocabulary && statement.kind === "propose") ? tallyOf(statement.event, notes) : undefined;
  const repliers = [
    ...(thread?.people ?? []).map((fingerprint) => ({ fingerprint, name: nameOf(fingerprint) })),
  ];

  return (
    <div
      id={"evt-" + statement.event}
      tabIndex={-1}
      className={cn(
        "group rise relative my-1.5 rounded-lg border bg-card px-4 py-3 outline-none",
        selected ? "border-accent/70" : bright ? "border-accent/40" : "border-border",
        statement.stale && !dead && "border-l-2 border-l-danger",
        ineffective && "border-dashed opacity-80",
      )}
    >
      <div className="flex items-center gap-2">
        <Avatar fingerprint={statement.actor} name={nameOf(statement.actor)} size={20} onClick={() => onOpenProfile(statement.actor)} />
        <button
          onClick={() => onOpenProfile(statement.actor)}
          className="shrink-0 text-xs font-semibold text-foreground/85 hover:underline focus-visible:outline focus-visible:outline-accent"
        >
          {nameOf(statement.actor)}
        </button>
        {statement.body?.to && <span className="text-xs text-faint">→ {nameOf(statement.body.to)}</span>}
        <button onClick={onSelect} className={cn("shrink-0 rounded border px-1.5 py-px text-xs font-medium uppercase tracking-wide focus-visible:outline focus-visible:outline-accent", kindTint(statement.kind, vocabulary))}>
          {kindLabel(statement.kind, vocabulary)}
        </button>
        <MentionBadges body={statement.body} nameOf={nameOf} me={me} />
        {statement.ratified && <BadgeCheck aria-label="ratified" className="h-3.5 w-3.5 text-ok" />}
        {commitment && <span className={cn("text-xs font-semibold", statusTint[commitment.status])}>{commitment.status}</span>}
        {statement.stale && !dead && (
          <span className="flex items-center gap-1 text-xs font-medium uppercase text-danger">
            <FileWarning className="h-3 w-3" /> stale
          </span>
        )}
        <span className="ml-auto flex items-center gap-1">
          <Ticket ticket={ticket} event={statement.event} onSelect={onSelect} />
        </span>
      </div>
      {ineffective && (
        <p className={cn("mt-1 flex items-center gap-1.5 text-xs", decision!.verdict === "disputed" ? "text-danger" : "text-accent-deep")}>
          <CircleSlash className="h-3.5 w-3.5 shrink-0" />
          not in force — {decision!.reason}
        </p>
      )}
      <p className={cn("mt-1.5 font-serif text-[16px] leading-relaxed", dead ? "text-faint line-through" : "text-foreground")}>{statement.text}</p>
      <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-faint">
        {statement.body?.conditions && <span>satisfied when: {statement.body.conditions}</span>}
        {statement.body?.path && (
          <span className="text-muted" title={`${statement.body.path}@${statement.body.commit}`}>
            {statement.body.path === "." ? "this repository" : statement.body.path}@{statement.body.commit?.slice(0, 8)}
          </span>
        )}
        <RestsOn event={statement.event} projection={projection} tickets={tickets} onJumpTo={onJumpTo} />
      </div>
      {statement.stale && !dead && <WhyStale event={statement.event} projection={projection} tickets={tickets} nameOf={nameOf} onJumpTo={onJumpTo} />}

      {tallies && (tallies.up > 0 || tallies.down > 0) && (
        <div className="mt-2 flex items-center gap-3 text-xs">
          <span className="flex items-center gap-1 text-ok">
            <ThumbsUp className="h-3 w-3" /> {tallies.up}
          </span>
          {tallies.down > 0 && (
            <span className="flex items-center gap-1 text-danger">
              <MessageSquareX className="h-3 w-3" /> {tallies.down}
            </span>
          )}
        </div>
      )}
      <ThreadIndicator people={repliers} count={thread?.count ?? 0} onOpen={onOpenThread} />
      <ActToolbar
        statement={statement}
        commitment={commitment}
        decision={decision}
        projection={projection}
        me={me}
        cited={cited}
        onCite={onCite}
        onOpenThread={onOpenThread}
        onRoute={onRoute}
        doAct={doAct}
      />
    </div>
  );
}

function tallyOf(event: string, notes: Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>) {
  const list = notes.get(event) ?? [];
  return {
    up: list.filter((n) => n.act?.type === "ratify" && n.act.verdict === "effective").length,
    down: list.filter((n) => n.dissent).length,
  };
}
