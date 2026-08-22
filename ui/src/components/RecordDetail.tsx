import { useState } from "react";
import type { Projection } from "../lib/api";
import type { RecordIndex } from "../lib/records";
import { eventTimestamp, firstLine, kindLabel } from "../lib/util";

// How many references a list shows before it says how many more there are.
// Reverse provenance has no natural bound — one decision can carry hundreds
// of dependents — and drawing them all on every open row is not a detail,
// it is a second screen.
export const REFERENCE_LIMIT = 12;

// The full detail of one durable record, opened under the row that names it.
// Everything here is read from the projection index and shown whole: full
// ids, every body field, what it rests on and what rests on it. Nothing is
// abbreviated, because the row above already did that.
export function RecordDetail({
  event,
  commit,
  index,
  actors,
  tickets,
  nameOf,
  onOpenThread,
}: {
  /** The record to show. Absent for a station that names only a commit. */
  event?: string;
  /** A git commit the row names, shown whole. */
  commit?: string;
  index: RecordIndex;
  actors: Projection["actors"];
  tickets: Map<string, number>;
  nameOf: (fingerprint: string) => string;
  onOpenThread: (event: string) => void;
}) {
  const statement = event ? index.statement(event) : undefined;
  const act = event ? index.act(event) : undefined;
  const decision = event ? index.decision(event) : undefined;
  const artifact = event ? index.artifact(event) : undefined;
  const review = event ? index.review(event) : undefined;
  const sequence = event ? index.sequence(event) : undefined;
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
  const ref = (target: string) => <Ref event={target} index={index} tickets={tickets} onOpenThread={onOpenThread} />;

  return (
    <dl
      data-record-detail={event ?? commit}
      className="grid gap-x-3 gap-y-1 border-l-2 border-border py-1.5 pl-3 text-xs text-muted"
      style={{ gridTemplateColumns: "max-content minmax(0, 1fr)" }}
    >
      {event && <Row label="event"><Id value={event} /></Row>}
      {commit && <Row label="commit"><Id value={commit} /></Row>}
      {(statement || act) && (
        <Row label="kind">
          {kindLabel(statement?.kind ?? act?.type ?? "")}
          {sequence !== undefined && ` · #${sequence}`}
        </Row>
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
      {act && <Row label="target">{ref(act.target)}</Row>}
      {statement?.body &&
        Object.entries(statement.body).map(([key, value]) => (
          <Row key={key} label={key}>
            {index.has(value) ? (
              ref(value)
            ) : actors[value] ? (
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
      {review?.artifact && <Row label="reviewed">{ref(review.artifact)}</Row>}
      {(decision || act) && (
        <Row label="fold">
          <span className={(decision?.verdict ?? act?.verdict) === "effective" ? "text-ok" : "text-danger"}>
            {decision?.verdict ?? act?.verdict}
          </span>
          {(decision?.reason ?? act?.reason) && ` — ${decision?.reason ?? act?.reason}`}
        </Row>
      )}
      {flags.length > 0 && <Row label="flags">{flags.join(" · ")}</Row>}
      {event && (
        <Row label="rests on">
          <References events={index.restsOn(event)} render={ref} />
        </Row>
      )}
      {event && index.restedOnBy(event).length > 0 && (
        <Row label="rested on by">
          <References events={index.restedOnBy(event)} render={ref} />
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

// A bounded list of references: the first REFERENCE_LIMIT, then one line
// saying how many are not shown, which opens to the rest.
function References({ events, render }: { events: string[]; render: (event: string) => React.ReactNode }) {
  const [all, setAll] = useState(false);
  if (events.length === 0) return <span className="text-faint">nothing</span>;
  const shown = all ? events : events.slice(0, REFERENCE_LIMIT);
  const remaining = events.length - shown.length;
  return (
    <ul className="list-none space-y-0.5">
      {shown.map((event) => (
        <li key={event}>{render(event)}</li>
      ))}
      {remaining > 0 && (
        <li>
          <button
            type="button"
            onClick={() => setAll(true)}
            className="text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent"
          >
            {remaining} more not shown — show all {events.length}
          </button>
        </li>
      )}
    </ul>
  );
}

// A reference to another record: its ticket, kind and first line when the
// projection knows it, and always its whole id. Opens the thread the record
// belongs to.
function Ref({
  event,
  index,
  tickets,
  onOpenThread,
}: {
  event: string;
  index: RecordIndex;
  tickets: Map<string, number>;
  onOpenThread: (event: string) => void;
}) {
  const statement = index.statement(event);
  const act = index.act(event);
  const ticket = tickets.get(event);
  const kind = statement?.kind ?? act?.type;
  return (
    <button
      type="button"
      data-ref={event}
      onClick={() => onOpenThread(event)}
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
