import { cn, eventTimestamp, isRenderableTimestamp } from "../lib/util";

export function EventTime({ timestamp, className }: { timestamp?: number; className?: string }) {
  if (!timestamp) return null;
  const style = cn("shrink-0 font-mono text-[11px] tabular-nums text-faint", className);
  // new Date(t*1000).toISOString() throws RangeError beyond +/-8.64e15 ms, and
  // React 19 unmounts the whole tree on an uncaught render throw — so one
  // poisoned record used to take the entire page down, permanently, because a
  // reload replays the same record. Say the time is unreadable instead.
  if (!isRenderableTimestamp(timestamp)) {
    return (
      <span className={style} title={`Sequenced at an unreadable time (${timestamp})`}>
        unreadable time
      </span>
    );
  }
  const date = new Date(timestamp * 1000);
  return (
    <time
      dateTime={date.toISOString()}
      title={`Sequenced ${date.toString()}`}
      className={style}
    >
      {eventTimestamp(timestamp)}
    </time>
  );
}
