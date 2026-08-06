import { useEffect, useMemo, useRef, useState } from "react";
import { BadgeCheck, CircleSlash, CornerUpLeft, FileWarning, MessageSquareX, ThumbsUp, Undo2 } from "lucide-react";
import { api, decodeFrame, type ActInput, type Commitment, type Decision, type Frame, type FrameView, type Projection, type Statement } from "../lib/api";
import { shortEvent } from "../lib/api";
import type { Workroom, Selection } from "../lib/store";
import type { Session } from "../lib/session";
import { actorTint, cn, fingerprintOfKey, kindTint, statusTint } from "../lib/util";
import type { ComposerContext } from "./Composer";

// One stream, three weights: plain talk, occasional formal cards, and each
// conversation-for-action loop folded into a single card (its promise,
// report, and satisfaction are the thread, not separate cards).
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
  const [actError, setActError] = useState<string>();
  const inFlight = useRef(new Set<string>());
  const orderRef = useRef(new Map<string, number>());
  const counterRef = useRef(0);
  const scroller = useRef<HTMLDivElement>(null);

  const byFingerprint = useMemo(() => new Map(workroom.actors.map((a) => [a.fingerprint, a.name])), [workroom.actors]);
  const myFingerprint = workroom.actors.find((a) => a.name === session.actor)?.fingerprint;
  const nameOf = (fp: string) =>
    byFingerprint.get(fp) ??
    projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fp)?.body?.name ??
    fp.slice(0, 8);

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
    ).then((groups) => !stopped && setFrames(groups.flat()));
    return () => {
      stopped = true;
    };
  }, [conversations.join(","), livePosition, workroom.actors, byFingerprint]);

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

  const items = useMemo(() => {
    const order = orderRef.current;
    const place = (key: string) => {
      if (!order.has(key)) order.set(key, counterRef.current++);
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

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 240) requestAnimationFrame(() => el.scrollTo({ top: 1e9 }));
  }, [items.length]);

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

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="mx-auto max-w-3xl space-y-1">
          {items.length === 0 && (
            <p className="py-10 text-center font-serif text-[15px] italic text-faint">
              A quiet room. Say something — or set something down for the record.
            </p>
          )}
          {items.map((item) =>
            item.frame ? (
              <MessageLine
                key={item.key}
                frame={item.frame}
                selected={composer.frames.some((f) => f.conversation === item.frame!.conversation && f.sequence === item.frame!.sequence)}
                onToggle={() => {
                  const f = item.frame!;
                  const exists = composer.frames.some((x) => x.conversation === f.conversation && x.sequence === f.sequence);
                  onComposer({
                    ...composer,
                    setDown: composer.setDown || !exists,
                    frames: exists
                      ? composer.frames.filter((x) => !(x.conversation === f.conversation && x.sequence === f.sequence))
                      : [...composer.frames, f],
                  });
                }}
              />
            ) : item.statement ? (
              <Card
                key={item.key}
                statement={item.statement}
                decision={decisions.get(item.statement.event)}
                commitment={commitmentByEvent.get(item.statement.event)}
                projection={projection!}
                notes={annotations}
                nameOf={nameOf}
                me={myFingerprint}
                bright={highlight.events.has(item.statement.event)}
                selected={selection?.kind === "event" && selection.id === item.statement.event}
                onSelect={() => onSelect({ kind: "event", id: item.statement!.event })}
                onReply={(event) =>
                  onComposer({ ...composer, setDown: true, restsOn: composer.restsOn.includes(event) ? composer.restsOn : [...composer.restsOn, event] })
                }
                doAct={doAct}
              />
            ) : null,
          )}
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

function MessageLine({ frame, selected, onToggle }: { frame: FrameView; selected: boolean; onToggle: () => void }) {
  return (
    <div className={cn("group flex gap-3 rounded-md px-2 py-1", selected && "bg-accent/10")}>
      <button
        onClick={onToggle}
        aria-pressed={selected}
        aria-label={selected ? "remove from evidence" : "select as evidence for a formal act"}
        title={selected ? "remove from evidence" : "select as evidence"}
        className={cn(
          "mt-1 h-3.5 w-3.5 shrink-0 rounded-sm border transition-opacity focus-visible:opacity-100 focus-visible:outline focus-visible:outline-accent",
          selected ? "border-accent bg-accent/70 opacity-100" : "border-border opacity-0 group-hover:opacity-100",
        )}
      />
      <div className="min-w-0">
        <span className={cn("mr-2 text-xs font-semibold", actorTint(frame.actor))}>{frame.actor}</span>
        <span className="text-[14px] leading-relaxed text-foreground/90">{frame.text}</span>
      </div>
    </div>
  );
}

// Semantic next actions, not wire verbs. The fold still judges everything.
function Card({
  statement,
  decision,
  commitment,
  projection,
  notes,
  nameOf,
  me,
  bright,
  selected,
  onSelect,
  onReply,
  doAct,
}: {
  statement: Statement;
  decision?: Decision;
  commitment?: Commitment;
  projection: Projection;
  notes: Map<string, { act?: Projection["acts"][number]; dissent?: Statement }[]>;
  nameOf: (fp: string) => string;
  me?: string;
  bright: boolean;
  selected: boolean;
  onSelect: () => void;
  onReply: (event: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
}) {
  const [withdrawing, setWithdrawing] = useState(false);
  const [reason, setReason] = useState("");
  const governance = ["roster", "infra-key", "seal"].includes(statement.kind);
  if (governance) {
    return <div className="px-2 py-0.5 text-center text-[11px] text-faint">— {statement.text} —</div>;
  }
  const dead = statement.retired;
  const ineffective = decision && decision.verdict !== "effective";
  const chain = commitment ? chainOf(commitment, projection) : [];
  const tallies = statement.kind === "propose" ? tallyOf(statement.event, notes) : undefined;

  const actions: { label: string; intent: string; input: Omit<ActInput, "session" | "idempotency_key">; tone?: "ok" | "danger" }[] = [];
  const key = (verb: string) => `${verb}:${statement.event}`;
  if (!dead && !ineffective) {
    if (statement.kind === "request" && commitment && !commitment.promise && me && statement.body?.to === me)
      actions.push({ label: "Accept", intent: key("promise"), tone: "ok", input: { act: "state", kind: "promise", text: "I will.", rests_on: [statement.event] } });
    if (statement.kind === "propose") {
      actions.push({ label: "Agree", intent: key("ratify"), tone: "ok", input: { act: "ratify", target: statement.event } });
      actions.push({ label: "Disagree", intent: key("dissent"), tone: "danger", input: { act: "state", kind: "dissent", text: "I disagree.", rests_on: [statement.event] } });
    }
    if (commitment?.promise && me === fingerprintOfActor(commitment.performer) && commitment.status === "promised")
      actions.push({ label: "Report done", intent: key("report"), tone: "ok", input: { act: "state", kind: "report", text: "Done as requested.", rests_on: [commitment.promise] } });
    if (commitment?.report && me === commitment.requester && commitment.status === "reported")
      actions.push({ label: "Accept", intent: key("satisfy"), tone: "ok", input: { act: "ratify", target: commitment.report } });
    if (me === statement.actor && !withdrawing) actions.push({ label: "Withdraw", intent: "ui:withdraw", tone: "danger", input: { act: "state" } });
  }

  return (
    <div
      className={cn(
        "group rise my-1.5 rounded-lg border bg-card px-4 py-3",
        selected ? "border-accent/70" : bright ? "border-accent/40" : "border-border",
        statement.stale && !dead && "border-l-2 border-l-danger",
        ineffective && "border-dashed opacity-80",
      )}
    >
      <div className="flex items-center gap-2">
        <button onClick={onSelect} className={cn("shrink-0 rounded border px-1.5 py-px text-[10px] font-medium uppercase tracking-wide focus-visible:outline focus-visible:outline-accent", kindTint[statement.kind] ?? "border-border text-muted")}>
          {statement.kind}
        </button>
        <span className="text-xs text-faint">
          {nameOf(statement.actor)}
          {statement.body?.to && <> → {nameOf(statement.body.to)}</>}
        </span>
        {statement.ratified && <BadgeCheck aria-label="ratified" className="h-3.5 w-3.5 text-ok" />}
        {commitment && <span className={cn("text-[11px] font-semibold", statusTint[commitment.status])}>{commitment.status}</span>}
        {statement.stale && !dead && (
          <span className="flex items-center gap-1 text-[10px] font-medium uppercase text-danger">
            <FileWarning className="h-3 w-3" /> stale
          </span>
        )}
        <button
          onClick={() => onReply(statement.event)}
          aria-label="reply — a new act resting on this"
          title="reply — a new act resting on this"
          className="ml-auto rounded p-1 text-faint opacity-0 transition-opacity focus-visible:opacity-100 focus-visible:outline focus-visible:outline-accent group-hover:opacity-100 hover:bg-elevated hover:text-foreground"
        >
          <CornerUpLeft className="h-3.5 w-3.5" />
        </button>
      </div>
      {ineffective && (
        <p className={cn("mt-1 flex items-center gap-1.5 text-xs", decision!.verdict === "disputed" ? "text-danger" : "text-accent-deep")}>
          <CircleSlash className="h-3.5 w-3.5 shrink-0" />
          not in force — {decision!.reason}
        </p>
      )}
      <p className={cn("mt-1.5 font-serif text-[16px] leading-relaxed", dead ? "text-faint line-through" : "text-foreground")}>{statement.text}</p>
      <div className="mt-1 flex items-center gap-3 text-[11px] text-faint">
        {statement.body?.conditions && <span>satisfied when: {statement.body.conditions}</span>}
        {statement.body?.path && (
          <span className="text-teal">
            {statement.body.path}@{statement.body.commit?.slice(0, 8)}
          </span>
        )}
        <code className="opacity-0 transition-opacity group-hover:opacity-100">{shortEvent(statement.event)}</code>
      </div>

      {chain.length > 0 && (
        <div className="mt-2 space-y-1 border-l border-border pl-3">
          {chain.map((step) => (
            <div key={step.event} className="text-xs text-muted">
              <span className={cn("mr-1.5 font-medium", kindTint[step.kind]?.split(" ")[0])}>{step.kind}</span>
              {step.text} <span className="text-faint">— {nameOf(step.actor)}</span>
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
      {(actions.length > 0 || withdrawing) && (
        <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
          {!withdrawing &&
            actions.map((action) => (
              <button
                key={action.label}
                onClick={() => (action.intent === "ui:withdraw" ? setWithdrawing(true) : doAct(action.intent, action.input))}
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
      )}
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

// Commitment.performer is already a fingerprint in the projection.
function fingerprintOfActor(performer?: string): string | undefined {
  return performer;
}
