import { cn, eventTimestamp } from "../lib/util";

export function EventTime({ timestamp, className }: { timestamp?: number; className?: string }) {
  if (!timestamp) return null;
  const date = new Date(timestamp * 1000);
  const label = eventTimestamp(timestamp);
  return (
    <time
      dateTime={date.toISOString()}
      title={`Sequenced ${date.toString()}`}
      className={cn("shrink-0 font-mono text-[11px] tabular-nums text-faint", className)}
    >
      {label}
    </time>
  );
}
