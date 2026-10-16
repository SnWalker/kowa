/**
 * The recovery commands a run view may offer, derived from the facts the owner
 * itself projected.
 *
 * `run.recovery` freezes five `kind` values and **no action list**: unlike a
 * human request, the run projection carries no `availableActions`. A page that
 * invented an action list would be deciding an authorization it does not own,
 * so this module does the only honest thing the contract allows:
 *
 * - every command is offered next to the exact server fact it rests on
 *   (`basis`), and each candidate states that the *server* decides;
 * - where the fact is absent the candidate is not offered, and the missing fact
 *   is named (never silently hidden);
 * - the version each kind is decided against is taken from the projection
 *   (`versionGuard`), matching the owner's own preconditions.
 *
 * Nothing here performs a check the owner would not perform, and nothing here
 * treats a candidate as permission.
 */
import type { FixedWorkflowView, RecoveryKind, RunDetail, WorkItemState } from '../api/contracts';

/** Which version the owner really checks for this command (recorded in S05 准入证据 §2.3). */
export type RecoveryVersionGuard = 'run' | 'workitem' | 'none';

export type RecoveryCandidate = {
  kind: RecoveryKind;
  /** Present only for the node-scoped commands. */
  nodeId?: string;
  available: boolean;
  /** The server fact the candidate rests on, or the fact that is missing. */
  basis: string;
  /** The version the command carries, and which version the owner checks. */
  expectedVersion: number;
  versionGuard: RecoveryVersionGuard;
  reasonRequired: boolean;
  /** Every candidate is a proposal: the server accepting it is the authority. */
  authority: string;
  summary: string;
};

const AUTHORITY = '这是候选动作：是否接受由服务端按它自己的前置事实裁决，本页不判定权限，也不把提交当作已生效。';

const TERMINAL_RUN_STATES = ['SUCCEEDED', 'FAILED', 'CANCELED'];
const RETRYABLE_ATTEMPT_STATES = ['FAILED', 'EXPIRED'];
/** The owner's own `rerun` preconditions: the round is decided or failed. */
const RERUNNABLE_ROUND_STATES = ['DECIDED', 'EXECUTION_FAILED'];

export function recoveryCandidates(input: {
  run: RunDetail;
  workItem: { state: WorkItemState; version: number; currentRunId?: string };
  workflow?: FixedWorkflowView;
}): RecoveryCandidate[] {
  const { run, workItem, workflow } = input;
  const candidates: RecoveryCandidate[] = [];

  const holdsAuthority = workItem.currentRunId === run.workflowRunId;
  const ended = TERMINAL_RUN_STATES.includes(run.state);

  candidates.push({
    kind: 'cancel',
    available: !ended,
    basis: ended
      ? `运行状态为 ${run.state}（终态）；服务端对已结束的运行没有可取消的内容。`
      : `运行状态为 ${run.state}，运行版本 v${run.version}（服务端记录）。`,
    expectedVersion: run.version,
    versionGuard: 'run',
    reasonRequired: false,
    authority: AUTHORITY,
    summary: '请求取消该运行：服务端按运行版本校验后请求取消，in-flight 尝试结算后才结束。',
  });

  candidates.push({
    kind: 'repair',
    available: workItem.state === 'OPEN',
    basis: workItem.state === 'OPEN'
      ? `工作项状态为 ${workItem.state}，工作项版本 v${workItem.version}（服务端记录）。`
      : `工作项状态为 ${workItem.state}；修复运行只在工作项仍为 OPEN 时准入。`,
    expectedVersion: workItem.version,
    versionGuard: 'workitem',
    reasonRequired: true,
    authority: AUTHORITY,
    summary: '创建修复运行：以实际合并提交为基线准入一条新的修复运行，必须填写原因。',
  });

  const suggestion = run.knowledgeSuggestion;
  const suggestionFailed = suggestion?.state === 'FAILED';
  candidates.push({
    kind: 'retry-knowledge-suggestion',
    available: run.state === 'SUCCEEDED' && workItem.state === 'CLOSED' && suggestionFailed,
    basis: suggestionFailed
      ? `运行状态 ${run.state}、工作项状态 ${workItem.state}、知识建议分支状态 ${suggestion?.state}（服务端记录）。`
      : `知识建议分支当前为 ${suggestion?.state ?? '未登记'}；该命令只在运行 SUCCEEDED、工作项 CLOSED 且建议分支 FAILED 时提出。`,
    expectedVersion: run.version,
    versionGuard: 'none',
    reasonRequired: false,
    authority: AUTHORITY,
    summary: '重试知识建议分支：辅助事实的重试，不改变已完成的业务交付。',
  });

  // Node-scoped commands read the node's own recorded round and attempts. The
  // precondition is the node's own round: a node the run never reached has
  // nothing to retry or rerun, and the owner refuses it with its true state.
  for (const node of workflow?.nodes ?? []) {
    let current: (typeof run.nodes)[number] | undefined;
    for (const round of run.nodes) {
      if (round.nodeId !== node.id) continue;
      if (current === undefined || round.iteration > current.iteration) current = round;
    }
    if (current === undefined) continue;
    let latestAttempt: (typeof current.attempts)[number] | undefined;
    for (const attempt of current.attempts) {
      if (latestAttempt === undefined || attempt.attempt > latestAttempt.attempt) latestAttempt = attempt;
    }

    const retryable = latestAttempt !== undefined && RETRYABLE_ATTEMPT_STATES.includes(latestAttempt.state);
    const retryBasis = retryable && latestAttempt !== undefined
      ? `节点 ${node.id} 的最新一次尝试为 ${latestAttempt.state}（第 ${current.iteration} 轮第 ${latestAttempt.attempt} 次，服务端记录）。`
      : `节点 ${node.id} 第 ${current.iteration} 轮没有失败或过期的尝试（最新一次尝试为 ${latestAttempt?.state ?? '未登记'}）。`;
    candidates.push({
      kind: 'retry',
      nodeId: node.id,
      available: retryable,
      basis: retryBasis,
      expectedVersion: run.version,
      versionGuard: 'none',
      reasonRequired: false,
      authority: AUTHORITY,
      summary: '重试该尝试：以完全相同的冻结输入再派发一次；服务端只接受失败或过期的尝试。',
    });

    const rerunnable = RERUNNABLE_ROUND_STATES.includes(current.state);
    candidates.push({
      kind: 'rerun',
      nodeId: node.id,
      available: rerunnable && holdsAuthority,
      basis: !rerunnable
        ? `节点 ${node.id} 第 ${current.iteration} 轮状态为 ${current.state}；rerun 只接受已决（DECIDED）或执行失败（EXECUTION_FAILED）的当前轮。`
        : holdsAuthority
          ? `节点 ${node.id} 第 ${current.iteration} 轮状态为 ${current.state}，且本运行仍持有推进权（工作项当前运行 = 本运行）。`
          : `节点 ${node.id} 第 ${current.iteration} 轮状态为 ${current.state}，但工作项当前运行不是本运行：只有持有推进权的运行可以开新一轮。`,
      expectedVersion: run.version,
      versionGuard: 'none',
      reasonRequired: false,
      authority: AUTHORITY,
      summary: '重新执行该节点：在当前运行中开新一轮（显式人工动作，授予一轮）。',
    });
  }

  return candidates;
}

/** The candidates for one node, in the owner's frozen order. */
export function nodeCandidates(candidates: RecoveryCandidate[], nodeId: string | undefined): RecoveryCandidate[] {
  if (nodeId === undefined) return [];
  return candidates.filter((candidate) => candidate.nodeId === nodeId);
}

/** The candidates that are not bound to a node. */
export function runCandidates(candidates: RecoveryCandidate[]): RecoveryCandidate[] {
  return candidates.filter((candidate) => candidate.nodeId === undefined);
}
