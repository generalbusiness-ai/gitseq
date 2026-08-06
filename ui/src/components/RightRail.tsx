import { useState } from "react";
import type { Workroom } from "../lib/store";
import type { Selection } from "../lib/store";
import { cn } from "../lib/util";
import { Railway } from "./Railway";
import { SequencePane } from "./SequencePane";

// The good-enough git viewer and the formal sequence, one rail, two tabs.
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
  const [tab, setTab] = useState<"repo" | "sequence">("repo");
  const projection = workroom.status?.durable.projection;

  return (
    <aside className="flex min-h-0 flex-col bg-background">
      <div className="flex gap-1 border-b border-border px-3 py-2">
        {(
          [
            ["repo", "Repository"],
            ["sequence", "Agreed sequence"],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={cn(
              "rounded-md px-2.5 py-1 text-xs font-medium transition-colors",
              tab === key ? "bg-elevated text-foreground" : "text-faint hover:text-muted",
            )}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "repo" ? (
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
