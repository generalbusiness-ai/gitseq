import { useState } from "react";
import { Undo2 } from "lucide-react";
import type { ActInput, Commitment, Decision, Projection, Statement } from "../lib/api";
import { cn } from "../lib/util";
import type { ComposerMode } from "./Composer";

// The one row affordance, Slack-shaped: a small raised card that floats at
// the top-right of a row on hover or focus, holding every action the row
// offers — cite, open thread, and the row's semantic shortcuts. Identical on
// chat lines and recorded acts. Rows carry tabIndex={-1}, so on touch a tap
// focuses the row and the toolbar appears — focus-within covers both worlds.
export function RowToolbar({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="toolbar"
      className={cn(
        "absolute -top-3.5 right-2 z-10 hidden items-center gap-0.5 rounded-lg border border-border bg-card p-0.5 shadow-lg",
        "group-hover:flex group-focus-within:flex",
      )}
    >
      {children}
    </div>
  );
}

// One button shape for the whole toolbar: icon-only buttons carry their
// label as aria-label/title; semantic shortcuts show a compact text label.
export function ToolbarButton({
  icon,
  label,
  showLabel,
  tone,
  active,
  onClick,
}: {
  icon?: React.ReactNode;
  label: string;
  showLabel?: boolean;
  tone?: "ok" | "danger";
  active?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      aria-label={label}
      title={label}
      aria-pressed={active}
      className={cn(
        "flex h-6 items-center gap-1 rounded-md px-1.5 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-accent",
        tone === "ok" && "text-ok hover:bg-ok/10",
        tone === "danger" && "text-danger hover:bg-danger/10",
        !tone && (active ? "bg-accent/15 text-accent" : "text-muted hover:bg-elevated hover:text-foreground"),
      )}
    >
      {icon}
      {showLabel && <span>{label}</span>}
    </button>
  );
}

export interface SemanticAction {
  label: string;
  tone?: "ok" | "danger";
  run: () => void;
}

// The row's semantic shortcuts — Agree/Accept/Report done/Disagree/Needs
// work/Withdraw — computed from the fold's state and the viewer's position.
// Authorization gating and the one-flight idempotency key are unchanged;
// only the rendering (inside the hover toolbar) is new.
export function semanticActions({
  statement,
  commitment,
  decision,
  projection,
  me,
  onRoute,
  doAct,
  onWithdraw,
}: {
  statement: Statement;
  commitment?: Commitment;
  decision?: Decision;
  projection: Projection;
  me?: string;
  onRoute: (mode: ComposerMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
  onWithdraw: () => void;
}): SemanticAction[] {
  const actions: SemanticAction[] = [];
  if (statement.retired || (decision && decision.verdict !== "effective")) return actions;
  const key = (verb: string) => `${verb}:${statement.event}`;
  // Already effectively ratified by me → agreeing again is meaningless; hide.
  const myRatify = projection.acts.some(
    (a) => a.type === "ratify" && a.target === statement.event && a.actor === me && a.verdict === "effective",
  );
  if (statement.kind === "request" && commitment && !commitment.promise && me && statement.body?.to === me)
    actions.push({ label: "Accept", tone: "ok", run: () => onRoute("promise", statement.event, "I will do this.") });
  if (statement.kind === "propose") {
    if (!myRatify) actions.push({ label: "Agree", tone: "ok", run: () => doAct(key("ratify"), { act: "ratify", target: statement.event }) });
    actions.push({ label: "Disagree", tone: "danger", run: () => onRoute("dissent", statement.event, "") });
  }
  if (commitment?.promise && me === commitment.performer && commitment.status === "promised")
    actions.push({ label: "Report done", tone: "ok", run: () => onRoute("report", commitment.promise!, "") });
  if (commitment?.report && me === commitment.requester && commitment.status === "reported") {
    actions.push({ label: "Accept", tone: "ok", run: () => doAct(key("satisfy"), { act: "ratify", target: commitment.report! }) });
    actions.push({ label: "Needs work", tone: "danger", run: () => onRoute("dissent", commitment.report!, "") });
  }
  if (me === statement.actor) actions.push({ label: "Withdraw", tone: "danger", run: onWithdraw });
  return actions;
}

// The withdraw reason input, opened by the toolbar's Withdraw shortcut.
// Superseding yourself is visible forever; the reason field says so.
export function WithdrawInput({
  statement,
  doAct,
  onDone,
}: {
  statement: Statement;
  doAct: (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => void;
  onDone: () => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <div className="mt-1 flex items-center gap-1.5">
      <input
        autoFocus
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            doAct(`supersede:${statement.event}`, { act: "supersede", target: statement.event, text: reason || "withdrawn" });
            onDone();
          }
          if (e.key === "Escape") onDone();
        }}
        placeholder="why — visible forever"
        aria-label="withdraw reason"
        className="min-w-0 flex-1 rounded-md border border-input bg-surface px-2.5 py-1 text-xs outline-none placeholder:text-faint focus:border-danger/60"
      />
      <Undo2 className="h-3.5 w-3.5 text-danger" />
    </div>
  );
}
