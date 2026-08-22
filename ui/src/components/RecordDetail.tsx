import type { Projection } from "../lib/api";
import { eventTimestamp, firstLine, kindLabel } from "../lib/util";

// The full detail of one durable record, opened under the row that names it.
// Everything here is read straight from the projection and shown whole: full
// ids, every body field, what it rests on and what rests on it. Nothing is
// abbreviated, because the row above already did that.
export function RecordDetail({
  event,
  commit,
  projection,
  tickets,
  nameOf,
  onOpenThread,
}: {
  /** The record to show. Absent for a station that names only a commit. */
  event?: string;
  /** A git commit the row names, shown whole. */
  commit?: string;
  projection: Projection;
  tickets: Map<string, number>;
  nameOf: (fingerprint: string) => string;
  onOpenThread: (event: string) => void;
}) {
  const statement = event ? projection.statements.find((s) => s.event === event) : undefined;
  const act = event ? projection.acts.find((a) => a.event === event) : undefined;
  const decision = event ? projection.decisions.find((d) => d.event === event) : undefined;
  const artifact = event ? projection.artifacts.find((a) => a.event === event) : undefined;
  const review = event ? projection.reviews?.find((r) => r.report === event) : undefined;
  const restsOn = event ? (projection.provenance[event] ?? []) : [];
  const restedOnBy = event
    ? Object.entries(projection.provenance)
        .filter(([, bases]) => bases.includes(event))
        .map(([dependent]) => dependent)
    : [];
  const actor = statement?.actor ?? act?.actor;
  const flags = [
    statement?.ratified && "ratified",
    (statement?.retired || artifact?.retired) && "retired",
    artifact?.succeeded && "succeeded",
    (statement?.stale || artifact?.stale) && "stale",
    (statement?.describes_superseded_world || artifact?.describes_superseded_world) && "describes superseded world",
    artifact?.unable_to_flare && "unable to flare",
    artifact?.succession_unrecorded && "succession unrecorded",
  ].filter((flag): flag is string => Boolean(flag));

  return (
    <dl
      data-record-detail={event ?? commit}
      className="grid gap-x-3 gap-y-1 border-l-2 border-border py-1.5 pl-3 text-xs text-muted"
      style={{ gridTemplateColumns: "max-content minmax(0, 1fr)" }}
      onClick={(e) => e.stopPropagation()}
    >
      {event && <Row label="event"><Id value={event} /></Row>}
      {commit && <Row label="commit"><Id value={commit} /></Row>}
      {(statement || act) && (
        <Row label="kind">{kindLabel(statement?.kind ?? act?.type ?? "")}{statement && ` · #${statement.sequence}`}</Row>
      )}
      {actor && (
        <Row label="actor">
          <span className="text-foreground/90">{nameOf(actor)}</span> <Id value={actor} />
        </Row>
      )}
      {(statement?.timestamp ?? act?.timestamp) !== undefined && (
        <Row label="time">{eventTimestamp((statement?.timestamp ?? act?.timestamp) as number)}</Row>
      )}
      {(statement?.text || act?.text) && (
        <Row label="text">
          <span className="whitespace-pre-wrap break-words text-foreground/90">{statement?.text ?? act?.text}</span>
        </Row>
      )}
      {act && (
        <Row label="target">
          <Ref event={act.target} projection={projection} tickets={tickets} onOpenThread={onOpenThread} />
        </Row>
      )}
      {statement?.body &&
        Object.entries(statement.body).map(([key, value]) => (
          <Row key={key} label={key}>
            {projection.provenance[value] !== undefined || projection.statements.some((s) => s.event === value) ? (
              <Ref event={value} projection={projection} tickets={tickets} onOpenThread={onOpenThread} />
            ) : projection.actors[value] ? (
              <>
                <span className="text-foreground/90">{nameOf(value)}</span> <Id value={value} />
              </>
            ) : (
              <span className="whitespace-pre-wrap break-words font-mono">{value}</span>
            )}
          </Row>
        ))}
      {artifact && <Row label="path"><span className="font-mono">{artifact.path}</span></Row>}
      {artifact && <Row label="at commit"><Id value={artifact.commit} /></Row>}
      {review && (
        <Row label="review">
          {review.verdict} by {nameOf(review.reviewer)}
          {review.head && <> over <Id value={review.head} /></>}
          {` · ${review.independence}`}
        </Row>
      )}
      {review?.artifact && (
        <Row label="reviewed">
          <Ref event={review.artifact} projection={projection} tickets={tickets} onOpenThread={onOpenThread} />
        </Row>
      )}
      {decision && (
        <Row label="fold">
          <span className={decision.verdict === "effective" ? "text-ok" : "text-danger"}>{decision.verdict}</span>
          {decision.reason && ` — ${decision.reason}`}
        </Row>
      )}
      {act && !decision && (
        <Row label="fold">
          <span className={act.verdict === "effective" ? "text-ok" : "text-danger"}>{act.verdict}</span>
          {act.reason && ` — ${act.reason}`}
        </Row>
      )}
      {flags.length > 0 && <Row label="flags">{flags.join(" · ")}</Row>}
      <Row label="rests on">
        {restsOn.length === 0 ? (
          <span className="text-faint">nothing</span>
        ) : (
          <ul className="list-none space-y-0.5">
            {restsOn.map((basis) => (
              <li key={basis}>
                <Ref event={basis} projection={projection} tickets={tickets} onOpenThread={onOpenThread} />
              </li>
            ))}
          </ul>
        )}
      </Row>
      {restedOnBy.length > 0 && (
        <Row label="rested on by">
          <ul className="list-none space-y-0.5">
            {restedOnBy.map((dependent) => (
              <li key={dependent}>
                <Ref event={dependent} projection={projection} tickets={tickets} onOpenThread={onOpenThread} />
              </li>
            ))}
          </ul>
        </Row>
      )}
    </dl>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="text-[11px] uppercase tracking-wide text-faint">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

function Id({ value }: { value: string }) {
  return <span className="select-all break-all font-mono text-[11px]">{value}</span>;
}

// A reference to another record: its ticket, kind and first line when the
// projection knows it, and always its whole id. Opens that record's thread.
function Ref({
  event,
  projection,
  tickets,
  onOpenThread,
}: {
  event: string;
  projection: Projection;
  tickets: Map<string, number>;
  onOpenThread: (event: string) => void;
}) {
  const statement = projection.statements.find((s) => s.event === event);
  const act = projection.acts.find((a) => a.event === event);
  const ticket = tickets.get(event);
  const kind = statement?.kind ?? act?.type;
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onOpenThread(event);
      }}
      className="max-w-full text-left hover:text-foreground focus-visible:outline focus-visible:outline-accent"
    >
      {(ticket || kind) && (
        <span className="mr-1">
          {ticket && <span className="font-mono text-[11px]">#{ticket}</span>}
          {kind && <span className="ml-1 text-[11px] uppercase tracking-wide text-faint">{kindLabel(kind)}</span>}
          {statement?.text && <span className="ml-1">{firstLine(statement.text, 80)}</span>}
        </span>
      )}
      <Id value={event} />
    </button>
  );
}
