import { useEffect, useMemo, useRef, useState } from "react";
import { Feather, Link2, Quote, SendHorizonal, X } from "lucide-react";
import { api, shortEvent, type FrameView } from "../lib/api";
import type { Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { loadDraft, saveDraft } from "../lib/memory";
import { mentionAt, mentionFingerprints, mentionNames } from "../lib/mentions";
import { cn } from "../lib/util";

// Semantic modes are reachable only through in-stream actions (Accept,
// Report done, Disagree, Needs work); they replace the type pills while
// active. The mode value drives the durable kind directly.
export type ComposerMode = "promise" | "report" | "dissent";

// The manual choice is one row of four: everything is a message; the type
// only decides whether — and as what — it is kept.
export type ComposerType = "say" | "assert" | "propose" | "request";

export interface ComposerContext {
  type: ComposerType;
  mode?: ComposerMode;
  restsOn: string[]; // cited recorded acts — the system routes these to rests_on
  frames: FrameView[]; // cited chat lines — routed to evidence as raw signed frames
  prefill?: string; // text a semantic route suggests; always editable
}

export const emptyComposer: ComposerContext = { type: "say", restsOn: [], frames: [] };

// The unified cite gesture, shared by the stream and the thread pane: a chat
// line and a recorded act select the same way, into the same tray; the
// system routes them (rests_on vs evidence) at send. Citing anything from
// plain Say turns the draft into a Note.
function typeAfterCite(context: ComposerContext): ComposerType {
  return context.mode === undefined && context.type === "say" ? "assert" : context.type;
}

export function toggleCiteEvent(context: ComposerContext, onContext: (c: ComposerContext) => void, event: string): void {
  const exists = context.restsOn.includes(event);
  onContext({
    ...context,
    type: exists ? context.type : typeAfterCite(context),
    restsOn: exists ? context.restsOn.filter((e) => e !== event) : [...context.restsOn, event],
  });
}

export function toggleCiteFrame(context: ComposerContext, onContext: (c: ComposerContext) => void, frame: FrameView): void {
  const key = (f: FrameView) => `${f.conversation}:${f.sequence}`;
  const exists = context.frames.some((f) => key(f) === key(frame));
  onContext({
    ...context,
    type: exists ? context.type : typeAfterCite(context),
    frames: exists ? context.frames.filter((f) => key(f) !== key(frame)) : [...context.frames, frame],
  });
}

const modeCopy: Record<ComposerMode, { banner: string; noun: string; placeholder: string }> = {
  promise: { banner: "promise", noun: "promise", placeholder: "what you undertake…" },
  report: { banner: "report", noun: "report", placeholder: "what was done…" },
  dissent: { banner: "objection", noun: "objection", placeholder: "what do you object to…" },
};

const pills: { type: ComposerType; label: string }[] = [
  { type: "say", label: "Say" },
  { type: "assert", label: "Note" },
  { type: "propose", label: "Proposal" },
  { type: "request", label: "Request" },
];

const typePlaceholder: Record<ComposerType, string> = {
  say: "say something…",
  assert: "a note for the record…",
  propose: "what do you propose…",
  request: "what do you ask…",
};

// One textbox, one send button. The type pills carry the whole distinction:
// Say evaporates with the room; the rest are kept, signed, in the record.
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
  const [to, setTo] = useState("");
  const [conditions, setConditions] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [caret, setCaret] = useState(0);
  const [suggestionIndex, setSuggestionIndex] = useState(0);
  const [dismissedMention, setDismissedMention] = useState<number>();
  const box = useRef<HTMLTextAreaElement>(null);
  const appliedPrefill = useRef<string | undefined>(undefined);
  const { type, mode, restsOn, frames } = context;
  const durable = mode !== undefined || type !== "say";
  // One idempotency key per user intention, held across retries.
  const intentKey = useMemo(
    () => crypto.randomUUID(),
    [mode, type, frames.length, busy === false && text],
  );
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

  // @mention autocomplete over the roster, keyed to the token at the caret.
  const activeMention = mentionAt(text, caret);
  const suggestions =
    activeMention && dismissedMention !== activeMention.start
      ? workroom.actors.filter((a) => a.name.toLowerCase().startsWith(activeMention.partial.toLowerCase()))
      : [];
  useEffect(() => setSuggestionIndex(0), [activeMention?.start, activeMention?.partial]);
  const applyMention = (name: string) => {
    if (!activeMention) return;
    const before = text.slice(0, activeMention.start) + "@" + name + " ";
    setText(before + text.slice(caret));
    setCaret(before.length);
    requestAnimationFrame(() => {
      box.current?.focus();
      box.current?.setSelectionRange(before.length, before.length);
    });
  };

  // The absent-mention hint: chat only reaches whoever is present. Presence
  // values read "name (fingerprint…)"; the first token is the actor name.
  const presence = workroom.status?.live.presence ?? {};
  const presentNames = new Set(Object.values(presence).map((value) => value.split(" ")[0].toLowerCase()));
  const knownNames = new Map(workroom.actors.map((a) => [a.name.toLowerCase(), a.name]));
  const absentMention =
    !durable && !suggestions.length
      ? mentionNames(text)
          .map((name) => knownNames.get(name.toLowerCase()))
          .find(
            (name): name is string =>
              !!name && name !== session.actor && !presentNames.has(name.toLowerCase()),
          )
      : undefined;

  // The last-holder cue: live presence is exactly this session. Quiet, inline,
  // honest about the witness's floor — never a modal.
  const lastHolder = session.live && Object.keys(presence).length === 1 && presence[session.id] !== undefined;

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
      const effectiveKind = mode ?? type;
      if (effectiveKind === "request") {
        if (!to || !conditions.trim()) throw new Error("a request names its performer and conditions");
        body.to = "@" + to;
        body.conditions = conditions.trim();
      }
      // The system files who was addressed: @name tokens resolve to roster
      // fingerprints and ride in body.mentions, space-separated.
      const mentioned = mentionFingerprints(text, workroom.actors);
      if (mentioned.length > 0) body.mentions = mentioned.join(" ");
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
      onContext(emptyComposer);
      setTo("");
      setConditions("");
      setText("");
    } catch (thrown) {
      setError(thrown instanceof Error ? thrown.message : String(thrown));
    } finally {
      setBusy(false);
    }
  };

  const trackCaret = () => setCaret(box.current?.selectionStart ?? 0);
  const sendLabel = mode
    ? `set down the ${modeCopy[mode].noun}`
    : type === "say"
      ? "say it"
      : `set down the ${pills.find((p) => p.type === type)!.label.toLowerCase()}`;

  return (
    <div className="border-t border-border bg-surface/60 px-4 py-3 sm:px-5">
      <div className="mx-auto max-w-3xl">
        {lastHolder && <p className="mb-1.5 text-xs italic text-faint">Only you are holding this conversation.</p>}
        <div className="mb-2 flex flex-wrap items-center gap-2">
          {mode ? (
            <>
              <span className="rounded-md border border-border bg-elevated px-2 py-0.5 text-xs text-muted">
                {modeCopy[mode].banner}
              </span>
              <button
                onClick={() => {
                  onContext(emptyComposer);
                  setTo("");
                  setConditions("");
                }}
                aria-label="cancel — back to plain talk"
                title="cancel — back to plain talk"
                className="rounded p-1 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </>
          ) : (
            pills.map((pill) => (
              <button
                key={pill.type}
                onClick={() => onContext({ ...context, type: pill.type })}
                aria-pressed={pill.type === type}
                className={cn(
                  "rounded-md px-2 py-0.5 text-xs font-medium focus-visible:outline focus-visible:outline-accent",
                  pill.type === type
                    ? "border border-accent/50 bg-accent/10 text-foreground"
                    : "text-faint hover:text-muted",
                )}
              >
                {pill.label}
              </button>
            ))
          )}
          {!mode && type === "request" && (
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
        </div>
        {(restsOn.length > 0 || frames.length > 0) && (
          <div className="mb-2 flex flex-wrap items-center gap-2">
            {restsOn.map((event) => (
              <CiteChip
                key={event}
                icon={<Link2 className="h-3 w-3 shrink-0" />}
                label={textOf(event)}
                onRemove={() => onContext({ ...context, restsOn: restsOn.filter((e) => e !== event) })}
              />
            ))}
            {frames.map((frame) => (
              <CiteChip
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
                  <span className="text-xs text-faint">{actor.role}</span>
                </button>
              ))}
            </div>
          )}
          <textarea
            ref={box}
            value={text}
            rows={durable ? 2 : 1}
            placeholder={mode ? modeCopy[mode].placeholder : typePlaceholder[type]}
            aria-label={durable ? "formal statement" : "message"}
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
              "min-w-0 flex-1 resize-none rounded-lg border border-input bg-surface px-3 py-2 outline-none placeholder:text-faint",
              durable ? "font-serif text-[15px] focus:border-accent/60" : "text-sm focus:border-input",
            )}
          />
          <button
            onClick={() => void send()}
            disabled={busy || !text.trim() || !session.live}
            aria-label={sendLabel}
            title={session.live ? sendLabel : "not present yet"}
            className={cn(
              "flex h-9 w-9 items-center justify-center rounded-lg transition-colors focus-visible:outline focus-visible:outline-accent disabled:opacity-40",
              durable
                ? "bg-accent text-background hover:bg-accent/90"
                : "border border-border text-muted hover:bg-elevated hover:text-foreground",
            )}
          >
            {durable ? <Feather className="h-4 w-4" /> : <SendHorizonal className="h-4 w-4" />}
          </button>
        </div>
        {absentMention && (
          <p className="mt-1.5 text-xs text-faint">
            {absentMention} isn't here —{" "}
            <button
              onClick={() => {
                onContext({ ...context, type: "request" });
                setTo(absentMention);
              }}
              className="text-accent-deep underline-offset-2 hover:underline focus-visible:outline focus-visible:outline-accent"
            >
              make it a request?
            </button>
          </p>
        )}
        {error && (
          <p role="alert" className="mt-1.5 text-xs text-danger">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}

// One chip style for every citation. The system decides the wire routing
// (rests_on for recorded acts, evidence frames for chat); only the tiny
// icon betrays which — a link for the record, a quote for talk.
function CiteChip({ icon, label, onRemove }: { icon: React.ReactNode; label: string; onRemove: () => void }) {
  return (
    <span className="flex max-w-full items-center gap-1.5 rounded-md border border-border bg-surface px-2 py-0.5 text-xs text-muted">
      {icon}
      <span className="truncate">citing: {label}</span>
      <button onClick={onRemove} aria-label="remove citation" className="shrink-0 text-faint hover:text-foreground focus-visible:outline focus-visible:outline-accent">
        <X className="h-3 w-3" />
      </button>
    </span>
  );
}
