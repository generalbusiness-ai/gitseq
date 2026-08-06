import { useMemo, useState } from "react";
import { Feather, SendHorizonal, X } from "lucide-react";
import { api, shortEvent, type FrameView } from "../lib/api";
import type { Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { cn, kindTint } from "../lib/util";

export interface ComposerContext {
  setDown: boolean; // the deliberate step from talk to record
  restsOn: string[];
  frames: FrameView[];
}

type FormalKind = "assert" | "propose" | "request";

// Two gestures: say something, or set something down. The smallest
// contextual question appears only after the deliberate step.
export function Composer({
  workroom,
  session,
  context,
  onContext,
}: {
  workroom: Workroom;
  session: Session;
  context: ComposerContext;
  onContext: (context: ComposerContext) => void;
}) {
  const [text, setText] = useState("");
  const [kind, setKind] = useState<FormalKind>("propose");
  const [to, setTo] = useState("");
  const [conditions, setConditions] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // One idempotency key per user intention, held across retries.
  const intentKey = useMemo(() => crypto.randomUUID(), [context.setDown, context.frames.length, busy === false && text]);
  const { setDown, restsOn, frames } = context;
  const statements = workroom.status?.durable.projection.statements ?? [];
  const textOf = (event: string) => statements.find((s) => s.event === event)?.text ?? shortEvent(event);

  const send = async () => {
    if (!text.trim() || busy || !session.actor) return;
    setBusy(true);
    setError(undefined);
    try {
      if (!setDown) {
        await api.say(session.id, "the workroom", text.trim());
      } else {
        const evidence: Record<string, string> = {};
        // Complete signed frames — a stranger can verify the promotion after
        // the conversation itself is forgotten.
        if (frames.length > 0) evidence["frames.json"] = JSON.stringify(frames.map((f) => f.raw), null, 2);
        const body: Record<string, string> = {};
        if (kind === "request") {
          if (!to || !conditions.trim()) throw new Error("a request names its performer and conditions");
          body.to = "@" + to;
          body.conditions = conditions.trim();
        }
        await api.act({
          session: session.id,
          act: "state",
          kind,
          text: text.trim(),
          body: Object.keys(body).length ? body : undefined,
          rests_on: restsOn, // never fabricated; free-standing is honest
          evidence: Object.keys(evidence).length ? evidence : undefined,
          idempotency_key: intentKey,
        });
        onContext({ setDown: false, restsOn: [], frames: [] });
        setTo("");
        setConditions("");
      }
      setText("");
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-border bg-surface/60 px-5 py-3">
      <div className="mx-auto max-w-3xl">
        {setDown && (
          <div className="mb-2 flex flex-wrap items-center gap-2">
            {(["assert", "propose", "request"] as const).map((candidate) => (
              <button
                key={candidate}
                onClick={() => setKind(candidate)}
                className={cn(
                  "rounded-md px-2 py-0.5 text-[11px] font-medium capitalize focus-visible:outline focus-visible:outline-accent",
                  candidate === kind ? cn("border", kindTint[candidate]) : "text-faint hover:text-muted",
                )}
              >
                {candidate === "assert" ? "note" : candidate === "propose" ? "proposal" : "request"}
              </button>
            ))}
            {restsOn.map((event) => (
              <Chip key={event} label={`rests on: ${textOf(event)}`} onRemove={() => onContext({ ...context, restsOn: restsOn.filter((e) => e !== event) })} />
            ))}
            {frames.length > 0 && (
              <Chip accent label={`${frames.length} message${frames.length > 1 ? "s" : ""} as evidence`} onRemove={() => onContext({ ...context, frames: [] })} />
            )}
            {kind === "request" && (
              <span className="flex items-center gap-2 text-xs">
                <label className="text-faint">to</label>
                <select value={to} onChange={(e) => setTo(e.target.value)} className="rounded-md border border-input bg-surface px-2 py-1 outline-none focus:border-accent/60">
                  <option value="">choose…</option>
                  {workroom.actors
                    .filter((a) => a.name !== session.actor)
                    .map((a) => (
                      <option key={a.name} value={a.name}>
                        {a.name}
                      </option>
                    ))}
                </select>
                <label className="text-faint">satisfied when</label>
                <input
                  value={conditions}
                  onChange={(e) => setConditions(e.target.value)}
                  placeholder="conditions of satisfaction"
                  className="w-56 rounded-md border border-input bg-surface px-2.5 py-1 outline-none placeholder:text-faint focus:border-accent/60"
                />
              </span>
            )}
          </div>
        )}
        <div className="flex items-end gap-2">
          <textarea
            value={text}
            rows={setDown ? 2 : 1}
            placeholder={setDown ? "what goes on the record…" : "say something…"}
            aria-label={setDown ? "formal statement" : "message"}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            className={cn(
              "min-w-0 flex-1 resize-none rounded-lg border border-input bg-surface px-3 py-2 outline-none placeholder:text-faint",
              setDown ? "font-serif text-[15px] focus:border-accent/60" : "text-sm focus:border-input",
            )}
          />
          <button
            onClick={() => onContext({ ...context, setDown: !setDown })}
            aria-pressed={setDown}
            title={setDown ? "back to plain talk" : "set this down — a durable, signed act"}
            className={cn(
              "flex h-9 items-center gap-1.5 rounded-lg border px-3 text-sm transition-colors focus-visible:outline focus-visible:outline-accent",
              setDown ? "border-accent/60 bg-accent/10 text-accent" : "border-border text-faint hover:text-muted",
            )}
          >
            <Feather className="h-4 w-4" />
            {setDown ? "on the record" : "set down"}
          </button>
          <button
            onClick={() => void send()}
            disabled={busy || !text.trim() || !session.live}
            title={session.live ? undefined : "not present yet"}
            className={cn(
              "flex h-9 items-center gap-1.5 rounded-lg px-3.5 text-sm font-medium transition-colors focus-visible:outline focus-visible:outline-accent disabled:opacity-40",
              setDown ? "bg-accent text-background hover:bg-accent/90" : "border border-border text-muted hover:bg-elevated hover:text-foreground",
            )}
          >
            <SendHorizonal className="h-4 w-4" />
            {setDown ? "commit" : "say"}
          </button>
        </div>
        {error && (
          <p role="alert" className="mt-1.5 text-xs text-danger">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}

function Chip({ label, accent, onRemove }: { label: string; accent?: boolean; onRemove: () => void }) {
  return (
    <span className={cn("flex max-w-full items-center gap-1 rounded-md border px-2 py-0.5 text-[11px]", accent ? "border-accent/50 bg-accent/10 text-accent" : "border-border bg-surface text-muted")}>
      <span className="truncate">{label}</span>
      <button onClick={onRemove} aria-label="remove" className="shrink-0 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
        <X className="h-3 w-3" />
      </button>
    </span>
  );
}
