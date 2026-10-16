/**
 * The delivery evidence semantics, as pure functions.
 *
 * Nothing here decides a business outcome. Every function either names a fact
 * the owner froze, compares two facts the owner froze against each other, or
 * states that a fact is absent. There is no merge, no approval and no closure
 * command: the page reads `delivery.query` and reports what the control plane
 * recorded.
 *
 * The one comparison this module performs on the browser side is the review ↔
 * head check, and it is a comparison of two observed values, not a gate: the
 * owner's own closure checklist is the authority, and the page says so.
 */
import type {
  ChecksFact,
  ClosureRecord,
  ExternalOperation,
  ExternalOperationKind,
  ExternalOperationState,
  GitHubObservation,
  ObservationSubjectKind,
  ObservationWatermark,
  ReviewFact,
  RunBinding,
  RunDetail,
  VerificationKind,
  VerificationRecord,
  WorkItem,
} from '../api/contracts';
import type { BadgeTone } from '../ui/States';

// ---- operations ------------------------------------------------------------

export function operationKindLabel(kind: ExternalOperationKind): string {
  return kind === 'publish_pr' ? '发布 PR（publish_pr）' : '远端变更核验（verify_change）';
}

export function operationStatePresentation(state: ExternalOperationState): { label: string; tone: BadgeTone; note: string } {
  switch (state) {
    case 'PENDING':
      return { label: 'PENDING 等待执行', tone: 'warn', note: '服务端已登记该外部操作，但还没有执行事实。' };
    case 'ATTEMPTED':
      return { label: 'ATTEMPTED 已尝试、结果未定', tone: 'warn', note: '远端副作用可能已经发生，服务端尚未核对到结果；在对账完成前不能当作成功或失败。' };
    case 'SUCCEEDED':
      return { label: 'SUCCEEDED 成功并已核对', tone: 'ok', note: '服务端核对到了真实远端对象，并记下了观察。' };
    case 'FAILED':
      return { label: 'FAILED 失败', tone: 'bad', note: '服务端已结算为失败；远端可能留下了部分副作用。' };
    case 'UNKNOWN':
      return { label: 'UNKNOWN 结果未知', tone: 'warn', note: '读取或写入的结果未知：服务端会先查询对账再决定重试，本页不把它显示为已发生或未发生。' };
  }
}

// ---- identity layers -------------------------------------------------------

export type IdentityLayer = {
  key: string;
  term: string;
  value: string;
  /** The frozen field this row was read from; `—` when nothing carries it. */
  source: string;
  note?: string;
};

function distinct(values: (string | undefined)[]): string[] {
  return [...new Set(values.filter((value): value is string => value !== undefined && value !== ''))];
}

function joined(values: string[]): string | undefined {
  return values.length === 0 ? undefined : values.join(' · ');
}

/**
 * One row per identity, each bound to the field it was read from. The five
 * layers are never merged: `C05` requires that the Kowa initiator, the Runner's
 * machine account, the reading account and GitHub's own PR/commit facts stay
 * distinguishable, and that no name text is upgraded into an author claim.
 */
export function identityLayers(input: {
  workItem: WorkItem;
  run?: RunDetail;
  operations: ExternalOperation[];
}): IdentityLayer[] {
  const { workItem, run, operations } = input;
  const expectedAccounts = distinct(operations.map((operation) => operation.request.expectedGitHubUserId));
  const prAuthors = distinct(operations.map((operation) => operation.result?.pullRequestAuthorId));
  const observers = distinct(operations.map((operation) => operation.result?.observedByGitHubUserId));

  return [
    {
      key: 'web-initiator',
      term: 'Web 发起人（当前 WorkflowRun）',
      value: run ? `GitHub 用户 ${run.startedByGitHubUserId}` : `运行未登记（工作项创建者 ${workItem.createdByGitHubUserId}）`,
      source: 'run.detail-history.startedByGitHubUserId',
      note: '在 Kowa 内发起这次运行的人。它不是 GitHub 写入者：远端的 commit、push 与 PR 由下面的机器账号完成，本页不把两者显示成同一个人。',
    },
    {
      key: 'machine-run',
      term: 'Runner 机器 gh 账号（运行准入时冻结）',
      value: run?.gitExecutionUserId ?? '服务端未登记',
      source: 'run.detail-history.gitExecutionUserId',
      note: 'Runner 本机个人 gh 身份；远端写入与 PR 创建由它在受控通道内完成。',
    },
    {
      key: 'machine-operation',
      term: '外部操作期望的机器账号',
      value: joined(expectedAccounts) ?? '本工作项还没有外部交付操作',
      source: 'delivery.query.operations[].request.expectedGitHubUserId',
      note: '每个外部操作在准入时冻结的期望写入身份；实际写入身份不等于它时，下面会列为冲突。',
    },
    {
      key: 'pr-author',
      term: 'GitHub PR 实际创建者',
      value: joined(prAuthors) ?? '还没有已核对的 PR',
      source: 'delivery.query.operations[].result.pullRequestAuthorId',
      note: 'GitHub 记录的 PR 作者，来自控制面的只读对账；没有该事实时这里保持空，不由页面推断。',
    },
    {
      key: 'observation-actor',
      term: 'GitHub 对账读取身份',
      value: joined(observers) ?? '还没有已核对的观察',
      source: 'delivery.query.operations[].result.observedByGitHubUserId',
      note: '执行只读对账的 GitHub 账号；读取身份不构成写入事实。',
    },
    {
      key: 'commit-signature',
      term: '远端 commit author / committer',
      value: '未在任何已冻结的浏览器消费合同里携带',
      source: '—',
      note: 'commit 的 author/committer 只存在于 Runner 本机证据与 GitHub 自身记录中（GitHub 认证操作者也不能从姓名文本反推）。本页不展示、也不据姓名或邮箱推断作者；远端写入的认证操作者以上面的机器账号与 PR 作者为准。',
    },
  ];
}

export type IdentityConflict = { operationId: string; code: string; detail: string };

/**
 * The frozen identity/version contradictions the owner refuses to let through:
 * a result written by a different account than the one admitted, or a commit or
 * ref that moved under the operation. The page shows them next to the facts
 * instead of presenting the operation as settled.
 */
export function identityConflicts(operation: ExternalOperation, run?: RunDetail): IdentityConflict[] {
  const conflicts: IdentityConflict[] = [];
  const add = (code: string, detail: string) => conflicts.push({ operationId: operation.operationId, code, detail });
  const { request, result } = operation;

  if (result?.pullRequestAuthorId !== undefined && result.pullRequestAuthorId !== request.expectedGitHubUserId) {
    add('pr-author-mismatch', `PR 作者为 ${result.pullRequestAuthorId}，与准入时冻结的机器账号 ${request.expectedGitHubUserId} 不一致。`);
  }
  if (result !== undefined && result.commitOid !== request.commitOid) {
    add('commit-mismatch', `对账到的提交为 ${result.commitOid}，与操作请求的提交 ${request.commitOid} 不一致。`);
  }
  if (result !== undefined && result.ref !== request.ref) {
    add('ref-mismatch', `对账到的 ref 为 ${result.ref}，与操作请求的 ${request.ref} 不一致。`);
  }
  if (run?.gitExecutionUserId !== undefined && request.expectedGitHubUserId !== run.gitExecutionUserId) {
    add('operation-account-differs-from-run', `该操作期望的机器账号 ${request.expectedGitHubUserId} 与本运行冻结的执行账号 ${run.gitExecutionUserId} 不一致。`);
  }
  return conflicts;
}

// ---- reviews ---------------------------------------------------------------

export type ReviewApplicability = 'applies-to-head' | 'superseded-by-head' | 'unknown';

export function reviewApplicability(review: ReviewFact | undefined, headOid: string | undefined): ReviewApplicability {
  if (review === undefined || review.commitOid === undefined || headOid === undefined) return 'unknown';
  return review.commitOid === headOid ? 'applies-to-head' : 'superseded-by-head';
}

export function reviewApplicabilityPresentation(value: ReviewApplicability, headOid?: string): { label: string; tone: BadgeTone; note: string } {
  switch (value) {
    case 'applies-to-head':
      return { label: '适用于当前 head', tone: 'ok', note: `该 Review 的提交与下面 PR 观察到的 head（${headOid}）相同。` };
    case 'superseded-by-head':
      return { label: '不适用于当前 head', tone: 'warn', note: `该 Review 绑定的是另一个提交，head 已经前进到 ${headOid ?? '未知'}；它不能用来放行当前 head。` };
    case 'unknown':
      return { label: '适用提交未知', tone: 'unknown', note: '该 Review 没有记录提交，或当前 head 未知；两者不能判断是否适用。' };
  }
}

/**
 * GitHub's four review verdicts are rendered in the owner's own vocabulary.
 * Any other value keeps its raw text and is marked unrecognised, so a state
 * this build does not know can never read as an approval.
 */
export function reviewStatePresentation(state: string): { label: string; tone: BadgeTone; recognised: boolean } {
  switch (state) {
    case 'APPROVED':
      return { label: 'APPROVED（批准）', tone: 'ok', recognised: true };
    case 'CHANGES_REQUESTED':
      return { label: 'CHANGES_REQUESTED（要求修改）', tone: 'warn', recognised: true };
    case 'COMMENTED':
      return { label: 'COMMENTED（仅评论）', tone: 'unknown', recognised: true };
    case 'DISMISSED':
      return { label: 'DISMISSED（已撤回）', tone: 'unknown', recognised: true };
    default:
      return { label: `${state}（本页不认识该结论，原样显示）`, tone: 'unknown', recognised: false };
  }
}

// ---- checks ----------------------------------------------------------------

export function checksPresentation(checks: ChecksFact): { label: string; tone: BadgeTone; detail: string } {
  const counts = `共 ${checks.total} 项 · 失败 ${checks.failed} · 进行中 ${checks.pending}`;
  switch (checks.state) {
    case 'passed':
      return { label: 'passed 全部通过', tone: 'ok', detail: counts };
    case 'failed':
      return { label: 'failed 存在失败', tone: 'bad', detail: counts };
    case 'pending':
      return { label: 'pending 尚未结束', tone: 'warn', detail: counts };
    case 'none':
      return { label: 'none 没有配置检查', tone: 'unknown', detail: `${counts}：Commit 没有任何检查，none 不是通过。` };
  }
}

// ---- observations ----------------------------------------------------------

export const OBSERVATION_SUBJECT_LABELS: Record<ObservationSubjectKind, string> = {
  pull_request: 'Pull Request 事实',
  pull_request_review: 'PR Review 事实',
  pull_request_checks: 'CI 检查事实',
  branch_head: '远端分支 HEAD 事实',
};

/** The newest successful read in the projection; failed reads are excluded. */
export function lastSuccessfulObservationAt(facts: GitHubObservation[]): string | undefined {
  const succeeded = facts.filter((fact) => !fact.unknown);
  const first = succeeded[0];
  const last = succeeded[succeeded.length - 1];
  if (first === undefined || last === undefined) return undefined;
  return succeeded.reduce((newest, fact) => (Date.parse(fact.observedAt) > Date.parse(newest) ? fact.observedAt : newest), first.observedAt);
}

export function unknownObservations(facts: GitHubObservation[]): GitHubObservation[] {
  return facts.filter((fact) => fact.unknown);
}

/** The PR head the facts agree on, if a pull-request observation recorded one. */
export function observedPullRequestHead(facts: GitHubObservation[]): string | undefined {
  const pr = facts.find((fact) => !fact.unknown && fact.pullRequest !== undefined)?.pullRequest;
  return pr?.headOid;
}

/** The merge commit GitHub actually reports, if any PR observation recorded one. */
export function observedMergeCommit(facts: GitHubObservation[]): string | undefined {
  const pr = facts.find((fact) => !fact.unknown && fact.pullRequest?.merged === true)?.pullRequest;
  return pr?.mergeCommitOid;
}

export function observationWatermarkPresentation(watermark: ObservationWatermark): { label: string; tone: BadgeTone; note: string } {
  const base = `最后一次读取尝试 ${watermark.lastAttemptAt}`;
  if (watermark.lastAttemptKnown) {
    return { label: '最后一次读取成功', tone: watermark.freshness === 'fresh' ? 'ok' : 'warn', note: `${base}；该次读取给出了已知事实。` };
  }
  const reason = watermark.freshnessReason === 'never-established'
    ? '该对象从未有过成功读取'
    : watermark.freshnessReason === 'superseded-by-failure'
      ? '此前有过成功读取，但已被这次失败取代'
      : '服务端未给出原因';
  return {
    label: '最后一次读取失败',
    tone: 'warn',
    note: `${base}${watermark.unknownReason !== undefined ? `（${watermark.unknownReason}）` : ''}；${reason}。最后成功观察时间不在这条水位里，本页不外推。`,
  };
}

// ---- verification evidence -------------------------------------------------

export const GATE_VERIFICATION_KINDS: VerificationKind[] = [
  'required_tests',
  'independent_review',
  'security_review',
  'post_merge_integration',
];

export const AUXILIARY_VERIFICATION_KINDS: VerificationKind[] = ['knowledge_suggestion', 'offline_evaluation'];

export function verificationGroups(records: VerificationRecord[]): { gates: VerificationRecord[]; auxiliary: VerificationRecord[] } {
  return {
    gates: records.filter((record) => GATE_VERIFICATION_KINDS.includes(record.kind)),
    auxiliary: records.filter((record) => AUXILIARY_VERIFICATION_KINDS.includes(record.kind)),
  };
}

export const VERIFICATION_KIND_LABELS: Record<VerificationKind, string> = {
  required_tests: '必需测试',
  independent_review: '独立评审',
  security_review: '安全评审',
  post_merge_integration: '合并后集成验证',
  knowledge_suggestion: '知识更新建议（辅助）',
  offline_evaluation: '离线评测（辅助）',
};

/**
 * A row's own facts. `simulated` is not evidence and never reads as a pass;
 * a gate result only reads as a pass when the source is `real`.
 */
export function verificationResultPresentation(record: VerificationRecord): { label: string; tone: BadgeTone; note: string } {
  if (record.source === 'simulated') {
    return { label: 'simulated 模拟来源', tone: 'warn', note: '模拟来源不满足门禁，无论结果字段是什么。' };
  }
  if (record.result === 'failed') {
    return { label: 'failed 未通过', tone: 'bad', note: '该证据记录为未通过。' };
  }
  return { label: 'passed 通过（real 来源）', tone: 'ok', note: '真实来源且结果为通过。' };
}

// ---- closure ---------------------------------------------------------------

export type ChecklistResolution = 'yes' | 'no' | 'unknown';
export type ChecklistItem = { key: string; term: string; value: string; resolved: ChecklistResolution; note: string };

type ClosureContext = {
  workItem: WorkItem;
  run?: RunDetail;
  operations: ExternalOperation[];
  facts: GitHubObservation[];
  verification: VerificationRecord[];
};

function verificationById(verification: VerificationRecord[], id: string): VerificationRecord | undefined {
  return verification.find((record) => record.verificationId === id);
}

function observationById(facts: GitHubObservation[], id: string): GitHubObservation | undefined {
  return facts.find((fact) => fact.observationId === id);
}

/**
 * Re-reads every binding the closure record names against the other facts in
 * the same projection. `unknown` means this projection cannot see the second
 * fact; `no` means it can see it and the two disagree. The page reports the
 * outcome and never substitutes its own decision for the owner's.
 */
export function closureChecklist(closure: ClosureRecord, context: ClosureContext): ChecklistItem[] {
  const { workItem, run, facts, verification } = context;
  const items: ChecklistItem[] = [];

  items.push({
    key: 'requirement',
    term: '需求摘要',
    value: closure.requirementDigest,
    resolved: closure.requirementDigest === workItem.requirement.digest ? 'yes' : 'no',
    note: closure.requirementDigest === workItem.requirement.digest
      ? '与工作项当前需求摘要一致。'
      : `工作项当前需求摘要为 ${workItem.requirement.digest}。`,
  });

  const definitionMatches = run !== undefined
    && closure.definitionName === run.definitionName
    && closure.definitionVersion === run.definitionVersion
    && closure.definitionDigest === run.definitionDigest;
  items.push({
    key: 'definition',
    term: '冻结定义',
    value: `${closure.definitionName}@${closure.definitionVersion} · ${closure.definitionDigest}`,
    resolved: run === undefined ? 'unknown' : definitionMatches ? 'yes' : 'no',
    note: run === undefined ? '运行投影不可用，无法与运行冻结的定义对照。' : definitionMatches
      ? '与运行准入时冻结的定义完全一致。'
      : '与运行冻结的定义不一致。',
  });

  items.push({
    key: 'run',
    term: '交付运行',
    value: closure.workflowRunId,
    resolved: closure.workflowRunId === workItem.currentRunId ? 'yes' : 'no',
    note: closure.workflowRunId === workItem.currentRunId
      ? '就是当前推进的运行。'
      : `工作项当前的推进运行是 ${workItem.currentRunId ?? '（无）'}。`,
  });

  const testRows = closure.requiredTestVerificationIds.map((id) => ({ id, record: verificationById(verification, id) }));
  const testsResolved = testRows.every((row) => row.record !== undefined && row.record.kind === 'required_tests' && row.record.source === 'real' && row.record.result === 'passed');
  items.push({
    key: 'required-tests',
    term: '必需测试',
    value: closure.requiredTestVerificationIds.join(' · '),
    resolved: testsResolved ? 'yes' : 'no',
    note: testsResolved
      ? '每一条都指向本工作项清单里 real 来源且通过的必需测试证据行。'
      : `以下条目无法在本投影中核对：${testRows.filter((row) => row.record === undefined).map((row) => row.id).join(' · ') || '（存在结果或来源不符的条目）'}。`,
  });

  const reviewEvidence = verificationById(verification, closure.independentReviewVerificationId);
  const reviewEvidenceResolved = reviewEvidence !== undefined && reviewEvidence.kind === 'independent_review' && reviewEvidence.source === 'real' && reviewEvidence.result === 'passed';
  items.push({
    key: 'independent-review',
    term: '独立评审证据',
    value: closure.independentReviewVerificationId,
    resolved: reviewEvidenceResolved ? 'yes' : 'no',
    note: reviewEvidenceResolved ? '指向 real 来源且通过的独立评审证据行。' : '在本工作项的证据清单里找不到匹配的 real/通过独立评审行。',
  });

  const reviewObservation = observationById(facts, closure.reviewObservationId);
  const review = reviewObservation?.unknown === false ? reviewObservation.review : undefined;
  const head = observedPullRequestHead(facts);
  const reviewChecks = review === undefined ? [] : [
    review.reviewerId === closure.reviewerGitHubUserId,
    review.commitOid !== undefined && review.commitOid === closure.reviewHeadOid,
    head !== undefined && review.commitOid === head,
    review.reviewerType === 'User',
    review.reviewerPermission === 'admin' || review.reviewerPermission === 'write',
  ];
  const reviewResolved = review !== undefined && reviewChecks.every(Boolean);
  items.push({
    key: 'review',
    term: '有效 Review',
    value: `${closure.reviewObservationId} · 处理人 ${closure.reviewerGitHubUserId} · 适用提交 ${closure.reviewHeadOid}`,
    resolved: reviewResolved ? 'yes' : 'no',
    note: reviewResolved
      ? '该观察的处理人、账号类型、仓库权限与适用提交都与关闭记录一致，且它绑定的提交就是 PR 当前 head。'
      : '观察缺失、为未知读取，或处理人/账号类型/仓库权限/适用提交与关闭记录或 PR 当前 head 不符。',
  });

  const mergeObservation = observationById(facts, closure.mergeObservationId);
  const pullRequest = mergeObservation?.unknown === false ? mergeObservation.pullRequest : undefined;
  const mergeResolved = pullRequest !== undefined
    && pullRequest.merged === true
    && pullRequest.mergeCommitOid === closure.mergeCommitOid
    && pullRequest.mergedById === closure.mergedByGitHubUserId;
  items.push({
    key: 'merge',
    term: '实际合并',
    value: `${closure.mergeObservationId} · 合并提交 ${closure.mergeCommitOid} · 合并账号 ${closure.mergedByGitHubUserId}`,
    resolved: mergeResolved ? 'yes' : 'no',
    note: mergeResolved
      ? '观察记录的合并提交与合并账号都与关闭记录一致（关闭记录绑定的是实际合并提交，不是目标分支最新 HEAD）。'
      : '观察缺失、为未知读取，或其合并提交/合并账号与关闭记录不一致。',
  });

  const postMerge = verificationById(verification, closure.postMergeVerificationId);
  const postMergeResolved = postMerge !== undefined
    && postMerge.kind === 'post_merge_integration'
    && postMerge.source === 'real'
    && postMerge.result === 'passed'
    && postMerge.commitOid === closure.mergeCommitOid;
  items.push({
    key: 'post-merge',
    term: '合并后验证',
    value: closure.postMergeVerificationId,
    resolved: postMergeResolved ? 'yes' : 'no',
    note: postMergeResolved
      ? '指向针对该合并提交的 real 来源且通过的合并后集成验证行。'
      : '找不到匹配的合并后集成验证行，或它并非针对该合并提交、并非 real 来源、并未通过。',
  });

  return items;
}

/** The repair runs a closed (or merged) delivery was continued by. */
export function fixRunBindings(bindings: RunBinding[], currentRunId?: string): RunBinding[] {
  return bindings
    .filter((binding) => binding.kind === 'repair' && binding.workflowRunId !== currentRunId)
    .sort((left, right) => Date.parse(left.boundAt) - Date.parse(right.boundAt));
}

// ---- GitHub links ----------------------------------------------------------

/** GitHub's own owner/repo syntax; anything else is not a navigable slug. */
const REPOSITORY_SLUG_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})?\/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/;

/**
 * The only input is the Workspace's registered project display name, and it is
 * accepted only when it already is a GitHub `owner/repo` slug. A free-text
 * label, a URL or anything script-bearing yields no link at all: the page
 * would otherwise send the operator to a repository nobody verified.
 */
export function githubRepositorySlug(displayName: string | undefined): string | undefined {
  if (displayName === undefined) return undefined;
  const trimmed = displayName.trim();
  if (!REPOSITORY_SLUG_PATTERN.test(trimmed)) return undefined;
  if (trimmed.endsWith('.git')) return undefined;
  return trimmed;
}

export function pullRequestUrl(slug: string, number: string): string {
  return `https://github.com/${slug}/pull/${encodeURIComponent(number)}`;
}

export function commitUrl(slug: string, oid: string): string {
  return `https://github.com/${slug}/commit/${encodeURIComponent(oid)}`;
}
