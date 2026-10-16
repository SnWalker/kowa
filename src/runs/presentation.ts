/**
 * How the run projection is put into words.
 *
 * Two rules run through everything here:
 *
 * - Execution state and business verdict have two producers and two meanings.
 *   Nothing in this module lets one stand in for the other, and no node state
 *   is painted as a favourable outcome — a `DECIDED` node says the node
 *   decided, not that it decided *yes*, and a `COMPLETED` Task says the
 *   execution finished, not that the business passed.
 * - A verdict is the owner's token. The frozen web contract types it as a
 *   free-form string, so the browser quotes it and never maps it onto a
 *   pass/fail of its own.
 */
import type {
  AttemptKind,
  AttemptState,
  ArtifactState,
  NodeHistory,
  NodeRoundState,
  NodeType,
  RunState,
} from '../api/contracts';
import type { BadgeTone } from '../ui/States';

export type Presentation = { tone: BadgeTone; label: string; detail: string };

/**
 * The state of one node round.
 *
 * `undefined` is the honest answer for a node the frozen definition carries but
 * this run has not materialised yet: it is a gap in the run, not a zero
 * outcome, and it must never be drawn as a quiet success.
 *
 * Note that no entry uses the `ok` tone. A node's execution state is never by
 * itself a favourable business fact, and one green "done" mark across the DAG
 * is exactly what this stage exists to prevent.
 */
export function nodeStatePresentation(state: NodeRoundState | undefined): Presentation {
  switch (state) {
    case 'PENDING':
      return {
        tone: 'unknown',
        label: '待就绪（PENDING）',
        detail: '节点轮次已登记但尚未进入可执行状态；这不代表被跳过，也不代表已有业务结果。',
      };
    case 'READY':
      return {
        tone: 'unknown',
        label: '可执行（READY）',
        detail: '上游门禁已满足，节点等待执行面领取；执行尚未开始。',
      };
    case 'RUNNING':
      return {
        tone: 'unknown',
        label: '执行中（RUNNING）',
        detail: '执行面正在执行该节点；「执行中」不是裁决，也不表示会得到任一方向的结论。',
      };
    case 'WAITING_HUMAN':
      return {
        tone: 'warn',
        label: '等待人工（WAITING_HUMAN）',
        detail: '该节点在等待人工答复；人工门禁同时阻止它和它的下游继续。',
      };
    case 'DECIDED':
      return {
        tone: 'unknown',
        label: '已决（DECIDED）',
        detail: '节点已产生服务端记录的轮次结果；执行状态本身不含裁决方向，方向见该轮次的独立裁决字段。',
      };
    case 'EXECUTION_FAILED':
      return {
        tone: 'bad',
        label: '执行失败（EXECUTION_FAILED）',
        detail: '执行面未能完成该节点（超时、基础设施或 Worker 失败）；这是执行事实，与业务裁决是两件事，需要按失败原因处置。',
      };
    case 'SKIPPED':
      return {
        tone: 'warn',
        label: '已跳过（SKIPPED）',
        detail: '该节点被服务端判定为不执行；跳过是独立事实，始终单独列出，不并入任何「已完成」类别。',
      };
    case 'CANCELED':
      return {
        tone: 'unknown',
        label: '已取消（CANCELED）',
        detail: '该节点轮次随运行取消而终止；已经发生的事实保留，不声称已回滚。',
      };
    default:
      return {
        tone: 'unknown',
        label: '未物化（尚无轮次）',
        detail: '该节点属于冻结定义，但本次运行尚未为它生成节点轮次；未物化既不是跳过，也不代表业务结果。',
      };
  }
}

/**
 * The business verdict as the owner published it.
 *
 * The token is quoted verbatim — no translation, no inferred tone — because
 * the frozen contract does not enumerate the vocabulary and the browser is not
 * the authority on it. `undefined` means the server recorded no verdict, which
 * is reported as absent rather than assumed favourable.
 */
export function nodeVerdictPresentation(verdict: string | undefined): { label: string } | undefined {
  if (verdict === undefined || verdict === '') return undefined;
  return { label: verdict };
}

export function nodeTypePresentation(type: NodeType): { label: string; detail: string } {
  switch (type) {
    case 'capability':
      return { label: '能力执行', detail: '由执行面的能力目录中的一项能力完成。' };
    case 'human_task':
      return { label: '人工任务', detail: '由人工答复推进；答复由服务端保存并驱动续接。' };
    case 'system':
      return { label: '系统操作', detail: '由控制面自身的操作完成，不派发能力任务。' };
    case 'join':
      return { label: '汇合', detail: '等待多个上游满足汇合条件后继续。' };
    default:
      return { label: '结束', detail: '流程的终点节点。' };
  }
}

/** The node's declared responsibility, taken from the frozen definition. */
export function nodeResponsibility(node: {
  operation?: string;
  capability?: { id: string; version: number };
  humanTask?: string;
}): string | undefined {
  if (node.capability) return `${node.capability.id}@${node.capability.version}`;
  if (node.operation) return node.operation;
  if (node.humanTask) return node.humanTask;
  return undefined;
}

export function attemptStatePresentation(state: AttemptState): Presentation {
  switch (state) {
    case 'QUEUED':
      return { tone: 'unknown', label: '已入队（QUEUED）', detail: 'Task 已持久化，等待 Runtime 领取。' };
    case 'LEASED':
      return { tone: 'unknown', label: '已租约（LEASED）', detail: 'Task 已被某个 Runtime 世代租用。' };
    case 'RUNNING':
      return { tone: 'unknown', label: '执行中（RUNNING）', detail: '执行面报告该 Task 正在运行。' };
    case 'AWAITING_INPUT':
      return { tone: 'warn', label: '等待输入（AWAITING_INPUT）', detail: 'Task 暂停等待输入；不等于结束。' };
    case 'COMPLETED':
      return {
        tone: 'unknown',
        label: '已完成（COMPLETED）',
        detail: '执行面报告该 Task 执行完毕；这是执行事实，不说明业务裁决，裁决由节点轮次单独记录。',
      };
    case 'FAILED':
      return { tone: 'bad', label: '失败（FAILED）', detail: 'Task 以执行失败结束；按失败原因处置，不自动重试。' };
    case 'EXPIRED':
      return { tone: 'warn', label: '租约过期（EXPIRED）', detail: '租约在完成前过期；迟到的结果不会覆盖当前事实。' };
    default:
      return { tone: 'unknown', label: '已取消（CANCELED）', detail: 'Task 在完成前被取消；已经发生的事实保留。' };
  }
}

export function attemptKindPresentation(kind: AttemptKind): { label: string } {
  switch (kind) {
    case 'retry':
      return { label: '重试（retry，同一有效输入再次尝试）' };
    case 'resume':
      return { label: '续接（resume，人工答复后继续）' };
    default:
      return { label: '初次执行（initial）' };
  }
}

export function artifactStatePresentation(state: ArtifactState): Presentation {
  switch (state) {
    case 'STAGING':
      return { tone: 'warn', label: '暂存（STAGING）', detail: '尚未成为可消费的发布产物。' };
    case 'UPLOADED':
      return { tone: 'unknown', label: '已上传（UPLOADED）', detail: '上传完成，尚未标记为发布。' };
    case 'PUBLISHED':
      return { tone: 'ok', label: '已发布（PUBLISHED）', detail: '该版本已由服务端作为交付物发布。' };
    case 'DELETING':
      return { tone: 'warn', label: '删除中（DELETING）', detail: '正在被回收；有效引用与清理存在竞态。' };
    default:
      return { tone: 'unknown', label: '已删除（DELETED）', detail: '内容已被回收；引用记录仍然保留。' };
  }
}

/**
 * The run's own state. Terminal execution states are reported as what they are:
 * a `SUCCEEDED` run is an execution fact and is not the WorkItem's closure,
 * which only the delivery-governance close transaction can commit.
 *
 * A merely `ACTIVE` run is deliberately not painted as a favourable outcome:
 * being in flight is not evidence about the result.
 */
export function runStatePresentation(state: RunState): Presentation {
  switch (state) {
    case 'ACTIVE':
      return { tone: 'unknown', label: '进行中（ACTIVE）', detail: '运行处于活跃状态；这不预示任何结论。' };
    case 'PAUSED':
      return { tone: 'warn', label: '已暂停（PAUSED）', detail: '运行已暂停，等待恢复条件。' };
    case 'CANCELING':
      return { tone: 'warn', label: '取消中（CANCELING）', detail: '取消已提交，尚未收束；已发生的外部事实不会自动回滚。' };
    case 'SUCCEEDED':
      return {
        tone: 'ok',
        label: '已成功（SUCCEEDED）',
        detail: '运行状态是服务端记录的终态；工作项关闭是另一项由交付治理单独提交的事实。',
      };
    case 'FAILED':
      return { tone: 'bad', label: '已失败（FAILED）', detail: '运行以失败结束；失败事实保留，不自动视为可放行。' };
    default:
      return { tone: 'unknown', label: '已取消（CANCELED）', detail: '运行已取消；已发生的事实保留，不声称已回滚。' };
  }
}

/** Rounds grouped per node, each group ordered by iteration. */
export function groupRoundsByNode(nodes: readonly NodeHistory[]): Map<string, NodeHistory[]> {
  const grouped = new Map<string, NodeHistory[]>();
  for (const node of nodes) {
    const existing = grouped.get(node.nodeId);
    if (existing) existing.push(node);
    else grouped.set(node.nodeId, [node]);
  }
  for (const rounds of grouped.values()) rounds.sort((left, right) => left.iteration - right.iteration);
  return grouped;
}

/** The highest iteration of a node: the round whose result is the current one. */
export function currentRound(rounds: readonly NodeHistory[]): NodeHistory | undefined {
  return rounds.reduce<NodeHistory | undefined>(
    (best, entry) => (best === undefined || entry.iteration > best.iteration ? entry : best),
    undefined,
  );
}

/**
 * Every earlier iteration of a node, oldest first. These are the rounds the
 * operator must be able to read *as history*: the current result never hides
 * them and they are never shown under the current round's presentation.
 */
export function historicalRounds(rounds: readonly NodeHistory[]): NodeHistory[] {
  const current = currentRound(rounds);
  if (current === undefined) return [];
  return [...rounds].filter((entry) => entry.iteration !== current.iteration).sort((left, right) => left.iteration - right.iteration);
}

export type RunRoundSummary = {
  /** Node rounds the server has materialised for this run. */
  materialised: number;
  decided: number;
  executionFailed: number;
  skipped: number;
  waitingHuman: number;
  running: number;
  pending: number;
  canceled: number;
};

/**
 * Counts of materialised rounds, by state.
 *
 * These are counts of server facts, never a completion percentage: nothing here
 * may be used to advance the DAG, and the deliberate absence of a `passed`
 * field is what keeps a count from being read as a verdict.
 */
export function summariseRounds(nodes: readonly NodeHistory[]): RunRoundSummary {
  const summary: RunRoundSummary = {
    materialised: nodes.length,
    decided: 0,
    executionFailed: 0,
    skipped: 0,
    waitingHuman: 0,
    running: 0,
    pending: 0,
    canceled: 0,
  };
  for (const node of nodes) {
    switch (node.state) {
      case 'DECIDED':
        summary.decided += 1;
        break;
      case 'EXECUTION_FAILED':
        summary.executionFailed += 1;
        break;
      case 'SKIPPED':
        summary.skipped += 1;
        break;
      case 'WAITING_HUMAN':
        summary.waitingHuman += 1;
        break;
      case 'RUNNING':
        summary.running += 1;
        break;
      case 'CANCELED':
        summary.canceled += 1;
        break;
      default:
        summary.pending += 1;
    }
  }
  return summary;
}

/** A byte count rendered as a machine fact, not rounded into a friendly guess. */
export function formatBytes(size: number): string {
  return `${size} B`;
}
