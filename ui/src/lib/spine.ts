import type { Act, Commitment, LandingDetails, Projection, Review, Statement } from "./api.ts";
import { activeRatification } from "./ratification.ts";
import { buildThreadIndex } from "./threads.ts";
import { firstLine, kindLabel } from "./util.ts";

// Shared by the table, graph cards and thread spine. These are the fold's
// destination and delivery facts; presentation does not infer them from Git
// ancestry, request prose or a historical lifecycle label.
export interface LandingDisplay {
  target: string;
  destination: string;
  legacy: boolean;
  delivery: string;
  holdOwner?: string;
  release?: string;
}

export function landingDisplay(commitment?: Commitment): LandingDisplay | undefined {
  if (!commitment) return undefined;
  const ref = commitment.target_ref;
  const repo = commitment.target_repo;
  return {
    target: ref ? ref.replace(/^refs\/heads\//, "") : repo ? "Incomplete target" : "No Git artifact",
    destination: ref ? `${repo || "Repository unavailable"} · ${ref}` : repo ? `${repo} · Target ref unavailable` : "This request owes no Git artifact",
    legacy: commitment.legacy === true,
    delivery: commitment.approved_not_landed
      ? "Approved, not landed"
      : commitment.terminal === "landed" ? "Landed" : "",
    holdOwner: commitment.hold_owner,
    release: commitment.release,
  };
}

export function sameLandingBasis(commitment: Commitment, value?: LandingDetails): boolean {
  return !!value && value.target_repo === commitment.target_repo &&
    value.target_ref === commitment.target_ref &&
    value.landing_receipt === commitment.landing_receipt &&
    value.candidate === commitment.candidate;
}

// One station on the rail. A station that has not happened is still a row:
// hollow, dim, and naming what would fill it and who owes it.
export interface Station {
  id: string;
  /**
   * The label the rail prints. Commitment stations use the fold's words
   * (request, promise, report…); a root that is itself a proposal or note
   * prints what it is instead of pretending to be a request.
   */
  kind: string;
  /** The durable event this station is, when it has happened. */
  event?: string;
  ticket?: number;
  actor?: string;
  timestamp?: number;
  /** What happened, in one line. */
  what: string;
  /** False for a station nobody has reached yet. */
  present: boolean;
  tone?: "ok" | "danger";
  /** The git commit this station names, if any. */
  commit?: string;
  /** Drawn as a branch leaving the rail beside the row it concerns. */
  branch?: boolean;
}

export interface Expander {
  id: string;
  label: string;
  hint: string;
  events: string[];
}

export interface Spine {
  stations: Station[];
  expanders: Expander[];
  /** The candidate head named by the selected commitment. */
  head?: string;
  /** Every durable record that descends from the root, station or not. */
  records: string[];
}

export interface SpineContext {
  projection: Projection;
  tickets: Map<string, number>;
  nameOf: (fingerprint: string) => string;
  commitment?: Commitment;
  /** Current bounded observation for this exact lifecycle. */
  landing?: LandingDetails;
  landingUnavailable?: boolean;
  /** How many temporary messages hang off this thread. */
  talk?: number;
}

// The salient set, computed from the fold rather than chosen by hand. A
// rendering that showed the first approval it found would name the wrong head:
// three of the four verdicts in the acceptance thread are superseded rounds,
// and only the latest one names the head that landed.
export function buildSpine(root: string, context: SpineContext): Spine {
  const { projection, tickets, nameOf } = context;
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const acts = new Map(projection.acts.map((act) => [act.event, act]));
  const thread = buildThreadIndex(projection).content(root);
  const inThread = new Set(thread.events);
  const request = statements.get(root);
  const commitment = context.commitment?.request === root ? context.commitment : projection.commitments.find((candidate) => candidate.request === root);
  const sequence = (event: string) => statements.get(event)?.sequence ?? tickets.get(event) ?? 0;

  const stations: Station[] = [];
  const claimed = new Set<string>();
  const station = (value: Station) => {
    if (value.event) claimed.add(value.event);
    stations.push(value);
  };

  if (request) {
    // The opening station says what the root is. A proposal or note that
    // opens as its own thread must not be announced as a request.
    const opening =
      request.kind === "request"
        ? `${nameOf(request.actor)} asked ${request.body?.to ? nameOf(request.body.to) : "the room"} — ${firstLine(request.text, 160)}`
        : `${nameOf(request.actor)} ${kindLabel(request.kind)} — ${firstLine(request.text, 160)}`;
    station({
      id: "root",
      kind: kindLabel(request.kind),
      event: root,
      ticket: tickets.get(root),
      actor: request.actor,
      timestamp: request.timestamp,
      what: opening,
      present: true,
    });
  }

  const promise = commitment?.promise ? statements.get(commitment.promise) : undefined;
  if (promise) {
    station({
      id: "promise",
      kind: "promise",
      event: promise.event,
      ticket: tickets.get(promise.event),
      actor: promise.actor,
      timestamp: promise.timestamp,
      what: `${nameOf(promise.actor)} claimed it`,
      present: true,
    });
  } else if (commitment?.report) {
    // Reported without ever being claimed. The station stays hollow, because
    // no promise happened, but it is not owed by anybody: saying "unclaimed,
    // addressed to X" about work already reported would ask its performer for
    // a claim after the fact, which is the thing dropping the mandatory
    // promise exists to stop.
    station({
      id: "promise",
      kind: "promise",
      what: "reported without a claim",
      present: false,
    });
  } else if (commitment || request?.kind === "request") {
    const owed = commitment?.addressed_to ?? request?.body?.to;
    station({
      id: "promise",
      kind: "promise",
      what: owed ? `unclaimed, addressed to ${nameOf(owed)}` : "unclaimed",
      present: false,
    });
  }
  // A root that is no commitment of anybody — a proposal, a note — owes
  // nobody a claim, so the rail does not invent an unclaimed row under it.

  const report = commitment?.report ? statements.get(commitment.report) : undefined;
  if (report) {
    const head = report.body?.head ?? report.body?.commit;
    station({
      id: "report",
      kind: "report",
      event: report.event,
      ticket: tickets.get(report.event),
      actor: report.actor,
      timestamp: report.timestamp,
      what: `${nameOf(report.actor)} reported ${report.body?.status ?? "it done"}${head ? ` at ${head.slice(0, 8)}` : ""}`,
      present: true,
      commit: head,
    });
  } else if (promise) {
    station({
      id: "report",
      kind: "report",
      what: `no report yet, owed by ${nameOf(promise.actor)}`,
      present: false,
    });
  }

  // Reviews come from the fold, which is the only reason salience can be
  // trusted here. The latest effective, unretired verdict in this thread is
  // the one that counts.
  const reviewsHere = (projection.reviews ?? [])
    .filter((review) => inThread.has(review.report) && !review.retired)
    .sort((left, right) => sequence(left.report) - sequence(right.report));
  const verdict = commitment?.approval
    ? (projection.reviews ?? []).find((review) => review.report === commitment.approval && !review.retired)
    : reviewsHere[reviewsHere.length - 1];
  if (verdict) {
    const verdictStatement = statements.get(verdict.report);
    const approved = verdict.verdict === "approved";
    station({
      id: "verdict",
      kind: "verdict",
      event: verdict.report,
      ticket: tickets.get(verdict.report),
      actor: verdict.reviewer,
      timestamp: verdict.timestamp,
      what: `${verdict.verdict} by ${nameOf(verdict.reviewer)}${verdict.ratified ? ", ratified" : ", not yet ratified"}${verdict.head ? `, over exact head ${verdict.head.slice(0, 8)}` : ""}`,
      present: true,
      tone: approved ? "ok" : "danger",
      commit: verdict.head,
    });
    if (verdictStatement) claimed.add(verdictStatement.event);
  }

  const head = commitment?.candidate ?? verdict?.head ?? report?.body?.head ?? report?.body?.commit;
  const target = commitment?.target_ref;
  const observed = commitment && sameLandingBasis(commitment, context.landing) ? context.landing : undefined;
  if (target && commitment) {
    station({ id: "target", kind: "target", present: true,
      what: `${target} in ${commitment.target_repo || "an unavailable repository"}${commitment.legacy ? " — legacy destination" : ""}` });
    if (commitment.hold_owner) {
      station({ id: "hold", kind: "hold", event: commitment.release, actor: commitment.hold_owner, present: true,
        what: commitment.release ? `released by ${nameOf(commitment.hold_owner)}` : `hold owner ${nameOf(commitment.hold_owner)} — no release recorded` });
    }
    station({ id: "merge", kind: "landing", event: commitment.landing_receipt, commit: observed?.merge_head,
      present: !!commitment.landing_receipt, tone: commitment.landing_receipt ? "ok" : undefined,
      what: commitment.landing_receipt
        ? `sealed landing into ${target}${observed?.merge_head ? ` at ${observed.merge_head.slice(0, 8)}` : ""}${observed?.receipt_legacy ? " — legacy receipt" : ""}`
        : `${commitment.approved_not_landed ? "approved head, not recorded as landed" : "no sealed landing recorded"} into ${target}` });
    if (commitment.landing_receipt && observed?.merge_hold_warning) {
      station({ id: "hold-warning", kind: "warning", present: true, tone: "danger", what: "the sealed receipt used the unreleased-hold compatibility window" });
    }
    const git = observed?.git;
    const observation = !git
      ? context.landingUnavailable ? "current Git observation unavailable" : "checking the current target…"
      : git.state === "target_gone" ? `target ${target} no longer exists`
      : git.state === "incorporated" ? `current ${target} contains the sealed merge`
      : git.state === "landed-then-removed" ? `current ${target} no longer contains the sealed merge; the receipt remains valid`
      : git.state === "no_receipt" ? `target ${target} exists; no sealed merge to measure`
      : "current target ancestry is unknown";
    station({ id: "git", kind: "Git now", present: !!git, what: observation });
  }

  // Or the ratification that closed the commitment. Both can be true, and
  // when they disagree that is the thing worth seeing. A thread whose root is
  // not a request — a proposal, an assert — closes when the root itself is
  // ratified, and that ratification is the last station, not a superseded
  // claim hidden behind an expander.
  //
  // Both arms resolve the fold's answer rather than searching the acts. The
  // root arm is why that matters most: a proposal ratified, withdrawn and
  // ratified again is the case this station exists to show, and it is exactly
  // the case where picking the first effective ratify names the withdrawn one.
  const closing = commitment?.report
    ? activeRatification(projection, report)
    : !commitment
      ? activeRatification(projection, statements.get(root))
      : undefined;
  if (closing) {
    station({
      id: "closed",
      kind: "closed",
      event: closing.event,
      ticket: tickets.get(closing.event),
      actor: closing.actor,
      timestamp: closing.timestamp,
      what: commitment ? `${nameOf(closing.actor)} closed it` : `${nameOf(closing.actor)} ratified it`,
      present: true,
      tone: "ok",
    });
  }

  for (const blocker of liveBlockers({ commitment, report, verdict, request, nameOf, tickets, projection, inThread })) {
    stations.push(blocker);
  }

  return {
    stations,
    expanders: buildExpanders({ thread, statements, acts, claimed, projection, reviewsHere, verdict, talk: context.talk ?? 0 }),
    head,
    records: thread.events,
  };
}

// A live blocker is something a person could act on now and nobody has. Every
// one of these is derived; none is a field anybody typed.
function liveBlockers(input: {
  commitment?: Projection["commitments"][number];
  report?: Statement;
  verdict?: Review;
  request?: Statement;
  nameOf: (fingerprint: string) => string;
  tickets: Map<string, number>;
  projection: Projection;
  inThread: Set<string>;
}): Station[] {
  const { commitment, report, verdict, nameOf, tickets } = input;
  const blockers: Station[] = [];
  // The single word these three replaced covered every wait at once. Each says who
  // owes the next move, so each gets its own sentence rather than one that is
  // wrong for two of them.
  const AWAITING: Record<string, string> = {
    "awaiting-review": "implementation published — awaiting an independently approved exact-head verdict",
    "awaiting-authorization": "approved exact head — awaiting the hold owner's release",
    "awaiting-landing": "approved exact head — awaiting the merge into its target ref",
  };
  if (commitment && AWAITING[commitment.status] && report) {
    blockers.push({
      id: "blocker-open",
      kind: "open",
      event: report.event,
      ticket: tickets.get(report.event),
      timestamp: report.timestamp,
      what: AWAITING[commitment.status],
      present: true,
      branch: true,
    });
  }
  if (commitment?.status === "reported" && report) {
    blockers.push({
      id: "blocker-open",
      kind: "open",
      event: report.event,
      ticket: tickets.get(report.event),
      timestamp: report.timestamp,
      what: `reported and not closed — still waits on ${nameOf(commitment.waiting_on || commitment.requester)}`,
      present: true,
      tone: "danger",
      branch: true,
    });
  }
  // World-staleness is loud: it is the one thing a merge will actually refuse.
  // Ordinary staleness is not here on purpose.
  if (verdict) {
    const worldStale = input.projection.statements.find(
      (statement) => statement.event === verdict.report && statement.describes_superseded_world,
    );
    if (worldStale) {
      blockers.push({
        id: "blocker-world",
        kind: "open",
        event: verdict.report,
        ticket: tickets.get(verdict.report),
        what: "this approval describes a superseded world — a merge refuses it until the behaviour is re-anchored",
        present: true,
        tone: "danger",
        branch: true,
      });
    }
  }
  if (commitment?.status === "disputed") {
    blockers.push({
      id: "blocker-disputed",
      kind: "open",
      what: "disputed — the room does not agree this is settled",
      present: true,
      tone: "danger",
      branch: true,
    });
  }
  return blockers;
}

// Elision is stated, never silent. Every record in the thread that is not on
// the rail lands in exactly one expander, so the counts partition the thread
// and each opens to exactly the records it counted.
function buildExpanders(input: {
  thread: { events: string[] };
  statements: Map<string, Statement>;
  acts: Map<string, Act>;
  claimed: Set<string>;
  projection: Projection;
  reviewsHere: Review[];
  verdict?: Review;
  talk: number;
}): Expander[] {
  const { thread, statements, acts, claimed, reviewsHere, verdict, talk } = input;
  const earlierRounds = new Set(reviewsHere.filter((review) => review.report !== verdict?.report).map((review) => review.report));
  const repair: string[] = [];
  const rounds: string[] = [];
  const superseded: string[] = [];
  const ratifications: string[] = [];
  const proposals: string[] = [];
  const chatter: string[] = [];

  for (const event of thread.events) {
    if (claimed.has(event)) continue;
    const statement = statements.get(event);
    const act = acts.get(event);
    if (act?.type === "supersede") {
      repair.push(event);
      continue;
    }
    if (statement?.kind === "artifact" && statement.retired) {
      repair.push(event);
      continue;
    }
    if (earlierRounds.has(event)) {
      rounds.push(event);
      continue;
    }
    if (act?.type === "ratify") {
      ratifications.push(event);
      continue;
    }
    if (statement?.retired && ["request", "promise", "report", "propose"].includes(statement.kind)) {
      superseded.push(event);
      continue;
    }
    // A proposal is an adoptable decision, not chatter; it earns its own
    // place instead of hiding behind a label that denies it exists.
    if (statement?.kind === "propose") {
      proposals.push(event);
      continue;
    }
    chatter.push(event);
  }

  const expanders: Expander[] = [
    { id: "repair", label: "Repair chain", hint: "retired predecessor artifacts and the supersessions that retired them", events: repair },
    { id: "rounds", label: "Earlier rounds", hint: "superseded review verdicts", events: rounds },
    { id: "superseded", label: "Superseded claims", hint: "refiled requests, replaced promises, reports and proposals", events: superseded },
    { id: "proposals", label: "Proposals", hint: "adoptable decisions awaiting a ratifier", events: proposals },
    { id: "ratifications", label: "Ratifications", hint: "records in this thread adopted by a ratifier", events: ratifications },
    { id: "talk", label: "Talk", hint: talk > 0 ? `notes, asserts and ${talk} temporary ${talk === 1 ? "message" : "messages"}` : "notes and asserts", events: chatter },
  ];
  // An expander whose count is zero is not drawn.
  return expanders.filter((expander) => expander.events.length > 0 || (expander.id === "talk" && talk > 0));
}
