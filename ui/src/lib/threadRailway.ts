export interface ThreadRailNode {
  event: string;
  row: number;
  lane: number;
  primary?: string;
  citations: string[];
}

export interface ThreadRailLayout {
  nodes: ThreadRailNode[];
  lanes: number;
}

// Event threads read from their root toward the present, the inverse of the
// commit railway's newest-first order. The first provenance basis is the
// conversational parent: its first child continues the parent's lane and
// later children branch into new lanes. Remaining bases are citations; they
// do not change conversational lane ownership.
export function layoutThreadRailway(
  events: string[],
  provenance: Record<string, string[]>,
): ThreadRailLayout {
  const included = new Set(events);
  const nodes: ThreadRailNode[] = [];
  const byEvent = new Map<string, ThreadRailNode>();
  const childCount = new Map<string, number>();
  let nextLane = 0;

  events.forEach((event, row) => {
    const bases = provenance[event] ?? [];
    const primary = bases[0];
    const parent = primary && included.has(primary) ? byEvent.get(primary) : undefined;
    let lane: number;
    if (parent) {
      const siblings = childCount.get(parent.event) ?? 0;
      lane = siblings === 0 ? parent.lane : nextLane++;
      childCount.set(parent.event, siblings + 1);
    } else {
      lane = nextLane++;
    }
    nextLane = Math.max(nextLane, lane + 1);
    const node = { event, row, lane, primary, citations: bases.slice(1) };
    nodes.push(node);
    byEvent.set(event, node);
  });

  return { nodes, lanes: Math.max(nextLane, 1) };
}
