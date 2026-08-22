import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, CircleSlash, SendHorizonal, Undo2 } from "lucide-react";
import { api, type ActInput, type FrameView, type Landing, type Statement } from "../lib/api";
import { ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { buildSpine, type Station } from "../lib/spine";
import { age } from "../lib/rows";
import { eventDiscussionEntries, RetryKeys, sendTemporaryReply } from "../lib/interaction";
import { soleCurrentSupersedeBasis } from "../lib/supersedeLinks";
import { mentionFingerprints } from "../lib/mentions";
import { actorTint, clock, cn, firstLine, interpretationNotice, kindLabel } from "../lib/util";
import { RowToolbar, ToolbarButton, semanticActions, type SemanticReplyMode } from "./Toolbar";
import { RecordDetail } from "./RecordDetail";

export interface PendingSay {
  id: string;
  text: string;
  at: number;
  re?: string;
  about?: string;
}

// The thread: one vertical rail down the left of the spine rows, one node per
// row, at that row's own vertical position. The spine and the railway are one
// thing, not two — an earlier draft listed the records and then drew the same
// records again above them, which is the same fact rendered twice.
export function Thread({
  workroom,
  session,
  frames,
  root,
  pending,
  onBack,
  onOpenThread,
  onSay,
  onSayFailed,
  doAct,
  actError,
}: {
  workroom: Workroom;
  session: Session;
  frames: FrameView[];
  root: string;
  pending: PendingSay[];
  onBack: () => void;
  onOpenThread: (event: string) => void;
  onSay: (text: string, re?: string, about?: string) => string;
  onSayFailed: (id: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "credential" | "idempotency_key">) => void;
  actError?: string;
}) {
  const projection = workroom.status?.durable.projection;
  const tickets = useMemo(() => ticketsOf(projection), [projection]);
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((actor) => [actor.fingerprint, actor.name]));
    return (fingerprint: string) =>
      byFingerprint.get(fingerprint) ?? projection?.actors?.[fingerprint]?.name ?? fingerprint.slice(0, 8);
  }, [projection, workroom.actors]);
  const me = workroom.actors.find((actor) => actor.name === session.actor)?.fingerprint;
  const discussion = useMemo(() => eventDiscussionEntries(root, frames), [frames, root]);

  const [landings, setLandings] = useState<{ branch: string; commits: Map<string, Landing> }>();
  const spine = useMemo(
    () =>
      projection
        ? buildSpine(root, {
            projection,
            tickets,
            nameOf,
            landings: landings?.commits,
            branch: landings?.branch,
            talk: discussion.length,
          })
        : undefined,
    [projection, root, tickets, nameOf, landings, discussion.length],
  );

  // The merge station asks git, and asking is a round trip. The rail says it
  // is asking until the answer lands, and never guesses in the meantime.
  const head = spine?.head;
  useEffect(() => {
    let stopped = false;
    if (!head) return;
    api
      .landed([head])
      .then((answer) => {
        if (stopped) return;
        setLandings({ branch: answer.branch, commits: new Map(answer.commits.map((c) => [c.commit, c])) });
      })
      // A check that fails must not read as a negative. Leaving the answer
      // absent keeps the station saying "asking", never "did not land".
      .catch(() => {});
    return () => {
      stopped = true;
    };
  }, [head]);

  const request = projection?.statements.find((statement) => statement.event === root);
  const [open, setOpen] = useState<Set<string>>(() => new Set());
  const [route, setRoute] = useState<{ id: string; mode: SemanticReplyMode; basis: string; prefill: string }>();
  const toggle = (id: string) =>
    setOpen((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  if (!projection || !request || !spine) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <ThreadHeader title="Gone" onBack={onBack} />
        <p className="px-4 py-8 text-xs text-faint">This thread is not in the projection.</p>
      </div>
    );
  }

  const actionsFor = (statement?: Statement) =>
    statement
      ? semanticActions({
          statement,
          commitment: projection.commitments.find((c) => [c.request, c.promise, c.report].includes(statement.event)),
          decision: projection.decisions.find((d) => d.event === statement.event),
          projection,
          me,
          onRoute: (mode, basis, prefill) => setRoute({ id: crypto.randomUUID(), mode, basis, prefill }),
          doAct,
        })
      : [];

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ThreadHeader title={`#${tickets.get(root) ?? "?"} · ${firstLine(request.text)}`} onBack={onBack} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-4xl px-4 py-3 sm:px-6">
          <ol className="list-none" aria-label="Commitment spine">
            {spine.stations.map((station, index) => (
              <SpineRow
                key={station.id}
                station={station}
                first={index === 0}
                last={index === spine.stations.length - 1}
                nameOf={nameOf}
                actions={actionsFor(projection.statements.find((s) => s.event === station.event))}
                open={open.has(`detail:${station.id}`)}
                onToggle={() => toggle(`detail:${station.id}`)}
                detail={
                  <RecordDetail
                    event={station.event}
                    commit={station.commit}
                    projection={projection}
                    tickets={tickets}
                    nameOf={nameOf}
                    onOpenThread={onOpenThread}
                  />
                }
              />
            ))}
          </ol>

          <div className="mt-3">
            {spine.expanders.map((expander) => (
              <div key={expander.id} className="border-t border-border">
                <button
                  type="button"
                  aria-expanded={open.has(expander.id)}
                  onClick={() => toggle(expander.id)}
                  className="flex w-full items-baseline gap-2 px-1 py-1.5 text-left text-xs text-muted hover:text-foreground focus-visible:outline focus-visible:outline-accent"
                >
                  <span aria-hidden className="text-faint">{open.has(expander.id) ? "▾" : "▸"}</span>
                  <span>{expander.label}</span>
                  <span className="font-mono text-faint">{expander.events.length}</span>
                  <span className="truncate text-faint">{expander.hint}</span>
                </button>
                {open.has(expander.id) && (
                  <div className="space-y-0.5 pb-2 pl-6">
                    {expander.events.map((event) => (
                      <ElidedRecord
                        key={event}
                        event={event}
                        projection={projection}
                        ticket={tickets.get(event)}
                        nameOf={nameOf}
                        onOpenThread={onOpenThread}
                        open={open.has(`detail:${event}`)}
                        onToggle={() => toggle(`detail:${event}`)}
                        detail={<RecordDetail event={event} projection={projection} tickets={tickets} nameOf={nameOf} onOpenThread={onOpenThread} />}
                      />
                    ))}
                    {expander.id === "talk" &&
                      discussion.map(({ frame, depth }) => (
                        <p
                          key={`${frame.conversation}:${frame.sequence}`}
                          style={{ marginLeft: `${Math.min(depth, 3) * 0.75}rem` }}
                          className="text-xs text-muted"
                        >
                          <span className={cn("font-semibold", actorTint(frame.actor))}>{frame.actor}</span>{" "}
                          <span className="text-faint">{clock(frame.seen)}</span> {frame.text}
                        </p>
                      ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
      <Composer
        workroom={workroom}
        session={session}
        root={root}
        route={route}
        onClearRoute={() => setRoute(undefined)}
        pending={pending.filter((say) => say.about === root && !say.re)}
        onSay={onSay}
        onSayFailed={onSayFailed}
        actError={actError}
      />
    </div>
  );
}

function ThreadHeader({ title, onBack }: { title: string; onBack: () => void }) {
  return (
    <div className="flex items-center gap-2 border-b border-border px-4 py-2 sm:px-6">
      <button
        type="button"
        onClick={onBack}
        className="flex items-center gap-1 rounded px-1 py-0.5 text-xs text-muted hover:text-foreground focus-visible:outline focus-visible:outline-accent"
      >
        <ArrowLeft className="h-3.5 w-3.5" /> requests
      </button>
      <h2 className="min-w-0 flex-1 truncate text-xs text-foreground/90">{title}</h2>
    </div>
  );
}

// One rail node at this row's own vertical position, with the rail drawn
// through the gutter. A branch — the blocker — leaves the rail sideways beside
// the row it concerns, which is exactly where a reader is already looking.
// Clicking the row opens the record's full detail beneath it; clicking again
// closes it. The node and rail hang from the row's top, so the detail can
// grow below without moving the node.
function SpineRow({
  station,
  first,
  last,
  nameOf,
  actions,
  open,
  onToggle,
  detail,
}: {
  station: Station;
  first: boolean;
  last: boolean;
  nameOf: (fingerprint: string) => string;
  actions: { label: string; symbol: string; tone?: "ok" | "danger"; run: () => void }[];
  open: boolean;
  onToggle: () => void;
  detail: React.ReactNode;
}) {
  const dim = !station.present;
  const detailed = Boolean(station.event || station.commit);
  return (
    <li
      data-station={station.id}
      data-present={station.present}
      aria-expanded={detailed ? open : undefined}
      onClick={detailed ? onToggle : undefined}
      className={cn("group relative text-xs", station.branch && "ml-5", detailed && "cursor-pointer")}
    >
      {!station.branch && (
        <span
          aria-hidden
          className="absolute left-[0.8rem] -ml-px w-0.5 bg-border"
          style={{ top: first ? "1.05rem" : 0, bottom: last ? "calc(100% - 1.05rem)" : 0 }}
        />
      )}
      {station.branch && (
        <span aria-hidden className="absolute -left-2 -top-2 h-5 w-4 rounded-bl-lg border-b-2 border-l-2 border-danger" />
      )}
      <span className="absolute left-0 top-0 h-[2.1rem] w-[1.6rem]">
        <span
          aria-hidden
          className={cn(
            "absolute left-1/2 top-1/2 -ml-[5.5px] -mt-[5.5px] h-[11px] w-[11px] rounded-full border-2 bg-background",
            station.tone === "ok" && "border-[3px] border-ok",
            station.tone === "danger" && "border-[3px] border-danger",
            !station.tone && (station.present ? "border-muted" : "border-dashed border-faint bg-transparent"),
          )}
        />
      </span>
      <div className="grid min-h-[2.1rem] items-center gap-x-2" style={{ gridTemplateColumns: "1.6rem 5rem 3.5rem 1fr auto" }}>
        <span />
        <span className={cn("text-[11px] uppercase tracking-[0.07em]", dim ? "text-faint" : "text-faint")}>{station.kind}</span>
        <span className="font-mono text-[11px] text-faint">
          {station.ticket ? `#${station.ticket}` : station.commit ? station.commit.slice(0, 8) : ""}
        </span>
        <span
          className={cn(
            station.branch ? "font-semibold text-danger" : "truncate",
            dim && !station.branch && "italic text-faint",
            !dim && !station.branch && "text-foreground/90",
          )}
        >
          {station.what}
          {station.actor && !station.branch && <span className="ml-1 text-faint">· {nameOf(station.actor)}</span>}
        </span>
        <span className="font-mono text-[11px] text-faint">{station.timestamp ? age(station.timestamp) : ""}</span>
      </div>
      {actions.length > 0 && (
        <RowToolbar>
          {actions.map((action) => (
            <ToolbarButton key={action.label} label={action.label} icon={<span aria-hidden>{action.symbol}</span>} tone={action.tone} onClick={action.run} />
          ))}
        </RowToolbar>
      )}
      {detailed && open && <div className="mb-2 ml-[1.6rem] cursor-auto">{detail}</div>}
    </li>
  );
}

// A record behind an expander. Anything the fold ruled ineffective is marked
// where it appears rather than given a bucket of its own. Clicking the line
// opens the record's full detail beneath it; clicking again closes it.
function ElidedRecord({
  event,
  projection,
  ticket,
  nameOf,
  onOpenThread,
  open,
  onToggle,
  detail,
}: {
  event: string;
  projection: import("../lib/api").Projection;
  ticket?: number;
  nameOf: (fingerprint: string) => string;
  onOpenThread: (event: string) => void;
  open: boolean;
  onToggle: () => void;
  detail: React.ReactNode;
}) {
  const statement = projection.statements.find((candidate) => candidate.event === event);
  const act = projection.acts.find((candidate) => candidate.event === event);
  const decision = projection.decisions.find((candidate) => candidate.event === event);
  const gap = interpretationNotice(decision?.verdict, decision?.reason);
  // What a supersession put in the retired record's place, when exactly one
  // current basis says. Neutral wording on purpose: naming a successor is not
  // a claim that it replaces anything.
  const linked = act ? soleCurrentSupersedeBasis(act, projection) : undefined;
  const ineffective = decision && decision.verdict !== "effective";
  const text = statement?.text ?? act?.text ?? (act ? `${act.type}d` : "");
  const actor = statement?.actor ?? act?.actor ?? "";
  const stop = (run: () => void) => (e: React.MouseEvent) => {
    e.stopPropagation();
    run();
  };
  return (
    <div data-record={event} aria-expanded={open} onClick={onToggle} className="cursor-pointer">
      <div className="flex items-baseline gap-2 text-xs">
        <button
          type="button"
          onClick={stop(() => onOpenThread(event))}
          title={event}
          className="shrink-0 font-mono text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
        >
          {ticket ? `#${ticket}` : "—"}
        </button>
        <span className="shrink-0 text-[11px] uppercase tracking-wide text-faint">
          {kindLabel(statement?.kind ?? act?.type ?? "record")}
        </span>
        <span className={cn("min-w-0 flex-1 truncate", statement?.retired ? "text-faint line-through" : "text-muted")}>
          {firstLine(text)}
        </span>
        {linked && (
          <button
            type="button"
            onClick={stop(() => onOpenThread(linked))}
            title={linked}
            className="shrink-0 text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
          >
            linked item
          </button>
        )}
        {statement?.describes_superseded_world && <span className="shrink-0 text-[11px] font-semibold text-danger">superseded world</span>}
        {ineffective && <span className="shrink-0 text-[11px] text-danger" title={decision?.reason}>{gap ? gap.verdict : "ineffective"}</span>}
        {actor && <span className="shrink-0 text-faint">{nameOf(actor)}</span>}
      </div>
      {open && <div className="mb-2 mt-1 cursor-auto">{detail}</div>}
    </div>
  );
}

// Acting is why the operator looked. The composer is the only one on the
// screen and the only place a durable reply is written.
function Composer({
  workroom,
  session,
  root,
  route,
  onClearRoute,
  pending,
  onSay,
  onSayFailed,
  actError,
}: {
  workroom: Workroom;
  session: Session;
  root: string;
  route?: { id: string; mode: SemanticReplyMode; basis: string; prefill: string };
  onClearRoute: () => void;
  pending: PendingSay[];
  onSay: (text: string, re?: string, about?: string) => string;
  onSayFailed: (id: string) => void;
  actError?: string;
}) {
  type ReplyType = "say" | "assert" | SemanticReplyMode;
  const [type, setType] = useState<ReplyType>("say");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const retryKeys = useRef(new RetryKeys());
  const seenRoute = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (route && seenRoute.current !== route.id) {
      seenRoute.current = route.id;
      setType(route.mode);
      setText(route.prefill);
    }
  }, [route]);
  const durable = type !== "say";
  const basis = route && type === route.mode ? route.basis : root;

  const send = async () => {
    if (!text.trim() || busy || !session.actor) return;
    setBusy(true);
    setError(undefined);
    const line = text.trim();
    if (!durable) {
      setText("");
      try {
        await sendTemporaryReply(line, { about: root }, {
          optimistic: onSay,
          publish: (delivery, message) => api.say(session.credential, delivery.about, message, delivery.conversation, delivery.re),
          failed: onSayFailed,
        });
      } catch (thrown) {
        setText(line);
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
      const input: ActInput =
        type === "withdraw"
          ? { credential: session.credential, act: "supersede", target: basis, text: line, rests_on: [] }
          : {
              credential: session.credential,
              act: "state",
              kind: type,
              text: line,
              body: Object.keys(body).length ? body : undefined,
              rests_on: [basis],
            };
      const scope = `${root}:${type}`;
      const key = retryKeys.current.forAttempt(scope, JSON.stringify(input));
      await api.act({ ...input, idempotency_key: key });
      retryKeys.current.succeeded(scope, key);
      setText("");
      setType("say");
      onClearRoute();
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-border px-4 py-2 sm:px-6">
      <div className="mx-auto flex max-w-4xl flex-col gap-1.5">
        {pending.map((say) => (
          <p key={say.id} className="text-xs italic text-faint" aria-label="sending temporary reply">
            {say.text}
          </p>
        ))}
        {durable && (
          <div className="flex items-center gap-2">
            <span className="rounded-md border border-accent/40 bg-accent/10 px-2 py-0.5 text-[11px] text-foreground">{type}</span>
            <button
              type="button"
              onClick={() => {
                setType("say");
                setText("");
                onClearRoute();
              }}
              className="text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
            >
              cancel
            </button>
          </div>
        )}
        <div className="flex items-end gap-2">
          <textarea
            value={text}
            rows={1}
            placeholder={durable ? `${type}…` : "Reply…"}
            aria-label="thread reply"
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void send();
              }
            }}
            className="min-w-0 flex-1 resize-none rounded-lg border border-input bg-surface px-3 py-1.5 text-sm outline-none placeholder:text-faint focus:border-accent/60"
          />
          <button
            type="button"
            onClick={() => void send()}
            disabled={busy || !text.trim() || !session.live}
            aria-label={type === "withdraw" ? "withdraw" : durable ? "keep reply" : "send temporary reply"}
            title={session.live ? undefined : "not present yet"}
            className="flex h-8 w-8 items-center justify-center rounded-lg bg-accent text-background transition-colors hover:bg-accent/90 focus-visible:outline focus-visible:outline-accent disabled:opacity-40"
          >
            {type === "withdraw" ? <Undo2 className="h-3.5 w-3.5" /> : <SendHorizonal className="h-3.5 w-3.5" />}
          </button>
        </div>
        {(error || actError) && (
          <p role="alert" className="flex items-center gap-1 text-xs text-danger">
            <CircleSlash className="h-3 w-3" /> {error ?? actError}
          </p>
        )}
      </div>
    </div>
  );
}
