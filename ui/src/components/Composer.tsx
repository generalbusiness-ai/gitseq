import { useEffect, useRef, useState } from "react";
import { Bookmark, Link2, Quote, SendHorizonal, X } from "lucide-react";
import { api, eventName, type FrameView } from "../lib/api";
import type { Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { loadDraft, saveDraft } from "../lib/memory";
import { mentionAt, mentionFingerprints } from "../lib/mentions";
import { RetryKeys } from "../lib/interaction";
import { cn } from "../lib/util";

// The composer exposes one choice: temporary or kept.
export type ComposerType = "say" | "assert";

export interface ComposerContext {
  type: ComposerType;
  restsOn: string[];
  frames: FrameView[];
  prefill?: string;
  prefillID?: string;
}

export const emptyComposer: ComposerContext = { type: "say", restsOn: [], frames: [] };

// Linking anything to a draft makes that draft worth keeping. The transport
// distinction between recorded events and signed chat frames stays internal.
function typeAfterLink(context: ComposerContext): ComposerType {
  return context.type === "say" ? "assert" : context.type;
}

export function toggleLinkEvent(context: ComposerContext, onContext: (c: ComposerContext) => void, event: string): void {
  const exists = context.restsOn.includes(event);
  onContext({
    ...context,
    type: exists ? context.type : typeAfterLink(context),
    restsOn: exists ? context.restsOn.filter((e) => e !== event) : [...context.restsOn, event],
  });
}

export function toggleLinkFrame(context: ComposerContext, onContext: (c: ComposerContext) => void, frame: FrameView): void {
  const key = (f: FrameView) => `${f.conversation}:${f.sequence}`;
  const exists = context.frames.some((f) => key(f) === key(frame));
  onContext({
    ...context,
    type: exists ? context.type : typeAfterLink(context),
    frames: exists ? context.frames.filter((f) => key(f) !== key(frame)) : [...context.frames, frame],
  });
}

// One textbox, one send button. Messages are temporary by default; the single
// bookmark control is the whole visible permanence model.
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [caret, setCaret] = useState(0);
  const [suggestionIndex, setSuggestionIndex] = useState(0);
  const [dismissedMention, setDismissedMention] = useState<number>();
  const box = useRef<HTMLTextAreaElement>(null);
  const retryKeys = useRef(new RetryKeys());
  const { type, restsOn, frames } = context;
  const durable = type !== "say";
  const statements = workroom.status?.durable.projection.statements ?? [];
  const textOf = (event: string) => statements.find((s) => s.event === event)?.text ?? eventName(event);

  // The draft survives a refresh — the one composer state worth keeping.
  useEffect(() => saveDraft(text), [text]);
  useEffect(() => {
    if (context.prefill !== undefined) {
      setText((current) => current.trim() ? `${current.trim()}\n\n${context.prefill}` : context.prefill!);
      requestAnimationFrame(() => box.current?.focus());
    }
  }, [context.prefillID]);

  // @mention autocomplete over the roster, keyed to the token at the caret.
  const activeMention = mentionAt(text, caret);
  const suggestions =
    activeMention && dismissedMention !== activeMention.start
      ? workroom.actors.filter((a) => a.name.toLowerCase().startsWith(activeMention.partial.toLowerCase()))
      : [];
  useEffect(() => setSuggestionIndex(0), [activeMention?.start, activeMention?.partial]);
  const applyMention = (name: string) => {
    if (!activeMention) return;
    const token = /\s/.test(name) ? `@"${name}"` : `@${name}`;
    const before = text.slice(0, activeMention.start) + token + " ";
    setText(before + text.slice(caret));
    setCaret(before.length);
    requestAnimationFrame(() => {
      box.current?.focus();
      box.current?.setSelectionRange(before.length, before.length);
    });
  };

  const send = async () => {
    if (!text.trim() || busy || !session.actor) return;
    setBusy(true);
    setError(undefined);
    if (!durable) {
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
      // The system files who was addressed: @name tokens resolve to roster
      // fingerprints and ride in body.mentions, space-separated.
      const mentioned = mentionFingerprints(text, workroom.actors);
      if (mentioned.length > 0) body.mentions = mentioned.join(" ");
      const input = {
        session: session.id,
        act: "state",
        kind: "assert",
        text: text.trim(),
        body: Object.keys(body).length ? body : undefined,
        rests_on: restsOn, // never fabricated; free-standing is honest
        evidence: Object.keys(evidence).length ? evidence : undefined,
      } as const;
      const payload = JSON.stringify(input);
      const intentKey = retryKeys.current.forAttempt("main", payload);
      await api.act({ ...input, idempotency_key: intentKey });
      retryKeys.current.succeeded("main", intentKey);
      onContext(emptyComposer);
      setText("");
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  const trackCaret = () => setCaret(box.current?.selectionStart ?? 0);
  const sendLabel = durable ? "keep" : "send message";

  return (
    <div className="border-t border-border bg-surface/60 px-4 py-3 sm:px-5">
      <div className="mx-auto max-w-3xl">
        <div className="mb-2 flex items-center gap-2">
          <button
            onClick={() => onContext({ ...context, type: durable ? "say" : "assert" })}
            aria-pressed={durable}
            className={cn(
              "flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium focus-visible:outline focus-visible:outline-accent",
              durable ? "bg-accent/12 text-accent-deep" : "text-faint hover:bg-elevated hover:text-muted",
            )}
          >
            <Bookmark className={cn("h-3.5 w-3.5", durable && "fill-current")} />
            {durable ? "Kept" : "Temporary"}
          </button>
        </div>
        {(restsOn.length > 0 || frames.length > 0) && (
          <div className="mb-2 flex flex-wrap items-center gap-2">
            {restsOn.map((event) => (
              <LinkChip
                key={event}
                icon={<Link2 className="h-3 w-3 shrink-0" />}
                label={textOf(event)}
                onRemove={() => onContext({ ...context, restsOn: restsOn.filter((e) => e !== event) })}
              />
            ))}
            {frames.map((frame) => (
              <LinkChip
                key={`${frame.conversation}:${frame.sequence}`}
                icon={<Quote className="h-3 w-3 shrink-0" />}
                label={frame.text}
                onRemove={() =>
                  onContext({
                    ...context,
                    frames: frames.filter((f) => !(f.conversation === frame.conversation && f.sequence === frame.sequence)),
                  })
                }
              />
            ))}
          </div>
        )}
        <div className="relative flex items-end gap-2">
          {suggestions.length > 0 && (
            <div
              role="listbox"
              aria-label="mention someone"
              className="absolute bottom-full left-0 z-10 mb-1 w-56 overflow-hidden rounded-lg border border-border bg-card shadow-xl"
            >
              {suggestions.map((actor, index) => (
                <button
                  key={actor.name}
                  role="option"
                  aria-selected={index === suggestionIndex}
                  onMouseDown={(e) => {
                    e.preventDefault(); // keep textarea focus
                    applyMention(actor.name);
                  }}
                  onMouseEnter={() => setSuggestionIndex(index)}
                  className={cn(
                    "flex w-full items-center justify-between px-3 py-1.5 text-left text-sm",
                    index === suggestionIndex ? "bg-elevated text-foreground" : "text-muted",
                  )}
                >
                  <span>@{actor.name}</span>
                </button>
              ))}
            </div>
          )}
          <textarea
            ref={box}
            value={text}
            rows={2}
            placeholder={durable ? "Keep something…" : "Message the room…"}
            aria-label="message"
            onChange={(e) => {
              setText(e.target.value);
              setCaret(e.target.selectionStart ?? 0);
              setDismissedMention(undefined);
            }}
            onClick={trackCaret}
            onKeyUp={trackCaret}
            onKeyDown={(e) => {
              if (suggestions.length > 0) {
                if (e.key === "ArrowDown") {
                  e.preventDefault();
                  setSuggestionIndex((i) => (i + 1) % suggestions.length);
                  return;
                }
                if (e.key === "ArrowUp") {
                  e.preventDefault();
                  setSuggestionIndex((i) => (i + suggestions.length - 1) % suggestions.length);
                  return;
                }
                if (e.key === "Enter" || e.key === "Tab") {
                  e.preventDefault();
                  applyMention(suggestions[suggestionIndex].name);
                  return;
                }
                if (e.key === "Escape") {
                  e.preventDefault();
                  setDismissedMention(activeMention?.start);
                  return;
                }
              }
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            className={cn(
              "min-w-0 flex-1 resize-none rounded-lg border border-input bg-surface px-3 py-2 text-sm outline-none placeholder:text-faint focus:border-accent/60",
            )}
          />
          <button
            onClick={() => void send()}
            disabled={busy || !text.trim() || !session.live}
            aria-label={sendLabel}
            title={session.live ? sendLabel : "not present yet"}
            className={cn(
              "flex h-9 w-9 items-center justify-center rounded-lg transition-colors focus-visible:outline focus-visible:outline-accent disabled:opacity-40",
              "bg-accent text-background hover:bg-accent/90",
            )}
          >
            <SendHorizonal className="h-4 w-4" />
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

function LinkChip({ icon, label, onRemove }: { icon: React.ReactNode; label: string; onRemove: () => void }) {
  return (
    <span className="flex max-w-full items-center gap-1.5 rounded-md border border-border bg-surface px-2 py-0.5 text-xs text-muted">
      {icon}
      <span className="truncate">linked: {label}</span>
      <button onClick={onRemove} aria-label="remove link" className="shrink-0 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
        <X className="h-3 w-3" />
      </button>
    </span>
  );
}
