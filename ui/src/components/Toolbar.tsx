import type { ActInput, Commitment, Decision, Projection, Statement } from "../lib/api";
import { cn } from "../lib/util";

export type SemanticReplyMode = "promise" | "report" | "dissent" | "withdraw";

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
  symbol: string;
  tone?: "ok" | "danger";
  run: () => void;
}

// The row's semantic shortcuts — Agree/Accept/Ratify yes/Report done/Disagree/
// Deny/Needs work/Withdraw — computed from the fold's state and the viewer's position.
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
}: {
  statement: Statement;
  commitment?: Commitment;
  decision?: Decision;
  projection: Projection;
  me?: string;
  onRoute: (mode: SemanticReplyMode, basis: string, prefill: string) => void;
  doAct: (intent: string, input: Omit<ActInput, "credential" | "idempotency_key">) => void;
}): SemanticAction[] {
  const actions: SemanticAction[] = [];
  if (statement.retired || (decision && decision.verdict !== "effective")) return actions;
  const key = (verb: string, target = statement.event) => `${verb}:${target}`;
  const ratifiedByMe = (target: string) => projection.acts.some(
    (a) => a.type === "ratify" && a.target === target && a.actor === me && a.verdict === "effective",
  );
  if (statement.kind === "request" && commitment && !commitment.promise && me && statement.body?.to === me) {
    const directProposal = (projection.provenance[statement.event] ?? [])
      .map((basis) => ({
        decision: projection.decisions.find((item) => item.event === basis),
        statement: projection.statements.find((item) => item.event === basis),
      }))
      .filter(({ decision: basisDecision, statement: basisStatement }) =>
        basisStatement?.kind === "propose" && !basisStatement.retired && basisDecision?.verdict === "effective",
      );
    if (directProposal.length === 1) {
      const target = directProposal[0].statement!.event;
      if (!ratifiedByMe(target))
        actions.push({ label: "ratify yes", symbol: "👍", tone: "ok", run: () => doAct(key("ratify", target), { act: "ratify", target }) });
      actions.push({ label: "deny", symbol: "👎", tone: "danger", run: () => onRoute("dissent", target, "") });
    } else {
      actions.push({ label: "accept", symbol: "👍", tone: "ok", run: () => onRoute("promise", statement.event, "I will do this.") });
    }
  }
  if (statement.kind === "propose") {
    if (!ratifiedByMe(statement.event)) actions.push({ label: "agree", symbol: "👍", tone: "ok", run: () => doAct(key("ratify"), { act: "ratify", target: statement.event }) });
    actions.push({ label: "disagree", symbol: "👎", tone: "danger", run: () => onRoute("dissent", statement.event, "") });
  }
  if (commitment?.promise && me === commitment.performer && commitment.status === "promised")
    actions.push({ label: "mark done", symbol: "✓", tone: "ok", run: () => onRoute("report", commitment.promise!, "") });
  if (commitment?.report && me === commitment.requester && commitment.status === "reported") {
    actions.push({ label: "accept", symbol: "👍", tone: "ok", run: () => doAct(key("satisfy"), { act: "ratify", target: commitment.report! }) });
    actions.push({ label: "needs work", symbol: "👎", tone: "danger", run: () => onRoute("dissent", commitment.report!, "") });
  }
  if (me === statement.actor)
    actions.push({ label: "withdraw", symbol: "↩", tone: "danger", run: () => onRoute("withdraw", statement.event, "") });
  return actions;
}
