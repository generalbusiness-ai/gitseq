import { useMemo, useState } from "react";
import type { Workroom, Selection } from "../lib/store";
import { cn } from "../lib/util";
import { Railway } from "./Railway";
import { SequencePane } from "./SequencePane";
import { Deliverables } from "./Deliverables";

// Deliverables lead; the repo is context; the full record is the audit
// drill-down, reachable but never the destination.
export function RightRail({
  workroom,
  highlight,
  selection,
  onSelect,
}: {
  workroom: Workroom;
  highlight: { events: Set<string>; commits: Set<string> };
  selection?: Selection;
  onSelect: (selection: Selection) => void;
}) {
  const [tab, setTab] = useState<"shelf" | "repo" | "record">("shelf");
  const projection = workroom.status?.durable.projection;
  const nameOf = useMemo(() => {
    const byFingerprint = new Map(workroom.actors.map((a) => [a.fingerprint, a.name]));
    return (fp: string) =>
      byFingerprint.get(fp) ??
      projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fp)?.body?.name ??
      fp.slice(0, 8);
  }, [workroom.actors, projection]);

  return (
    <aside className="flex min-h-0 flex-col bg-background">
      <div className="flex gap-1 border-b border-border px-3 py-2" role="tablist">
        {(
          [
            ["shelf", "Deliverables"],
            ["repo", "Repository"],
            ["record", "Durable record"],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            role="tab"
            aria-selected={tab === key}
            onClick={() => setTab(key)}
            className={cn(
              "rounded-md px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-accent",
              tab === key ? "bg-elevated text-foreground" : "text-faint hover:text-muted",
            )}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "shelf" ? (
        <Deliverables
          projection={projection}
          nameOf={nameOf}
          onSelect={(next) => {
            onSelect(next);
            if (next.kind === "commit") setTab("repo");
          }}
        />
      ) : tab === "repo" ? (
        <Railway
          commits={workroom.commits}
          statements={projection?.statements ?? []}
          highlight={highlight}
          selection={selection}
          onSelect={onSelect}
        />
      ) : (
        <SequencePane
          projection={projection}
          actors={workroom.actors}
          highlight={highlight}
          selection={selection}
          onSelect={onSelect}
        />
      )}
    </aside>
  );
}
