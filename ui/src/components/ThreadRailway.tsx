import { useMemo } from "react";
import { BadgeCheck, CircleSlash, GitBranch, Link2, Undo2 } from "lucide-react";
import type { Act, Projection, Statement } from "../lib/api";
import type { ThreadContent } from "../lib/threads";
import { layoutThreadRailway } from "../lib/threadRailway";
import { cn, kindLabel, kindTint } from "../lib/util";
import { EventTime } from "./EventTime";

const ROW = 68;
const LANE = 22;
const LEFT = 14;
const laneColors = ["#34d399", "#fbbf24", "#38bdf8", "#a78bfa", "#2dd4bf", "#f87171"];

type DurableEvent = Statement | Act;

function isStatement(event: DurableEvent): event is Statement {
  return "kind" in event;
}

function labelOf(event: DurableEvent): string {
  if (isStatement(event)) return kindLabel[event.kind] ?? event.kind;
  return event.type === "ratify" ? "agree" : "withdraw";
}

function textOf(event: DurableEvent): string {
  if (isStatement(event)) return event.text;
  if (event.text) return event.text;
  if (event.verdict !== "effective") return event.reason;
  return event.type === "ratify" ? "Agreed" : "Withdrawn";
}

function actorOf(event: DurableEvent): string {
  return event.actor;
}

export function ThreadRailway({
  root,
  thread,
  projection,
  tickets,
  nameOf,
  onJumpTo,
}: {
  root: Statement;
  thread: ThreadContent;
  projection: Projection;
  tickets: Map<string, number>;
  nameOf: (fingerprint: string) => string;
  onJumpTo: (event: string) => void;
}) {
  const ordered = useMemo(() => [root.event, ...thread.events], [root.event, thread.events]);
  const layout = useMemo(() => layoutThreadRailway(ordered, projection.provenance), [ordered, projection.provenance]);
  const byID = useMemo(
    () => new Map<string, DurableEvent>([
      ...projection.statements.map((statement) => [statement.event, statement] as const),
      ...projection.acts.map((act) => [act.event, act] as const),
    ]),
    [projection.statements, projection.acts],
  );
  const byNode = useMemo(() => new Map(layout.nodes.map((node) => [node.event, node])), [layout.nodes]);
  const graphWidth = LEFT + layout.lanes * LANE + 10;
  const height = layout.nodes.length * ROW;
  const x = (lane: number) => LEFT + lane * LANE;
  const y = (row: number) => row * ROW + ROW / 2;
  const ticketLabel = (event: string) => {
    const ticket = tickets.get(event);
    return ticket ? `#${ticket}` : "outside thread";
  };

  return (
    <div className="relative min-h-full overflow-auto" data-thread-railway>
      <div className="relative min-w-[18rem]" style={{ height }}>
        {layout.nodes.map((node) => {
          const event = byID.get(node.event);
          if (!event) return null;
          const statement = isStatement(event) ? event : undefined;
          const act = statement ? undefined : (event as Act);
          const bases = projection.provenance[node.event] ?? [];
          return (
            <div
              key={node.event}
              data-thread-rail-event={node.event}
              className="absolute right-0 flex h-[68px] w-full items-center pr-2"
              style={{ top: node.row * ROW, paddingLeft: graphWidth + 4 }}
            >
              <button
                onClick={() => onJumpTo(node.event)}
                className="group min-w-0 flex-1 rounded-md px-2 py-1 text-left hover:bg-elevated/60 focus-visible:outline focus-visible:outline-accent"
              >
                <span className="flex min-w-0 items-center gap-1.5">
                  <span
                    className={cn(
                      "shrink-0 border px-1 text-[10px] uppercase leading-4 tracking-wide",
                      statement
                        ? kindTint[statement.kind] ?? "border-border text-muted"
                        : act?.verdict === "effective"
                          ? "border-border text-muted"
                          : "border-danger/40 text-danger",
                    )}
                  >
                    {labelOf(event)}
                  </span>
                  <span className={cn("truncate text-[12px]", statement?.retired ? "text-faint line-through" : "text-foreground/90")}>{textOf(event)}</span>
                  {statement?.ratified && !statement.retired && <BadgeCheck aria-label="ratified" className="h-3 w-3 shrink-0 text-ok" />}
                  {statement?.stale && !statement.retired && <span className="shrink-0 text-[10px] text-danger">stale</span>}
                  {act && act.verdict !== "effective" && <CircleSlash aria-label={act.verdict} className="h-3 w-3 shrink-0 text-danger" />}
                  {act?.type === "supersede" && act.verdict === "effective" && <Undo2 aria-label="withdrawn" className="h-3 w-3 shrink-0 text-danger" />}
                </span>
                <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[10px] text-faint">
                  <span className="truncate">{nameOf(actorOf(event))}</span>
                  <EventTime timestamp={event.timestamp} />
                  <span className="ml-auto shrink-0 font-mono" title={node.event}>#{tickets.get(node.event) ?? "?"}</span>
                </span>
                {bases.length > 0 && (
                  <span className="mt-0.5 flex min-w-0 items-center gap-1 text-[10px] text-faint">
                    <GitBranch className="h-2.5 w-2.5 shrink-0" />
                    <span className="shrink-0">reply to {ticketLabel(bases[0])}</span>
                    {bases.length > 1 && (
                      <span className="flex min-w-0 items-center gap-1 truncate text-accent-deep" title={bases.slice(1).join("\n")}>
                        <Link2 className="h-2.5 w-2.5 shrink-0" /> cites {bases.slice(1).map(ticketLabel).join(", ")}
                      </span>
                    )}
                  </span>
                )}
              </button>
            </div>
          );
        })}
        <svg width={graphWidth} height={height} className="pointer-events-none absolute left-0 top-0" aria-hidden="true">
          {layout.nodes.map((node) => {
            if (!node.primary) return null;
            const parent = byNode.get(node.primary);
            const x2 = x(node.lane);
            const y2 = y(node.row);
            if (!parent) {
              return (
                <path
                  key={`${node.event}:parent`}
                  d={`M ${x2} 0 L ${x2} ${y2}`}
                  stroke={laneColors[node.lane % laneColors.length]}
                  strokeOpacity={0.35}
                  strokeWidth={1.5}
                  fill="none"
                />
              );
            }
            const x1 = x(parent.lane);
            const y1 = y(parent.row);
            const bend = Math.min(ROW * 0.8, (y2 - y1) / 2);
            return (
              <path
                key={`${node.event}:parent`}
                d={`M ${x1} ${y1} C ${x1} ${y1 + bend}, ${x2} ${y2 - bend}, ${x2} ${y2}`}
                stroke={laneColors[node.lane % laneColors.length]}
                strokeOpacity={0.65}
                strokeWidth={1.5}
                fill="none"
              />
            );
          })}
          {layout.nodes.flatMap((node) =>
            node.citations.map((citation) => {
              const source = byNode.get(citation);
              if (!source) return null;
              const x1 = x(source.lane);
              const y1 = y(source.row);
              const x2 = x(node.lane);
              const y2 = y(node.row);
              return (
                <path
                  key={`${node.event}:cite:${citation}`}
                  d={`M ${x1} ${y1} C ${x1 + LANE / 2} ${y1 + ROW / 3}, ${x2 + LANE / 2} ${y2 - ROW / 3}, ${x2} ${y2}`}
                  stroke="#fbbf24"
                  strokeOpacity={0.5}
                  strokeWidth={1}
                  strokeDasharray="3 3"
                  fill="none"
                />
              );
            }),
          )}
          {layout.nodes.map((node) => (
            <g key={node.event}>
              <circle cx={x(node.lane)} cy={y(node.row)} r={4.5} fill="#18181b" stroke={laneColors[node.lane % laneColors.length]} strokeWidth={2} />
            </g>
          ))}
        </svg>
      </div>
    </div>
  );
}
