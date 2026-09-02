import type { ActInput, Commitment, Decision, Projection, Statement } from "../lib/api";
import type { RecordIndex } from "../lib/records";
import { isLiveParticipant, isRosterGovernance, mayRatify } from "../lib/authority";
import { activeRatification } from "../lib/ratification";
import { cn } from "../lib/util";

export type SemanticReplyMode = "promise" | "report" | "dissent" | "withdraw" | "propose" | "request";

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
  /** Words beside the symbol. The yes/no pair reads without them; nothing else does. */
  showLabel?: boolean;
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
  index,
  me,
  onRoute,
  doAct,
}: {
  statement: Statement;
  commitment?: Commitment;
  decision?: Decision;
  projection: Projection;
  /** The projection's index, built once by App: every lookup here is a map read. */
  index: RecordIndex;
  me?: string;
  onRoute: (mode: SemanticReplyMode, bases: string[], prefill: string, body?: Record<string, string>) => void;
  doAct: (intent: string, input: Omit<ActInput, "credential" | "idempotency_key">) => void;
}): SemanticAction[] {
  const actions: SemanticAction[] = [];
  if (statement.retired || (decision && decision.verdict !== "effective")) return actions;
  const key = (verb: string, target = statement.event) => `${verb}:${target}`;
  // Whether the ratification in force is mine — not whether I ever ratified.
  // Withdrawing my own ratification must bring the action back, and asking
  // "did I ever" hides it forever.
  const ratifiedByMe = (target: Statement) =>
    me !== undefined && activeRatification(projection, target)?.actor === me;
  // Two separate questions, and both have to be yes before a ratification is
  // offered. "Is it already mine?" is about the fold's current state; "may I
  // at all?" is about authority, and until this was asked the toolbar put
  // "agree" in front of every signed-in actor including those without the
  // ratifier role, whose act the fold then recorded as ineffective for ever.
  const canRatify = (target: Statement, originatingRequester?: string) =>
    mayRatify(target, { actors: projection.actors, me, originatingRequester });
  // These routes all reach the one `state` signing site in Thread. Hold them
  // to the fold's participant rule before opening the composer, while the
  // signing boundary remains the final check if the projection moves later.
  const mayComposeState = isLiveParticipant(projection.actors, me);
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
      const proposal = directProposal[0].statement!;
      const target = proposal.event;
      if (!ratifiedByMe(proposal) && canRatify(proposal))
        actions.push({ label: "ratify yes", symbol: "👍", tone: "ok", run: () => doAct(key("ratify", target), { act: "ratify", target }) });
      if (mayComposeState)
        actions.push({ label: "deny", symbol: "👎", tone: "danger", run: () => onRoute("dissent", [target], "") });
    } else {
      if (mayComposeState)
        actions.push({ label: "accept", symbol: "👍", tone: "ok", run: () => onRoute("promise", [statement.event], "I will do this.") });
    }
  }
  if (statement.kind === "propose") {
    if (!ratifiedByMe(statement) && canRatify(statement))
      actions.push({ label: "agree", symbol: "👍", tone: "ok", run: () => doAct(key("ratify"), { act: "ratify", target: statement.event }) });
    if (mayComposeState)
      actions.push({ label: "disagree", symbol: "👎", tone: "danger", run: () => onRoute("dissent", [statement.event], "") });
  }
  // The two acts of the decision-records loop, offered on an artifact row.
  //
  // Both are offered, always. Which one is right depends on whether the
  // decision at that path has been adopted, and the browser does not know
  // that: the fold projects proposals, ratifications and artifacts, and no
  // relation between them that means "adopted". Choosing for the operator
  // would mean inventing that relation here, which layer 7 may not do, so the
  // operator chooses — see docs/how-to/keep-decision-records.md for which act
  // comes when, and docs/reference/architecture.md for why the screen stays
  // out of it.
  //
  // What the browser does supply is the citations, so an identifier nobody
  // should retype by hand is not retyped by hand. They are read from the
  // fold's own provenance, ordered by the fold's own sequence and bounded, and
  // they are visible in the composer before anything is signed.
  if (statement.kind === "artifact" && mayComposeState) {
    const path = statement.body?.path ?? "";
    const named = path || "this artifact";
    actions.push({
      label: "propose adoption",
      symbol: "◇",
      showLabel: true,
      run: () => onRoute("propose", [statement.event], `Adopt the decision recorded at ${named} at this exact commit`),
    });
    const body: Record<string, string> = { artifact: statement.event };
    if (statement.body?.commit) body.head = statement.body.commit;
    actions.push({
      label: "request review",
      symbol: "◎",
      showLabel: true,
      run: () =>
        onRoute("request", [statement.event, ...index.citableProposals(statement.event)], `Review ${named} at its exact head`, body),
    });
  }
  if (mayComposeState && commitment?.promise && me === commitment.performer && commitment.status === "promised")
    actions.push({ label: "mark done", symbol: "✓", tone: "ok", run: () => onRoute("report", [commitment.promise!], "") });
  if (commitment?.report && commitment.status === "reported") {
    const report = index.statement(commitment.report);
    if (report && canRatify(report, commitment.requester))
      actions.push({ label: "accept", symbol: "👍", tone: "ok", run: () => doAct(key("satisfy"), { act: "ratify", target: commitment.report! }) });
    if (mayComposeState && me === commitment.requester)
      actions.push({ label: "needs work", symbol: "👎", tone: "danger", run: () => onRoute("dissent", [commitment.report!], "") });
  }
  // Withdraw, on authorship — which is the fold's rule for an ordinary record
  // and not its rule for a roster one. The projection emits a statement row for
  // every state record it admits, membership and role grants included, and they
  // arrive here as an unrecognised kind whose only action is this one. But
  // `decideSupersede` routes a roster target through governance before it ever
  // looks at the author: the founding seed can never be retired, an operator
  // grant or a membership carrying operator needs `operator`, every other
  // roster change needs `ratifier`. Offering withdraw there offers an act the
  // fold refuses, and the cost of that is a permanent ineffective row.
  // `signingRefusal` refuses the same target at the boundary that signs; this
  // is the courtesy that keeps the button from being drawn in the first place.
  if (me === statement.actor && !isRosterGovernance(statement))
    actions.push({ label: "withdraw", symbol: "↩", tone: "danger", run: () => onRoute("withdraw", [statement.event], "") });
  return actions;
}
