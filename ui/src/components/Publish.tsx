import { useState } from "react";
import { CircleSlash } from "lucide-react";
import { useModalFocus } from "../lib/modalFocus";

export interface PublishInput {
  path: string;
  commit: string;
  text: string;
  /** The record this pointer rests on, when the operator chose to cite one. */
  basis?: string;
}

// Publishing an artifact is the one durable act that starts from nothing: it
// answers no request and replies to no thread, so it has no row to hang off.
// Everything else the browser writes is a reply, which is why this is a dialog
// rather than another composer mode.
//
// The commit is typed, not chosen. The browser has no checkout, and the page
// this implements says so plainly: "Today you copy the commit hash yourself;
// this is the manual step a publish tool would later do from the push."
export function PublishArtifact({
  basis,
  busy,
  refusal,
  error,
  onPublish,
  onClose,
}: {
  /** The record on screen when this opened. A stamped predecessor rests on its replacement. */
  basis?: { event: string; label: string };
  /** Filed and not yet folded: the dialog waits rather than claiming the record is gone. */
  busy: boolean;
  /**
   * Why publishing is refused as of this render, or undefined when it is
   * allowed. Recomputed by the caller on every render from the same rule the
   * signing boundary asks, so a session that expires or a membership that is
   * superseded while this dialog is open closes the submit rather than
   * offering a record the fold will refuse. It carries the reason and not a
   * bare yes/no so the disabled control can say which refusal this is.
   */
  refusal?: string;
  error?: string;
  onPublish: (input: PublishInput) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState("");
  const [commit, setCommit] = useState("");
  const [text, setText] = useState("");
  const [cite, setCite] = useState(false);
  const ready = path.trim() !== "" && commit.trim() !== "" && text.trim() !== "";
  // Two different refusals, kept apart: an unfinished form is the operator's
  // own business, and lost authority is news about the room. They are reported
  // in different words, and the second is also shown in the dialog rather than
  // hidden in a tooltip, because it arrives while the form is being filled and
  // nothing else on this screen would announce it.
  const blocked = !ready || busy || refusal !== undefined;
  // Initial focus, containment, Escape and restoration, which is what
  // aria-modal below already claims. Escape closes rather than publishing:
  // abandoning a form nobody signed costs nothing.
  const dialog = useModalFocus<HTMLFormElement>(onClose);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 p-4 backdrop-blur-sm">
      <form
        ref={dialog}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-labelledby="publish-title"
        onSubmit={(event) => {
          event.preventDefault();
          // Enter in a field submits the form, which no disabled button can
          // stop, so the same gate is applied here. It is still not the
          // guarantee: `App`'s publish asks the rule again before it signs.
          if (blocked) return;
          onPublish({
            path: path.trim(),
            commit: commit.trim(),
            text: text.trim(),
            basis: cite && basis ? basis.event : undefined,
          });
        }}
        className="w-[28rem] max-w-full rounded-xl border border-border bg-card p-6 shadow-2xl outline-none"
      >
        <h2 id="publish-title" className="font-serif text-lg font-semibold">
          Publish an artifact
        </h2>
        <p className="mt-1 text-xs text-muted">
          A signed pointer to one path at one exact commit. It claims nothing about the content — adoption and review are
          separate acts.
        </p>
        <div className="mt-4 space-y-2">
          <input
            type="text"
            aria-label="what this artifact records"
            placeholder="what this artifact records"
            value={text}
            onChange={(event) => setText(event.target.value)}
            className="h-8 w-full rounded-md border border-input bg-surface px-2 text-xs outline-none placeholder:text-faint focus:border-accent/60"
          />
          <input
            type="text"
            aria-label="path"
            placeholder="docs/decisions/0001-use-postgres.md"
            value={path}
            onChange={(event) => setPath(event.target.value)}
            className="h-8 w-full rounded-md border border-input bg-surface px-2 font-mono text-xs outline-none placeholder:text-faint focus:border-accent/60"
          />
          <input
            type="text"
            aria-label="commit"
            placeholder="the exact commit, in full"
            value={commit}
            onChange={(event) => setCommit(event.target.value)}
            className="h-8 w-full rounded-md border border-input bg-surface px-2 font-mono text-xs outline-none placeholder:text-faint focus:border-accent/60"
          />
          {basis && (
            <label className="flex items-baseline gap-2 text-xs text-muted">
              <input type="checkbox" checked={cite} onChange={(event) => setCite(event.target.checked)} />
              <span className="min-w-0 truncate">rests on {basis.label}</span>
            </label>
          )}
        </div>
        {refusal && (
          <p role="status" className="mt-3 flex items-center gap-1 text-xs text-muted">
            <CircleSlash className="h-3 w-3 shrink-0" /> {refusal}
          </p>
        )}
        {busy && (
          <p className="mt-3 text-xs text-muted" role="status">
            Filed. Waiting for the workroom to record it…
          </p>
        )}
        {error && (
          <p role="alert" className="mt-3 flex items-center gap-1 text-xs text-danger">
            <CircleSlash className="h-3 w-3" /> {error}
          </p>
        )}
        <div className="mt-4 flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-md px-2 py-1 text-xs text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent"
          >
            cancel
          </button>
          <button
            type="submit"
            disabled={blocked}
            title={refusal ?? (ready ? undefined : "fill in what this artifact records, its path and its exact commit")}
            className="rounded-md bg-accent px-3 py-1 text-xs text-background transition-colors hover:bg-accent/90 focus-visible:outline focus-visible:outline-accent disabled:opacity-40"
          >
            publish
          </button>
        </div>
      </form>
    </div>
  );
}
