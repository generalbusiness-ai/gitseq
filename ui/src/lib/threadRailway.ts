export interface ThreadRailNode {
  event: string;
  row: number;
  lane: number;
  // True when the branch did not fit on the rail and shares the folded lane.
  folded: boolean;
  primary?: string;
  citations: string[];
}

export interface ThreadRailLayout {
  nodes: ThreadRailNode[];
  lanes: number;
  // How many nodes share the folded lane; zero when everything fits.
  folded: number;
}

// The thread pane is 24rem wide and the label needs most of it, so the rail
// is bounded rather than allowed to grow with the thread. The last lane is
// reserved: it never carries a branch of its own, it carries every branch
// that did not fit. A reader can therefore tell folded rows apart from placed
// ones, and each row still names its own parent in its own text.
export const RAIL_LANES = 10;
export const FOLDED_LANE = RAIL_LANES - 1;

// Event threads read from their root toward the present, the inverse of the
// commit railway's newest-first order. The first provenance basis is the
// conversational parent: its first child continues the parent's lane and
// later children branch into new lanes. Remaining bases are citations; they
// do not change conversational lane ownership.
//
// Lanes are recycled the way the commit railway recycles them: a lane holds
// the event it expects next, it is cleared when that event lands, and the
// first empty lane is reclaimed. Without that, a lane would be spent at every
// branch point a thread ever had rather than at every branch open on a row.
export function layoutThreadRailway(
  events: string[],
  provenance: Record<string, string[]>,
): ThreadRailLayout {
  const included = new Set(events);
  const nodes: ThreadRailNode[] = [];
  const byEvent = new Map<string, ThreadRailNode>();

  // Conversational children in layout order, so a node can book the lanes its
  // branches will land on.
  const children = new Map<string, string[]>();
  for (const event of events) {
    const parent = (provenance[event] ?? [])[0];
    if (parent && included.has(parent)) children.set(parent, [...(children.get(parent) ?? []), event]);
  }

  const lanes: (string | null)[] = [];
  let widest = 0;
  let folded = 0;

  // Take the first free lane, or open one while the rail has room. Past the
  // cap there is nowhere left to place a branch, so it folds.
  const claim = (event: string): number => {
    const free = lanes.indexOf(null);
    if (free !== -1) {
      lanes[free] = event;
      return free;
    }
    if (lanes.length < FOLDED_LANE) {
      lanes.push(event);
      return lanes.length - 1;
    }
    return FOLDED_LANE;
  };

  events.forEach((event, row) => {
    const bases = provenance[event] ?? [];
    const primary = bases[0];
    const parent = primary && included.has(primary) ? byEvent.get(primary) : undefined;

    // A folded branch owns no lane, so it books none for its own replies:
    // everything under it stays folded, and the fold reads as one bundle
    // rather than wandering back onto the rail.
    let lane: number;
    if (parent?.folded) {
      lane = FOLDED_LANE;
    } else {
      lane = lanes.indexOf(event);
      if (lane === -1) lane = claim(event);
    }
    const isFolded = lane === FOLDED_LANE;
    if (isFolded) folded++;
    else {
      for (let i = 0; i < lanes.length; i++) if (i !== lane && lanes[i] === event) lanes[i] = null;
      const kids = children.get(event) ?? [];
      lanes[lane] = kids[0] ?? null;
      for (const kid of kids.slice(1)) if (!lanes.includes(kid)) claim(kid);
    }

    widest = Math.max(widest, lane);
    const node: ThreadRailNode = { event, row, lane, folded: isFolded, primary, citations: bases.slice(1) };
    nodes.push(node);
    byEvent.set(event, node);
  });

  return { nodes, lanes: Math.min(widest + 1, RAIL_LANES), folded };
}
