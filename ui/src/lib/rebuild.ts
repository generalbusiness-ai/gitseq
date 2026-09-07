import type { Rebuild, Status } from "./api";

// What an already-open page says about the status it is still showing while
// the resident is verifying durable history from cold.
//
// The page keeps the last status it received: that status was current and
// verified when the resident sent it, and nothing the browser can do makes
// it more or less true now. What must not happen is showing it as if it were
// current. So while the resident reports a rebuild in progress, the page
// names the frontier it is showing and says the resident is verifying, with
// the count when the kernel has one. A new reader with no status sees the
// rebuild notice instead; this qualifier is only for a retained status, and
// it never turns that status into permission to act — every act still goes
// to the resident and is judged against the current log.
export function rebuildQualifier(status: Status | undefined, rebuild: Rebuild | undefined): string | undefined {
  if (!status || !rebuild?.running) return undefined;
  const head = status.durable.head.slice(0, 8);
  const depth = status.durable.depth;
  const total = rebuild.total ?? 0;
  const verified = rebuild.verified ?? 0;
  const progress = total > 0 ? `${verified.toLocaleString()} of ${total.toLocaleString()} records verified` : "counting records";
  return `Showing frontier ${head} at depth ${depth.toLocaleString()} from before the resident began verifying durable history; ${progress}. Nothing here is current until that finishes.`;
}

// probeRebuild asks the resident, once a second, whether it is verifying from
// cold, for as long as one wait is outstanding. Each wait owns one probe: the
// stop function returned here is that wait's generation guard, so an answer
// that arrives after the wait returned (or after the page was torn down) is
// dropped rather than qualifying a newer status. At most one request is in
// flight; a tick that finds one outstanding does nothing, so a slow endpoint
// never accumulates overlapping requests. The timer is unreferenced where the
// runtime allows, so a probe whose wait never returns cannot keep a process
// alive by itself.
export function probeRebuild(
  fetchRebuild: () => Promise<Rebuild>,
  report: (rebuild: Rebuild | undefined) => void,
  intervalMs = 1000,
): () => void {
  let stopped = false;
  let inFlight = false;
  const timer = setInterval(() => {
    if (inFlight) return;
    inFlight = true;
    fetchRebuild()
      .then((next) => {
        if (!stopped) report(next.running ? next : undefined);
      })
      .catch(() => undefined)
      .finally(() => {
        inFlight = false;
      });
  }, intervalMs);
  (timer as unknown as { unref?: () => void }).unref?.();
  return () => {
    stopped = true;
    clearInterval(timer);
  };
}
