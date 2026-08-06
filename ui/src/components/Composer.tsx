import { useEffect, useMemo, useRef, useState } from "react";
import { Feather, SendHorizonal, X } from "lucide-react";
import { api, shortEvent, type FrameView } from "../lib/api";
import type { Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { loadDraft, saveDraft } from "../lib/memory";
import { cn, kindLabel } from "../lib/util";

// Semantic modes are reachable only through in-stream actions (Accept,
// Report done, Disagree, Needs work); the manual chooser stays limited to
// note/proposal/request. The mode value drives the durable kind directly.
export type ComposerMode = "promise" | "report" | "dissent";

export interface ComposerContext {
  setDown: boolean; // the deliberate step from talk to record
  mode?: ComposerMode;
  restsOn: string[];
  frames: FrameView[];
  prefill?: string; // text a semantic route suggests; always editable
}

type ManualKind = "assert" | "propose" | "request";

const modeCopy: Record<ComposerMode, { banner: string; placeholder: string }> = {
  promise: { banner: "a promise — resting on the request", placeholder: "what you undertake…" },
  report: { banner: "reporting done — the requester declares satisfaction", placeholder: "what was done…" },
  dissent: { banner: "an objection — resting on what it contests, visible forever", placeholder: "what do you object to…" },
};

// Two gestures: say something, or set something down. The smallest
// contextual question appears only after the deliberate step.
export function Composer({
  workroom,
  session,
  context,
  onContext,
  onSay,
  onSayFailed,
}: {
  workroom: Workroom;
  session: Session;
  context: ComposerContext;
  onContext: (context: ComposerContext) => void;
  onSay: (text: string) => string; // optimistic echo; returns a pending id
  onSayFailed: (id: string) => void;
}) {
  const [text, setText] = useState(loadDraft);
  const [kind, setKind] = useState<ManualKind>("propose");
  const [to, setTo] = useState("");
  const [conditions, setConditions] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const appliedPrefill = useRef<string | undefined>(undefined);
  // One idempotency key per user intention, held across retries.
  const intentKey = useMemo(() => crypto.randomUUID(), [context.setDown, context.frames.length, busy === false && text]);
  const { setDown, mode, restsOn, frames } = context;
  const statements = workroom.status?.durable.projection.statements ?? [];
  const textOf = (event: string) => statements.find((s) => s.event === event)?.text ?? shortEvent(event);

  // A semantic route may suggest text ("I will do this."); apply it once per
  // route, never clobbering what the user has since typed.
  useEffect(() => {
    if (context.prefill !== undefined && context.prefill !== appliedPrefill.current) {
      appliedPrefill.current = context.prefill;
      setText(context.prefill);
    }
    if (context.prefill === undefined) appliedPrefill.current = undefined;
  }, [context.prefill]);

  // The draft survives a refresh — the one composer state worth keeping.
  useEffect(() => saveDraft(text), [text]);

  // The last-holder cue: live presence is exactly this session. Quiet, inline,
  // honest about the witness's floor — never a modal.
  const presence = workroom.status?.live.presence ?? {};
  const lastHolder = session.live && Object.keys(presence).length === 1 && presence[session.id] !== undefined;

  const send = async () => {
    if (!text.trim() || busy || !session.actor) return;
    setBusy(true);
    setError(undefined);
    if (!setDown) {
      const line = text.trim();
      const pendingID = onSay(line); // optimistic: it appears before the round trip
      setText("");
      try {
        await api.say(session.id, "the workroom", line);
      } catch (thrown) {
        onSayFailed(pendingID);
        setText(line); // give the words back rather than losing them
        setError(thrown instanceof Error ? thrown.message : String(thrown));
      } finally {
        setBusy(false);
      }
      return;
    }
    try {
      const evidence: Record<string, string> = {};
      // Complete signed frames — a stranger can verify the promotion after
      // the conversation itself is forgotten.
      if (frames.length > 0) evidence["frames.json"] = JSON.stringify(frames.map((f) => f.raw), null, 2);
      const body: Record<string, string> = {};
      const effectiveKind = mode ?? kind;
      if (effectiveKind === "request") {
        if (!to || !conditions.trim()) throw new Error("a request names its performer and conditions");
        body.to = "@" + to;
        body.conditions = conditions.trim();
      }
      await api.act({
        session: session.id,
        act: "state",
        kind: effectiveKind,
        text: text.trim(),
        body: Object.keys(body).length ? body : undefined,
        rests_on: restsOn, // never fabricated; free-standing is honest
        evidence: Object.keys(evidence).length ? evidence : undefined,
        idempotency_key: intentKey,
      });
      onContext({ setDown: false, restsOn: [], frames: [] });
      setTo("");
      setConditions("");
      setText("");
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-border bg-surface/60 px-4 py-3 sm:px-5">
      <div className="mx-auto max-w-3xl">
        {lastHolder && (
          <p className="mb-1.5 text-xs italic text-faint">
            You're the last one holding this conversation — what leaves with you is forgotten.
          </p>
        )}
        {setDown && (
          <div className="mb-2 flex flex-wrap items-center gap-2">
            {mode ? (
              <span className="rounded-md border border-border bg-elevated px-2 py-0.5 text-xs text-muted">
                {modeCopy[mode].banner}
              </span>
            ) : (
              (["assert", "propose", "request"] as const).map((candidate) => (
                <button
                  key={candidate}
                  onClick={() => setKind(candidate)}
                  className={cn(
                    "rounded-md px-2 py-0.5 text-xs font-medium capitalize focus-visible:outline focus-visible:outline-accent",
                    candidate === kind ? "border border-accent/50 bg-accent/10 text-foreground" : "text-faint hover:text-muted",
                  )}
                >
                  {kindLabel[candidate] ?? candidate}
                </button>
              ))
            )}
            {restsOn.map((event) => (
              <Chip key={event} label={`rests on: ${textOf(event)}`} onRemove={() => onContext({ ...context, restsOn: restsOn.filter((e) => e !== event) })} />
            ))}
            {frames.length > 0 && (
              <Chip accent label={`${frames.length} message${frames.length > 1 ? "s" : ""} as evidence`} onRemove={() => onContext({ ...context, frames: [] })} />
            )}
            {!mode && kind === "request" && (
              <span className="flex w-full flex-col gap-2 text-xs sm:w-auto sm:flex-row sm:items-center">
                <span className="flex items-center gap-2">
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
                </span>
                <span className="flex min-w-0 items-center gap-2">
                  <label className="shrink-0 text-faint">satisfied when</label>
                  <input
                    value={conditions}
                    onChange={(e) => setConditions(e.target.value)}
                    placeholder="conditions of satisfaction"
                    className="w-full min-w-0 rounded-md border border-input bg-surface px-2.5 py-1 outline-none placeholder:text-faint focus:border-accent/60 sm:w-56"
                  />
                </span>
              </span>
            )}
            <button
              onClick={() => onContext({ ...context, setDown: false, mode: undefined, prefill: undefined })}
              aria-label="back to plain talk"
              title="back to plain talk"
              className="ml-auto rounded p-1 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
        <div className="flex items-end gap-2">
          <textarea
            value={text}
            rows={setDown ? 2 : 1}
            placeholder={setDown ? (mode ? modeCopy[mode].placeholder : "what goes on the record…") : "say something…"}
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
          {!setDown && (
            <button
              onClick={() => onContext({ ...context, setDown: true })}
              title="set this down — a durable, signed act"
              className="flex h-9 items-center gap-1.5 rounded-lg border border-border px-3 text-sm text-faint transition-colors hover:text-muted focus-visible:outline focus-visible:outline-accent"
            >
              <Feather className="h-4 w-4" />
              Set down…
            </button>
          )}
          <button
            onClick={() => void send()}
            disabled={busy || !text.trim() || !session.live}
            title={session.live ? undefined : "not present yet"}
            className={cn(
              "flex h-9 items-center gap-1.5 rounded-lg px-3.5 text-sm font-medium transition-colors focus-visible:outline focus-visible:outline-accent disabled:opacity-40",
              setDown ? "bg-accent text-background hover:bg-accent/90" : "border border-border text-muted hover:bg-elevated hover:text-foreground",
            )}
          >
            {setDown ? <Feather className="h-4 w-4" /> : <SendHorizonal className="h-4 w-4" />}
            {setDown ? "Set down" : "Say"}
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
    <span className={cn("flex max-w-full items-center gap-1 rounded-md border px-2 py-0.5 text-xs", accent ? "border-accent/50 bg-accent/10 text-foreground" : "border-border bg-surface text-muted")}>
      <span className="truncate">{label}</span>
      <button onClick={onRemove} aria-label="remove" className="shrink-0 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
        <X className="h-3 w-3" />
      </button>
    </span>
  );
}
