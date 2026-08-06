import type { Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { shortHash } from "../lib/api";
import { actorTint, cn } from "../lib/util";

export function TopBar({ workroom, session }: { workroom: Workroom; session: Session }) {
  const durable = workroom.status?.durable;
  const people = Object.values(workroom.status?.live.presence ?? {});

  return (
    <header className="flex items-center justify-between gap-6 border-b border-border px-6 py-3">
      <div className="flex items-baseline gap-3">
        <h1 className="font-serif text-xl font-semibold tracking-tight">The Workroom</h1>
        <span className="text-xs text-faint">
          talk freely · commit deliberately · everything auditable
        </span>
      </div>
      <div className="flex items-center gap-4">
        <div className="flex items-center -space-x-1">
          {people.length === 0 ? (
            <span className="text-xs text-faint">nobody here — durable state remains</span>
          ) : (
            people.map((person) => {
              const name = person.split(" ")[0];
              return (
                <span
                  key={person}
                  title={person}
                  className={cn(
                    "flex h-6 w-6 items-center justify-center rounded-full border border-border bg-elevated text-[10px] font-semibold uppercase",
                    actorTint(name),
                  )}
                >
                  {name.slice(0, 2)}
                </span>
              );
            })
          )}
        </div>
        <div className="h-4 w-px bg-border" />
        <div className="flex items-center gap-2 text-xs text-faint">
          {workroom.offline ? (
            <span className="text-danger">offline — durable data still in git</span>
          ) : durable ? (
            <>
              <span className={cn("inline-block h-1.5 w-1.5 rounded-full", session.live ? "pulse-dot bg-ok" : "bg-faint")} />
              <span>
                {durable.depth} events · <code className="text-muted">{shortHash(durable.head)}</code>
              </span>
            </>
          ) : (
            "connecting…"
          )}
        </div>
      </div>
    </header>
  );
}
