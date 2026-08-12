import type { Commitment, Projection, Statement } from "./api.ts";

// This is the same lifecycle matrix as statusview's global actionable lane.
// "Open" is only one leaf status; the umbrella also contains promised work
// in progress and reported work awaiting review.
export const ACTIVE_WORK_STATUSES = ["open", "promised", "reported"] as const;
export const CLOSED_WORK_STATUSES = ["satisfied", "withdrawn", "cancelled", "reneged"] as const;
export const ATTENTION_WORK_STATUSES = ["stale", "disputed"] as const;

export type WorkLane = "available" | "inProgress" | "review" | "closed";

// Topic labels are ordinary durable statements, not mutations of the signed
// request that opened the topic. A statement may carry either field or both;
// authorship and retirement remain visible at statement granularity.
export const TOPIC_TITLE_FIELD = "topic_title";
export const TOPIC_ALIAS_FIELD = "topic_alias";

export interface WorkTopicLabel {
  value: string;
  event: string;
  actor: string;
  order: number;
}

export interface WorkItem {
  key: string;
  commitment: Commitment;
  request: Statement;
  topicEvent: string;
  active: boolean;
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
  titleLabel?: WorkTopicLabel;
  aliases: WorkTopicLabel[];
  items: WorkItem[];
  activeCount: number;
  attentionCount: number;
  closedCount: number;
  latestActor: string;
  latestTimestamp?: number;
  latestOrder: number;
  activity: WorkActivity[];
  searchText: string;
  rootSearchText: string;
}

export interface WorkActivity {
  event: string;
  actor: string;
  timestamp?: number;
  order: number;
}

export interface WorkTopicChange extends WorkActivity {
  status: string;
}

export interface WorkProjection {
  topics: WorkTopic[];
  authors: string[];
  attention: WorkAttentionItem[];
}

export interface WorkAttentionItem {
  event: string;
  kind: "artifact" | "unlinked-promise";
  // The one word that says why this row is here. An artifact earns it two
  // ways now — withdrawn, or standing over a world that moved — so the row
  // cannot derive it from the kind alone.
  label: string;
  title: string;
  actor?: string;
  timestamp?: number;
  commit?: string;
  searchText: string;
}

export interface WorkFilters {
  active: boolean;
  attention: boolean;
  closed: boolean;
  author?: string;
  query?: string;
}

export type PersonalWorkView = "all" | "needs" | "unread" | "following";

const includes = (values: readonly string[], value: string) => values.includes(value);

export function workItemState(commitment: Commitment): Pick<WorkItem, "active" | "attention" | "closed" | "lane"> {
  const active = includes(ACTIVE_WORK_STATUSES, commitment.status);
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
  return { active, attention, closed, lane };
}

// Count directly from the durable projection so the global UI total remains
// exactly statusview actionable, including commitments whose request text is
// unavailable to the topic renderer. It is intentionally not actor-scoped.
export function workActiveCount(projection?: Projection): number {
  return workCommitmentCounts(projection).active;
}

// The headline numbers describe one population — commitments — and they are
// counted here rather than summed over topics, because a commitment whose root
// is not a topic belongs to the total just the same. Counting two of them from
// topics while the third came from the projection made them disagree with each
// other before anything else did.
//
// Active, closed and lifecycleStale partition that population by status and sum
// to the total. Attention does not belong in that sum: staleness and dispute
// are qualifiers that sit on top of a lifecycle status, so most of what needs
// attention is already counted as active or closed. Adding it made the three
// figures exceed the number of commitments, which is what made a reader
// conclude one of the surfaces was miscounting.
//
// Artifacts needing attention are not folded in either. They are a real signal
// and the drawer exists partly to surface them, but the CLI reports them on
// their own line because they are a different population, and a count that
// mixes the two matches nothing on either surface.
export interface WorkCommitmentCounts {
  active: number;
  closed: number;
  lifecycleStale: number;
  attention: number;
  total: number;
}

export function workCommitmentCounts(projection?: Projection): WorkCommitmentCounts {
  const commitments = projection?.commitments ?? [];
  const state = commitments.map((commitment) => workItemState(commitment));
  return {
    active: state.filter((item) => item.active).length,
    closed: state.filter((item) => item.closed).length,
    lifecycleStale: commitments.filter((commitment) => includes(ATTENTION_WORK_STATUSES, commitment.status)).length,
    attention: state.filter((item) => item.attention).length,
    total: commitments.length,
  };
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
  const titlesByTopic = new Map<string, WorkTopicLabel[]>();
  const aliasesByTopic = new Map<string, WorkTopicLabel[]>();
  const activityByTopic = new Map<string, { actor: string; timestamp?: number; order: number }>();
  const activitiesByTopic = new Map<string, WorkActivity[]>();
  const considerActivity = (event: string, actor: string, timestamp?: number) => {
    const topic = topicOf(event);
    if (!topic || !itemsByTopic.has(topic)) return;
    const order = orderByEvent.get(event) ?? -1;
    activitiesByTopic.set(topic, [...(activitiesByTopic.get(topic) ?? []), { event, actor, timestamp, order }]);
    if (order >= (activityByTopic.get(topic)?.order ?? -1)) activityByTopic.set(topic, { actor, timestamp, order });
  };
  for (const statement of projection.statements) {
    const topic = topicOf(statement.event);
    const title = topicLabel(statement.body?.[TOPIC_TITLE_FIELD]);
    const alias = topicLabel(statement.body?.[TOPIC_ALIAS_FIELD]);
    const isLabelStatement = Boolean(title || alias);
    const labelIsLive = isLabelStatement && !statement.retired && !statement.stale;
    if (topic && itemsByTopic.has(topic)) {
      // A retired or stale label no longer resolves. Skip the whole label
      // statement here so repeating its value in explanatory prose cannot keep
      // a retired alias accidentally searchable. Ordinary descendant history
      // remains searchable, including stale work records.
      if (!isLabelStatement || labelIsLive) {
        searchableByTopic.set(topic, [
          ...(searchableByTopic.get(topic) ?? []),
          statement.text,
          ...Object.entries(statement.body ?? {})
            .filter(([field]) => field !== TOPIC_TITLE_FIELD && field !== TOPIC_ALIAS_FIELD)
            .map(([, value]) => value),
        ]);
      }
      const label = (value: string): WorkTopicLabel => ({
        value, event: statement.event, actor: statement.actor,
        order: orderByEvent.get(statement.event) ?? -1,
      });
      if (labelIsLive && title) titlesByTopic.set(topic, [...(titlesByTopic.get(topic) ?? []), label(title)]);
      if (labelIsLive && alias) aliasesByTopic.set(topic, [...(aliasesByTopic.get(topic) ?? []), label(alias)]);
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
    const titles = distinctLatestLabels(titlesByTopic.get(event) ?? []);
    const titleLabel = latestLabel(titles);
    const aliases = distinctLatestLabels(aliasesByTopic.get(event) ?? []);
    const explicitSearch = [...titles, ...aliases].map((label) => label.value);
    topics.push({
      event,
      root,
      author: root.actor,
      title: titleLabel?.value ?? topicTitle(root.text),
      titleLabel,
      aliases,
      items,
      activeCount: items.filter((item) => item.active).length,
      attentionCount: items.filter((item) => item.attention).length,
      closedCount: items.filter((item) => item.closed).length,
      latestActor: activity.actor,
      latestTimestamp: activity.timestamp,
      latestOrder: activity.order,
      activity: [...(activitiesByTopic.get(event) ?? [])].sort((a, b) => a.order - b.order),
      searchText: [...explicitSearch, ...(searchableByTopic.get(event) ?? [root.text])].join("\n").toLowerCase(),
      rootSearchText: [...explicitSearch, root.text, ...Object.values(root.body ?? {})].join("\n").toLowerCase(),
    });
  }
  topics.sort((a, b) => b.latestOrder - a.latestOrder);
  return {
    topics,
    authors: [...new Set(topics.map((topic) => topic.author))],
    attention: otherWorkAttention(projection),
  };
}

// Personal action is not an unread signal and cannot be dismissed. It is
// recomputed from the unresolved request/promise/report relationship every
// time the durable projection changes.
export function workItemNeedsAction(item: WorkItem, me: string | undefined): boolean {
  if (!me) return false;
  const commitment = item.commitment;
  switch (commitment.status) {
    case "reported":
      return commitment.requester === me;
    case "promised":
      return commitment.performer === me;
    case "open":
      return (commitment.addressed_to ?? item.request.body?.to) === me;
    default:
      // Lifecycle stale and every terminal state are attention/history, not
      // an actionable responsibility. A reported commitment with stale=true
      // still enters the reported case above because staleness is a qualifier.
      return false;
  }
}

export function topicChangeSince(
  topic: WorkTopic,
  me: string | undefined,
  followed: ReadonlySet<string>,
  watermark: number,
): WorkTopicChange | undefined {
  if (!me || (topic.author !== me && !followed.has(topic.event))) return undefined;
  const activity = [...topic.activity].reverse().find((item) => item.actor !== me && item.order > watermark);
  if (!activity) return undefined;
  return { ...activity, status: topic.items[0]?.commitment.status ?? "open" };
}

export function filterPersonalWorkProjection(
  work: WorkProjection,
  view: PersonalWorkView,
  me: string | undefined,
  followed: ReadonlySet<string>,
  viewed: Readonly<Record<string, number>>,
): WorkProjection {
  if (view === "all") return work;
  const topics = work.topics.flatMap((topic) => {
    if (view === "following") return followed.has(topic.event) ? [topic] : [];
    if (view === "unread") return topicChangeSince(topic, me, followed, viewed[topic.event] ?? -1) ? [topic] : [];
    const items = topic.items.filter((item) => workItemNeedsAction(item, me));
    if (items.length === 0) return [];
    return [{
      ...topic,
      items,
      activeCount: items.filter((item) => item.active).length,
      attentionCount: items.filter((item) => item.attention).length,
      closedCount: items.filter((item) => item.closed).length,
    }];
  });
  return { topics, authors: work.authors, attention: [] };
}

export function filterWorkProjection(work: WorkProjection, filters: WorkFilters): WorkProjection {
  const query = filters.query?.trim().toLowerCase() ?? "";
  const topics = work.topics.flatMap((topic) => {
    if (filters.author && topic.author !== filters.author) return [];
    if (query && !topic.searchText.includes(query)) return [];
    const items = topic.items.filter(
      (item) =>
        (filters.active && item.active) ||
        (filters.attention && item.attention) ||
        (filters.closed && item.closed),
    );
    return items.length > 0 ? [{
      ...topic,
      items,
      activeCount: items.filter((item) => item.active).length,
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
  // Retired and stale are separate facts on the projection now; both mean this
  // artifact is not the current one, which is what this list is about.
  const artifacts = projection.artifacts.filter((artifact) => artifact.retired || artifact.stale).map((artifact): WorkAttentionItem => {
    const statement = statements.get(artifact.event);
    const what = artifact.retired ? "retired" : "stale";
    const title = `${what} artifact: ${artifact.path === "." ? "this repository" : artifact.path} @ ${artifact.commit.slice(0, 8)}`;
    return {
      event: artifact.event,
      kind: "artifact",
      label: what,
      title,
      actor: statement?.actor,
      timestamp: statement?.timestamp,
      commit: artifact.commit,
      searchText: [title, statement?.text ?? "", artifact.path, artifact.commit].join("\n").toLowerCase(),
    };
  });
  const unlinked = danglingPromises(projection).map((statement): WorkAttentionItem => ({
    event: statement.event,
    kind: "unlinked-promise",
    label: "unlinked",
    title: `unlinked promise: ${statement.text}`,
    actor: statement.actor,
    timestamp: statement.timestamp,
    searchText: [statement.text, ...Object.values(statement.body ?? {})].join("\n").toLowerCase(),
  }));
  return [...artifacts, ...unlinked];
}

// What otherWorkAttention is actually made of. The list has always held two
// populations — artifacts that are no longer current, and promises that rest on
// no request — and calling the total "artifacts" was the same defect this file
// was changed to fix: a number that reads as one thing while counting two. The
// composition is returned rather than the total alone so that no call site has
// to guess, and `total` is here so nobody recomputes it by adding.
export function attentionItemCounts(items: readonly WorkAttentionItem[]): {
  artifacts: number;
  unlinkedPromises: number;
  total: number;
} {
  const artifacts = items.filter((item) => item.kind === "artifact").length;
  const unlinkedPromises = items.filter((item) => item.kind === "unlinked-promise").length;
  return { artifacts, unlinkedPromises, total: items.length };
}

export function otherWorkAttentionCounts(projection: Projection): ReturnType<typeof attentionItemCounts> {
  return attentionItemCounts(otherWorkAttention(projection));
}

// How a reader should be told about that list in one phrase. Naming both parts
// is longer than naming one, and it is the length that makes it true.
export function otherWorkAttentionLabel(counts: { artifacts: number; unlinkedPromises: number }): string {
  const parts: string[] = [];
  if (counts.artifacts > 0) parts.push(`${counts.artifacts} ${counts.artifacts === 1 ? "artifact" : "artifacts"}`);
  if (counts.unlinkedPromises > 0) {
    parts.push(`${counts.unlinkedPromises} unlinked ${counts.unlinkedPromises === 1 ? "promise" : "promises"}`);
  }
  return parts.length ? parts.join(" and ") : "nothing else";
}

// The same phrase with its verb, because the verb depends on what the phrase
// counted and a call site cannot know. "nothing else" and a single artifact
// both take "needs"; anything else takes "need". Composing the noun at one call
// site and the verb at another produced "nothing else need attention", which is
// the sentence a healthy workroom shows most often and therefore the one most
// people read.
export function otherWorkAttentionClause(counts: { artifacts: number; unlinkedPromises: number }): string {
  const items = counts.artifacts + counts.unlinkedPromises;
  // "nothing else" is singular too, so zero and one both take "needs".
  return `${otherWorkAttentionLabel(counts)} ${items <= 1 ? "needs" : "need"} attention`;
}

// Commitments needing attention. Artifacts and unlinked promises are counted by
// otherWorkAttentionCounts and reported beside this rather than added to it:
// they are a different population, the CLI keeps them on their own line, and a
// total that mixed them matched nothing on either surface.
export function workAttentionCount(projection: Projection): number {
  return workCommitmentCounts(projection).attention;
}

export function topicTitle(text: string): string {
  const first = text.split(/\r?\n/, 1)[0].trim();
  return first.length > 150 ? `${first.slice(0, 147).trimEnd()}…` : first || "Untitled work";
}

function topicLabel(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? topicTitle(trimmed) : undefined;
}

function latestLabel(labels: WorkTopicLabel[]): WorkTopicLabel | undefined {
  return labels.reduce<WorkTopicLabel | undefined>((latest, label) => !latest || label.order >= latest.order ? label : latest, undefined);
}

// Labels are compared case-insensitively for discovery. Repeating one label on
// a topic does not create duplicate chips; the latest live signed statement is
// the visible attribution. The same label on different topics is deliberately
// not resolved here: search returns each matching topic group.
function distinctLatestLabels(labels: WorkTopicLabel[]): WorkTopicLabel[] {
  const byValue = new Map<string, WorkTopicLabel>();
  for (const label of labels) {
    const key = label.value.toLowerCase();
    const previous = byValue.get(key);
    if (!previous || label.order >= previous.order) byValue.set(key, label);
  }
  return [...byValue.values()].sort((a, b) => a.value < b.value ? -1 : a.value > b.value ? 1 : 0);
}
