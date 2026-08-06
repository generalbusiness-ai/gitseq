import { useMemo } from "react";
import { GitBranch, Link2, X } from "lucide-react";
import type { GraphCommit, Statement } from "../lib/api";
import { shortEvent, shortHash } from "../lib/api";
import type { Selection } from "../lib/store";
import { cn, kindTint, timeAgo } from "../lib/util";

const ROW = 46;
const LANE = 24;
const LEFT = 14;
const laneColors = ["#34d399", "#fbbf24", "#38bdf8", "#a78bfa", "#2dd4bf", "#f87171"];

interface Node {
  commit: GraphCommit;
  lane: number;
  row: number;
}

// Classic railway lane assignment over newest-first topological order: each
// lane carries the hash it expects next; a commit lands on the first lane
// expecting it (or a fresh one), then its parents book the ongoing lanes.
function layout(commits: GraphCommit[]): { nodes: Node[]; lanes: number } {
  const nodes: Node[] = [];
  const lanes: (string | null)[] = [];
  let maxLane = 0;

  const claim = (hash: string): number => {
    const free = lanes.indexOf(null);
    if (free !== -1) {
      lanes[free] = hash;
      return free;
    }
    lanes.push(hash);
    return lanes.length - 1;
  };

  commits.forEach((commit, row) => {
    let lane = lanes.indexOf(commit.hash);
    if (lane === -1) lane = claim(commit.hash);
    for (let i = 0; i < lanes.length; i++) {
      if (i !== lane && lanes[i] === commit.hash) lanes[i] = null;
    }
    lanes[lane] = commit.parents[0] ?? null;
    for (const parent of commit.parents.slice(1)) {
      if (!lanes.includes(parent)) claim(parent);
    }
    maxLane = Math.max(maxLane, lanes.length - 1);
    nodes.push({ commit, lane, row });
  });
  return { nodes, lanes: maxLane + 1 };
}

export function Railway({
  commits,
  statements,
  highlight,
  selection,
  onSelect,
}: {
  commits: GraphCommit[];
  statements: Statement[];
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
}) {
  const { nodes, lanes } = useMemo(() => layout(commits), [commits]);
  const byHash = useMemo(() => new Map(nodes.map((n) => [n.commit.hash, n])), [nodes]);
  const graphWidth = LEFT + lanes * LANE + 10;
  const height = nodes.length * ROW;

  const x = (lane: number) => LEFT + lane * LANE;
  const y = (row: number) => row * ROW + ROW / 2;

  const selectedCommit =
    selection?.kind === "commit" ? commits.find((commit) => commit.hash === selection.id) : undefined;

  return (
    <section className="flex h-full min-h-0 flex-col bg-background">
      <PaneTitle icon={<GitBranch className="h-3.5 w-3.5" />} title="git railway" hint="the repo underneath — ordinary commits, branches, PRs" />
      <div className="relative min-h-0 flex-1 overflow-auto">
        {nodes.length === 0 && (
          <p className="p-6 text-sm text-faint">No commits yet — the railway begins with your first ordinary git commit.</p>
        )}
        <div className="relative" style={{ height }}>
          {/* Rows first; the graph SVG paints on top so selection tint never hides it. */}
          {nodes.map((node) => {
            const commit = node.commit;
            const selected = selection?.kind === "commit" && selection.id === commit.hash;
            const bright = highlight.commits.has(commit.hash);
            return (
              <button
                key={commit.hash}
                onClick={() => onSelect({ kind: "commit", id: commit.hash })}
                className={cn(
                  "absolute right-0 flex h-[46px] w-full items-center gap-3 border-l-2 border-transparent pr-4 text-left transition-colors",
                  "hover:bg-card/70",
                  selected && "border-accent bg-card/80",
                  bright && !selected && "bg-accent/5",
                )}
                style={{ top: node.row * ROW, paddingLeft: graphWidth + 6 }}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-[13px] text-foreground">{commit.subject}</span>
                    {commit.refs?.map((ref) => (
                      <span key={ref} className="shrink-0 border border-ok/40 px-1.5 text-xs leading-4 text-ok">
                        {ref}
                      </span>
                    ))}
                  </div>
                  <div className="flex items-center gap-2 text-xs text-faint">
                    <code className="text-accent-deep">{shortHash(commit.hash)}</code>
                    <span>{commit.author}</span>
                    <span>{timeAgo(commit.time)}</span>
                    {commit.rests_on && commit.rests_on.length > 0 && (
                      <span className="flex items-center gap-1 text-accent">
                        <Link2 className="h-3 w-3" />
                        rests on {commit.rests_on.length} agreed
                      </span>
                    )}
                  </div>
                </div>
              </button>
            );
          })}
          <svg width={graphWidth} height={height} className="pointer-events-none absolute left-0 top-0">
            {nodes.map((node) =>
              node.commit.parents.map((parent) => {
                const target = byHash.get(parent);
                const x1 = x(node.lane);
                const y1 = y(node.row);
                if (!target) {
                  return (
                    <path
                      key={node.commit.hash + parent}
                      d={`M ${x1} ${y1} L ${x1} ${y1 + ROW}`}
                      stroke={laneColors[node.lane % laneColors.length]}
                      strokeOpacity={0.25}
                      strokeDasharray="2 4"
                      fill="none"
                    />
                  );
                }
                const x2 = x(target.lane);
                const y2 = y(target.row);
                const bend = Math.min(ROW * 0.9, (y2 - y1) / 2);
                return (
                  <path
                    key={node.commit.hash + parent}
                    d={`M ${x1} ${y1} C ${x1} ${y1 + bend}, ${x2} ${y2 - bend}, ${x2} ${y2}`}
                    stroke={laneColors[target.lane % laneColors.length]}
                    strokeOpacity={0.55}
                    strokeWidth={1.5}
                    fill="none"
                  />
                );
              }),
            )}
            {nodes.map((node) => {
              const cx = x(node.lane);
              const cy = y(node.row);
              const color = laneColors[node.lane % laneColors.length];
              const bright = highlight.commits.has(node.commit.hash);
              return (
                <g key={node.commit.hash}>
                  {bright && <circle cx={cx} cy={cy} r={9} fill="none" stroke="#fbbf24" strokeOpacity={0.9} />}
                  <circle cx={cx} cy={cy} r={4.5} fill="#18181b" stroke={color} strokeWidth={2} />
                </g>
              );
            })}
          </svg>
        </div>
      </div>
      {selectedCommit && (
        <CommitDetail
          commit={selectedCommit}
          statements={statements}
          onSelect={onSelect}
          onClose={() => onSelect({ kind: "commit", id: selectedCommit.hash })}
        />
      )}
    </section>
  );
}

// The inspector for a selected commit: full message, and the agreed events
// it is bridged to — both directions (cited via Rests-On, and artifact
// statements that cite it).
function CommitDetail({
  commit,
  statements,
  onSelect,
  onClose,
}: {
  commit: GraphCommit;
  statements: Statement[];
  onSelect: (selection: Selection) => void;
  onClose: () => void;
}) {
  const byEvent = useMemo(() => new Map(statements.map((s) => [s.event, s])), [statements]);
  const cited = (commit.rests_on ?? []).map((id) => ({ id, statement: byEvent.get(id) }));
  const citing = statements.filter((s) => s.kind === "artifact" && s.body?.commit === commit.hash);

  return (
    <div className="max-h-[42%] overflow-auto border-t border-border bg-card/95 px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-display text-[15px] text-foreground">{commit.subject}</span>
            {commit.refs?.map((ref) => (
              <span key={ref} className="shrink-0 border border-ok/40 px-1.5 text-xs leading-4 text-ok">
                {ref}
              </span>
            ))}
          </div>
          <div className="mt-0.5 flex items-center gap-2 text-xs text-faint">
            <code className="text-accent-deep">{commit.hash}</code>
            <span>{commit.author}</span>
            <span>{timeAgo(commit.time)}</span>
          </div>
        </div>
        <button onClick={onClose} className="shrink-0 p-1 text-faint hover:text-foreground">
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
      {commit.body && (
        <pre className="mt-2 whitespace-pre-wrap border-l-2 border-border pl-3 font-mono text-xs leading-relaxed text-muted">
          {commit.body}
        </pre>
      )}
      {(cited.length > 0 || citing.length > 0) && (
        <div className="mt-2.5 space-y-1">
          {cited.map(({ id, statement }) => (
            <BridgeChip key={id} label="rests on" id={id} statement={statement} onSelect={onSelect} />
          ))}
          {citing.map((statement) => (
            <BridgeChip key={statement.event} label="cited by" id={statement.event} statement={statement} onSelect={onSelect} />
          ))}
        </div>
      )}
    </div>
  );
}

function BridgeChip({
  label,
  id,
  statement,
  onSelect,
}: {
  label: string;
  id: string;
  statement?: Statement;
  onSelect: (selection: Selection) => void;
}) {
  return (
    <button
      onClick={() => onSelect({ kind: "event", id })}
      className="flex w-full items-center gap-2 border border-border/60 px-2 py-1 text-left text-xs transition-colors hover:border-accent-deep"
    >
      <span className="flex shrink-0 items-center gap-1 text-xs uppercase tracking-wide text-accent">
        <Link2 className="h-3 w-3" /> {label}
      </span>
      {statement ? (
        <>
          <span className={cn("shrink-0 border px-1 text-xs uppercase leading-4", kindTint[statement.kind] ?? "text-muted border-border")}>
            {statement.kind}
          </span>
          <span className="truncate text-foreground">{statement.text}</span>
        </>
      ) : (
        <code className="text-faint">{shortEvent(id)}</code>
      )}
    </button>
  );
}

export function PaneTitle({ icon, title, hint }: { icon: React.ReactNode; title: string; hint: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border/60 px-4 py-2.5">
      <span className="flex shrink-0 items-center gap-2 whitespace-nowrap text-xs uppercase tracking-[0.16em] text-muted">
        {icon}
        {title}
      </span>
      <span className="hidden max-w-[65%] truncate text-xs text-faint xl:inline">{hint}</span>
    </div>
  );
}
