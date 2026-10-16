import { useId, useRef, type KeyboardEvent } from 'react';
import type { FixedWorkflowNode, NodeHistory } from '../api/contracts';
import { Badge } from '../ui/States';
import { positionAt, type DagLayout } from './dag';
import { currentRound, historicalRounds, nodeStatePresentation, nodeTypePresentation, nodeVerdictPresentation } from './presentation';

/**
 * The frozen workflow, drawn.
 *
 * The drawing carries exactly two independent facts per node — the execution
 * state of the node's current round and the business verdict the server
 * recorded for it — and it never merges them. A node the run has not reached
 * yet is drawn as unmaterialised rather than omitted, so the picture never
 * implies more progress than the run has.
 *
 * The edges are decoration over text that is also in the document: every node
 * is a real button whose accessible description states the nodes it depends on,
 * so the topology survives without seeing the lines.
 */

const NODE_WIDTH = 224;
/**
 * Tall enough for the tallest card: id, type, state, verdict and the history
 * marker, plus the gaps between them. A card that clipped its own facts would
 * hide exactly the distinctions this view exists to draw.
 */
const NODE_HEIGHT = 124;
const GAP_X = 64;
const GAP_Y = 14;

export function WorkflowGraph({
  nodes,
  layout,
  roundsByNode,
  selectedNodeId,
  onSelect,
}: {
  nodes: readonly FixedWorkflowNode[];
  layout: DagLayout;
  roundsByNode: Map<string, NodeHistory[]>;
  selectedNodeId?: string;
  onSelect: (nodeId: string) => void;
}) {
  const baseId = useId().replace(/:/g, '');
  const buttons = useRef(new Map<string, HTMLButtonElement>());

  const width = layout.layerCount * NODE_WIDTH + Math.max(layout.layerCount - 1, 0) * GAP_X;
  const height = layout.rowCount * NODE_HEIGHT + Math.max(layout.rowCount - 1, 0) * GAP_Y;

  function coordinatesOf(nodeId: string) {
    const position = positionAt(layout, nodeId);
    return { x: position.layer * (NODE_WIDTH + GAP_X), y: position.row * (NODE_HEIGHT + GAP_Y) };
  }

  /**
   * Move focus along the graph the way it is drawn: sideways goes one column
   * (to the nearest row), up and down stay inside the column. Tab still walks
   * the nodes in topological order, so the graph is reachable either way.
   */
  function move(nodeId: string, key: string) {
    const from = positionAt(layout, nodeId);
    let target: string | undefined;
    if (key === 'ArrowRight' || key === 'ArrowLeft') {
      const layer = key === 'ArrowRight' ? from.layer + 1 : from.layer - 1;
      const column = layout.layers[layer];
      if (column) {
        target = column.reduce((best, candidate) =>
          Math.abs(positionAt(layout, candidate).row - from.row) < Math.abs(positionAt(layout, best).row - from.row) ? candidate : best,
        );
      }
    } else {
      const row = key === 'ArrowDown' ? from.row + 1 : from.row - 1;
      target = layout.layers[from.layer]?.find((candidate) => positionAt(layout, candidate).row === row);
    }
    if (target === undefined) return;
    buttons.current.get(target)?.focus();
  }

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>, nodeId: string) {
    if (!['ArrowRight', 'ArrowLeft', 'ArrowUp', 'ArrowDown'].includes(event.key)) return;
    event.preventDefault();
    move(nodeId, event.key);
  }

  return (
    <div className="overflow-x-auto pb-1">
      <div className="relative" style={{ width, height }}>
        <svg
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
        >
          <defs>
            <marker id={`${baseId}-arrow`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
              <path d="M 0 0 L 8 4 L 0 8 z" className="fill-border-muted" />
            </marker>
          </defs>
          {layout.edges.map((edge) => {
            const from = coordinatesOf(edge.from);
            const to = coordinatesOf(edge.to);
            const startX = from.x + NODE_WIDTH;
            const startY = from.y + NODE_HEIGHT / 2;
            const endX = to.x;
            const endY = to.y + NODE_HEIGHT / 2;
            const bend = Math.max((endX - startX) / 2, 12);
            return (
              <path
                key={`${edge.from}->${edge.to}`}
                d={`M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`}
                className="fill-none stroke-border-muted"
                strokeWidth={1.5}
                markerEnd={`url(#${baseId}-arrow)`}
              />
            );
          })}
        </svg>

        {nodes.map((node) => {
          const rounds = roundsByNode.get(node.id) ?? [];
          const round = currentRound(rounds);
          const history = historicalRounds(rounds);
          const state = nodeStatePresentation(round?.state);
          const verdict = nodeVerdictPresentation(round?.verdict);
          const type = nodeTypePresentation(node.type);
          const position = coordinatesOf(node.id);
          const selected = node.id === selectedNodeId;
          // The card is a stack of inline fragments, so its own text reads as
          // one run-on word to a screen reader; the name is spelled out with
          // separators instead, and it still contains every visible label.
          const accessibleName = [
            node.id,
            type.label,
            state.label,
            verdict ? `裁决 ${verdict.label}` : '裁决 未记录',
            history.length > 0 ? `另有 ${history.length} 条历史轮次` : undefined,
          ]
            .filter((part): part is string => part !== undefined)
            .join(' ');
          return (
            <button
              key={node.id}
              type="button"
              ref={(element) => {
                if (element) buttons.current.set(node.id, element);
                else buttons.current.delete(node.id);
              }}
              aria-label={accessibleName}
              aria-pressed={selected}
              aria-describedby={`${baseId}-deps-${node.id}`}
              onClick={() => onSelect(node.id)}
              onKeyDown={(event) => onKeyDown(event, node.id)}
              style={{ left: position.x, top: position.y, width: NODE_WIDTH, height: NODE_HEIGHT }}
              className={`absolute flex flex-col items-start gap-1 rounded-md border bg-card px-3 py-2 text-left ${
                selected ? 'border-primary ring-2 ring-ring' : 'border-border-muted'
              }`}
            >
              <span className="w-full truncate font-mono text-xs font-medium text-foreground">{node.id}</span>
              <span className="text-xs text-muted-foreground">{type.label}</span>
              <Badge tone={state.tone}>{state.label}</Badge>
              {verdict ? (
                <span className="w-full truncate text-xs text-muted-foreground">
                  裁决 <code className="text-foreground">{verdict.label}</code>
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">裁决 未记录</span>
              )}
              {history.length > 0 ? (
                <span className="text-xs text-muted-foreground">历史轮次 {history.length}</span>
              ) : null}
            </button>
          );
        })}

        <div className="sr-only">
          {nodes.map((node) => (
            <p key={node.id} id={`${baseId}-deps-${node.id}`}>
              依赖：{node.dependsOn.length === 0 ? '无（起始节点）' : node.dependsOn.join('、')}
            </p>
          ))}
        </div>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        节点按冻结定义的依赖分层绘制；连线是视觉辅助，同样的拓扑也写在每个节点的可访问描述里。用 Tab 或方向键在节点间移动，回车打开节点详情。
      </p>
    </div>
  );
}
