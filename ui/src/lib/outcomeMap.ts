import type { Commitment, Projection } from "./api.ts";
import type { RowState, WorkRow } from "./rows.ts";
import { buildThreadIndex } from "./threads.ts";
import { firstLine } from "./util.ts";

export type OutcomeRelationFamily = "rests-on" | "ratified-by" | "superseded";

export interface OutcomeRelationContributor {
  kind: "provenance" | "supersede-act" | "successor-request";
  family: OutcomeRelationFamily;
  dependent?: string;
  basis?: string;
  ratification?: string;
  act?: string;
  target?: string;
  request?: string;
  successor?: string;
  /** Basis-to-dependent (or old-to-successor) reading for a tooltip. */
  forward: string;
  /** The same exact fact read from the other endpoint. */
  reverse: string;
}

export interface OutcomeRelation {
  id: string;
  family: OutcomeRelationFamily;
  source: string;
  target: string;
  contributors: OutcomeRelationContributor[];
}

export interface OutcomeNode {
  thread: string;
  context: boolean;
  title: string;
  kind: string;
  state: RowState | "recorded";
  waitsOn?: string;
  rootOfView: boolean;
  recordedBasis: boolean;
  basisOutsideView: boolean;
  layer: number;
  order: number;
}

type OutcomeFocal = string | Pick<WorkRow, "event" | "key" | "state" | "title" | "waitsOn">;

export interface OutcomeWarning {
  kind: "malformed" | "cycle" | "bounded";
  message: string;
  events?: string[];
  omitted?: number;
}

export interface OutcomeMapLimits {
  /** Total thread cards, including the selected board population and context. */
  nodes: number;
  contextNodes: number;
  edges: number;
  contributorsPerEdge: number;
  warnings: number;
}

export const DEFAULT_OUTCOME_MAP_LIMITS: OutcomeMapLimits = {
  // The recorded default board has 27 focal threads and roughly 70 direct
  // context threads. Keep all of that context while bounding every view to
  // 160 cards, so a malformed or imported projection cannot turn one board
  // into the log.
  nodes: 160,
  contextNodes: 96,
  edges: 160,
  contributorsPerEdge: 64,
  warnings: 20,
};

export interface OutcomeMapStats {
  focalNodes: number;
  contextNodes: number;
  edges: number;
  contributors: number;
  omittedContextNodes: number;
  omittedEdges: number;
}

export interface OutcomeMap {
  nodes: OutcomeNode[];
  relations: OutcomeRelation[];
  warnings: OutcomeWarning[];
  stats: OutcomeMapStats;
}

interface DraftRelation {
  source: string;
  target: string;
  contributor: OutcomeRelationContributor;
}

function finiteLimit(value: number, fallback: number): number {
  return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : fallback;
}

function limitsWith(overrides: Partial<OutcomeMapLimits> | undefined): OutcomeMapLimits {
  return {
    nodes: finiteLimit(overrides?.nodes ?? DEFAULT_OUTCOME_MAP_LIMITS.nodes, DEFAULT_OUTCOME_MAP_LIMITS.nodes),
    contextNodes: finiteLimit(overrides?.contextNodes ?? DEFAULT_OUTCOME_MAP_LIMITS.contextNodes, DEFAULT_OUTCOME_MAP_LIMITS.contextNodes),
    edges: finiteLimit(overrides?.edges ?? DEFAULT_OUTCOME_MAP_LIMITS.edges, DEFAULT_OUTCOME_MAP_LIMITS.edges),
    contributorsPerEdge: finiteLimit(
      overrides?.contributorsPerEdge ?? DEFAULT_OUTCOME_MAP_LIMITS.contributorsPerEdge,
      DEFAULT_OUTCOME_MAP_LIMITS.contributorsPerEdge,
    ),
    warnings: finiteLimit(overrides?.warnings ?? DEFAULT_OUTCOME_MAP_LIMITS.warnings, DEFAULT_OUTCOME_MAP_LIMITS.warnings),
  };
}

function contributorKey(contributor: OutcomeRelationContributor): string {
  return [
    contributor.kind,
    contributor.family,
    contributor.dependent,
    contributor.basis,
    contributor.ratification,
    contributor.act,
    contributor.target,
    contributor.request,
    contributor.successor,
  ].join("\u0000");
}

function relationKey(relation: Pick<OutcomeRelation, "family" | "source" | "target">): string {
  return [relation.family, relation.source, relation.target].join("\u0000");
}

/**
 * Project the selected board rows into a bounded thread graph.
 *
 * The projection is deliberately a pure read over layer-five fields. It scans
 * provenance twice but materialises only the selected roots, their bounded
 * direct context, and relations whose two endpoints survive that bound.
 */
export function buildOutcomeMap(
  projection: Projection,
  focalRows: readonly OutcomeFocal[],
  overrides?: Partial<OutcomeMapLimits>,
): OutcomeMap {
  const limits = limitsWith(overrides);
  const warnings: OutcomeWarning[] = [];
  let droppedWarnings = 0;
  const warn = (warning: OutcomeWarning) => {
    if (warnings.length < limits.warnings) warnings.push(warning);
    else droppedWarnings += 1;
  };

  const statements = new Map((projection.statements ?? []).map((statement) => [statement.event, statement]));
  const acts = new Map((projection.acts ?? []).map((act) => [act.event, act]));
  const known = new Set([...statements.keys(), ...acts.keys()]);
  const threads = buildThreadIndex(projection);
  for (const cycle of threads.cycles) {
    warn({
      kind: "cycle",
      message: `Malformed reply provenance forms a ${cycle.length}-event cycle; the events share one stable thread root.`,
      events: cycle.slice(0, 8),
    });
  }
  const rootCache = new Map<string, string>();
  const rootOf = (event: string): string | undefined => {
    if (!known.has(event)) return undefined;
    const cached = rootCache.get(event);
    if (cached) return cached;
    const root = threads.root(event);
    rootCache.set(event, root);
    return root;
  };

  const sequence = new Map<string, number>();
  for (const statement of projection.statements ?? []) sequence.set(statement.event, statement.sequence);
  for (const decision of projection.decisions ?? []) {
    if (!sequence.has(decision.event)) sequence.set(decision.event, decision.sequence);
  }
  const compareThreads = (left: string, right: string): number =>
    (sequence.get(left) ?? Number.MAX_SAFE_INTEGER) - (sequence.get(right) ?? Number.MAX_SAFE_INTEGER) || left.localeCompare(right);

  const focalCandidates = new Set<string>();
  const compareLifecycleEvents = (left: string, right: string): number =>
    (sequence.get(left) ?? Number.MIN_SAFE_INTEGER) - (sequence.get(right) ?? Number.MIN_SAFE_INTEGER) || left.localeCompare(right);
  const focalCards = new Map<string, Pick<WorkRow, "key" | "state" | "title" | "waitsOn">>();
  for (const focalRow of focalRows) {
    const event = typeof focalRow === "string" ? focalRow : focalRow.event;
    const root = rootOf(event);
    if (root) {
      focalCandidates.add(root);
      if (typeof focalRow !== "string") {
        const existing = focalCards.get(root);
        if (!existing || compareLifecycleEvents(existing.key, focalRow.key) < 0) {
          focalCards.set(root, {
            key: focalRow.key,
            state: focalRow.state,
            title: focalRow.title,
            waitsOn: focalRow.waitsOn,
          });
        }
      }
    }
    else warn({ kind: "malformed", message: `The selected event ${event} is not present in the projection.`, events: [event] });
  }
  const selectedFocal = [...focalCandidates].sort(compareThreads).slice(0, limits.nodes);
  const focal = new Set(selectedFocal);
  if (focalCandidates.size > selectedFocal.length) {
    warn({
      kind: "bounded",
      message: `Selected board threads were bounded at ${limits.nodes} cards.`,
      omitted: focalCandidates.size - selectedFocal.length,
    });
  }

  const forEachRelation = (visit: (relation: DraftRelation) => void, reportMissing: boolean, visible?: Set<string>) => {
    for (const [dependent, rawBases] of Object.entries(projection.provenance ?? {})) {
      const dependentThread = rootOf(dependent);
      for (const basis of rawBases ?? []) {
        const basisThread = rootOf(basis);
        if (!dependentThread || !basisThread) {
          if (reportMissing && ((dependentThread && visible?.has(dependentThread)) || (basisThread && visible?.has(basisThread)))) {
            const missing = dependentThread ? basis : dependent;
            warn({
              kind: "malformed",
              message: `Recorded provenance names ${missing}, which is absent from the projection; no line was drawn for that relation.`,
              events: [dependent, basis],
            });
          }
          continue;
        }
        if (dependentThread === basisThread) continue;
        const basisAct = acts.get(basis);
        const basisStatement = statements.get(basis);
        const ratification =
          basisAct?.type === "ratify" && basisAct.verdict === "effective"
            ? basisAct.event
            : basisStatement?.ratified_by;
        const family: OutcomeRelationFamily = ratification ? "ratified-by" : "rests-on";
        visit({
          source: basisThread,
          target: dependentThread,
          contributor: {
            kind: "provenance",
            family,
            dependent,
            basis,
            ratification,
            forward: `${basis} is a direct basis for ${dependent}.${ratification ? ` Projected ratification: ${ratification}.` : ""}`,
            reverse: `${dependent} rests on ${basis}.${ratification ? ` The projection names ${ratification} as its ratification.` : ""}`,
          },
        });
      }
    }

    for (const act of projection.acts ?? []) {
      if (act.type !== "supersede" || act.verdict !== "effective") continue;
      const source = rootOf(act.event);
      const target = rootOf(act.target);
      if (!source || !target) {
        if (reportMissing && ((source && visible?.has(source)) || (target && visible?.has(target)))) {
          warn({
            kind: "malformed",
            message: `Supersede act ${act.event} names a target absent from the projection; no supersession line was drawn.`,
            events: [act.event, act.target],
          });
        }
        continue;
      }
      if (source === target) continue;
      visit({
        source,
        target,
        contributor: {
          kind: "supersede-act",
          family: "superseded",
          act: act.event,
          target: act.target,
          forward: `${act.event} superseded ${act.target}.`,
          reverse: `${act.target} was superseded by ${act.event}.`,
        },
      });
    }

    for (const commitment of projection.commitments ?? []) {
      if (!commitment.successor_request) continue;
      const source = rootOf(commitment.request);
      const target = rootOf(commitment.successor_request);
      if (!source || !target) {
        if (reportMissing && ((source && visible?.has(source)) || (target && visible?.has(target)))) {
          warn({
            kind: "malformed",
            message: `Commitment ${commitment.request} names a successor request absent from the projection.`,
            events: [commitment.request, commitment.successor_request],
          });
        }
        continue;
      }
      if (source === target) continue;
      visit({
        source,
        target,
        contributor: {
          kind: "successor-request",
          family: "superseded",
          request: commitment.request,
          successor: commitment.successor_request,
          forward: `${commitment.request} has projected successor request ${commitment.successor_request}.`,
          reverse: `${commitment.successor_request} is the projected successor request of ${commitment.request}.`,
        },
      });
    }
  };

  // First pass: discover only the one-hop context touching a focal thread.
  const contextCandidates = new Set<string>();
  forEachRelation(({ source, target }) => {
    if (focal.has(source) && !focal.has(target)) contextCandidates.add(target);
    if (focal.has(target) && !focal.has(source)) contextCandidates.add(source);
  }, false);
  const contextAllowance = Math.max(0, limits.nodes - focal.size);
  const contextLimit = Math.min(limits.contextNodes, contextAllowance);
  const selectedContext = [...contextCandidates].sort(compareThreads).slice(0, contextLimit);
  const visible = new Set([...focal, ...selectedContext]);
  if (contextCandidates.size > selectedContext.length) {
    warn({
      kind: "bounded",
      message: `Direct context was bounded at ${contextLimit} threads.`,
      omitted: contextCandidates.size - selectedContext.length,
    });
  }

  // After choosing the bounded view, retain its basis visibility facts and
  // materialise only relations wholly inside it.
  const familyOrder: Record<OutcomeRelationFamily, number> = { "rests-on": 0, "ratified-by": 1, superseded: 2 };
  const compareRelations = (left: OutcomeRelation, right: OutcomeRelation): number => {
    const focalRank = (relation: OutcomeRelation) => (focal.has(relation.source) && focal.has(relation.target) ? 0 : 1);
    return (
      focalRank(left) - focalRank(right) ||
      familyOrder[left.family] - familyOrder[right.family] ||
      compareThreads(left.source, right.source) ||
      compareThreads(left.target, right.target)
    );
  };
  type RetainedRelation = OutcomeRelation & {
    contributorKeys: Set<string>;
    overflow: boolean;
  };
  const grouped = new Map<string, RetainedRelation>();
  const visibleThreads = [...visible].sort(compareThreads);
  const visibleRank = new Map(visibleThreads.map((thread, index) => [thread, index]));
  const relationSlots = new Uint8Array(3 * visibleThreads.length * visibleThreads.length);
  let seenRelationGroups = 0;
  const basisRoots = new Map<string, Set<string>>();
  const missingBasis = new Set<string>();
  for (const [dependent, bases] of Object.entries(projection.provenance ?? {})) {
    const dependentThread = rootOf(dependent);
    if (!dependentThread || !visible.has(dependentThread)) continue;
    for (const basis of bases ?? []) {
      const basisThread = rootOf(basis);
      if (!basisThread) missingBasis.add(dependentThread);
      else if (basisThread !== dependentThread) {
        const roots = basisRoots.get(dependentThread) ?? new Set<string>();
        roots.add(basisThread);
        basisRoots.set(dependentThread, roots);
      }
    }
  }
  forEachRelation(({ source, target, contributor }) => {
    if (!visible.has(source) || !visible.has(target)) return;
    const key = relationKey({ family: contributor.family, source, target });
    let relation = grouped.get(key);
    if (!relation) {
      const slot =
        (familyOrder[contributor.family] * visibleThreads.length + visibleRank.get(source)!) * visibleThreads.length +
        visibleRank.get(target)!;
      if (relationSlots[slot] === 1) return;
      relationSlots[slot] = 1;
      seenRelationGroups += 1;
      const candidate: RetainedRelation = {
        id: `${contributor.family}:${source}->${target}`,
        family: contributor.family,
        source,
        target,
        contributors: [],
        contributorKeys: new Set<string>(),
        overflow: false,
      };
      if (limits.edges === 0) return;
      if (grouped.size >= limits.edges) {
        let worstKey: string | undefined;
        let worst: RetainedRelation | undefined;
        for (const [candidateKey, retained] of grouped) {
          if (!worst || compareRelations(retained, worst) > 0) {
            worstKey = candidateKey;
            worst = retained;
          }
        }
        if (!worst || compareRelations(candidate, worst) >= 0) return;
        grouped.delete(worstKey!);
      }
      grouped.set(key, candidate);
      relation = candidate;
    }
    if (relation.overflow) return;
    const exact = contributorKey(contributor);
    if (relation.contributorKeys.has(exact)) return;
    if (relation.contributors.length >= limits.contributorsPerEdge) {
      relation.overflow = true;
      relation.contributors = [];
      relation.contributorKeys.clear();
      return;
    }
    relation.contributorKeys.add(exact);
    relation.contributors.push(contributor);
  }, true, visible);

  const completeRelations: OutcomeRelation[] = [];
  for (const relation of [...grouped.values()].sort(compareRelations)) {
    if (relation.overflow) {
      warn({
        kind: "bounded",
        message: `The complete ${relation.family} relation ${relation.source} → ${relation.target} has more than ${limits.contributorsPerEdge} contributors and was omitted rather than truncated.`,
        events: [relation.source, relation.target],
      });
      continue;
    }
    relation.contributors.sort((left, right) => contributorKey(left).localeCompare(contributorKey(right)));
    completeRelations.push({
      id: relation.id,
      family: relation.family,
      source: relation.source,
      target: relation.target,
      contributors: relation.contributors,
    });
  }
  if (seenRelationGroups > grouped.size) {
    warn({
      kind: "bounded",
      message: `Visible relation groups were bounded at ${limits.edges}; omitted groups are not rendered as partial lines.`,
      omitted: seenRelationGroups - grouped.size,
    });
  }
  const relations = completeRelations;

  // A context card exists only with an incident complete line. This gives the
  // renderer an atomic node-and-edge population and prevents orphan arrows or
  // context cards when a bound is applied.
  const finalVisible = new Set(focal);
  for (const relation of relations) {
    finalVisible.add(relation.source);
    finalVisible.add(relation.target);
  }
  const finalRelations = relations.filter((relation) => finalVisible.has(relation.source) && finalVisible.has(relation.target));
  const nodeIDs = [...finalVisible].sort(compareThreads);

  const allCycles = stronglyConnected(nodeIDs, finalRelations).filter((component) => component.length > 1);
  for (const cycle of allCycles) {
    warn({
      kind: "cycle",
      message: `The displayed thread relations contain a ${cycle.length}-thread cycle; it remains visible and shares a stable layout layer where required.`,
      events: cycle.slice(0, 8),
    });
  }

  const placement = placeNodes(nodeIDs, finalRelations, compareThreads);
  const incoming = new Set(finalRelations.map((relation) => relation.target));
  const commitmentByThread = new Map<string, Commitment>();
  for (const commitment of projection.commitments ?? []) {
    const thread = rootOf(commitment.request);
    if (!thread) continue;
    const existing = commitmentByThread.get(thread);
    const lifecycle = commitment.promise ?? commitment.report ?? commitment.request;
    const existingLifecycle = existing?.promise ?? existing?.report ?? existing?.request;
    if (!existing || compareLifecycleEvents(existingLifecycle!, lifecycle) < 0) commitmentByThread.set(thread, commitment);
  }
  const nodes = nodeIDs
    .map((thread): OutcomeNode => {
      const bases = basisRoots.get(thread) ?? new Set<string>();
      const basisOutsideView = missingBasis.has(thread) || [...bases].some((basis) => !finalVisible.has(basis));
      const statement = statements.get(thread);
      const focalCard = focalCards.get(thread);
      const commitment = commitmentByThread.get(thread);
      return {
        thread,
        context: !focal.has(thread),
        title: focalCard?.title ?? (statement ? firstLine(statement.text) : thread),
        kind: statement?.kind ?? "record",
        state: focalCard?.state ?? outcomeDisplayState(commitment),
        waitsOn: focalCard ? (focalCard.waitsOn || undefined) : commitment?.waiting_on,
        rootOfView: !incoming.has(thread),
        recordedBasis: bases.size > 0 || missingBasis.has(thread),
        basisOutsideView,
        layer: placement.get(thread)?.layer ?? 0,
        order: placement.get(thread)?.order ?? 0,
      };
    })
    .sort((left, right) => left.layer - right.layer || left.order - right.order || compareThreads(left.thread, right.thread));

  if (droppedWarnings > 0 && limits.warnings > 0) {
    if (warnings.length === limits.warnings) {
      warnings.pop();
      droppedWarnings += 1;
    }
    warnings.push({ kind: "bounded", message: `${droppedWarnings} additional graph warnings were omitted.`, omitted: droppedWarnings });
  }

  return {
    nodes,
    relations: finalRelations,
    warnings,
    stats: {
      focalNodes: focal.size,
      contextNodes: nodes.length - focal.size,
      edges: finalRelations.length,
      contributors: finalRelations.reduce((count, relation) => count + relation.contributors.length, 0),
      omittedContextNodes: contextCandidates.size - (nodes.length - focal.size),
      omittedEdges: seenRelationGroups - finalRelations.length,
    },
  };
}

function outcomeDisplayState(commitment: Commitment | undefined): OutcomeNode["state"] {
  if (!commitment) return "recorded";
  switch (commitment.status) {
    case "open": return "unclaimed";
    case "promised": return "in progress";
    case "reported": return "reported";
    case "awaiting-review": return "awaiting review";
    case "awaiting-authorization": return "awaiting authorization";
    case "awaiting-landing": return "awaiting landing";
    case "stale": return "stale";
    case "superseded": return "superseded";
    case "satisfied": return "satisfied";
    case "cancelled": return "cancelled";
    case "reneged": return "reneged";
    case "withdrawn": return "withdrawn";
    case "abandoned": return "abandoned";
    case "disputed": return "needs attention";
    default: return "recorded";
  }
}

function stronglyConnected(nodes: readonly string[], relations: readonly OutcomeRelation[]): string[][] {
  const edges = new Map(nodes.map((node) => [node, [] as string[]]));
  for (const relation of relations) edges.get(relation.source)?.push(relation.target);
  for (const targets of edges.values()) targets.sort();
  let nextIndex = 0;
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const stack: string[] = [];
  const stacked = new Set<string>();
  const components: string[][] = [];
  const visit = (node: string) => {
    index.set(node, nextIndex);
    low.set(node, nextIndex);
    nextIndex += 1;
    stack.push(node);
    stacked.add(node);
    for (const target of edges.get(node) ?? []) {
      if (!index.has(target)) {
        visit(target);
        low.set(node, Math.min(low.get(node)!, low.get(target)!));
      } else if (stacked.has(target)) {
        low.set(node, Math.min(low.get(node)!, index.get(target)!));
      }
    }
    if (low.get(node) !== index.get(node)) return;
    const component: string[] = [];
    while (stack.length > 0) {
      const member = stack.pop()!;
      stacked.delete(member);
      component.push(member);
      if (member === node) break;
    }
    components.push(component.sort());
  };
  for (const node of [...nodes].sort()) if (!index.has(node)) visit(node);
  return components.sort((left, right) => left[0].localeCompare(right[0]));
}

function placeNodes(
  nodes: readonly string[],
  relations: readonly OutcomeRelation[],
  stable: (left: string, right: string) => number,
): Map<string, { layer: number; order: number }> {
  // Direct provenance determines columns. Supersession may order cards but it
  // cannot move a basis or dependent out of the column their direct relation
  // requires.
  const structural = relations.filter((relation) => relation.family !== "superseded");
  const components = stronglyConnected(nodes, structural);
  const componentOf = new Map<string, number>();
  components.forEach((component, componentIndex) => component.forEach((node) => componentOf.set(node, componentIndex)));
  const outgoing = new Map<number, Set<number>>(components.map((_, index) => [index, new Set<number>()]));
  const indegree = new Map<number, number>(components.map((_, index) => [index, 0]));
  for (const relation of structural) {
    const source = componentOf.get(relation.source)!;
    const target = componentOf.get(relation.target)!;
    if (source === target || outgoing.get(source)!.has(target)) continue;
    outgoing.get(source)!.add(target);
    indegree.set(target, indegree.get(target)! + 1);
  }
  const componentKey = (component: number) => [...components[component]].sort(stable)[0];
  const ready = [...indegree.entries()].filter(([, count]) => count === 0).map(([component]) => component).sort((a, b) => stable(componentKey(a), componentKey(b)));
  const componentLayer = new Map<number, number>(components.map((_, index) => [index, 0]));
  while (ready.length > 0) {
    const source = ready.shift()!;
    for (const target of [...outgoing.get(source)!].sort((a, b) => stable(componentKey(a), componentKey(b)))) {
      componentLayer.set(target, Math.max(componentLayer.get(target)!, componentLayer.get(source)! + 1));
      indegree.set(target, indegree.get(target)! - 1);
      if (indegree.get(target) === 0) {
        ready.push(target);
        ready.sort((a, b) => stable(componentKey(a), componentKey(b)));
      }
    }
  }

  const layerOf = new Map<string, number>();
  for (const node of nodes) layerOf.set(node, componentLayer.get(componentOf.get(node)!) ?? 0);
  const byLayer = new Map<number, string[]>();
  for (const node of nodes) byLayer.set(layerOf.get(node)!, [...(byLayer.get(layerOf.get(node)!) ?? []), node]);
  const placement = new Map<string, { layer: number; order: number }>();
  for (const layer of [...byLayer.keys()].sort((a, b) => a - b)) {
    const candidates = byLayer.get(layer)!.sort(stable);
    // Same-column supersession is an ordering hint. Cyclic hints fall back to
    // the stable event order after every acyclic predecessor has been placed.
    const sameLayerEdges = relations.filter(
      (relation) => relation.family === "superseded" && layerOf.get(relation.source) === layer && layerOf.get(relation.target) === layer,
    );
    const sameOutgoing = new Map(candidates.map((node) => [node, [] as string[]]));
    const sameIndegree = new Map(candidates.map((node) => [node, 0]));
    for (const relation of sameLayerEdges) {
      if (sameOutgoing.get(relation.source)!.includes(relation.target)) continue;
      sameOutgoing.get(relation.source)!.push(relation.target);
      sameIndegree.set(relation.target, sameIndegree.get(relation.target)! + 1);
    }
    const ordered: string[] = [];
    const sameReady = candidates.filter((node) => sameIndegree.get(node) === 0).sort(stable);
    while (sameReady.length > 0) {
      const source = sameReady.shift()!;
      ordered.push(source);
      for (const target of sameOutgoing.get(source)!.sort(stable)) {
        sameIndegree.set(target, sameIndegree.get(target)! - 1);
        if (sameIndegree.get(target) === 0) {
          sameReady.push(target);
          sameReady.sort(stable);
        }
      }
    }
    for (const node of candidates) if (!ordered.includes(node)) ordered.push(node);

    // Incoming direct-basis order wins; an adjacent supersession is the next
    // tie-break. This makes parallel branches track their sources without
    // letting a retirement relation override provenance-defined columns.
    const basePosition = new Map(ordered.map((node, index) => [node, index]));
    const incomingRank = (node: string, family: "structural" | "superseded"): number => {
      const positions = relations
        .filter(
          (relation) =>
            relation.target === node &&
            layerOf.get(relation.source) === layer - 1 &&
            (family === "structural" ? relation.family !== "superseded" : relation.family === "superseded"),
        )
        .map((relation) => placement.get(relation.source)?.order)
        .filter((value): value is number => value !== undefined);
      return positions.length > 0 ? Math.min(...positions) : Number.MAX_SAFE_INTEGER;
    };
    ordered.sort(
      (left, right) =>
        incomingRank(left, "structural") - incomingRank(right, "structural") ||
        incomingRank(left, "superseded") - incomingRank(right, "superseded") ||
        basePosition.get(left)! - basePosition.get(right)! ||
        stable(left, right),
    );
    ordered.forEach((node, order) => placement.set(node, { layer, order }));
  }
  return placement;
}
