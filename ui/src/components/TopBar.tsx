import { useEffect, useState } from "react";
import { AtSign, PanelRight } from "lucide-react";
import { forYouItems, workSummary, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { loadForYouWatermark, saveForYouWatermark } from "../lib/memory";
import { shortHash } from "../lib/api";
import { actorTint, cn } from "../lib/util";

export function TopBar({
  workroom,
  session,
  onOpenWork,
  onJumpEvent,
}: {
  workroom: Workroom;
  session: Session;
  onOpenWork: () => void;
  onJumpEvent: (event: string) => void;
}) {
  const durable = workroom.status?.durable;
  const people = Object.values(workroom.status?.live.presence ?? {});
  const summary = workSummary(durable?.projection);

  // "For you": durable acts addressed to me since the stored watermark.
  // Clicking steps to the oldest unseen one and marks it read; each click
  // advances one act, so nothing addressed to you can be skipped unseen.
  const genesis = durable?.genesis ?? "";
  const myFingerprint = workroom.actors.find((a) => a.name === session.actor)?.fingerprint ?? "";
  const [watermark, setWatermark] = useState(0);
  useEffect(() => {
    setWatermark(loadForYouWatermark(genesis, myFingerprint));
  }, [genesis, myFingerprint]);
  const unseen = forYouItems(durable?.projection, myFingerprint || undefined, watermark);
  const readOldest = () => {
    const oldest = unseen[0];
    if (!oldest) return;
    onJumpEvent(oldest.event);
    saveForYouWatermark(genesis, myFingerprint, oldest.ticket);
    setWatermark(oldest.ticket);
  };

  return (
    <header className="flex items-center justify-between gap-3 border-b border-border px-3 py-3 sm:gap-6 sm:px-6">
      <div className="flex min-w-0 flex-1 items-baseline gap-3">
        <h1 className="truncate font-serif text-lg font-semibold tracking-tight sm:text-xl">The Workroom</h1>
        <span className="hidden text-xs text-faint lg:inline">
          talk freely · commit deliberately · everything auditable
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-2 sm:gap-4">
        <div className="hidden items-center -space-x-1 sm:flex">
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
                    "flex h-6 w-6 items-center justify-center rounded-full border border-border bg-elevated text-xs font-semibold uppercase",
                    actorTint(name),
                  )}
                >
                  {name.slice(0, 2)}
                </span>
              );
            })
          )}
        </div>
        {unseen.length > 0 && (
          <button
            onClick={readOldest}
            title="durable acts addressed to you — click to read the oldest unseen"
            className="flex items-center gap-1 rounded-md border border-accent/50 bg-accent/10 px-2 py-0.5 text-xs font-medium text-accent transition-colors hover:bg-accent/20 focus-visible:outline focus-visible:outline-accent"
          >
            <AtSign className="h-3 w-3" />
            {unseen.length}
            <span className="hidden sm:inline"> for you</span>
          </button>
        )}
        {session.actor && (
          <button
            onClick={() => {
              localStorage.removeItem("workroom.actor");
              location.reload();
            }}
            title="signing as — click to leave and rejoin as someone else"
            className={cn("rounded-md border border-border px-2 py-0.5 text-xs", actorTint(session.actor))}
          >
            {session.actor}
          </button>
        )}
        <button
          onClick={onOpenWork}
          title="Work — what needs attention, what's owed, what stands"
          className={cn(
            "flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs text-muted transition-colors hover:bg-elevated hover:text-foreground focus-visible:outline focus-visible:outline-accent",
          )}
        >
          <PanelRight className="h-3.5 w-3.5" />
          <span className={cn(summary.stale > 0 && "text-danger")}>{summary.stale} stale</span>
          <span aria-hidden className="text-faint">
            ·
          </span>
          <span>{summary.open} open</span>
          <span aria-hidden className="text-faint">
            ·
          </span>
          <span className={cn(summary.done > 0 && "text-ok")}>{summary.done} done</span>
        </button>
        <div className="hidden h-4 w-px bg-border sm:block" />
        <div className="flex items-center gap-2 text-xs text-faint">
          {workroom.offline ? (
            <span className="text-danger">offline — durable data still in git</span>
          ) : durable ? (
            <>
              <span className={cn("inline-block h-1.5 w-1.5 rounded-full", session.live ? "pulse-dot bg-ok" : "bg-faint")} />
              <span className="hidden sm:inline">
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
