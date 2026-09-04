import type { OutcomeNode } from "./outcomeMap.ts";

// Geometry belongs outside the React component so that a small mutation to the
// drawing order cannot quietly change the graph's stable reading order. Cards
// are placed in layer columns: bases to the left, their dependents to the
// right. A layer may contain several independent roots.
export const OUTCOME_CARD = { width: 248, height: 122, margin: 32, gapX: 88, gapY: 28 } as const;

export interface OutcomePosition {
  x: number;
  y: number;
}

export interface OutcomeLayout {
  positions: Map<string, OutcomePosition>;
  width: number;
  height: number;
}

export function layoutOutcomeMap(nodes: readonly OutcomeNode[]): OutcomeLayout {
  const columns = new Map<number, OutcomeNode[]>();
  for (const node of nodes) {
    const column = columns.get(node.layer) ?? [];
    column.push(node);
    columns.set(node.layer, column);
  }
  const layers = [...columns.keys()].sort((left, right) => left - right);
  const positions = new Map<string, OutcomePosition>();
  let tallest = 0;
  for (const layer of layers) {
    const column = columns.get(layer)!;
    column.sort((left, right) => left.order - right.order || left.thread.localeCompare(right.thread));
    tallest = Math.max(tallest, column.length);
    for (const [index, node] of column.entries()) {
      positions.set(node.thread, {
        x: OUTCOME_CARD.margin + layer * (OUTCOME_CARD.width + OUTCOME_CARD.gapX),
        y: OUTCOME_CARD.margin + index * (OUTCOME_CARD.height + OUTCOME_CARD.gapY),
      });
    }
  }
  return {
    positions,
    width: Math.max(1, layers.length) * OUTCOME_CARD.width + Math.max(0, layers.length - 1) * OUTCOME_CARD.gapX + 2 * OUTCOME_CARD.margin,
    height: Math.max(1, tallest) * OUTCOME_CARD.height + Math.max(0, tallest - 1) * OUTCOME_CARD.gapY + 2 * OUTCOME_CARD.margin,
  };
}

// The arrow always starts at one card boundary and ends at the other. This is
// true even for a malformed cycle that is drawn within one column, so a line
// never appears to point into empty space.
export function outcomeEdgePath(source: OutcomePosition, target: OutcomePosition): string {
  const startX = source.x + OUTCOME_CARD.width;
  const startY = source.y + OUTCOME_CARD.height / 2;
  const endX = target.x;
  const endY = target.y + OUTCOME_CARD.height / 2;
  const bend = Math.max(44, Math.abs(endX - startX) / 2);
  return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`;
}
