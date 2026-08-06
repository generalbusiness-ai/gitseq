import { useState } from "react";
import { SendHorizonal, X } from "lucide-react";
import { api, shortEvent, type FrameView } from "../lib/api";
import type { Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { cn, kindTint } from "../lib/util";

export type ComposerMode = "say" | "assert" | "propose" | "request" | "promise" | "report" | "dissent";

export interface ComposerContext {
  mode: ComposerMode;
  restsOn: string[];
  frames: FrameView[];
}

const modes: ComposerMode[] = ["say", "assert", "propose", "request", "promise", "report", "dissent"];

const modeHint: Record<ComposerMode, string> = {
  say: "ephemeral — forgotten when the room empties",
  assert: "a claim you can ground; cite what grounds it",
  propose: "seeks ratification; a decision is a ratified proposal",
  request: "asks someone to act, with conditions of satisfaction",
  promise: "an undertaking — never one you can't keep",
  report: "claims completion; the requester declares satisfaction",
  dissent: "objection, attached forever to what it contests",
};

// Every workroom action, one composer. Say is the default; durable modes
// carry rests_on (from replies), evidence (selected frames), and the
// request's structured fields.
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
  const [to, setTo] = useState("");
  const [conditions, setConditions] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const { mode, restsOn, frames } = context;
  const durable = mode !== "say";
  const statements = workroom.status?.durable.projection.statements ?? [];
  const textOf = (event: string) => statements.find((s) => s.event === event)?.text ?? shortEvent(event);

  const send = async () => {
    if (!text.trim() || busy || !session.actor) return;
    setBusy(true);
    setError(undefined);
    try {
      if (mode === "say") {
        await api.say(session.id, "the workroom", text.trim());
      } else {
        const evidence: Record<string, string> = {};
        if (frames.length > 0) {
          evidence["frames.json"] = JSON.stringify(
            frames.map((f) => ({ conversation: f.conversation, sequence: f.sequence, actor: f.actor, text: f.text })),
            null,
            2,
          );
        }
        const body: Record<string, string> = {};
        if (mode === "request") {
          if (!to || !conditions.trim()) throw new Error("a request names its performer and conditions");
          body.to = "@" + to;
          body.conditions = conditions.trim();
        }
        await api.act({
          session: session.id,
          act: "state",
          kind: mode,
          text: text.trim(),
          body: Object.keys(body).length ? body : undefined,
          rests_on: restsOn.length ? restsOn : ["seed"],
          evidence: Object.keys(evidence).length ? evidence : undefined,
        });
        onContext({ mode: "say", restsOn: [], frames: [] });
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
        <div className="flex items-center gap-1">
          {modes.map((candidate) => (
            <button
              key={candidate}
              onClick={() => onContext({ ...context, mode: candidate })}
              className={cn(
                "rounded-md px-2 py-0.5 text-[11px] font-medium capitalize transition-colors",
                candidate === mode
                  ? candidate === "say"
                    ? "bg-elevated text-foreground"
                    : cn("border", kindTint[candidate])
                  : "text-faint hover:text-muted",
              )}
            >
              {candidate}
            </button>
          ))}
          <span className="ml-auto hidden text-[10.5px] text-faint lg:inline">{modeHint[mode]}</span>
        </div>

        {(restsOn.length > 0 || frames.length > 0) && durable && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {restsOn.map((event) => (
              <Chip
                key={event}
                label={`rests on: ${textOf(event)}`}
                onRemove={() => onContext({ ...context, restsOn: restsOn.filter((e) => e !== event) })}
              />
            ))}
            {frames.length > 0 && (
              <Chip
                accent
                label={`${frames.length} frame${frames.length > 1 ? "s" : ""} as evidence`}
                onRemove={() => onContext({ ...context, frames: [] })}
              />
            )}
          </div>
        )}

        {mode === "request" && (
          <div className="mt-2 flex items-center gap-2 text-xs">
            <label className="text-faint">to</label>
            <select
              value={to}
              onChange={(event) => setTo(event.target.value)}
              className="rounded-md border border-input bg-surface px-2 py-1 outline-none focus:border-accent/60"
            >
              <option value="">choose…</option>
              {workroom.actors
                .filter((actor) => actor.name !== session.actor)
                .map((actor) => (
                  <option key={actor.name} value={actor.name}>
                    {actor.name} · {actor.role}
                  </option>
                ))}
            </select>
            <label className="ml-2 text-faint">satisfied when</label>
            <input
              value={conditions}
              onChange={(event) => setConditions(event.target.value)}
              placeholder="the conditions of satisfaction"
              className="min-w-0 flex-1 rounded-md border border-input bg-surface px-2.5 py-1 outline-none placeholder:text-faint focus:border-accent/60"
            />
          </div>
        )}

        <div className="mt-2 flex items-end gap-2">
          <div className="flex items-center gap-1.5 self-stretch">
            <select
              value={session.actor ?? ""}
              onChange={(event) => session.setActor(event.target.value)}
              title="acting as"
              className="h-full rounded-md border border-input bg-surface px-2 text-xs outline-none focus:border-accent/60"
            >
              {workroom.actors.map((actor) => (
                <option key={actor.name} value={actor.name}>
                  {actor.name}
                </option>
              ))}
            </select>
          </div>
          <textarea
            value={text}
            rows={mode === "say" ? 1 : 2}
            placeholder={mode === "say" ? "say something ephemeral…" : `set down a ${mode}…`}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
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
            title={session.live ? undefined : "not present — waiting for the room"}
            className={cn(
              "flex h-9 items-center gap-1.5 rounded-lg px-3.5 text-sm font-medium transition-colors disabled:opacity-40",
              durable
                ? "bg-accent text-background hover:bg-accent/90"
                : "border border-border text-muted hover:bg-elevated hover:text-foreground",
            )}
          >
            <SendHorizonal className="h-4 w-4" />
            {durable ? "commit" : "say"}
          </button>
        </div>
        {error && <p className="mt-1.5 text-xs text-danger">{error}</p>}
      </div>
    </div>
  );
}

function Chip({ label, accent, onRemove }: { label: string; accent?: boolean; onRemove: () => void }) {
  return (
    <span
      className={cn(
        "flex max-w-full items-center gap-1 rounded-md border px-2 py-0.5 text-[11px]",
        accent ? "border-accent/50 bg-accent/10 text-accent" : "border-border bg-surface text-muted",
      )}
    >
      <span className="truncate">{label}</span>
      <button onClick={onRemove} className="shrink-0 text-faint hover:text-foreground">
        <X className="h-3 w-3" />
      </button>
    </span>
  );
}
