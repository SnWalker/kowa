/**
 * Presentation helpers for the human-task surfaces.
 *
 * Every helper formats a fact the server already froze — the request's fields,
 * the object binding, the action list it computed for this member — and none
 * of them decides an authorization, an outcome or a version. Two rules run
 * through the whole file:
 *
 * - a page can only submit what `human.detail` declared: a field this renderer
 *   cannot draw makes the answer *impossible* here rather than approximate;
 * - an answer is the member's act, never a claim about the object it binds.
 */
import type {
  ArtifactRecord,
  FixedWorkflowNode,
  HumanDecision,
  HumanField,
  HumanFieldType,
  HumanObjectBinding,
  HumanTask,
  NodeHistory,
} from '../api/contracts';
import type { BadgeTone } from '../ui/States';
import { canRespond as serverAuthorizedAnswer } from '../workitems/presentation';

/** What settles a request, in the owner's two frozen modes. */
export function completionModePresentation(mode: HumanTask['request']['completionMode']): {
  label: string;
  detail: string;
  answerable: boolean;
} {
  if (mode === 'kowa_answer') {
    return {
      label: `Kowa 内答复（${mode}）`,
      detail: '这类请求由被指派成员在 Kowa 内的结构化答复结算：答复、任务解决与续接意图由服务端在同一事务提交。',
      answerable: true,
    };
  }
  return {
    label: `外部事实（${mode}）`,
    detail: '只有已核对的 GitHub 事实能收束这类请求，它永远不会产生 Kowa 内的答复记录；本页没有「我已批准/已合并」这类按钮。',
    answerable: false,
  };
}

/**
 * What this page may offer, taken from the server's own action list. An empty
 * list is the server's answer — the page reports it instead of substituting a
 * permission of its own.
 */
export function actionPresentation(task: HumanTask): {
  canAnswer: boolean;
  awaitingExternalFact: boolean;
  detail: string;
} {
  const awaitingExternalFact = task.availableActions.includes('await-external-fact');
  const canAnswer = serverAuthorizedAnswer(task);
  if (canAnswer) {
    return {
      canAnswer: true,
      awaitingExternalFact,
      detail: '服务端为当前身份返回了 respond 动作；可答复仍以服务端对请求版本、对象绑定与身份的再次校验为准。',
    };
  }
  return {
    canAnswer: false,
    awaitingExternalFact,
    detail: awaitingExternalFact
      ? '服务端只为当前身份返回 await-external-fact：等待已核对的 GitHub 事实，浏览器不代它收束。'
      : '服务端没有为当前身份返回 respond 动作（任务状态或身份不满足）；是否可答复由服务端判定，浏览器不会代它放行。',
  };
}

export const RENDERABLE_FIELD_TYPES: readonly HumanFieldType[] = ['text', 'decision', 'choice', 'comment'];

export function fieldControl(type: HumanFieldType): 'input' | 'radio' | 'select' | 'textarea' {
  switch (type) {
    case 'decision':
      return 'radio';
    case 'choice':
      return 'select';
    case 'comment':
      return 'textarea';
    default:
      return 'input';
  }
}

/** Frozen field type → the only control this page may use for it. */
export function humanFieldPresentation(field: HumanField): { label: string; detail: string; required: string } {
  const control = RENDERABLE_FIELD_TYPES.includes(field.type) ? fieldControl(field.type) : undefined;
  return {
    label: field.name,
    detail: control
      ? `类型 ${field.type} · 控件 ${control}${field.type === 'choice' && (field.options?.length ?? 0) > 0 ? ` · 可选值 ${(field.options ?? []).join('、')}` : ''}`
      : `类型 ${field.type} 没有本页可用的控件`,
    required: field.required ? '必填' : '可选',
  };
}

/** The fields this page cannot render — a request carrying one is never submitted. */
export function unrenderableFields(fields: HumanField[]): HumanField[] {
  return fields.filter((field) => !RENDERABLE_FIELD_TYPES.includes(field.type)
    || (field.type === 'choice' && (field.options?.length ?? 0) === 0));
}

export type AnswerValues =
  | { ok: true; values: Record<string, string> }
  | { ok: false; reason: string };

/**
 * Builds exactly the payload the owner accepts, or explains why it cannot be
 * built. The rules mirror the owner's own validation: only declared fields,
 * required fields non-empty, a `choice` inside its declared options, and the
 * top-level decision equal to the `decision` field it declared.
 */
export function answerValues(input: {
  fields: HumanField[];
  decision: HumanDecision;
  values: Record<string, string>;
}): AnswerValues {
  const blocked = unrenderableFields(input.fields);
  if (blocked.length > 0) {
    return {
      ok: false,
      reason: `该请求声明了本页无法渲染的字段：${blocked.map((field) => field.name).join('、')}；不会提交一个残缺的答复。`,
    };
  }
  const values: Record<string, string> = {};
  for (const field of input.fields) {
    const value = (input.values[field.name] ?? '').trim();
    if (value === '') continue;
    if (field.type === 'decision' && value !== 'approved' && value !== 'rejected') {
      return { ok: false, reason: `字段 ${field.name} 只接受 approved 或 rejected。` };
    }
    if (field.type === 'choice' && !(field.options ?? []).includes(value)) {
      return { ok: false, reason: `字段 ${field.name} 不接受 ${value}。` };
    }
    values[field.name] = value;
  }
  // A required value that is simply not filled in yet is reported as that, not
  // as a disagreement: the two are different facts and the operator reads them
  // differently.
  const missing = input.fields.filter((field) => field.required && (values[field.name] ?? '') === '');
  if (missing.length > 0) {
    return { ok: false, reason: `必要字段尚未填写：${missing.map((field) => field.name).join('、')}。` };
  }
  const decisionField = input.fields.find((field) => field.name === 'decision' && field.type === 'decision');
  if (decisionField && values.decision !== input.decision) {
    return { ok: false, reason: '顶层 decision 与字段 decision 不一致：服务端会拒绝这样的答复。' };
  }
  return { ok: true, values };
}

/**
 * The exact object version an answer applies to. It is shown verbatim from
 * `human.detail`; a later version of the same object never inherits this
 * request's answer, and no page re-binds it to a newer one.
 */
export function objectBindingPresentation(binding: HumanObjectBinding): {
  kind: string;
  version: string;
  digest?: string;
  detail: string;
} {
  return {
    kind: binding.kind,
    version: binding.version,
    digest: binding.digest,
    detail: '答复只对这一版本有效：对象移动到新版本后，服务端会把旧请求置为过期；本页不会改绑到新版本，也不会用答复自动重试。',
  };
}

export type VersionCheck = 'match' | 'mismatch' | 'unknown';

/**
 * Compares the frozen object version against a digest the page itself read.
 * A missing projection is `unknown` (never `match`): the page does not know
 * what it has not read.
 */
export function reviewedVersionCheck(binding: HumanObjectBinding, projectionDigest: string | undefined): VersionCheck {
  if (projectionDigest === undefined) return 'unknown';
  return binding.version === projectionDigest ? 'match' : 'mismatch';
}

export function versionCheckPresentation(check: VersionCheck, what: string): { tone: BadgeTone; label: string; detail: string } {
  if (check === 'match') {
    return { tone: 'ok', label: '版本一致', detail: `所审版本与${what}一致。` };
  }
  if (check === 'mismatch') {
    return {
      tone: 'bad',
      label: '版本不一致',
      detail: `页面读取到的${what}与所审版本不同：以服务端冻结的对象版本为准，请重新读取后再决定；本页不会改绑或自动重试。`,
    };
  }
  return { tone: 'unknown', label: '版本未知', detail: `${what}当前不可读，无法核对所审版本；不代表版本一致。` };
}

/** The published deliverable carrying the reviewed digest, if the list has one. */
export function reviewedArtifact(artifacts: ArtifactRecord[], digest: string | undefined): ArtifactRecord | undefined {
  if (digest === undefined) return undefined;
  return artifacts.find((artifact) => artifact.digest === digest);
}

/**
 * The frozen automatic-revision budget of one node next to the rounds the run
 * really recorded.
 *
 * The reviewing node's `revision` policy names the node it may send back
 * (`targetNodeId`), so the rounds that count are the *target* node's — the
 * budget governs further rounds of that node. Both numbers are facts; whether a
 * further round is granted stays the server's decision (an explicit human rerun
 * grants one round).
 */
export function revisionBudget(
  nodes: FixedWorkflowNode[] | undefined,
  reviewingNodeId: string,
  rounds: NodeHistory[],
): { maxAutoRevisions?: number; targetNodeId?: string; recordedRounds: number; detail: string } {
  const node = nodes?.find((candidate) => candidate.id === reviewingNodeId);
  const policy = node?.revision;
  if (node === undefined || policy === undefined) {
    return {
      maxAutoRevisions: undefined,
      recordedRounds: 0,
      detail: node === undefined
        ? '冻结定义不可读（或该节点不在定义中），无法给出返修预算；不把未知当作 0。'
        : '冻结定义没有为该节点登记自动返修预算；是否还能开新一轮由服务端裁决。',
    };
  }
  const recordedRounds = rounds.filter((round) => round.nodeId === policy.targetNodeId).length;
  return {
    maxAutoRevisions: policy.maxAutoRevisions,
    targetNodeId: policy.targetNodeId,
    recordedRounds,
    detail: `自动返修预算 ${policy.maxAutoRevisions} 次（目标节点 ${policy.targetNodeId}）；这是定义事实，预算是否已用尽与能否再开一轮由服务端裁决。`,
  };
}

/**
 * The node a `revision_escalation` request is about. The owner freezes the
 * object id as `<runId>:<nodeId>` (`internal/orchestration/advance.go:743`), so
 * the node is read out of the identifier when it carries the run under
 * discussion, and is reported as unlocated otherwise — the id is never guessed.
 */
export function escalationRegion(runId: string, regionId: string): { nodeId?: string; detail: string } {
  const prefix = `${runId}:`;
  if (!regionId.startsWith(prefix) || regionId.slice(prefix.length) === '') {
    return { detail: `区域标识 ${regionId} 不携带本运行前缀，本页无从定位其节点，不猜测。` };
  }
  return { nodeId: regionId.slice(prefix.length), detail: `区域标识 ${regionId} 指向节点 ${regionId.slice(prefix.length)}。` };
}

/**
 * One accepted answer described as a human act. The wording deliberately stays
 * about the member's decision: an `approved` answer never reads as "the object
 * passed".
 */
export function decisionPresentation(decision: HumanDecision): { label: string; detail: string } {
  if (decision === 'approved') {
    return { label: `批准（approved）`, detail: '这是一次人工动作记录（批准）；它不代表被审对象已落地，也不代表任何下游节点已推进。' };
  }
  if (decision === 'rejected') {
    return { label: `驳回（rejected）`, detail: '这是一次人工动作记录（驳回）；流程随后如何处置由服务端与冻结定义裁决。' };
  }
  return { label: `提交（submitted）`, detail: '这是一次人工动作记录（提交）；它只说明答复已被接受。' };
}
