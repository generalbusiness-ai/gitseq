import { useEffect, useRef, useState } from "react";
import { AtSign, Bell, Inbox } from "lucide-react";
import {
  forYouItems,
  markAllForYouRead,
  markForYouRead,
  NOTHING_READ,
  ticketsOf,
  unreadForYou,
  type ForYouRead,
  type Workroom,
} from "../lib/store";
import type { Session } from "../lib/session";
import { loadForYouRead, saveForYouRead } from "../lib/memory";
import { cn, firstLine, kindLabel } from "../lib/util";
import { Avatar } from "./Avatar";
import { EventTime } from "./EventTime";
import { fingerprintOfPresentActor, presentActors, toggleActivityFocus } from "../lib/interaction";

export function TopBar({
  workroom,
  session,
  onJumpEvent,
  selectedEvent,
}: {
  workroom: Workroom;
  session: Session;
  onJumpEvent: (event: string) => void;
  /** The thread that is open, if one is: what advisory focus would name. */
  selectedEvent?: string;
}) {
  const durable = workroom.status?.durable;
  const people = presentActors(workroom.status?.live.presence, workroom.status?.live.activity);
  const tickets = ticketsOf(durable?.projection);
  const selectedFocused = Boolean(selectedEvent && session.activity?.focus.includes(selectedEvent));
  const fingerprintOf = (name: string) => workroom.actors.find((a) => a.name === name)?.fingerprint ?? "";
  const nameOfFingerprint = (fingerprint: string) =>
    workroom.actors.find((a) => a.fingerprint === fingerprint)?.name ?? `${fingerprint.slice(0, 8)}…`;

  // "For you": durable acts addressed to me — a request put to me, or a
  // mention of me. They hang off my own face in the bar, because that is
  // where a reader looks for what is theirs, and they open as a list rather
  // than a count, so choosing which one to read is the reader's move.
  const genesis = durable?.genesis ?? "";
  const myFingerprint = fingerprintOf(session.actor ?? "");
  const [read, setRead] = useState<ForYouRead>(NOTHING_READ);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    setRead(loadForYouRead(genesis, myFingerprint));
  }, [genesis, myFingerprint]);
  const mine = forYouItems(durable?.projection, myFingerprint || undefined);
  const unseen = unreadForYou(mine, read);
  const allTickets = mine.map((item) => item.ticket);
  const remember = (next: ForYouRead) => {
    saveForYouRead(genesis, myFingerprint, next);
    setRead(next);
  };
  const openItem = (event: string, ticket: number) => {
    onJumpEvent(event);
    remember(markForYouRead(allTickets, read, ticket));
    setOpen(false);
  };

  // The panel closes on Escape and on a click anywhere outside it, so it never
  // becomes a thing you have to dismiss by finding the button again.
  const panelRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    const onPointer = (event: MouseEvent) => {
      if (!panelRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onPointer);
    };
  }, [open]);

  return (
    <header className="flex items-center justify-between gap-3 border-b border-border px-3 py-2.5 sm:gap-6 sm:px-6">
      <div className="flex min-w-0 flex-1 items-baseline gap-3">
        <h1 className="shrink-0 font-serif text-lg font-semibold tracking-tight sm:text-xl">The Workroom</h1>
        {workroom.repo && (
          <span className="truncate font-mono text-xs text-faint" title={workroom.repo}>
            {workroom.repo}
          </span>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2 sm:gap-4">
        {session.actor && (
          <div className="hidden items-center gap-1 md:flex" aria-label="Advisory activity">
            <select
              aria-label="Activity status"
              value={session.activity?.status ?? "available"}
              onChange={(event) => session.setActivity({ status: event.target.value as import("../lib/api").ActivityStatus })}
              className="h-7 rounded border border-border bg-background px-1.5 text-[11px] text-muted outline-none focus:border-accent/60"
            >
              <option value="available">available</option>
              <option value="busy">busy</option>
              <option value="waiting">waiting</option>
              <option value="blocked">blocked</option>
            </select>
            {selectedEvent && (
              <button
                type="button"
                aria-pressed={selectedFocused}
                title="Advisory focus only; this does not claim or complete work"
                onClick={() => session.setActivity({ focus: toggleActivityFocus(session.activity.focus, selectedEvent) })}
                className={cn("h-7 rounded border px-1.5 text-[11px] focus-visible:outline focus-visible:outline-accent", selectedFocused ? "border-accent/60 bg-accent/10 text-accent" : "border-border text-muted")}
              >
                {selectedFocused ? "unfocus" : `focus #${tickets.get(selectedEvent) ?? "?"}`}
              </button>
            )}
            {(session.activity?.focus.length ?? 0) > 0 && (
              <button type="button" onClick={() => session.setActivity({ focus: [] })} className="h-7 rounded px-1 text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent">
                clear
              </button>
            )}
          </div>
        )}
        <div className="hidden items-center -space-x-1.5 sm:flex">
          {people.length === 0 ? (
            <span className="text-xs text-faint">nobody here</span>
          ) : (
            people.map((person) => {
              const fingerprint = fingerprintOfPresentActor(person, workroom.actors);
              return (
                <span key={person.label} className="relative">
                  <Avatar
                    fingerprint={fingerprint}
                    name={person.name}
                    title={`${person.name} — ${person.status}${person.note ? ` — ${person.note}` : ""}${person.sessions > 1 ? ` — ${person.sessions} sessions` : ""}`}
                    size={24}
                    className="ring-2 ring-background"
                  />
                  {person.sessions > 1 && (
                    <span
                      aria-hidden
                      className="pointer-events-none absolute -bottom-0.5 -right-0.5 rounded-full bg-elevated px-1 font-mono text-[9px] leading-[1.3] text-muted ring-1 ring-background"
                    >
                      {person.sessions}
                    </span>
                  )}
                </span>
              );
            })
          )}
        </div>
        {session.actor && (
          <div ref={panelRef} className="relative">
            <button
              type="button"
              aria-haspopup="menu"
              aria-expanded={open}
              title={unseen.length > 0 ? `${session.actor} — ${unseen.length} for you` : `${session.actor} — nothing for you`}
              onClick={() => setOpen((was) => !was)}
              className={cn(
                "flex items-center gap-1.5 rounded-md border px-1.5 py-0.5 text-xs transition-colors focus-visible:outline focus-visible:outline-accent",
                unseen.length > 0
                  ? "border-accent/50 bg-accent/10 text-accent hover:bg-accent/20"
                  : "border-border text-foreground/85 hover:border-accent/40",
              )}
            >
              <span className="relative flex">
                <Avatar fingerprint={myFingerprint} name={session.actor} size={18} />
                {unseen.length > 0 && (
                  <span
                    aria-hidden
                    className="pointer-events-none absolute -bottom-1 -right-1.5 flex items-center gap-px rounded-full bg-accent px-1 py-px text-[9px] font-semibold leading-none text-background ring-1 ring-background"
                  >
                    <Bell className="h-2 w-2" />
                    {unseen.length}
                  </span>
                )}
              </span>
              {session.actor}
            </button>
            {open && (
              <div
                role="menu"
                aria-label="For you"
                className="absolute right-0 top-full z-30 mt-1.5 max-h-[70vh] w-[min(24rem,calc(100vw-1.5rem))] overflow-y-auto rounded-md border border-border bg-elevated shadow-lg"
              >
                <div className="flex items-center justify-between gap-2 border-b border-border px-2.5 py-1.5">
                  <span className="text-[11px] font-medium text-muted">
                    {unseen.length > 0 ? `${unseen.length} for you` : "for you"}
                  </span>
                  {unseen.length > 0 && (
                    <button
                      type="button"
                      onClick={() => remember(markAllForYouRead(allTickets, read))}
                      className="rounded px-1 text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
                    >
                      mark all read
                    </button>
                  )}
                </div>
                {unseen.length === 0 ? (
                  <p className="flex items-center gap-1.5 px-2.5 py-3 text-xs text-faint">
                    <Inbox className="h-3.5 w-3.5" />
                    Nothing addressed to you is unread.
                  </p>
                ) : (
                  <ul>
                    {unseen.map((item) => (
                      <li key={item.event}>
                        <button
                          type="button"
                          role="menuitem"
                          onClick={() => openItem(item.event, item.ticket)}
                          className="flex w-full flex-col gap-0.5 border-b border-border/60 px-2.5 py-2 text-left last:border-b-0 hover:bg-accent/10 focus-visible:outline focus-visible:-outline-offset-2 focus-visible:outline-accent"
                        >
                          <span className="flex items-center gap-1.5 text-[11px] text-muted">
                            {item.reason === "request" ? (
                              <AtSign className="h-3 w-3 shrink-0 text-accent" aria-hidden />
                            ) : (
                              <AtSign className="h-3 w-3 shrink-0 text-faint" aria-hidden />
                            )}
                            <span className="font-mono tabular-nums text-faint">#{item.ticket}</span>
                            <span className="truncate">
                              {nameOfFingerprint(item.actor)} {item.reason === "request" ? "asked you" : "mentioned you"}
                            </span>
                            <span className="ml-auto shrink-0 rounded bg-background px-1 font-mono text-[10px] text-faint">
                              {kindLabel(item.kind)}
                            </span>
                          </span>
                          <span className="line-clamp-2 text-xs text-foreground/90">{firstLine(item.text, 160)}</span>
                          {item.timestamp ? (
                            <EventTime timestamp={item.timestamp} className="text-[10px]" />
                          ) : null}
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
          </div>
        )}
        <div className="hidden h-4 w-px bg-border sm:block" />
        <div className="flex items-center gap-2 text-xs text-faint">
          {workroom.offline ? (
            <span className="text-danger">offline</span>
          ) : durable ? (
            <span
              className={cn("inline-block h-1.5 w-1.5 rounded-full", session.live ? "pulse-dot bg-ok" : "bg-faint")}
              aria-label={session.live ? "connected" : "connecting"}
              title={session.live ? "connected" : "connecting"}
            />
          ) : (
            "connecting…"
          )}
        </div>
      </div>
    </header>
  );
}
