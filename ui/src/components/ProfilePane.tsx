import { useEffect, useMemo, useRef, useState } from "react";
import { Bookmark, Check, Copy, X } from "lucide-react";
import { ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { cn } from "../lib/util";
import { Avatar } from "./Avatar";
import { parsePresenceLabel } from "../lib/interaction";
import { Ticket } from "./Stream";

// The profile pane: who an actor is and where they stand — computed entirely
// from the projection and live presence, no new endpoints. Opened by
// clicking any avatar or author name; docked like the thread pane.
export function ProfilePane({
  workroom,
  session,
  fingerprint,
  onClose,
  onJumpTo,
}: {
  workroom: Workroom;
  session: Session;
  fingerprint: string;
  onClose: () => void;
  onJumpTo: (event: string) => void;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const [copied, setCopied] = useState(false);
  const projection = workroom.status?.durable.projection;
  const tickets = useMemo(() => ticketsOf(projection), [projection]);

  const actor =
    workroom.actors.find((a) => a.fingerprint === fingerprint) ??
    (() => {
      const state = projection?.actors[fingerprint];
      return state ? { name: state.name || fingerprint.slice(0, 8), fingerprint, kind: state.kind, roles: state.roles, custody: false } : undefined;
    })();
  const name = actor?.name ?? fingerprint.slice(0, 8);

  // Online when any live session announces this actor's name.
  const online = Object.values(workroom.status?.live.presence ?? {}).some((value) => parsePresenceLabel(value).name === name);
  const isMe = session.actor === name;

  // Their recent durable acts, newest first — each row jumps the stream.
  const recent = useMemo(
    () =>
      (projection?.statements ?? [])
        .filter((s) => s.actor === fingerprint && !["roster", "infra-key", "seal"].includes(s.kind))
        .slice(-8)
        .reverse(),
    [projection, fingerprint],
  );

  // Focus discipline mirrors the thread pane: take focus, return it, Escape.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    panel.current?.focus();
    return () => opener?.focus();
  }, [fingerprint]);

  const copy = () => {
    navigator.clipboard?.writeText(fingerprint).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <div
      ref={panel}
      role="complementary"
      aria-label={`Profile — ${name}`}
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
      className="fixed inset-0 z-40 flex flex-col border-border bg-background outline-none sm:static sm:z-auto sm:w-[24rem] sm:shrink-0 sm:border-l"
    >
      <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
        <h2 className="text-sm font-semibold text-foreground/90">Profile</h2>
        <button onClick={onClose} aria-label="close profile" className="rounded p-1 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        <div className="flex items-center gap-4">
          <Avatar fingerprint={fingerprint} name={name} size={72} />
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="truncate text-lg font-semibold">{name}</h3>
              <span
                className={cn("inline-block h-2 w-2 shrink-0 rounded-full", online ? "bg-ok" : "border border-faint")}
                title={online ? "online" : "away"}
                aria-label={online ? "online" : "away"}
                role="img"
              />
            </div>
            <div className="mt-1 flex items-center gap-1.5">
              <code className="text-xs text-faint" title={fingerprint}>
                {fingerprint.slice(0, 16)}…
              </code>
              <button
                onClick={copy}
                aria-label="copy fingerprint"
                title="copy fingerprint"
                className="rounded p-0.5 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent"
              >
                {copied ? <Check className="h-3 w-3 text-ok" /> : <Copy className="h-3 w-3" />}
              </button>
            </div>
          </div>
        </div>

        <section className="mt-5">
          <h4 className="mb-1.5 text-xs font-medium text-faint">Recent</h4>
          {recent.length === 0 && <p className="px-1 text-xs italic text-faint">Nothing yet.</p>}
          <div className="space-y-0.5">
            {recent.map((statement) => (
              <button
                key={statement.event}
                onClick={() => onJumpTo(statement.event)}
                className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs hover:bg-elevated/60 focus-visible:outline focus-visible:outline-accent"
              >
                <Bookmark className="h-3 w-3 shrink-0 fill-current text-faint" aria-label="kept" />
                <span className={cn("min-w-0 truncate", statement.retired ? "text-faint line-through" : "text-foreground/90")}>
                  {statement.text}
                </span>
                <span className="ml-auto shrink-0">
                  <Ticket ticket={tickets.get(statement.event)} event={statement.event} onSelect={() => onJumpTo(statement.event)} />
                </span>
              </button>
            ))}
          </div>
        </section>

        {isMe && (
          <button
            onClick={() => {
              localStorage.removeItem("workroom.actor");
              location.reload();
            }}
            className="mt-5 rounded-md border border-border px-2.5 py-1 text-xs text-muted hover:bg-elevated hover:text-foreground focus-visible:outline focus-visible:outline-accent"
          >
            Switch identity
          </button>
        )}
      </div>
    </div>
  );
}
