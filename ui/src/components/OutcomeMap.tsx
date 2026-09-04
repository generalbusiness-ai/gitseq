import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { OutcomeMap as OutcomeMapData, OutcomeRelation, OutcomeRelationFamily } from "../lib/outcomeMap.ts";
import { clampOutcomeScale, fitOutcomeScale, OUTCOME_CARD, OUTCOME_SCALE, layoutOutcomeMap, outcomeEdgePath } from "../lib/outcomeMapLayout.ts";
import { cn } from "../lib/util.ts";

const RELATION_LABEL: Record<OutcomeRelationFamily, string> = {
  "rests-on": "rests on",
  "ratified-by": "ratified by",
  superseded: "superseded",
};

const RELATION_STYLE: Record<OutcomeRelationFamily, { dash?: string; className: string }> = {
  "rests-on": { className: "stroke-muted" },
  "ratified-by": { dash: "2 5", className: "stroke-accent" },
  superseded: { dash: "8 5", className: "stroke-danger" },
};

function statusClass(state: string): string {
  if (state === "needs attention") return "border-danger/60 bg-danger/10 text-danger";
  if (state === "in progress" || state === "awaiting review" || state === "awaiting authorization" || state === "awaiting landing")
    return "border-accent/60 bg-accent/10 text-accent";
  if (state === "reported") return "border-muted/60 bg-muted/10 text-muted";
  return "border-border bg-surface/70 text-muted";
}

function relationText(relation: OutcomeRelation): string {
  const readings = relation.contributors.map((contributor) => `${contributor.forward} Reverse: ${contributor.reverse}`).join(" ");
  return `${RELATION_LABEL[relation.family]}: ${readings}`;
}

function connectedComponent(graph: OutcomeMapData, start: string): Set<string> {
  const adjacent = new Map(graph.nodes.map((node) => [node.thread, [] as string[]]));
  for (const relation of graph.relations) {
    adjacent.get(relation.source)?.push(relation.target);
    adjacent.get(relation.target)?.push(relation.source);
  }
  const selected = new Set([start]);
  const queue = [start];
  for (let index = 0; index < queue.length; index += 1) {
    for (const next of adjacent.get(queue[index]) ?? []) {
      if (selected.has(next)) continue;
      selected.add(next);
      queue.push(next);
    }
  }
  return selected;
}

export function OutcomeMap({
  graph,
  nameOf,
  onOpenThread,
}: {
  graph: OutcomeMapData;
  nameOf: (fingerprint: string) => string;
  onOpenThread: (event: string) => void;
}) {
  const layout = useMemo(() => layoutOutcomeMap(graph.nodes), [graph.nodes]);
  const viewport = useRef<HTMLDivElement>(null);
  const drag = useRef<{ pointer: number; x: number; y: number } | undefined>(undefined);
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 16, y: 16 });
  const [pointedRelation, setPointedRelation] = useState<string>();
  const [selectedRelation, setSelectedRelation] = useState<string>();
  const [selectedThread, setSelectedThread] = useState<string>();
  const fit = useCallback(() => {
    const box = viewport.current;
    setScale(fitOutcomeScale(layout, box?.clientWidth ?? 0, box?.clientHeight ?? 0));
    setOffset({ x: 16, y: 16 });
  }, [layout.height, layout.width]);
  useEffect(() => fit(), [fit]);
  useEffect(() => {
    if (selectedThread && !graph.nodes.some((node) => node.thread === selectedThread)) setSelectedThread(undefined);
    if (selectedRelation && !graph.relations.some((relation) => relation.id === selectedRelation)) setSelectedRelation(undefined);
  }, [graph.nodes, graph.relations, selectedRelation, selectedThread]);
  const activeRelation = pointedRelation ?? selectedRelation;
  const active = graph.relations.find((relation) => relation.id === activeRelation);
  const selectedNode = graph.nodes.find((node) => node.thread === selectedThread);
  const emphasized = useMemo(() => {
    if (selectedThread) return connectedComponent(graph, selectedThread);
    const relation = graph.relations.find((candidate) => candidate.id === selectedRelation);
    return relation ? new Set([relation.source, relation.target]) : undefined;
  }, [graph, selectedRelation, selectedThread]);

  return (
    <section data-board-presentation="graph" className="mt-2" aria-label="Outcome map">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-[11px] text-faint">
        <span>Direct recorded relations only</span>
        <span aria-hidden>·</span>
        <span><span className="font-semibold text-muted">solid</span> rests on</span>
        <span><span className="font-semibold text-accent">dotted</span> ratified by</span>
        <span><span className="font-semibold text-danger">dashed</span> superseded</span>
        <div className="ml-auto flex items-center gap-1">
          <button type="button" onClick={() => setScale((value) => clampOutcomeScale(Number((value + OUTCOME_SCALE.step).toFixed(2))))} className="rounded border border-border px-2 py-1 hover:bg-surface focus-visible:outline focus-visible:outline-accent" aria-label="Zoom in">Zoom in</button>
          <button type="button" onClick={() => setScale((value) => clampOutcomeScale(Number((value - OUTCOME_SCALE.step).toFixed(2))))} className="rounded border border-border px-2 py-1 hover:bg-surface focus-visible:outline focus-visible:outline-accent" aria-label="Zoom out">Zoom out</button>
          <button type="button" onClick={fit} className="rounded border border-border px-2 py-1 hover:bg-surface focus-visible:outline focus-visible:outline-accent" aria-label="Fit graph to view">Fit to view</button>
          <button type="button" onClick={() => { setScale(1); setOffset({ x: 16, y: 16 }); }} className="rounded border border-border px-2 py-1 hover:bg-surface focus-visible:outline focus-visible:outline-accent" aria-label="Reset graph view">Reset</button>
        </div>
      </div>
      <div
        ref={viewport}
        aria-label="Outcome map viewport"
        data-scale={scale}
        className="relative min-h-[32rem] touch-none overflow-hidden rounded-md border border-border bg-surface/20"
        onPointerDown={(event) => {
          drag.current = { pointer: event.pointerId, x: event.clientX, y: event.clientY };
          event.currentTarget.setPointerCapture(event.pointerId);
        }}
        onPointerMove={(event) => {
          if (!drag.current || drag.current.pointer !== event.pointerId) return;
          const deltaX = event.clientX - drag.current.x;
          const deltaY = event.clientY - drag.current.y;
          drag.current = { pointer: event.pointerId, x: event.clientX, y: event.clientY };
          setOffset((value) => ({ x: value.x + deltaX, y: value.y + deltaY }));
        }}
        onPointerUp={() => { drag.current = undefined; }}
        onPointerCancel={() => { drag.current = undefined; }}
      >
        <div
          className="absolute left-0 top-0"
          style={{ width: layout.width, height: layout.height, transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})`, transformOrigin: "top left" }}
        >
          <svg width={layout.width} height={layout.height} viewBox={`0 0 ${layout.width} ${layout.height}`} className="absolute inset-0 overflow-visible" aria-label="Recorded outcome relations">
            <defs>
              <marker id="outcome-arrowhead" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto" markerUnits="strokeWidth">
                <path d="M 0 0 L 8 4 L 0 8 z" className="fill-current text-muted" />
              </marker>
            </defs>
            {graph.relations.map((relation) => {
              const source = layout.positions.get(relation.source);
              const target = layout.positions.get(relation.target);
              if (!source || !target) return null;
              const style = RELATION_STYLE[relation.family];
              const path = outcomeEdgePath(source, target);
              const labelX = (source.x + OUTCOME_CARD.width + target.x) / 2;
              const labelY = (source.y + target.y + OUTCOME_CARD.height) / 2 - 7;
              return (
                <g
                  key={relation.id}
                  tabIndex={0}
                  role="button"
                  aria-label={relationText(relation)}
                  aria-pressed={selectedRelation === relation.id}
                  data-relation={relation.family}
                  data-selected={selectedRelation === relation.id || undefined}
                  className={emphasized && (!emphasized.has(relation.source) || !emphasized.has(relation.target)) ? "opacity-20" : ""}
                  onMouseEnter={() => setPointedRelation(relation.id)}
                  onMouseLeave={() => setPointedRelation(undefined)}
                  onFocus={() => setPointedRelation(relation.id)}
                  onBlur={() => setPointedRelation(undefined)}
                  onPointerDown={(event) => event.stopPropagation()}
                  onClick={() => { setSelectedRelation((current) => current === relation.id ? undefined : relation.id); setSelectedThread(undefined); }}
                  onKeyDown={(event) => {
                    if (event.key !== "Enter" && event.key !== " ") return;
                    event.preventDefault();
                    setSelectedRelation((current) => current === relation.id ? undefined : relation.id);
                    setSelectedThread(undefined);
                  }}
                >
                  <path d={path} fill="none" strokeWidth="1.5" strokeDasharray={style.dash} markerEnd="url(#outcome-arrowhead)" className={style.className} />
                  <path d={path} fill="none" stroke="transparent" strokeWidth="14" />
                  <text x={labelX} y={labelY} textAnchor="middle" className="fill-muted text-[10px]">{RELATION_LABEL[relation.family]}</text>
                </g>
              );
            })}
          </svg>
          {graph.nodes.map((node) => {
            const position = layout.positions.get(node.thread);
            if (!position) return null;
            const tags = [
              node.rootOfView ? "Root of view" : "",
              !node.recordedBasis ? "No recorded basis" : "",
              node.basisOutsideView ? "Basis outside view" : "",
            ].filter(Boolean);
            return (
              <button
                key={node.thread}
                type="button"
                data-outcome-card={node.thread}
                data-selected={selectedThread === node.thread || undefined}
                data-dimmed={(emphasized && !emphasized.has(node.thread)) || undefined}
                aria-pressed={selectedThread === node.thread}
                onClick={() => {
                  if (selectedThread === node.thread) onOpenThread(node.thread);
                  else {
                    setSelectedThread(node.thread);
                    setSelectedRelation(undefined);
                  }
                }}
                onPointerDown={(event) => event.stopPropagation()}
                className={cn(
                  "absolute rounded-md border border-border bg-background p-2 text-left shadow-sm hover:border-accent/70 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent",
                  emphasized && !emphasized.has(node.thread) && "opacity-25",
                  selectedThread === node.thread && "ring-2 ring-accent",
                )}
                style={{ left: position.x, top: position.y, width: OUTCOME_CARD.width, height: OUTCOME_CARD.height }}
                aria-label={`${node.state}; ${node.title}; ${selectedThread === node.thread ? "activate again to open full thread" : "select connected work"}`}
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="max-w-[9rem] truncate font-serif text-xs font-semibold text-foreground">{node.title}</span>
                  <span className={cn("shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-semibold", statusClass(node.state))}>{node.state}</span>
                </div>
                <div className="mt-1 flex flex-wrap gap-1 text-[10px] text-faint">
                  <span className="rounded border border-border px-1 py-0.5">{node.kind}</span>
                  {node.context && <span className="rounded border border-border px-1 py-0.5">context</span>}
                  {tags.map((tag) => <span key={tag} className="rounded border border-border px-1 py-0.5">{tag}</span>)}
                </div>
                {node.waitsOn && <p className="mt-2 truncate text-[10px] text-muted">Waits on {nameOf(node.waitsOn)}</p>}
              </button>
            );
          })}
        </div>
      </div>
      {selectedNode && (
        <p role="status" className="mt-2 flex items-center gap-2 rounded border border-border bg-surface px-2 py-1 text-[11px] text-muted">
          <span className="truncate">Selected: {selectedNode.title}</span>
          <button type="button" className="ml-auto shrink-0 rounded border border-border px-2 py-1 text-foreground hover:bg-background focus-visible:outline focus-visible:outline-accent" onClick={() => onOpenThread(selectedNode.thread)}>
            Open full thread
          </button>
        </p>
      )}
      {active && <p role="tooltip" className="mt-2 rounded border border-border bg-surface px-2 py-1 text-[11px] text-muted">{relationText(active)}</p>}
      {(graph.stats.omittedContextNodes > 0 || graph.stats.omittedEdges > 0) && (
        <p role="status" data-omitted-context={graph.stats.omittedContextNodes} data-omitted-edges={graph.stats.omittedEdges} className="mt-2 text-[11px] text-faint">
          Graph bounded: {graph.stats.omittedContextNodes} context cards and {graph.stats.omittedEdges} relation groups omitted.
        </p>
      )}
      {graph.warnings.length > 0 && (
        <ul className="mt-2 space-y-1 text-[11px] text-muted" aria-label="Outcome map warnings">
          {graph.warnings.map((warning, index) => <li key={`${warning.kind}-${index}`}>{warning.message}</li>)}
        </ul>
      )}
    </section>
  );
}
