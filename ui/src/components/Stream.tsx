import { useEffect, useMemo, useRef, useState } from "react";
import { BadgeCheck, BookOpen, CircleSlash, CornerUpLeft, FileWarning, MessageSquareX, ThumbsUp, Undo2 } from "lucide-react";
import { api, decodeFrame, type ActInput, type Commitment, type Decision, type Frame, type FrameView, type Projection, type Statement } from "../lib/api";
import { staleCauses, ticketsOf, statementWeight, type Workroom, type Selection } from "../lib/store";
import type { Session } from "../lib/session";
import { loadMemory, rememberFrames, type MemoryEntry } from "../lib/memory";
import { mentionedFingerprints, mentionsActor, mentionTokens } from "../lib/mentions";
import { actorTint, cn, fingerprintOfKey, kindLabel, kindTint, seenAt, statusTint } from "../lib/util";
import { type ComposerContext, type ComposerMode } from "./Composer";

export interface PendingSay {
  id: string;
  text: string;
  at: number;
}

// One stream, three weights: plain talk as light message rows; settled record
// entries as compact one-liners; only what still awaits a response — active
// commitments and open proposals — as rich cards. Each loop folds its
// promise, report, and satisfaction into the request's card.
export function Stream({
  workroom,
  session,
  highlight,
  selection,
  onSelect,
  onJump,
  composer,
  onComposer,
  pending,
  onReconcile,
}: {
  workroom: Workroom;
  session: Session;
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
  onJump: (selection: Selection) => void; // select without toggling — basis links
  composer: ComposerContext;
  onComposer: (context: ComposerContext) => void;
  pending: PendingSay[];
  onReconcile: (ids: string[]) => void;
}) {
  const projection = workroom.status?.durable.projection;
  const genesis = workroom.status?.durable.genesis ?? "";
  const conversations = workroom.status?.live.conversations ?? [];
  const livePosition = workroom.status?.live.cursor.position ?? 0;
  const [frames, setFrames] = useState<FrameView[]>([]);
  const [actError, setActError] = useState<string>();
  const [memoryOpen, setMemoryOpen] = useState(false);
  const inFlight = useRef(new Set<string>());
  const orderRef = useRef(new Map<string, number>());
  const counterRef = useRef(0);
  // When this browser first saw each frame — arrival time, honestly labeled.
  const firstSeenRef = useRef(new Map<string, number>());
  const scroller = useRef<HTMLDivElement>(null);
  // Aging is a function of time, not of data: re-render each minute so
  // unpromoted talk visibly fades even in a quiet room.
  const [, setAgeTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setAgeTick((t) => t + 1), 60000);
    return () => clearInterval(timer);
  }, []);

  const byFingerprint = useMemo(() => new Map(workroom.actors.map((a) => [a.fingerprint, a.name])), [workroom.actors]);
  const actorNames = useMemo(() => new Set(workroom.actors.map((a) => a.name.toLowerCase())), [workroom.actors]);
  const myFingerprint = workroom.actors.find((a) => a.name === session.actor)?.fingerprint;
  const nameOf = (fp: string) =>
    byFingerprint.get(fp) ??
    projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fp)?.body?.name ??
    fp.slice(0, 8);

  // Counts completed frame deliveries: the first one is the state of the room
  // as we opened it, so nothing in it should knock (title-flash below).
  const frameDeliveries = useRef(0);
  useEffect(() => {
    let stopped = false;
    Promise.all(
      conversations.map((c) =>
        api
          .frames(c)
          .then(async (raw: Frame[]) =>
            Promise.all(
              raw.map(async (frame) => {
                const view = decodeFrame(frame, workroom.actors);
                const fp = await fingerprintOfKey(frame.ActorKey).catch(() => "");
                return { ...view, actor: byFingerprint.get(fp) ?? view.actor };
              }),
            ),
          )
          .catch(() => [] as FrameView[]),
      ),
    ).then((groups) => {
      if (stopped) return;
      frameDeliveries.current += 1;
      setFrames(groups.flat());
    });
    return () => {
      stopped = true;
    };
  }, [conversations.join(","), livePosition, workroom.actors, byFingerprint]);

  // Personal memory: append what this session witnessed to a local, capped
  // transcript. Local only — the room's amnesia is the contract; this is mine.
  useEffect(() => {
    if (!genesis || frames.length === 0) return;
    const now = Date.now();
    rememberFrames(
      genesis,
      frames.map((f) => ({
        key: `${f.conversation}:${f.sequence}`,
        actor: f.actor,
        text: f.text,
        at: firstSeenRef.current.get(`f:${f.conversation}:${f.sequence}`) ?? now,
      })),
    );
  }, [genesis, frames]);

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
    const fresh = frames.filter((f) => !flashSeen.current.has(`${f.conversation}:${f.sequence}`));
    for (const f of fresh) flashSeen.current.add(`${f.conversation}:${f.sequence}`);
    if (frameDeliveries.current <= 1) return;
    const addressed = fresh.some((f) => f.actor !== session.actor && mentionsActor(f.text, session.actor));
    if (addressed && !document.hasFocus() && flashTimer.current === undefined) {
      let on = false;
      flashTimer.current = window.setInterval(() => {
        on = !on;
        document.title = on ? `● @${session.actor} — you're mentioned` : baseTitle.current;
      }, 1000);
    }
  }, [frames, session.actor]);
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

  // A one-flight, one-key guard per user intention: double-clicks and retries
  // reuse the same idempotency key, so at most one durable event results.
  const doAct = async (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => {
    if (inFlight.current.has(intent)) return;
    inFlight.current.add(intent);
    setActError(undefined);
    try {
      await api.act({ ...input, session: session.id, idempotency_key: intent });
    } catch (error) {
      setActError(error instanceof Error ? error.message : String(error));
    } finally {
      inFlight.current.delete(intent);
    }
  };

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

  // Jump the selection to a basis and bring its row into view.
  const jumpTo = (event: string) => {
    onJump({ kind: "event", id: event });
    const anchor = foldedTo.get(event) ?? event;
    requestAnimationFrame(() => document.getElementById("evt-" + anchor)?.scrollIntoView({ block: "center" }));
  };

  // Semantic actions never manufacture text — they open the composer in the
  // right mode with the basis prefilled, and the human says the words.
  const route = (mode: ComposerMode, basis: string, prefill: string) =>
    onComposer({ type: "say", mode, restsOn: [basis], frames: [], prefill });

  // The unified cite gesture: a chat line and a recorded act select the same
  // way, into the same tray; the system routes them (rests_on vs evidence)
  // at send. Citing anything from plain Say turns the draft into a Note.
  const typeAfterCite = composer.mode === undefined && composer.type === "say" ? ("assert" as const) : composer.type;
  const citeEvent = (event: string) => {
    const exists = composer.restsOn.includes(event);
    onComposer({
      ...composer,
      type: exists ? composer.type : typeAfterCite,
      restsOn: exists ? composer.restsOn.filter((e) => e !== event) : [...composer.restsOn, event],
    });
  };
  const citeFrame = (frame: FrameView) => {
    const exists = composer.frames.some((f) => f.conversation === frame.conversation && f.sequence === frame.sequence);
    onComposer({
      ...composer,
      type: exists ? composer.type : typeAfterCite,
      frames: exists
        ? composer.frames.filter((f) => !(f.conversation === frame.conversation && f.sequence === frame.sequence))
        : [...composer.frames, frame],
    });
  };

  const items = useMemo(() => {
    const order = orderRef.current;
    const place = (key: string) => {
      if (!order.has(key)) {
        order.set(key, counterRef.current++);
        if (key.startsWith("f:")) firstSeenRef.current.set(key, Date.now());
      }
      return order.get(key)!;
    };
    const list: { key: string; order: number; statement?: Statement; frame?: FrameView }[] = [];
    for (const statement of projection?.statements ?? []) {
      // promise/report cards fold into their request's card
      const commitment = commitmentByEvent.get(statement.event);
      if (commitment && statement.event !== commitment.request && decisions.get(statement.event)?.verdict === "effective") continue;
      list.push({ key: "e:" + statement.event, order: place("e:" + statement.event), statement });
    }
    for (const frame of frames) {
      const key = `f:${frame.conversation}:${frame.sequence}`;
      list.push({ key, order: place(key), frame });
    }
    return list.sort((a, b) => a.order - b.order);
  }, [projection, frames, commitmentByEvent, decisions]);

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
      if (s.kind !== "dissent") continue;
      const target = projection?.provenance[s.event]?.[0];
      if (target) attach(target, { dissent: s });
    }
    return map;
  }, [projection]);

  // Past local entries: what I witnessed that is no longer in the live room.
  const memory = useMemo(() => {
    if (!memoryOpen || !genesis) return [] as MemoryEntry[];
    const liveKeys = new Set(frames.map((f) => `${f.conversation}:${f.sequence}`));
    return loadMemory(genesis).filter((entry) => !liveKeys.has(entry.key));
  }, [memoryOpen, genesis, frames]);

  // Build the rendered sequence with client-side "seen" dividers between
  // gaps in arrival time, and consecutive same-actor messages grouped.
  const now = Date.now();
  const rendered: React.ReactNode[] = [];
  let previousFrame: { actor: string; seen: number } | undefined;
  for (const item of items) {
    if (item.frame) {
      const key = `f:${item.frame.conversation}:${item.frame.sequence}`;
      const seen = firstSeenRef.current.get(key) ?? now;
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
          selected={composer.frames.some((f) => f.conversation === item.frame!.conversation && f.sequence === item.frame!.sequence)}
          onToggle={() => citeFrame(item.frame!)}
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
      tickets,
      nameOf,
      me: myFingerprint,
      bright: highlight.events.has(statement.event),
      selected: selection?.kind === "event" && selection.id === statement.event,
      cited: composer.restsOn.includes(statement.event),
      onSelect: () => onSelect({ kind: "event", id: statement.event }),
      onJumpTo: jumpTo,
      onCite: () => citeEvent(statement.event),
      onRoute: route,
      doAct,
    };
    if (statementWeight(statement, projection, commitment) === "compact") {
      rendered.push(<CompactRow key={item.key} {...common} notes={annotations} />);
    } else {
      rendered.push(<Card key={item.key} {...common} notes={annotations} />);
    }
  }
  for (const say of pending) {
    rendered.push(
      <div key={"p:" + say.id} className="flex gap-3 rounded-md px-2 py-1 opacity-50">
        <span className="mt-1 h-3.5 w-3.5 shrink-0" />
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
          <div className="flex justify-end">
            <button
              onClick={() => setMemoryOpen((open) => !open)}
              aria-pressed={memoryOpen}
              className={cn(
                "flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs focus-visible:outline focus-visible:outline-accent",
                memoryOpen ? "text-muted" : "text-faint hover:text-muted",
              )}
            >
              <BookOpen className="h-3.5 w-3.5" />
              your memory
            </button>
          </div>
          {memoryOpen && (
            <div className="mb-3 rounded-lg border border-dashed border-border/70 px-3 py-2">
              <p className="mb-1.5 text-xs italic text-faint">your memory, not the room's — not citable</p>
              {memory.length === 0 && <p className="text-xs text-faint">nothing beyond what the room still holds</p>}
              <div className="max-h-56 space-y-0.5 overflow-y-auto">
                {memory.map((entry) => (
                  <p key={entry.key} className="text-sm text-faint">
                    <span className="mr-2 text-xs font-semibold">{entry.actor}</span>
                    {entry.text}
                  </p>
                ))}
              </div>
            </div>
          )}
          {items.length === 0 && pending.length === 0 && (
            <p className="py-10 text-center font-serif text-[15px] italic text-faint">
              A quiet room. Say something — or set something down for the record.
            </p>
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

// The one cite affordance, identical on chat lines and recorded acts: same
// icon, same size, same right-edge position, same hover reveal. Selecting is
// the same gesture everywhere; the system files the difference.
function CiteButton({ selected, onToggle, what }: { selected: boolean; onToggle: () => void; what: string }) {
  const label = selected ? "remove citation" : `cite this ${what} in your next message`;
  return (
    <button
      onClick={(e) => {
        e.stopPropagation();
        onToggle();
      }}
      aria-pressed={selected}
      aria-label={label}
      title={label}
      className={cn(
        "shrink-0 rounded p-1 transition-opacity focus-visible:opacity-100 focus-visible:outline focus-visible:outline-accent hover:bg-elevated hover:text-foreground",
        selected ? "text-accent opacity-100" : "text-faint opacity-0 group-hover:opacity-100 max-lg:opacity-60 pointer-coarse:opacity-60",
      )}
    >
      <CornerUpLeft className="h-3.5 w-3.5" />
    </button>
  );
}

// Free text with @name tokens highlighted; my own name glows warmer.
function MentionText({ text, known, myName, className }: { text: string; known: Set<string>; myName?: string; className?: string }) {
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

function MessageLine({
  frame,
  showActor,
  ageClass,
  known,
  myName,
  selected,
  onToggle,
}: {
  frame: FrameView;
  showActor: boolean;
  ageClass?: string;
  known: Set<string>;
  myName?: string;
  selected: boolean;
  onToggle: () => void;
}) {
  // Being addressed lights the whole line — someone else said my name.
  const addressed = frame.actor !== myName && mentionsActor(frame.text, myName);
  return (
    <div className={cn("group rounded-md px-2", selected ? "bg-accent/10" : addressed && "bg-info/10", ageClass)}>
      {showActor && <div className={cn("pt-1 text-xs font-semibold", actorTint(frame.actor))}>{frame.actor}</div>}
      <div className="flex gap-3 py-0.5">
        <div className="min-w-0 flex-1">
          <MentionText text={frame.text} known={known} myName={myName} className="text-sm leading-relaxed text-foreground/90" />
        </div>
        <CiteButton selected={selected} onToggle={onToggle} what="message" />
      </div>
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

// What a recorded act rests on, as ticket links — in-log bases only.
function RestsOn({ event, projection, tickets, onJumpTo, className }: { event: string; projection: Projection; tickets: Map<string, number>; onJumpTo: (event: string) => void; className?: string }) {
  const bases = (projection.provenance[event] ?? []).filter((basis) => tickets.has(basis));
  if (bases.length === 0) return null;
  return (
    <span className={cn("text-xs text-faint", className)}>
      rests on{" "}
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

// The uniform reply shortcuts — Agree/Disagree/Accept/Report done/Needs
// work/Withdraw — one shape, one size, one footer position on every row that
// has any. Authorization gating and the one-flight idempotency key are
// unchanged; only the rendering is unified.
function ActionsRow({
  statement,
  commitment,
  decision,
  projection,
  me,
  onRoute,
  doAct,
  className,
  withdrawable = true,
}: {
  statement: Statement;
  commitment?: Commitment;
  decision?: Decision;
  projection: Projection;
  me?: string;
  onRoute: (mode: ComposerMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
  className?: string;
  // Compact settled rows keep the reply shortcuts but not Withdraw: a
  // one-liner under every record you authored would drown the stream.
  withdrawable?: boolean;
}) {
  const [withdrawing, setWithdrawing] = useState(false);
  const [reason, setReason] = useState("");
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  // Already effectively ratified by me → agreeing again is meaningless; hide.
  const myRatify = projection.acts.some((a) => a.type === "ratify" && a.target === statement.event && a.actor === me && a.verdict === "effective");

  const actions: { label: string; tone?: "ok" | "danger"; run: () => void }[] = [];
  const key = (verb: string) => `${verb}:${statement.event}`;
  if (!dead && !ineffective) {
    if (statement.kind === "request" && commitment && !commitment.promise && me && statement.body?.to === me)
      actions.push({ label: "Accept", tone: "ok", run: () => onRoute("promise", statement.event, "I will do this.") });
    if (statement.kind === "propose") {
      if (!myRatify) actions.push({ label: "Agree", tone: "ok", run: () => doAct(key("ratify"), { act: "ratify", target: statement.event }) });
      actions.push({ label: "Disagree", tone: "danger", run: () => onRoute("dissent", statement.event, "") });
    }
    if (commitment?.promise && me === commitment.performer && commitment.status === "promised")
      actions.push({ label: "Report done", tone: "ok", run: () => onRoute("report", commitment.promise!, "") });
    if (commitment?.report && me === commitment.requester && commitment.status === "reported") {
      actions.push({ label: "Accept", tone: "ok", run: () => doAct(key("satisfy"), { act: "ratify", target: commitment.report! }) });
      actions.push({ label: "Needs work", tone: "danger", run: () => onRoute("dissent", commitment.report!, "") });
    }
    if (withdrawable && me === statement.actor && !withdrawing) actions.push({ label: "Withdraw", tone: "danger", run: () => setWithdrawing(true) });
  }
  if (actions.length === 0 && !withdrawing) return null;

  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {!withdrawing &&
        actions.map((action) => (
          <button
            key={action.label}
            onClick={action.run}
            className={cn(
              "rounded-md border px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-accent",
              action.tone === "ok" && "border-ok/40 text-ok hover:bg-ok/10",
              action.tone === "danger" && "border-danger/40 text-danger hover:bg-danger/10",
              !action.tone && "border-border text-muted hover:bg-elevated",
            )}
          >
            {action.label}
          </button>
        ))}
      {withdrawing && (
        <>
          <input
            autoFocus
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                doAct(`supersede:${statement.event}`, { act: "supersede", target: statement.event, text: reason || "withdrawn" });
                setWithdrawing(false);
              }
              if (e.key === "Escape") setWithdrawing(false);
            }}
            placeholder="why — visible forever"
            className="min-w-0 flex-1 rounded-md border border-input bg-surface px-2.5 py-1 text-xs outline-none placeholder:text-faint focus:border-danger/60"
          />
          <Undo2 className="h-3.5 w-3.5 text-danger" />
        </>
      )}
    </div>
  );
}

// Weight (b): a settled record entry — kind tag, text, ticket. One line, no
// card chrome; its basis, mentions, reply shortcuts, and (if stale) the
// explanation ride quietly below or beside it.
function CompactRow({
  statement,
  ticket,
  decision,
  commitment,
  projection,
  tickets,
  notes,
  nameOf,
  me,
  bright,
  selected,
  cited,
  onSelect,
  onJumpTo,
  onCite,
  onRoute,
  doAct,
}: {
  statement: Statement;
  ticket?: number;
  decision?: Decision;
  commitment?: Commitment;
  projection: Projection;
  tickets: Map<string, number>;
  notes: Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>;
  nameOf: (fp: string) => string;
  me?: string;
  bright: boolean;
  selected: boolean;
  cited: boolean;
  onSelect: () => void;
  onJumpTo: (event: string) => void;
  onCite: () => void;
  onRoute: (mode: ComposerMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
}) {
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  const ratifiedNote = (notes.get(statement.event) ?? []).some((n) => n.act?.type === "ratify" && n.act.verdict === "effective");
  return (
    <div
      id={"evt-" + statement.event}
      className={cn(
        "group rounded-md px-2 py-1",
        selected ? "bg-accent/10" : bright && "bg-accent/5",
      )}
    >
      <div className="flex items-baseline gap-2">
        <button
          onClick={onSelect}
          className={cn("shrink-0 rounded border px-1.5 py-px text-xs font-medium uppercase tracking-wide focus-visible:outline focus-visible:outline-accent", kindTint[statement.kind] ?? "border-border text-muted")}
        >
          {kindLabel[statement.kind] ?? statement.kind}
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
        <span className="ml-auto flex shrink-0 items-center gap-1 self-center">
          <RestsOn event={statement.event} projection={projection} tickets={tickets} onJumpTo={onJumpTo} className="hidden sm:inline" />
          <Ticket ticket={ticket} event={statement.event} onSelect={onSelect} />
          <CiteButton selected={cited} onToggle={onCite} what="act" />
        </span>
      </div>
      {statement.stale && !dead && <WhyStale event={statement.event} projection={projection} tickets={tickets} nameOf={nameOf} onJumpTo={onJumpTo} />}
      <ActionsRow
        statement={statement}
        commitment={commitment}
        decision={decision}
        projection={projection}
        me={me}
        onRoute={onRoute}
        doAct={doAct}
        className="mt-1"
        withdrawable={false}
      />
    </div>
  );
}

// Weight (c): the rich card — only for what still awaits a response.
// Semantic next actions, not wire verbs. The fold still judges everything.
function Card({
  statement,
  ticket,
  decision,
  commitment,
  projection,
  tickets,
  notes,
  nameOf,
  me,
  bright,
  selected,
  cited,
  onSelect,
  onJumpTo,
  onCite,
  onRoute,
  doAct,
}: {
  statement: Statement;
  ticket?: number;
  decision?: Decision;
  commitment?: Commitment;
  projection: Projection;
  tickets: Map<string, number>;
  notes: Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>;
  nameOf: (fp: string) => string;
  me?: string;
  bright: boolean;
  selected: boolean;
  cited: boolean;
  onSelect: () => void;
  onJumpTo: (event: string) => void;
  onCite: () => void;
  onRoute: (mode: ComposerMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
}) {
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  const chain = commitment ? chainOf(commitment, projection) : [];
  const tallies = statement.kind === "propose" ? tallyOf(statement.event, notes) : undefined;

  return (
    <div
      id={"evt-" + statement.event}
      className={cn(
        "group rise my-1.5 rounded-lg border bg-card px-4 py-3",
        selected ? "border-accent/70" : bright ? "border-accent/40" : "border-border",
        statement.stale && !dead && "border-l-2 border-l-danger",
        ineffective && "border-dashed opacity-80",
      )}
    >
      <div className="flex items-center gap-2">
        <button onClick={onSelect} className={cn("shrink-0 rounded border px-1.5 py-px text-xs font-medium uppercase tracking-wide focus-visible:outline focus-visible:outline-accent", kindTint[statement.kind] ?? "border-border text-muted")}>
          {kindLabel[statement.kind] ?? statement.kind}
        </button>
        <span className="text-xs text-faint">
          {nameOf(statement.actor)}
          {statement.body?.to && <> → {nameOf(statement.body.to)}</>}
        </span>
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
          <CiteButton selected={cited} onToggle={onCite} what="act" />
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

      {chain.length > 0 && (
        <div className="mt-2 space-y-1 border-l border-border pl-3">
          {chain.map((step) => (
            <div key={step.event} className="text-xs text-muted">
              <span className="mr-1.5 font-medium text-foreground/80">{kindLabel[step.kind] ?? step.kind}</span>
              {step.text} <span className="text-faint">— {nameOf(step.actor)}</span>
              <Ticket ticket={tickets.get(step.event)} event={step.event} onSelect={() => onJumpTo(step.event)} className="ml-1.5" />
            </div>
          ))}
        </div>
      )}
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
      <Notes notes={notes.get(statement.event) ?? []} nameOf={nameOf} />
      <ActionsRow
        statement={statement}
        commitment={commitment}
        decision={decision}
        projection={projection}
        me={me}
        onRoute={onRoute}
        doAct={doAct}
        className="mt-2.5"
      />
    </div>
  );
}

function Notes({ notes, nameOf }: { notes: { act?: Projection["acts"][number]; dissent?: Statement }[]; nameOf: (f: string) => string }) {
  if (notes.length === 0) return null;
  return (
    <div className="mt-2 space-y-1 border-l border-border pl-3">
      {notes.map((note, i) => {
        if (note.dissent)
          return (
            <div key={i} className="flex items-start gap-1.5 text-xs text-danger">
              <MessageSquareX className="mt-0.5 h-3 w-3 shrink-0" />
              <span>
                {nameOf(note.dissent.actor)} disagrees: <span className="text-muted">{note.dissent.text}</span>
              </span>
            </div>
          );
        const act = note.act!;
        if (act.verdict === "effective" && act.type === "ratify")
          return (
            <div key={i} className="flex items-center gap-1.5 text-xs text-ok">
              <BadgeCheck className="h-3 w-3 shrink-0" /> agreed by {nameOf(act.actor)}
            </div>
          );
        if (act.verdict === "effective")
          return (
            <div key={i} className="flex items-start gap-1.5 text-xs text-danger">
              <Undo2 className="mt-0.5 h-3 w-3 shrink-0" />
              <span>
                withdrawn by {nameOf(act.actor)}
                {act.text && <span className="text-muted"> — {act.text}</span>}
              </span>
            </div>
          );
        return (
          <div key={i} className={cn("flex items-start gap-1.5 text-xs", act.verdict === "disputed" ? "text-danger" : "text-accent-deep")}>
            <CircleSlash className="mt-0.5 h-3 w-3 shrink-0" />
            <span>
              {nameOf(act.actor)} tried to {act.type} — {act.reason}
            </span>
          </div>
        );
      })}
    </div>
  );
}

function chainOf(commitment: Commitment, projection: Projection): Statement[] {
  const chain: Statement[] = [];
  for (const event of [commitment.promise, commitment.report]) {
    if (!event) continue;
    const statement = projection.statements.find((s) => s.event === event);
    if (statement) chain.push(statement);
  }
  return chain;
}

function tallyOf(event: string, notes: Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>) {
  const list = notes.get(event) ?? [];
  return {
    up: list.filter((n) => n.act?.type === "ratify" && n.act.verdict === "effective").length,
    down: list.filter((n) => n.dissent).length,
  };
}
