/**
 * Layered placement for the frozen workflow graph.
 *
 * This is geometry, not semantics: the nodes, the edges and their sequence all
 * come from the server's frozen definition view (`dependsOn` and `order`), and
 * nothing here decides whether a node may run or whether it passed. The layout
 * only chooses which column and row a node is drawn in.
 *
 * The MVP ships exactly one fixed workflow, so the graph is small and static
 * and a deterministic longest-path layering is enough — no graph-layout
 * dependency is added for it. A projection this function cannot place *exactly*
 * is refused rather than approximated, because an approximated graph would
 * misstate the frozen topology the operator is reading.
 */

export class DagLayoutError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DagLayoutError';
  }
}

export type DagNodeInput = {
  id: string;
  dependsOn: readonly string[];
};

export type DagPosition = { layer: number; row: number };

export type DagEdge = { from: string; to: string };

export type DagLayout = {
  /** Node ids per column, columns left to right, each already in draw order. */
  layers: string[][];
  positions: Record<string, DagPosition>;
  edges: DagEdge[];
  layerCount: number;
  /** Rows of the deepest column. */
  rowCount: number;
};

/**
 * The position of a node the layout placed.
 *
 * `positions` is a plain record, so an index read is `| undefined` under the
 * project's strict index rules; this accessor is the one place that turns "the
 * layout did not place it" back into the error it is, instead of letting a
 * caller draw a node at the origin.
 */
export function positionAt(layout: DagLayout, nodeId: string): DagPosition {
  const position = layout.positions[nodeId];
  if (position === undefined) throw new DagLayoutError(`节点 ${nodeId} 不在布局中`);
  return position;
}

/**
 * Place one frozen definition view.
 *
 * `order` is the server's topological sequence. It decides the sequence inside
 * a column and the edge order, so the same projection always draws identically;
 * it never decides the columns, which follow the edges themselves (a node is
 * always strictly right of every node it depends on).
 */
export function layoutDag(nodes: readonly DagNodeInput[], order: readonly string[]): DagLayout {
  const rank = new Map<string, number>();
  order.forEach((id, index) => rank.set(id, index));
  if (rank.size !== order.length) {
    throw new DagLayoutError('拓扑序包含重复节点');
  }
  const byId = new Map<string, DagNodeInput>();
  for (const entry of nodes) {
    if (byId.has(entry.id)) throw new DagLayoutError(`节点重复：${entry.id}`);
    byId.set(entry.id, entry);
  }
  if (byId.size !== rank.size) {
    const missing = [...byId.keys()].filter((id) => !rank.has(id));
    const extra = [...rank.keys()].filter((id) => !byId.has(id));
    throw new DagLayoutError(
      `定义视图的节点与拓扑序不一致（缺拓扑序：${missing.join('、') || '无'}；缺节点：${extra.join('、') || '无'}）`,
    );
  }
  for (const entry of nodes) {
    for (const dependency of entry.dependsOn) {
      if (!byId.has(dependency)) {
        throw new DagLayoutError(`节点 ${entry.id} 依赖了定义视图之外的节点：${dependency}`);
      }
      if (dependency === entry.id) {
        throw new DagLayoutError(`节点 ${entry.id} 依赖自身`);
      }
    }
  }

  // Kahn's algorithm gives a total sequence from the edges alone; a leftover
  // node means the projection carries a cycle, which has no drawing.
  const indegree = new Map<string, number>();
  for (const entry of nodes) indegree.set(entry.id, entry.dependsOn.length);
  const seeded = nodes
    .filter((entry) => entry.dependsOn.length === 0)
    .map((entry) => entry.id)
    .sort((left, right) => (rank.get(left) as number) - (rank.get(right) as number));
  const queue = [...seeded];
  const sorted: string[] = [];
  while (queue.length > 0) {
    const current = queue.shift() as string;
    sorted.push(current);
    const dependents = nodes
      .filter((entry) => entry.dependsOn.includes(current))
      .map((entry) => entry.id)
      .sort((left, right) => (rank.get(left) as number) - (rank.get(right) as number));
    for (const dependent of dependents) {
      const remaining = (indegree.get(dependent) as number) - 1;
      indegree.set(dependent, remaining);
      if (remaining === 0) queue.push(dependent);
    }
  }
  if (sorted.length !== nodes.length) {
    throw new DagLayoutError('定义视图存在依赖环，无法分层');
  }

  // Longest path from the roots: a node sits one column right of its deepest
  // ancestor, so no edge can ever point sideways or backwards.
  const layerOf = new Map<string, number>();
  for (const id of sorted) {
    const entry = byId.get(id) as DagNodeInput;
    const deepest = entry.dependsOn.reduce((best, dependency) => Math.max(best, layerOf.get(dependency) as number), -1);
    layerOf.set(id, deepest + 1);
  }
  const layerCount = Math.max(...[...layerOf.values()], 0) + 1;
  const layers: string[][] = Array.from({ length: layerCount }, () => []);
  for (const id of [...byId.keys()].sort((left, right) => (rank.get(left) as number) - (rank.get(right) as number))) {
    (layers[layerOf.get(id) as number] as string[]).push(id);
  }

  const positions: Record<string, DagPosition> = {};
  layers.forEach((column, index) => {
    column.forEach((id, row) => {
      positions[id] = { layer: index, row };
    });
  });

  const edges: DagEdge[] = [];
  for (const entry of nodes) {
    for (const dependency of entry.dependsOn) edges.push({ from: dependency, to: entry.id });
  }
  edges.sort((left, right) => {
    const from = (rank.get(left.from) as number) - (rank.get(right.from) as number);
    return from !== 0 ? from : (rank.get(left.to) as number) - (rank.get(right.to) as number);
  });

  return {
    layers,
    positions,
    edges,
    layerCount,
    rowCount: layers.reduce((best, column) => Math.max(best, column.length), 0),
  };
}
