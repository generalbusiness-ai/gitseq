import { useEffect, useState } from "react";
import { api, type Rebuild } from "../lib/api";

// What a reader sees while the resident verifies durable history from cold.
//
// The wording is the point. It says verification is happening and how far it
// has got; it does not say something failed, and it does not show work data,
// because on a fold-profile change there is no verified projection to show —
// the previous checkpoint is invalid under a contract this binary no longer
// implements. An empty page with an explanation is honest. A page of stale
// rows labelled current is not, and neither is a spinner that implies the
// answer is nearly here when nobody knows that.
export function RebuildNotice({ poll = 1000 }: { poll?: number }) {
  const [rebuild, setRebuild] = useState<Rebuild>();

  useEffect(() => {
    let live = true;
    const read = () =>
      api
        .rebuild()
        .then((next) => live && setRebuild(next))
        // A failed poll is not evidence of a failed rebuild. Keep the last
        // reading rather than claiming something went wrong.
        .catch(() => undefined);
    read();
    const timer = setInterval(read, poll);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [poll]);

  if (!rebuild?.running) {
    return <p className="py-12 text-center text-sm text-faint">Loading work…</p>;
  }

  const total = rebuild.total ?? 0;
  const verified = rebuild.verified ?? 0;
  // Only claim a proportion after the kernel has enumerated the cold history.
  // Before that, running is still meaningful but no denominator is known.
  const measured = total > 0;
  const percent = measured ? Math.min(100, Math.round((verified / total) * 100)) : 0;
  const complete = measured && verified >= total;
  const heading = !measured
    ? "Preparing to verify durable history"
    : complete
      ? "Preparing the verified work view"
      : "Verifying durable history";

  return (
    <div
      className="mx-auto max-w-xl py-16 text-center"
      role="status"
      aria-live="polite"
      aria-label={heading}
    >
      <p className="font-serif text-lg text-foreground/90">{heading}</p>
      <p className="mt-1 text-xs text-faint">
        This workroom's signed log is being checked from the beginning, one record at a time. On a
        large history this expected check can take several minutes. Work appears after verification
        and projection finish; no unverified work is shown.
      </p>
      {measured && (
        <>
          <div
            className="mx-auto mt-4 h-1 w-64 overflow-hidden rounded-full bg-border"
            role="progressbar"
            aria-valuenow={verified}
            aria-valuemin={0}
            aria-valuemax={total}
            aria-valuetext={`${verified} of ${total} records verified`}
          >
            <div className="h-full bg-accent transition-[width] duration-300" style={{ width: `${percent}%` }} />
          </div>
          <p className="mt-2 font-mono text-[10px] text-faint">
            verified {verified.toLocaleString()} of {total.toLocaleString()}
          </p>
        </>
      )}
    </div>
  );
}
