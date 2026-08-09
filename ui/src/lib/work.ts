import type { Commitment, Projection, Statement } from "./api.ts";

export const OPEN_WORK_STATUSES = ["open", "promised", "reported"] as const;
export const CLOSED_WORK_STATUSES = ["satisfied", "withdrawn", "cancelled", "reneged"] as const;
export const ATTENTION_WORK_STATUSES = ["stale", "disputed"] as const;

export type WorkLane = "available" | "inProgress" | "review" | "closed";

export interface WorkItem {
  key: string;
  commitment: Commitment;
  request: Statement;
  topicEvent: string;
  open: boolean;
  attention: boolean;
  closed: boolean;
  lane?: WorkLane;
  order: number;
}

export interface WorkTopic {
  event: string;
  root: Statement;
  author: string;
  title: string;
  items: WorkItem[];
  openCount: number;
  attentionCount: number;
  closedCount: number;
  latestActor: string;
  latestTimestamp?: number;
  latestOrder: number;
  searchText: string;
  rootSearchText: string;
}

export interface WorkProjection {
  topics: WorkTopic[];
  authors: string[];
  attention: WorkAttentionItem[];
}

export interface WorkAttentionItem {
  event: string;
  kind: "artifact" | "unlinked-promise";
  title: string;
  actor?: string;
  timestamp?: number;
  commit?: string;
  searchText: string;
}

export interface WorkFilters {
  open: boolean;
  attention: boolean;
  closed: boolean;
  author?: string;
  query?: string;
}

const includes = (values: readonly string[], value: string) => values.includes(value);

export function workItemState(commitment: Commitment): Pick<WorkItem, "open" | "attention" | "closed" | "lane"> {
  const open = includes(OPEN_WORK_STATUSES, commitment.status);
  const closed = includes(CLOSED_WORK_STATUSES, commitment.status);
  // Staleness is a qualifier in the intended projection. Accept the current
  // status-only shape as well, so the Work view remains honest while the fold
  // migrates and does not lose today's stale rows.
  const attention =
    includes(ATTENTION_WORK_STATUSES, commitment.status) ||
    commitment.stale === true ||
    commitment.disputed === true;
  const lane: WorkLane | undefined =
    commitment.status === "open"
      ? "available"
      : commitment.status === "promised"
        ? "inProgress"
        : commitment.status === "reported"
          ? "review"
          : closed
            ? "closed"
            : undefined;
  return { open, attention, closed, lane };
}

export function buildWorkProjection(projection: Projection): WorkProjection {
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const firstParent = (event: string) => projection.provenance[event]?.[0];
  const topicMemo = new Map<string, string | undefined>();

  // Only the first provenance edge is conversational parentage. Later bases
  // remain citations and must not collapse independently actionable work.
  const topicOf = (event: string, visiting = new Set<string>()): string | undefined => {
    if (topicMemo.has(event)) return topicMemo.get(event);
    if (visiting.has(event)) return undefined;
    visiting.add(event);
    const parent = firstParent(event);
    const parentTopic = parent ? topicOf(parent, visiting) : undefined;
    const statement = statements.get(event);
    const ownTopic = statement && (statement.kind === "request" || statement.kind === "propose") ? event : undefined;
    const topic = parentTopic ?? ownTopic;
    topicMemo.set(event, topic);
    visiting.delete(event);
    return topic;
  };

  const orderByEvent = new Map<string, number>();
  const ordered = projection.decisions.length > 0
    ? projection.decisions.map((decision) => decision.event)
    : [...projection.statements.map((statement) => statement.event), ...projection.acts.map((act) => act.event)];
  ordered.forEach((event, index) => orderByEvent.set(event, index));

  const itemsByTopic = new Map<string, WorkItem[]>();
  for (const commitment of projection.commitments) {
    const request = statements.get(commitment.request);
    if (!request) continue;
    const topicEvent = topicOf(commitment.request) ?? commitment.request;
    const state = workItemState(commitment);
    const anchors = [commitment.request, commitment.promise, commitment.report].filter((event): event is string => Boolean(event));
    const order = Math.max(...anchors.map((event) => orderByEvent.get(event) ?? -1));
    const item: WorkItem = {
      key: `${commitment.request}:${commitment.promise ?? "unclaimed"}`,
      commitment,
      request,
      topicEvent,
      order,
      ...state,
    };
    itemsByTopic.set(topicEvent, [...(itemsByTopic.get(topicEvent) ?? []), item]);
  }

  const searchableByTopic = new Map<string, string[]>();
  const activityByTopic = new Map<string, { actor: string; timestamp?: number; order: number }>();
  const considerActivity = (event: string, actor: string, timestamp?: number) => {
    const topic = topicOf(event);
    if (!topic || !itemsByTopic.has(topic)) return;
    const order = orderByEvent.get(event) ?? -1;
    if (order >= (activityByTopic.get(topic)?.order ?? -1)) activityByTopic.set(topic, { actor, timestamp, order });
  };
  for (const statement of projection.statements) {
    const topic = topicOf(statement.event);
    if (topic && itemsByTopic.has(topic)) {
      searchableByTopic.set(topic, [
        ...(searchableByTopic.get(topic) ?? []),
        statement.text,
        ...Object.values(statement.body ?? {}),
      ]);
    }
    considerActivity(statement.event, statement.actor, statement.timestamp);
  }
  for (const act of projection.acts) {
    const topic = topicOf(act.event);
    if (topic && itemsByTopic.has(topic) && act.text) {
      searchableByTopic.set(topic, [...(searchableByTopic.get(topic) ?? []), act.text]);
    }
    considerActivity(act.event, act.actor, act.timestamp);
  }
  for (const artifact of projection.artifacts) {
    const topic = topicOf(artifact.event);
    if (topic && itemsByTopic.has(topic)) {
      searchableByTopic.set(topic, [...(searchableByTopic.get(topic) ?? []), artifact.path, artifact.commit]);
    }
  }

  const topics: WorkTopic[] = [];
  for (const [event, unsortedItems] of itemsByTopic) {
    const root = statements.get(event) ?? unsortedItems[0]?.request;
    if (!root) continue;
    const items = [...unsortedItems].sort((a, b) => b.order - a.order);
    const activity = activityByTopic.get(event) ?? { actor: root.actor, timestamp: root.timestamp, order: orderByEvent.get(event) ?? -1 };
    topics.push({
      event,
      root,
      author: root.actor,
      title: topicTitle(root.text),
      items,
      openCount: items.filter((item) => item.open).length,
      attentionCount: items.filter((item) => item.attention).length,
      closedCount: items.filter((item) => item.closed).length,
      latestActor: activity.actor,
      latestTimestamp: activity.timestamp,
      latestOrder: activity.order,
      searchText: searchableByTopic.get(event)?.join("\n").toLocaleLowerCase() ?? root.text.toLocaleLowerCase(),
      rootSearchText: [root.text, ...Object.values(root.body ?? {})].join("\n").toLocaleLowerCase(),
    });
  }
  topics.sort((a, b) => b.latestOrder - a.latestOrder);
  return {
    topics,
    authors: [...new Set(topics.map((topic) => topic.author))],
    attention: otherWorkAttention(projection),
  };
}

export function filterWorkProjection(work: WorkProjection, filters: WorkFilters): WorkProjection {
  const query = filters.query?.trim().toLocaleLowerCase() ?? "";
  const topics = work.topics.flatMap((topic) => {
    if (filters.author && topic.author !== filters.author) return [];
    if (query && !topic.searchText.includes(query)) return [];
    const items = topic.items.filter(
      (item) =>
        (filters.open && item.open) ||
        (filters.attention && item.attention) ||
        (filters.closed && item.closed),
    );
    return items.length > 0 ? [{
      ...topic,
      items,
      openCount: items.filter((item) => item.open).length,
      attentionCount: items.filter((item) => item.attention).length,
      closedCount: items.filter((item) => item.closed).length,
    }] : [];
  });
  if (query) topics.sort((a, b) => Number(b.rootSearchText.includes(query)) - Number(a.rootSearchText.includes(query)) || b.latestOrder - a.latestOrder);
  const attention = !filters.attention || filters.author
    ? []
    : work.attention.filter((item) => !query || item.searchText.includes(query));
  return { topics, authors: work.authors, attention };
}

export function danglingPromises(projection: Projection): Statement[] {
  const decisions = new Map(projection.decisions.map((decision) => [decision.event, decision]));
  return projection.statements.filter(
    (statement) => statement.kind === "promise" && decisions.get(statement.event)?.reason.includes("dangling"),
  );
}

export function otherWorkAttention(projection: Projection): WorkAttentionItem[] {
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const artifacts = projection.artifacts.filter((artifact) => artifact.stale).map((artifact): WorkAttentionItem => {
    const statement = statements.get(artifact.event);
    const title = `stale artifact: ${artifact.path === "." ? "this repository" : artifact.path} @ ${artifact.commit.slice(0, 8)}`;
    return {
      event: artifact.event,
      kind: "artifact",
      title,
      actor: statement?.actor,
      timestamp: statement?.timestamp,
      commit: artifact.commit,
      searchText: [title, statement?.text ?? "", artifact.path, artifact.commit].join("\n").toLocaleLowerCase(),
    };
  });
  const unlinked = danglingPromises(projection).map((statement): WorkAttentionItem => ({
    event: statement.event,
    kind: "unlinked-promise",
    title: `unlinked promise: ${statement.text}`,
    actor: statement.actor,
    timestamp: statement.timestamp,
    searchText: [statement.text, ...Object.values(statement.body ?? {})].join("\n").toLocaleLowerCase(),
  }));
  return [...artifacts, ...unlinked];
}

export function workAttentionCount(projection: Projection): number {
  return projection.commitments.filter((commitment) => workItemState(commitment).attention).length + otherWorkAttention(projection).length;
}

export function topicTitle(text: string): string {
  const first = text.split(/\r?\n/, 1)[0].trim();
  return first.length > 150 ? `${first.slice(0, 147).trimEnd()}…` : first || "Untitled work";
}
