import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, CircleSlash, SendHorizonal, Undo2 } from "lucide-react";
import { api, type ActInput, type FrameView, type Landing, type Statement } from "../lib/api";
import { ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { buildSpine, type Station } from "../lib/spine";
import { age } from "../lib/rows";
import { eventDiscussionEntries, RetryKeys, sendTemporaryReply } from "../lib/interaction";
import { soleCurrentSupersedeBasis } from "../lib/supersedeLinks";
import { signingRefusal } from "../lib/authority";
import { mentionFingerprints } from "../lib/mentions";
import { actorTint, clock, cn, firstLine, interpretationNotice, kindLabel } from "../lib/util";
import { RowToolbar, ToolbarButton, semanticActions, type SemanticAction, type SemanticReplyMode } from "./Toolbar";
import { RecordDetail } from "./RecordDetail";
import { COMPOSED_CITATION_LIMIT, type RecordIndex } from "../lib/records";

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
  index,
  focus,
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
  /** The projection's record index, built once by App and shared. */
  index: RecordIndex;
  /**
   * The record the user clicked, when navigation resolved it into a wider
   * thread. Arrival opens whatever was hiding it and names it, so the user
   * never has to guess which row they asked for.
   */
  focus?: string;
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

  const request = index.statement(root);
  // Arrival state for a focused record: its detail opens by itself, and so
  // does the expander hiding it, because "it is on the page, collapsed"
  // is not visible.
  const focusedStation = focus ? spine?.stations.find((station) => station.event === focus) : undefined;
  const focusedHolder = focus ? spine?.expanders.find((expander) => expander.events.includes(focus)) : undefined;
  const [open, setOpen] = useState<Set<string>>(() => {
    const initial = new Set<string>();
    if (focusedHolder) initial.add(focusedHolder.id);
    if (focus && !focusedStation) initial.add(`detail:${focus}`);
    if (focusedStation) initial.add(`detail:${focusedStation.id}`);
    return initial;
  });
  const [route, setRoute] = useState<{
    id: string;
    mode: SemanticReplyMode;
    /** Everything the reply will cite, in the order the row named it. */
    bases: string[];
    prefill: string;
    /** Body fields the row already knows — never typed, so never mistyped. */
    body?: Record<string, string>;
  }>();
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
          commitment: index.commitment(statement.event),
          decision: index.decision(statement.event),
          projection,
          index,
          me,
          onRoute: (mode, bases, prefill, body) => setRoute({ id: crypto.randomUUID(), mode, bases, prefill, body }),
          doAct,
        })
      : [];

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ThreadHeader title={`#${tickets.get(root) ?? "?"} · ${firstLine(request.text)}`} onBack={onBack} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-4xl px-4 py-3 sm:px-6">
          <ol className="list-none" aria-label="Commitment spine">
            {spine.stations.map((station, position) => (
              <SpineRow
                key={station.id}
                station={station}
                first={position === 0}
                last={position === spine.stations.length - 1}
                nameOf={nameOf}
                focused={Boolean(focus && station.event === focus)}
                actions={actionsFor(station.event ? index.statement(station.event) : undefined)}
                open={open.has(`detail:${station.id}`)}
                onToggle={() => toggle(`detail:${station.id}`)}
                detail={
                  <RecordDetail
                    event={station.event}
                    commit={station.commit}
                    index={index}
                    actors={projection.actors}
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
                        index={index}
                        ticket={tickets.get(event)}
                        nameOf={nameOf}
                        focused={event === focus}
                        onOpenThread={onOpenThread}
                        open={open.has(`detail:${event}`)}
                        onToggle={() => toggle(`detail:${event}`)}
                        detail={
                          <RecordDetail event={event} index={index} actors={projection.actors} tickets={tickets} nameOf={nameOf} onOpenThread={onOpenThread} />
                        }
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
        index={index}
        tickets={tickets}
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
// The row's text is a disclosure button: it opens the record's full detail
// beneath the row and closes it again, by mouse or keyboard alike. The node
// and rail hang from the row's top, so the detail can grow below without
// moving the node.
function SpineRow({
  station,
  first,
  last,
  nameOf,
  actions,
  open,
  onToggle,
  detail,
  focused,
}: {
  station: Station;
  first: boolean;
  last: boolean;
  nameOf: (fingerprint: string) => string;
  actions: SemanticAction[];
  open: boolean;
  onToggle: () => void;
  detail: React.ReactNode;
  focused: boolean;
}) {
  const dim = !station.present;
  const detailed = Boolean(station.event || station.commit);
  return (
    <li
      data-station={station.id}
      data-present={station.present}
      aria-current={focused || undefined}
      className={cn("group relative text-xs", station.branch && "ml-5", focused && "-mx-1 rounded bg-elevated px-1")}
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
        <Disclosure
          open={open}
          onToggle={onToggle}
          controls={detailed ? `detail-${station.id}` : undefined}
          className={cn(
            station.branch ? "font-semibold text-danger" : "truncate",
            dim && !station.branch && "italic text-faint",
            !dim && !station.branch && "text-foreground/90",
          )}
        >
          {station.what}
          {station.actor && !station.branch && <span className="ml-1 text-faint">· {nameOf(station.actor)}</span>}
        </Disclosure>
        <span className="font-mono text-[11px] text-faint">{station.timestamp ? age(station.timestamp) : ""}</span>
      </div>
      {actions.length > 0 && (
        <RowToolbar>
          {actions.map((action) => (
            <ToolbarButton
              key={action.label}
              label={action.label}
              icon={<span aria-hidden>{action.symbol}</span>}
              showLabel={action.showLabel}
              tone={action.tone}
              onClick={action.run}
            />
          ))}
        </RowToolbar>
      )}
      {detailed && open && (
        <div id={`detail-${station.id}`} className="mb-2 ml-[1.6rem]">
          {detail}
        </div>
      )}
    </li>
  );
}

// A record behind an expander. Anything the fold ruled ineffective is marked
// where it appears rather than given a bucket of its own. The line's text is
// a disclosure button for the record's full detail.
function ElidedRecord({
  event,
  projection,
  index,
  ticket,
  nameOf,
  onOpenThread,
  open,
  onToggle,
  detail,
  focused,
}: {
  event: string;
  projection: import("../lib/api").Projection;
  index: RecordIndex;
  ticket?: number;
  nameOf: (fingerprint: string) => string;
  onOpenThread: (event: string) => void;
  open: boolean;
  onToggle: () => void;
  detail: React.ReactNode;
  focused: boolean;
}) {
  const statement = index.statement(event);
  const act = index.act(event);
  const decision = index.decision(event);
  const gap = interpretationNotice(decision?.verdict, decision?.reason);
  // What a supersession put in the retired record's place, when exactly one
  // current basis says. Neutral wording on purpose: naming a successor is not
  // a claim that it replaces anything.
  const linked = act ? soleCurrentSupersedeBasis(act, projection) : undefined;
  const ineffective = decision && decision.verdict !== "effective";
  const text = statement?.text ?? act?.text ?? (act ? `${act.type}d` : "");
  const actor = statement?.actor ?? act?.actor ?? "";
  return (
    <div data-record={event} aria-current={focused || undefined} className={cn(focused && "rounded bg-elevated")}>
      <div className="flex items-baseline gap-2 text-xs">
        <button
          type="button"
          onClick={() => onOpenThread(event)}
          title={event}
          className="shrink-0 font-mono text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
        >
          {ticket ? `#${ticket}` : "—"}
        </button>
        <span className="shrink-0 text-[11px] uppercase tracking-wide text-faint">
          {kindLabel(statement?.kind ?? act?.type ?? "record")}
        </span>
        <Disclosure
          open={open}
          onToggle={onToggle}
          controls={`detail-${event}`}
          className={cn("min-w-0 flex-1 truncate", statement?.retired ? "text-faint line-through" : "text-muted")}
        >
          {firstLine(text)}
        </Disclosure>
        {linked && (
          <button
            type="button"
            onClick={() => onOpenThread(linked)}
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
      {open && (
        <div id={`detail-${event}`} className="mb-2 mt-1">
          {detail}
        </div>
      )}
    </div>
  );
}

// The one disclosure primitive for a record's detail: a native button, so
// Enter and Space work without any handler of their own, with aria-expanded
// and aria-controls saying what it opens. A row with nothing to open renders
// the same text with no button.
function Disclosure({
  open,
  onToggle,
  controls,
  className,
  children,
}: {
  open: boolean;
  onToggle: () => void;
  controls?: string;
  className?: string;
  children: React.ReactNode;
}) {
  if (!controls) return <span className={className}>{children}</span>;
  return (
    <button
      type="button"
      aria-expanded={open}
      aria-controls={controls}
      data-disclosure={controls}
      onClick={onToggle}
      className={cn("block w-full text-left focus-visible:outline focus-visible:outline-accent", className)}
    >
      {children}
    </button>
  );
}

// Acting is why the operator looked. The composer is the only one on the
// screen and the only place a durable reply is written.
function Composer({
  workroom,
  session,
  root,
  index,
  tickets,
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
  /** The projection's record index, for naming what the reply will cite. */
  index: RecordIndex;
  /** Ticket numbers, so a citation reads as #12 and not only as a hash. */
  tickets: Map<string, number>;
  route?: { id: string; mode: SemanticReplyMode; bases: string[]; prefill: string; body?: Record<string, string> };
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
  // What a request needs and nothing else knows: who it is addressed to, and
  // what would satisfy it. The workroom refuses a request missing either, so
  // they are asked for here rather than filed empty and refused.
  const [addressee, setAddressee] = useState("");
  const [conditions, setConditions] = useState("");
  // Citations the operator named, beside the ones the row resolved. The
  // revision case in docs/how-to/keep-decision-records.md is why this exists:
  // a review request for a revised decision has to rest on the proposal that
  // adopted the decision, and that proposal rests on the *earlier* artifact at
  // the same path. Joining them would be the browser asserting that a decision
  // at a path stays adopted across revisions, which is the one relation
  // docs/reference/architecture.md forbids it to invent. So it does not join
  // them: the operator names the record, and the browser only resolves the
  // name against the projection and shows the result in the same list.
  const [cited, setCited] = useState<string[]>([]);
  const [citing, setCiting] = useState("");
  const [citeError, setCiteError] = useState<string>();
  const retryKeys = useRef(new RetryKeys());
  const seenRoute = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (route && seenRoute.current !== route.id) {
      seenRoute.current = route.id;
      setType(route.mode);
      setText(route.prefill);
      setAddressee("");
      setConditions("");
      setCited([]);
      setCiting("");
      setCiteError(undefined);
    }
  }, [route]);
  const durable = type !== "say";
  const routed = route && type === route.mode ? route : undefined;
  // A withdrawal names the record it retires in `bases[0]` and files no causal
  // references at all, so an operator citation there would either be silently
  // dropped or change what is retired. It is refused rather than either.
  const citable = durable && type !== "withdraw";
  const bases = [...(routed ? routed.bases : [root]), ...(citable ? cited : [])];
  const ready = type !== "request" || (addressee !== "" && conditions.trim() !== "");

  // A name the operator typed, resolved against the projection and never past
  // it. A ticket number is what the screen shows them; a whole event
  // identifier is what a record's detail offers to copy. Anything the
  // projection does not carry resolves to nothing, and the caller refuses it
  // rather than putting an unresolvable string into `rests_on`.
  const resolveCitation = (typed: string): string | undefined => {
    const trimmed = typed.trim();
    if (trimmed === "") return undefined;
    if (index.has(trimmed)) return trimmed;
    const digits = trimmed.startsWith("#") ? trimmed.slice(1) : trimmed;
    if (!/^[0-9]+$/.test(digits)) return undefined;
    const wanted = Number(digits);
    for (const [event, ticket] of tickets) if (ticket === wanted) return event;
    return undefined;
  };

  const addCitation = () => {
    const resolved = resolveCitation(citing);
    if (!resolved) {
      setCiteError("no record in this workroom has that number or identifier");
      return;
    }
    if (bases.includes(resolved)) {
      setCiteError("this reply already cites that record");
      return;
    }
    if (bases.length >= COMPOSED_CITATION_LIMIT) {
      setCiteError(`a reply may cite at most ${COMPOSED_CITATION_LIMIT} records`);
      return;
    }
    setCited((current) => [...current, resolved]);
    setCiting("");
    setCiteError(undefined);
  };

  const send = async () => {
    if (!text.trim() || busy || !session.actor || !ready) return;
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
      // Fields the row already resolved come first; nothing here re-derives an
      // identifier the operator would otherwise have to copy by hand.
      const body: Record<string, string> = { ...(routed?.body ?? {}) };
      const mentioned = mentionFingerprints(line, workroom.actors);
      if (mentioned.length > 0) body.mentions = mentioned.join(" ");
      if (type === "request") {
        body.to = addressee;
        body.conditions = conditions.trim();
      }
      const input: ActInput =
        type === "withdraw"
          ? { credential: session.credential, act: "supersede", target: bases[0] ?? root, text: line, rests_on: [] }
          : {
              credential: session.credential,
              act: "state",
              kind: type,
              text: line,
              body: Object.keys(body).length ? body : undefined,
              rests_on: bases,
            };
      // Asked at the boundary that signs, not only on the button. session.live
      // gates the control above, but a lease can expire and a participant
      // grant can be superseded while the composer is open, and the fold
      // judges this record by what is true when it arrives.
      //
      // withdraw needs no participation — an author retiring their own act is
      // the fold's documented cleanup exception — but it is still held to
      // authorship, so the target it names is resolved and passed. This is the
      // only site that signs supersede; leaving the target out here would make
      // that rule unreachable.
      const denied = signingRefusal(input, {
        live: session.live,
        actors: workroom.status?.durable.projection?.actors ?? {},
        me: workroom.actors.find((actor) => actor.name === session.actor)?.fingerprint,
        target: input.target ? index.statement(input.target) : undefined,
        targetDecision: input.target ? index.decision(input.target) : undefined,
        originatingRequester: input.target ? index.commitment(input.target)?.requester : undefined,
      });
      if (denied) {
        setError(`not filed: ${denied}`);
        setBusy(false);
        return;
      }
      const scope = `${root}:${type}`;
      const key = retryKeys.current.forAttempt(scope, JSON.stringify(input));
      await api.act({ ...input, idempotency_key: key });
      retryKeys.current.succeeded(scope, key);
      setText("");
      setType("say");
      setAddressee("");
      setConditions("");
      setCited([]);
      setCiting("");
      setCiteError(undefined);
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
                setAddressee("");
                setConditions("");
                setCited([]);
                setCiting("");
                setCiteError(undefined);
                onClearRoute();
              }}
              className="text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
            >
              cancel
            </button>
          </div>
        )}
        {/*
          What is about to be signed, before it is signed. The row resolves
          these so that an identifier nobody should retype by hand is not
          retyped by hand; that is a reason to prefill them and no reason to
          hide them. Causal references are what a record means, and an operator
          answerable for a record has to be able to read them first.

          A withdrawal names the record it retires rather than a basis it
          rests on, so it says so: one list, two honest labels.

          One list, not two, for the same reason: what the row resolved and
          what the operator named are signed together and read together, and
          only the operator's own additions carry a control to take them out
          again.
        */}
        {durable && (
          <ul
            data-citations
            aria-label={type === "withdraw" ? "the record this will retire" : "citations this reply will sign"}
            className="list-none space-y-0.5 text-[11px] text-faint"
          >
            {bases.map((basis) => {
              const record = index.statement(basis);
              const ticket = tickets.get(basis);
              const mine = citable && cited.includes(basis);
              return (
                <li key={basis} className="flex min-w-0 items-baseline gap-1">
                  <span aria-hidden>↳</span>
                  {ticket !== undefined && <span className="font-mono">#{ticket}</span>}
                  {record && <span className="uppercase tracking-wide">{kindLabel(record.kind)}</span>}
                  {record && <span className="truncate text-muted">{firstLine(record.text, 80)}</span>}
                  <span className="truncate font-mono text-faint">{basis}</span>
                  {mine && (
                    <button
                      type="button"
                      aria-label={`stop citing ${basis}`}
                      onClick={() => setCited((current) => current.filter((event) => event !== basis))}
                      className="shrink-0 text-faint hover:text-danger focus-visible:outline focus-visible:outline-accent"
                    >
                      remove
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        {/*
          One citation the row could not resolve, named by the operator.
          Nothing here derives a relation: the operator says which record, by
          the number the screen already shows them or by the whole identifier a
          record's detail offers to copy, and the browser only looks that name
          up in the projection. A name the projection does not carry is refused
          here rather than filed as a dangling reference, and whatever is added
          joins the disclosure list above before the send control is used.
        */}
        {citable && (
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="text"
              aria-label="cite another record"
              placeholder="also cite… #12, or a whole event identifier"
              value={citing}
              onChange={(event) => {
                setCiting(event.target.value);
                setCiteError(undefined);
              }}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                addCitation();
              }}
              className="h-7 min-w-0 flex-1 rounded border border-input bg-surface px-2 font-mono text-[11px] outline-none placeholder:text-faint focus:border-accent/60"
            />
            <button
              type="button"
              aria-label="add citation"
              onClick={addCitation}
              disabled={citing.trim() === ""}
              className="h-7 rounded border border-input px-2 text-[11px] text-muted hover:text-foreground focus-visible:outline focus-visible:outline-accent disabled:opacity-40"
            >
              cite
            </button>
          </div>
        )}
        {citeError && (
          <p role="alert" aria-label="citation refused" className="flex items-center gap-1 text-[11px] text-danger">
            <CircleSlash className="h-3 w-3 shrink-0" /> {citeError}
          </p>
        )}
        {type === "request" && (
          <div className="flex flex-wrap items-center gap-2">
            <select
              aria-label="addressed to"
              value={addressee}
              onChange={(event) => setAddressee(event.target.value)}
              className="h-7 rounded border border-input bg-surface px-1.5 text-[11px] text-muted outline-none focus:border-accent/60"
            >
              <option value="">addressed to…</option>
              {workroom.actors.map((actor) => (
                <option key={actor.fingerprint} value={actor.fingerprint}>
                  {actor.name}
                </option>
              ))}
            </select>
            <input
              type="text"
              aria-label="conditions of satisfaction"
              placeholder="conditions of satisfaction"
              value={conditions}
              onChange={(event) => setConditions(event.target.value)}
              className="h-7 min-w-0 flex-1 rounded border border-input bg-surface px-2 text-[11px] outline-none placeholder:text-faint focus:border-accent/60"
            />
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
            disabled={busy || !text.trim() || !session.live || !ready}
            aria-label={type === "withdraw" ? "withdraw" : durable ? "keep reply" : "send temporary reply"}
            title={session.live ? (ready ? undefined : "a request needs an addressee and its conditions") : "not present yet"}
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
