import { useEffect, useMemo, useRef, useState } from "react";
import { Check, Copy, X } from "lucide-react";
import { ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { cn, kindLabel, kindTint } from "../lib/util";
import { Avatar } from "./Avatar";
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
      const roster = projection?.statements.find((s) => s.kind === "roster" && s.body?.actor === fingerprint);
      return roster ? { name: roster.body!.name ?? fingerprint.slice(0, 8), fingerprint, role: roster.body!.role ?? "" } : undefined;
    })();
  const name = actor?.name ?? fingerprint.slice(0, 8);

  // Online when any live session announces this actor's name.
  const online = Object.values(workroom.status?.live.presence ?? {}).some((value) => value.split(" ")[0] === name);
  const isMe = session.actor === name;

  // Standing, from the projection: what they owe, kept, carried, contested.
  const stats = useMemo(() => {
    if (!projection) return { open: 0, kept: 0, ratified: 0, dissents: 0 };
    const mine = (projection.commitments ?? []).filter((c) => c.performer === fingerprint);
    const statements = projection.statements ?? [];
    return {
      open: mine.filter((c) => ["promised", "reported"].includes(c.status)).length,
      kept: mine.filter((c) => c.status === "satisfied").length,
      ratified: statements.filter((s) => s.kind === "propose" && s.actor === fingerprint && s.ratified).length,
      dissents: statements.filter((s) => s.kind === "dissent" && s.actor === fingerprint).length,
    };
  }, [projection, fingerprint]);

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
        <h2 className="text-xs font-medium uppercase tracking-[0.16em] text-muted">Profile</h2>
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
            {actor?.role && <p className="text-xs text-muted">{actor.role}</p>}
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

        <div className="mt-4 grid grid-cols-2 gap-2">
          <Stat value={stats.open} label="open promises" />
          <Stat value={stats.kept} label="kept" tone={stats.kept > 0 ? "ok" : undefined} />
          <Stat value={stats.ratified} label="proposals ratified" />
          <Stat value={stats.dissents} label="dissents" tone={stats.dissents > 0 ? "danger" : undefined} />
        </div>

        <section className="mt-5">
          <h4 className="mb-1.5 text-xs font-medium uppercase tracking-[0.14em] text-faint">Recent acts</h4>
          {recent.length === 0 && <p className="px-1 text-xs italic text-faint">Nothing yet.</p>}
          <div className="space-y-0.5">
            {recent.map((statement) => (
              <button
                key={statement.event}
                onClick={() => onJumpTo(statement.event)}
                className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs hover:bg-elevated/60 focus-visible:outline focus-visible:outline-accent"
              >
                <span className={cn("shrink-0 rounded border px-1 text-[10px] font-medium uppercase leading-4 tracking-wide", kindTint[statement.kind] ?? "border-border text-muted")}>
                  {kindLabel[statement.kind] ?? statement.kind}
                </span>
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

function Stat({ value, label, tone }: { value: number; label: string; tone?: "ok" | "danger" }) {
  return (
    <div className="rounded-lg border border-border px-3 py-2">
      <div className={cn("text-lg font-semibold leading-6", tone === "ok" && "text-ok", tone === "danger" && "text-danger")}>{value}</div>
      <div className="text-xs text-muted">{label}</div>
    </div>
  );
}
