/**
 * Presentation helpers for the WorkItem and knowledge-binding pages.
 *
 * Every helper only formats a frozen server fact; none of them decides a
 * workflow outcome, an authorization or a stage result. The raw enum stays
 * visible next to its label so a projection can never be mistaken for the
 * machine fact.
 */
import type {
  HumanDecision,
  HumanTask,
  HumanTaskState,
  KnowledgeCandidate,
  KnowledgeRecommendationState,
  RunState,
  WorkItemState,
} from '../api/contracts';
import type { BadgeTone } from '../ui/States';

export function workItemStatePresentation(state: WorkItemState, hasRun: boolean): { tone: BadgeTone; label: string; detail: string } {
  if (state === 'OPEN') {
    return {
      tone: 'ok',
      label: `未关闭（${state}）`,
      detail: hasRun ? '服务端已关联一条交付运行。' : '尚未关联交付运行；启动固定流程后才会有运行。',
    };
  }
  if (state === 'CLOSED') {
    return { tone: 'unknown', label: `已关闭（${state}）`, detail: '关闭事实由服务端交付事务提交；已关闭工作项不重开。' };
  }
  return { tone: 'warn', label: `已取消（${state}）`, detail: '取消是服务端事实，不表示任何外部副作用已回滚。' };
}

export function runStatePresentation(state: RunState): { tone: BadgeTone; label: string } {
  switch (state) {
    case 'ACTIVE':
      return { tone: 'ok', label: `进行中（${state}）` };
    case 'PAUSED':
      return { tone: 'warn', label: `已暂停（${state}）` };
    case 'CANCELING':
      return { tone: 'warn', label: `取消中（${state}）` };
    case 'SUCCEEDED':
      return { tone: 'ok', label: `已成功（${state}）` };
    case 'FAILED':
      return { tone: 'bad', label: `已失败（${state}）` };
    default:
      return { tone: 'unknown', label: `已取消（${state}）` };
  }
}

export function recommendationStatePresentation(
  state: KnowledgeRecommendationState,
): { tone: BadgeTone; label: string; detail: string } {
  if (state === 'CONFIRMED') {
    return { tone: 'ok', label: `已确认（${state}）`, detail: '服务端已接受人工确认并冻结知识绑定。' };
  }
  return {
    tone: 'warn',
    label: `等待人工确认（${state}）`,
    detail: '知识绑定尚未确认；服务端在确认前不会派发任何任务。',
  };
}

export function humanTaskStatePresentation(state: HumanTaskState): { tone: BadgeTone; label: string } {
  switch (state) {
    case 'OPEN':
      return { tone: 'warn', label: `待处理（${state}）` };
    case 'RESOLVED':
      return { tone: 'ok', label: `已解决（${state}）` };
    case 'EXPIRED':
      return { tone: 'bad', label: `已过期（${state}）` };
    default:
      return { tone: 'unknown', label: `已取消（${state}）` };
  }
}

export function decisionLabel(decision: HumanDecision): string {
  if (decision === 'approved') return '批准';
  if (decision === 'rejected') return '驳回';
  return '提交';
}

/** Number of open HumanTasks the server reports for one work item. */
export function openTaskCount(tasks: HumanTask[], workItemId: string): number {
  return tasks.filter((task) => task.workItemId === workItemId && task.state === 'OPEN').length;
}

/**
 * The frozen selection format of the knowledge confirmation: newline-separated
 * index paths. Ordering follows the frozen index, never the click order.
 */
export function formatSelection(paths: string[]): string {
  return paths.join('\n');
}

export function parseSelection(value: string): string[] {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '');
}

/** The confirmation is decided by the server; the page only renders its answer. */
export function canRespond(task: HumanTask): boolean {
  return task.state === 'OPEN' && task.availableActions.includes('respond');
}

export function requiredPaths(candidates: KnowledgeCandidate[]): string[] {
  return candidates.filter((candidate) => candidate.required).map((candidate) => candidate.path);
}

/**
 * A drop of a required entry can never be submitted: the control is disabled
 * and the payload is computed from the frozen set, so the browser never even
 * attempts a selection the owner refuses.
 */
export function toggledSelection(
  selected: ReadonlySet<string>,
  candidate: KnowledgeCandidate,
): ReadonlySet<string> {
  if (candidate.required) return selected;
  const next = new Set(selected);
  if (next.has(candidate.path)) next.delete(candidate.path);
  else next.add(candidate.path);
  return next;
}

/** Selected paths in frozen index order, so the same set is always the same payload. */
export function orderedSelection(candidates: KnowledgeCandidate[], selected: ReadonlySet<string>): string[] {
  return candidates.filter((candidate) => selected.has(candidate.path) || candidate.required).map((candidate) => candidate.path);
}
