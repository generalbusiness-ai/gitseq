import { useEffect, useState } from "react";
import { AtSign, PanelRight } from "lucide-react";
import { forYouItems, workSummary, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { loadForYouWatermark, saveForYouWatermark } from "../lib/memory";
import { shortHash } from "../lib/api";
import { cn } from "../lib/util";
import { Avatar } from "./Avatar";

export function TopBar({
  workroom,
  session,
  onOpenWork,
  onJumpEvent,
  onOpenProfile,
}: {
  workroom: Workroom;
  session: Session;
  onOpenWork: () => void;
  onJumpEvent: (event: string) => void;
  onOpenProfile: (fingerprint: string) => void;
}) {
  const durable = workroom.status?.durable;
  const people = Object.values(workroom.status?.live.presence ?? {});
  const summary = workSummary(durable?.projection);
  const fingerprintOf = (name: string) => workroom.actors.find((a) => a.name === name)?.fingerprint ?? "";

  // "For you": durable acts addressed to me since the stored watermark.
  // Clicking steps to the oldest unseen one and marks it read; each click
  // advances one act, so nothing addressed to you can be skipped unseen.
  const genesis = durable?.genesis ?? "";
  const myFingerprint = fingerprintOf(session.actor ?? "");
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
    <header className="flex items-center justify-between gap-3 border-b border-border px-3 py-2.5 sm:gap-6 sm:px-6">
      <div className="flex min-w-0 flex-1 items-baseline gap-3">
        <h1 className="truncate font-serif text-lg font-semibold tracking-tight sm:text-xl">The Workroom</h1>
      </div>
      <div className="flex shrink-0 items-center gap-2 sm:gap-4">
        <div className="hidden items-center -space-x-1.5 sm:flex">
          {people.length === 0 ? (
            <span className="text-xs text-faint">nobody here</span>
          ) : (
            people.map((person) => {
              const name = person.split(" ")[0];
              const fingerprint = fingerprintOf(name);
              return (
                <Avatar
                  key={person}
                  fingerprint={fingerprint}
                  name={name}
                  size={24}
                  onClick={() => onOpenProfile(fingerprint)}
                  className="ring-2 ring-background"
                />
              );
            })
          )}
        </div>
        {unseen.length > 0 && (
          <button
            onClick={readOldest}
            title="for you"
            className="flex items-center gap-1 rounded-md border border-accent/50 bg-accent/10 px-2 py-0.5 text-xs font-medium text-accent transition-colors hover:bg-accent/20 focus-visible:outline focus-visible:outline-accent"
          >
            <AtSign className="h-3 w-3" />
            {unseen.length}
            <span className="hidden sm:inline"> for you</span>
          </button>
        )}
        {session.actor && (
          <button
            onClick={() => onOpenProfile(myFingerprint)}
            title="you"
            className="flex items-center gap-1.5 rounded-md border border-border px-1.5 py-0.5 text-xs text-foreground/85 hover:bg-elevated focus-visible:outline focus-visible:outline-accent"
          >
            <Avatar fingerprint={myFingerprint} name={session.actor} size={18} />
            {session.actor}
          </button>
        )}
        <button
          onClick={onOpenWork}
          title="Work"
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
            <span className="text-danger">offline</span>
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
