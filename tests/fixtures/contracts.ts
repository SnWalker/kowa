/**
 * Contract-bound fixtures.
 *
 * Every builder produces a payload that the frozen schemas accept (checked by
 * `tests/unit/contract-binding.test.ts` against the four `api/<contract>/v1`
 * schemas) and that the runtime decoders accept. They mirror the owners'
 * official examples; they never invent fields or statuses the contracts do not
 * define.
 */
import type {
  Admission,
  AdmissionResponse,
  ArtifactListResponse,
  ArtifactRecord,
  ClosureRecord,
  DeliveryViewResponse,
  ExecutionConfig,
  ExternalOperation,
  FixedWorkflowNode,
  FixedWorkflowView,
  GitHubObservation,
  GitRemoteStatus,
  HumanTask,
  HumanTaskListResponse,
  HumanTaskResponse,
  KnowledgeRecommendation,
  NodeHistory,
  RecoveryResponse,
  RunDetail,
  RuntimeListResponse,
  RuntimeView,
  SessionBootstrapResponse,
  VerificationRecord,
  WatermarkView,
  WorkItem,
  WorkItemListResponse,
  WorkItemResponse,
  WorkspaceConfig,
} from '../../src/api/contracts';

const TOKEN_43 = 'B'.repeat(43);
export const DIGEST_A = `sha256:${'a'.repeat(64)}`;
const DIGEST_B = `sha256:${'b'.repeat(64)}`;
const DIGEST_C = `sha256:${'c'.repeat(64)}`;
const DIGEST_D = `sha256:${'d'.repeat(64)}`;
const INDEX_DIGEST = `sha256:${'e'.repeat(64)}`;
export const OID_MAIN = '1'.repeat(40);

export function identityFixture(overrides: Partial<{ githubUserId: string; login: string }> = {}) {
  return { githubUserId: '101', login: 'member-a', ...overrides };
}

export function sessionBootstrapFixture(overrides: Partial<SessionBootstrapResponse> = {}): SessionBootstrapResponse {
  return {
    identity: identityFixture(),
    csrfToken: TOKEN_43,
    sessionId: 'C'.repeat(43),
    expiresAt: '2026-10-16T12:00:00Z',
    requestId: 'bootstrap-fixture',
    ...overrides,
  };
}

export function workspaceConfigFixture(overrides: Partial<WorkspaceConfig> = {}): WorkspaceConfig {
  const base: WorkspaceConfig = {
    workspaceId: 'workspace-a1b2c3d4',
    name: 'Kowa',
    version: 2,
    members: [
      { githubUserId: '101', role: 'admin' },
      { githubUserId: '202', role: 'viewer' },
    ],
    project: {
      githubRepositoryId: '3001',
      installationId: '4001',
      displayName: 'team/project',
      role: 'project',
      access: 'read_write',
    },
    knowledge: {
      githubRepositoryId: '3002',
      installationId: '4001',
      displayName: 'team/knowledge',
      role: 'knowledge',
      access: 'read',
    },
  };
  return { ...base, ...overrides };
}

export const freshRuntime: RuntimeView = {
  runtimeId: 'runtime-mac-codex',
  gitHubUserId: '135722937',
  providerId: 'codex',
  providerVersion: 'codex-cli 0.162.1',
  providerAuthenticated: true,
  capabilities: [
    { id: 'plan.create', version: 1 },
    { id: 'code.review', version: 2 },
  ],
  registeredAt: '2026-10-14T01:00:00Z',
  freshness: 'fresh',
  observation: {
    runtimeId: 'runtime-mac-codex',
    runtimeEpoch: 'epoch-0123456789abcdef0123456789abcdef',
    observedAt: '2026-10-14T01:05:30Z',
    receivedAt: '2026-10-14T01:05:31Z',
    executable: true,
    cliVersion: 'codex-cli 0.162.1',
    versionCompatible: true,
    authenticated: true,
    canExecute: true,
  },
};

export const staleRuntime: RuntimeView = {
  runtimeId: 'runtime-mac-claude',
  gitHubUserId: '135722937',
  providerId: 'claude',
  providerVersion: '2.1.296 (Claude Code)',
  providerAuthenticated: true,
  capabilities: [{ id: 'code.review', version: 1 }],
  registeredAt: '2026-10-14T00:00:00Z',
  freshness: 'stale',
  staleReason: 'expired',
  observation: {
    runtimeId: 'runtime-mac-claude',
    runtimeEpoch: 'epoch-fedcba9876543210fedcba9876543210',
    observedAt: '2026-10-13T22:00:00Z',
    receivedAt: '2026-10-13T22:00:01Z',
    executable: true,
    cliVersion: '2.1.296 (Claude Code)',
    versionCompatible: true,
    authenticated: false,
    canExecute: false,
    detail: 'probe execution did not complete: provider quota',
  },
};

export const unknownRuntime: RuntimeView = {
  runtimeId: 'runtime-mac-unobserved',
  gitHubUserId: '135722937',
  providerId: 'claude',
  providerVersion: '2.1.296 (Claude Code)',
  providerAuthenticated: false,
  capabilities: [{ id: 'plan.create', version: 1 }],
  registeredAt: '2026-10-14T01:10:00Z',
  freshness: 'unknown',
};

export function runtimeListFixture(runtimes?: RuntimeView[]): RuntimeListResponse {
  return {
    schemaVersion: 'kowa.runtime-web.v1',
    kind: 'runtimeList',
    runtimes: runtimes ?? [freshRuntime, staleRuntime, unknownRuntime],
  };
}

export function runtimeViewFixture(runtime: RuntimeView = staleRuntime) {
  return { schemaVersion: 'kowa.runtime-web.v1' as const, kind: 'runtimeView' as const, runtime };
}

export function executionConfigFixture(overrides: Partial<ExecutionConfig> = {}) {
  return {
    schemaVersion: 'kowa.runtime-web.v1' as const,
    kind: 'executionConfig' as const,
    config: {
      workspaceId: 'workspace-a1b2c3d4',
      version: 3,
      capabilities: [
        { id: 'code.change', version: 2 },
        { id: 'plan.create', version: 1 },
      ],
      runtimes: ['runtime-mac-claude', 'runtime-mac-codex'],
      updatedByGitHubUserId: '135722937',
      updatedAt: '2026-10-14T01:20:00Z',
      ...overrides,
    },
  };
}

export function gitRemoteStatusFixture(overrides: Partial<GitRemoteStatus> = {}) {
  const status: GitRemoteStatus = {
    schemaVersion: 'kowa.delivery-governance.v1',
    kind: 'gitRemoteStatus',
    workspaceId: 'workspace-a1b2c3d4',
    requestedByGitHubUserId: '101',
    probeOutcome: 'completed',
    observedAt: '2026-10-14T16:12:04Z',
    machineIdentity: {
      runtimeIds: ['runtime-mac-codex'],
      registeredGitHubUserIds: ['135722937'],
      observedGitHubUserId: '135722937',
      state: 'matched',
    },
    repositories: [
      {
        repositoryId: '3001',
        role: 'project',
        remoteRead: { state: 'readable', transport: 'https' },
        pushPermission: { state: 'granted' },
        writeObservation: 'none',
        observedAt: '2026-10-14T16:12:04Z',
        observedByGitHubUserId: '135722937',
      },
      {
        repositoryId: '3002',
        role: 'knowledge',
        remoteRead: { state: 'unreadable', reason: 'ls-remote exited 128' },
        pushPermission: { state: 'denied', reason: 'GitHub reports read-only access' },
        writeObservation: 'none',
        observedAt: '2026-10-14T16:12:04Z',
        observedByGitHubUserId: '135722937',
      },
    ],
    ...overrides,
  };
  return { schemaVersion: 'kowa.delivery-governance.v1' as const, kind: 'gitRemoteStatus' as const, status };
}

// ---- kowa.workitem.v1 ------------------------------------------------------

export const FIXTURE_WORK_ITEM_ID = 'wi_9f2c1d';
export const FIXTURE_RUN_ID = 'run_7c1a';

export function workItemFixture(overrides: Partial<WorkItem> = {}): WorkItem {
  return {
    workItemId: FIXTURE_WORK_ITEM_ID,
    workspaceId: 'workspace-a1b2c3d4',
    title: 'Add a deterministic greeting helper',
    targetRepositoryId: '3001',
    requirement: { version: 1, digest: DIGEST_A, content: 'Add a helper that greets deterministically.' },
    acceptance: { version: 1, digest: DIGEST_B, content: 'The helper has a passing unit test.' },
    state: 'OPEN',
    version: 1,
    createdByGitHubUserId: '101',
    createdAt: '2026-10-16T08:00:00Z',
    updatedAt: '2026-10-16T08:00:00Z',
    ...overrides,
  };
}

export function workItemResponseFixture(workItem: WorkItem = workItemFixture()): WorkItemResponse {
  return { schemaVersion: 'kowa.workitem.v1', kind: 'workItem', workItem };
}

export function workItemListFixture(workItems?: WorkItem[]): WorkItemListResponse {
  return { schemaVersion: 'kowa.workitem.v1', kind: 'workItemList', workItems: workItems ?? [workItemFixture()] };
}

// ---- kowa.web-application.v1 -------------------------------------------------

export const FIXTURE_HUMAN_TASK_ID = 'ht_5d2a';

export function admissionFixture(overrides: Partial<Admission> = {}): Admission {
  return {
    workflowRunId: FIXTURE_RUN_ID,
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    definitionName: 'kowa-fixed-workflow',
    definitionVersion: 1,
    definitionDigest: DIGEST_C,
    projectRepositoryId: '3001',
    knowledgeRepositoryId: '3002',
    projectCommitOid: OID_MAIN,
    knowledgeCommitOid: '2'.repeat(40),
    projectBranchRef: 'refs/heads/main',
    workingBranchRef: `refs/heads/task/kowa-${FIXTURE_RUN_ID}`,
    providerId: 'scripted',
    providerVersion: 'scripted-1',
    policyVersion: 'kowa.mvp-policy.v1',
    admittedByGitHubUserId: '101',
    admittedAt: '2026-10-16T08:05:00Z',
    ...overrides,
  };
}

/**
 * The frozen index the recommendation carries: one `required` entry plus two
 * optional ones, so removal, filtering and the required lock all have a real
 * subject. An empty `candidates` array is a successful empty read, never a
 * failure.
 */
export function knowledgeRecommendationFixture(overrides: Partial<KnowledgeRecommendation> = {}): KnowledgeRecommendation {
  return {
    workflowRunId: FIXTURE_RUN_ID,
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    knowledgeRepositoryId: '3002',
    knowledgeCommitOid: '2'.repeat(40),
    indexPath: 'kowa-index.json',
    indexDigest: INDEX_DIGEST,
    state: 'RECOMMENDED',
    humanTaskId: FIXTURE_HUMAN_TASK_ID,
    candidates: [
      { path: 'docs/design.md', digest: DIGEST_A, scope: 'design', required: true },
      { path: 'docs/api.md', digest: DIGEST_B, scope: 'api', required: false },
      { path: 'ops/runbook.md', digest: DIGEST_C, scope: 'operations', required: false },
    ],
    recommendedAt: '2026-10-16T08:05:01Z',
    ...overrides,
  };
}

export function admissionResponseFixture(overrides: Partial<AdmissionResponse> = {}): AdmissionResponse {
  const base: AdmissionResponse = {
    admission: admissionFixture(),
    workItemVersion: 2,
    knowledgeRecommendation: knowledgeRecommendationFixture(),
    replayed: false,
  };
  return { ...base, ...overrides };
}

// ---- kowa.delivery-governance.v1 (human tasks) ------------------------------

export function humanTaskFixture(overrides: Partial<HumanTask> = {}): HumanTask {
  const recommendation = knowledgeRecommendationFixture();
  const base: HumanTask = {
    humanTaskId: FIXTURE_HUMAN_TASK_ID,
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    workflowRunId: FIXTURE_RUN_ID,
    nodeRunId: 'knowledge_confirm:1',
    request: {
      version: 1,
      title: 'Confirm the frozen knowledge selection',
      kind: 'knowledge_confirmation',
      completionMode: 'kowa_answer',
      fields: [
        { name: 'decision', type: 'decision', required: true },
        { name: 'selection', type: 'text', required: false },
        { name: 'feedback', type: 'comment', required: false },
      ],
      object: {
        kind: 'knowledge_recommendation',
        id: FIXTURE_RUN_ID,
        version: recommendation.indexDigest,
        digest: recommendation.indexDigest,
      },
      roles: ['admin', 'developer'],
    },
    requestDigest: DIGEST_D,
    state: 'OPEN',
    version: 1,
    createdAt: '2026-10-16T08:05:02Z',
    updatedAt: '2026-10-16T08:05:02Z',
    availableActions: ['respond'],
  };
  return withoutUndefined({ ...base, ...overrides });
}

export function humanTaskResponseFixture(humanTask: HumanTask = humanTaskFixture()): HumanTaskResponse {
  return { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTask', humanTask };
}

export function humanTaskListFixture(humanTasks?: HumanTask[]): HumanTaskListResponse {
  return { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTaskList', humanTasks: humanTasks ?? [humanTaskFixture()] };
}

/** The accepted knowledge confirmation the server keeps: a new human action. */
export function resolvedHumanTaskFixture(selection = 'docs/design.md\ndocs/api.md'): HumanTask {
  const open = humanTaskFixture();
  return {
    ...open,
    state: 'RESOLVED',
    version: 2,
    updatedAt: '2026-10-16T08:10:00Z',
    resolvedAt: '2026-10-16T08:10:00Z',
    resolvedByGitHubUserId: '101',
    resolutionKind: 'kowa_answer',
    availableActions: [],
    response: {
      humanResponseId: 'hr_9a1b',
      humanTaskId: open.humanTaskId,
      workspaceId: open.workspaceId,
      requestVersion: open.request.version,
      object: open.request.object,
      githubUserId: '101',
      decision: 'approved',
      values: { decision: 'approved', selection },
      payloadDigest: DIGEST_D,
      createdAt: '2026-10-16T08:10:00Z',
    },
    continuation: {
      continuationId: 'ct_1001',
      humanResponseId: 'hr_9a1b',
      humanTaskId: open.humanTaskId,
      workspaceId: open.workspaceId,
      workflowRunId: FIXTURE_RUN_ID,
      nodeRunId: 'knowledge_confirm:1',
      intent: 'resume_node',
      state: 'PENDING',
      createdAt: '2026-10-16T08:10:00Z',
    },
  };
}

/**
 * An external-fact request: settled only by a reconciled GitHub fact, and the
 * shape that must never reach an answer form.
 */
export function externalFactHumanTaskFixture(overrides: Partial<HumanTask> = {}): HumanTask {
  return humanTaskFixture({
    request: {
      version: 1,
      title: 'Review the delivered change',
      kind: 'pull_request_review',
      completionMode: 'external_fact',
      fields: [{ name: 'decision', type: 'decision', required: true }],
      object: { kind: 'pull_request_review', id: '42', version: '1'.repeat(40) },
      roles: ['admin', 'developer'],
    },
    availableActions: ['await-external-fact'],
    ...overrides,
  });
}

/** The owner's own answer to one recovery command. */
export function recoveryResponseFixture(overrides: Partial<RecoveryResponse> = {}): RecoveryResponse {
  const base: RecoveryResponse = {
    workflowRunId: FIXTURE_RUN_ID,
    kind: 'cancel',
    applied: true,
    state: 'CANCELING',
    version: 3,
    detail: 'cancellation was requested; in-flight attempts settle before the run ends',
    recordedAt: '2026-10-16T09:30:00Z',
  };
  return withoutUndefined({ ...base, ...overrides });
}

export function runDetailFixture(overrides: Partial<RunDetail> = {}): RunDetail {
  const base: RunDetail = {
    schemaVersion: 'kowa.web-application.v1',
    kind: 'runDetail',
    workItemId: FIXTURE_WORK_ITEM_ID,
    workflowRunId: FIXTURE_RUN_ID,
    state: 'ACTIVE',
    version: 2,
    definitionName: 'kowa-fixed-workflow',
    definitionVersion: 1,
    definitionDigest: DIGEST_C,
    startedByGitHubUserId: '101',
    gitExecutionUserId: '135722937',
    nodes: [
      {
        nodeId: 'knowledge_recommend',
        type: 'system',
        iteration: 1,
        state: 'DECIDED',
        verdict: 'approved',
        inputDigest: DIGEST_A,
        output: { candidateCount: 3, indexDigest: INDEX_DIGEST },
        attempts: [],
      },
      {
        nodeId: 'knowledge_confirm',
        type: 'human_task',
        iteration: 1,
        state: 'WAITING_HUMAN',
        inputDigest: DIGEST_B,
        attempts: [],
      },
    ],
    runBindings: [
      {
        workItemId: FIXTURE_WORK_ITEM_ID,
        workflowRunId: FIXTURE_RUN_ID,
        kind: 'initial',
        boundByGitHubUserId: '101',
        boundAt: '2026-10-16T08:05:00Z',
      },
    ],
    knowledgeRecommendation: knowledgeRecommendationFixture(),
    watermark: { observedAt: '2026-10-16T08:05:03Z', runUpdatedAt: '2026-10-16T08:05:03Z' },
  };
  return { ...base, ...overrides };
}

// ---- kowa.fixed-workflow.v1 -------------------------------------------------

/** The frozen definition identity the fixtures share with the run projection. */
export const FIXTURE_DEFINITION_NAME = 'kowa-fixed-workflow';
export const FIXTURE_DEFINITION_DIGEST = DIGEST_C;

/**
 * A small stand-in for the one fixed workflow: it keeps the real projection's
 * shape (a fan-out from an approval, a join that waits for both branches, an
 * end node, a capability carrying a revision budget) so the drawing is
 * exercised, while staying small enough to assert on.
 */
export function fixedWorkflowFixture(overrides: Partial<FixedWorkflowView> = {}): FixedWorkflowView {
  const nodes: FixedWorkflowNode[] = [
    { id: 'knowledge_recommend', type: 'system', dependsOn: [], inputs: [], operation: 'knowledge.recommend', mandatory: true },
    { id: 'knowledge_confirm', type: 'human_task', dependsOn: ['knowledge_recommend'], inputs: [], humanTask: 'knowledge_confirmation', mandatory: true },
    { id: 'plan', type: 'capability', dependsOn: ['knowledge_confirm'], inputs: [], capability: { id: 'plan.create', version: 1 }, mandatory: true },
    {
      id: 'plan_review',
      type: 'capability',
      dependsOn: ['plan'],
      inputs: [{ key: 'plan_ref', producerNodeId: 'plan', output: 'plan_ref', type: 'artifact_ref', required: true }],
      capability: { id: 'plan.review', version: 1 },
      revision: { targetNodeId: 'plan', maxAutoRevisions: 3 },
      mandatory: true,
    },
    {
      id: 'plan_approval',
      type: 'human_task',
      dependsOn: ['plan_review'],
      inputs: [{ key: 'plan_ref', producerNodeId: 'plan', output: 'plan_ref', type: 'artifact_ref', required: true }],
      humanTask: 'plan_approval',
      mandatory: true,
    },
    { id: 'code', type: 'capability', dependsOn: ['plan_approval'], inputs: [], capability: { id: 'code.implement', version: 1 }, mandatory: true },
    { id: 'design_tests', type: 'capability', dependsOn: ['plan_approval'], inputs: [], capability: { id: 'testcases.design', version: 1 }, mandatory: true },
    { id: 'verify_change', type: 'join', dependsOn: ['code', 'design_tests'], inputs: [], mandatory: true },
    { id: 'end', type: 'end', dependsOn: ['verify_change'], inputs: [], mandatory: true },
  ];
  return {
    schemaVersion: 'kowa.fixed-workflow.v1',
    kind: 'fixedWorkflowView',
    name: FIXTURE_DEFINITION_NAME,
    version: 1,
    definitionSchemaVersion: 'kowa.workflow-execution.v1',
    definitionDigest: FIXTURE_DEFINITION_DIGEST,
    order: nodes.map((node) => node.id),
    nodes,
    ...overrides,
  };
}

// ---- kowa.web-application.v1 (delivery.artifact-fixrun) ---------------------

export function artifactFixture(overrides: Partial<ArtifactRecord> = {}): ArtifactRecord {
  return {
    artifactId: 'art_1a2b3c',
    subjectId: 'run_7c1a:node:plan:1',
    kind: 'plan_document',
    digest: DIGEST_A,
    size: 2048,
    mediaType: 'application/json',
    producerKind: 'task',
    producerId: 'run_7c1a:task:plan:1',
    requiresApproval: false,
    state: 'PUBLISHED',
    createdAt: '2026-10-16T08:04:00Z',
    ...overrides,
  };
}

export function artifactListFixture(
  artifacts?: ArtifactRecord[],
  runBindings?: RunDetail['runBindings'],
): ArtifactListResponse {
  return {
    schemaVersion: 'kowa.web-application.v1',
    kind: 'artifactList',
    workItemId: FIXTURE_WORK_ITEM_ID,
    artifacts: artifacts ?? [artifactFixture()],
    runBindings: runBindings ?? [
      {
        workItemId: FIXTURE_WORK_ITEM_ID,
        workflowRunId: FIXTURE_RUN_ID,
        kind: 'initial',
        boundByGitHubUserId: '101',
        boundAt: '2026-10-16T08:05:00Z',
      },
    ],
  };
}

/** One node round, with the fields the run projection always carries. */
export function nodeHistoryFixture(overrides: Partial<NodeHistory> = {}): NodeHistory {
  return {
    nodeId: 'plan',
    type: 'capability',
    iteration: 1,
    state: 'DECIDED',
    inputDigest: DIGEST_A,
    attempts: [],
    ...overrides,
  };
}

// ---- kowa.delivery-governance.v1 (delivery.query) --------------------------

/**
 * A field set to `undefined` is an absent field, not a present one: the frozen
 * schemas reject `null`/undefined values, and the wire never carries them. The
 * builders below therefore drop those keys, exactly as `JSON.stringify` does.
 */
function withoutUndefined<T extends object>(value: T): T {
  return Object.fromEntries(Object.entries(value).filter(([, entry]) => entry !== undefined)) as T;
}

/** The machine gh account the run froze: distinct from every Web actor. */
export const FIXTURE_MACHINE_GITHUB_ID = '135722937';
/** A second human who reviewed; not the PR author and not the web initiator. */
export const FIXTURE_REVIEWER_GITHUB_ID = '277460407';
export const FIXTURE_DELIVERY_COMMIT = 'de62a4324cad57908376f8c5f3e14bd3bd3dab95';
export const FIXTURE_MERGE_COMMIT = 'ca6c6ec8446ec687213d58064a7fd8363050321a';
export const FIXTURE_PR_NUMBER = '42';
export const FIXTURE_PR_BRANCH = 'refs/heads/task/s06-delivery';

export function externalOperationFixture(overrides: Partial<ExternalOperation> = {}): ExternalOperation {
  const base: ExternalOperation = {
    operationId: 'op_31cf',
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    workflowRunId: FIXTURE_RUN_ID,
    kind: 'publish_pr',
    state: 'SUCCEEDED',
    request: {
      repositoryId: '3001',
      role: 'project',
      operation: 'publish_pr',
      ref: FIXTURE_PR_BRANCH,
      baseRef: 'refs/heads/main',
      commitOid: FIXTURE_DELIVERY_COMMIT,
      expectedGitHubUserId: FIXTURE_MACHINE_GITHUB_ID,
      requestedByGitHubUserId: '101',
    },
    requestDigest: DIGEST_A,
    result: {
      repositoryId: '3001',
      ref: FIXTURE_PR_BRANCH,
      commitOid: FIXTURE_DELIVERY_COMMIT,
      pullRequestNumber: FIXTURE_PR_NUMBER,
      pullRequestAuthorId: FIXTURE_MACHINE_GITHUB_ID,
      baseRef: 'refs/heads/main',
      reconciled: true,
      observationId: 'obs_pr_42',
      observedAt: '2026-10-16T09:30:00Z',
      observedByGitHubUserId: FIXTURE_MACHINE_GITHUB_ID,
    },
    version: 3,
    createdAt: '2026-10-16T09:20:00Z',
    updatedAt: '2026-10-16T09:30:00Z',
    settledAt: '2026-10-16T09:30:00Z',
  };
  return withoutUndefined({ ...base, ...overrides });
}

/** A PR that was merged by a human, on the same head the review applies to. */
export function pullRequestObservationFixture(overrides: Partial<GitHubObservation> = {}): GitHubObservation {
  const base: GitHubObservation = {
    observationId: 'obs_pr_42',
    workspaceId: 'workspace-a1b2c3d4',
    repositoryId: '3001',
    subjectKind: 'pull_request',
    subjectKey: FIXTURE_PR_NUMBER,
    observedAt: '2026-10-16T10:00:00Z',
    actorId: FIXTURE_MACHINE_GITHUB_ID,
    unknown: false,
    pullRequest: {
      number: FIXTURE_PR_NUMBER,
      authorId: FIXTURE_MACHINE_GITHUB_ID,
      headOid: FIXTURE_DELIVERY_COMMIT,
      headRef: 'task/s06-delivery',
      baseRef: 'refs/heads/main',
      state: 'closed',
      merged: true,
      mergeCommitOid: FIXTURE_MERGE_COMMIT,
      mergedById: FIXTURE_REVIEWER_GITHUB_ID,
      mergedAt: '2026-10-16T09:55:00Z',
      mergedByType: 'User',
    },
  };
  return withoutUndefined({ ...base, ...overrides });
}

export function reviewObservationFixture(overrides: Partial<GitHubObservation> = {}): GitHubObservation {
  const base: GitHubObservation = {
    observationId: 'obs_review_5001',
    workspaceId: 'workspace-a1b2c3d4',
    repositoryId: '3001',
    subjectKind: 'pull_request_review',
    subjectKey: `${FIXTURE_PR_NUMBER}/5001`,
    observedAt: '2026-10-16T09:40:00Z',
    actorId: FIXTURE_MACHINE_GITHUB_ID,
    unknown: false,
    review: {
      reviewId: '5001',
      reviewerId: FIXTURE_REVIEWER_GITHUB_ID,
      state: 'APPROVED',
      commitOid: FIXTURE_DELIVERY_COMMIT,
      submittedAt: '2026-10-16T09:38:00Z',
      authorAssociation: 'MEMBER',
      reviewerLogin: 'reviewer-a',
      reviewerType: 'User',
      reviewerPermission: 'write',
    },
  };
  return withoutUndefined({ ...base, ...overrides });
}

export function checksObservationFixture(overrides: Partial<GitHubObservation> = {}): GitHubObservation {
  const base: GitHubObservation = {
    observationId: 'obs_checks_head',
    workspaceId: 'workspace-a1b2c3d4',
    repositoryId: '3001',
    subjectKind: 'pull_request_checks',
    subjectKey: FIXTURE_DELIVERY_COMMIT,
    observedAt: '2026-10-16T09:45:00Z',
    actorId: FIXTURE_MACHINE_GITHUB_ID,
    unknown: false,
    checks: { commitOid: FIXTURE_DELIVERY_COMMIT, state: 'passed', total: 2, failed: 0, pending: 0 },
  };
  return withoutUndefined({ ...base, ...overrides });
}

export function verificationFixture(overrides: Partial<VerificationRecord> = {}): VerificationRecord {
  const base: VerificationRecord = {
    verificationId: 've_tests',
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    workflowRunId: FIXTURE_RUN_ID,
    kind: 'required_tests',
    subjectKind: 'commit',
    repositoryId: '3001',
    commitOid: FIXTURE_DELIVERY_COMMIT,
    configurationVersion: 'build-v1',
    producerKind: 'task',
    producerId: 'task_1',
    source: 'real',
    result: 'passed',
    // The published artifact the evidence points at; the page only ever shows
    // the reference, never the content.
    reportArtifactId: 'art_1a2b3c',
    reportDigest: DIGEST_A,
    observedAt: '2026-10-16T09:00:00Z',
    createdAt: '2026-10-16T09:00:00Z',
  };
  return withoutUndefined({ ...base, ...overrides });
}

export function closureRecordFixture(overrides: Partial<ClosureRecord> = {}): ClosureRecord {
  const base: ClosureRecord = {
    closureId: 'cl_77aa',
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    workflowRunId: FIXTURE_RUN_ID,
    definitionName: 'kowa-fixed-workflow',
    definitionVersion: 1,
    definitionDigest: DIGEST_C,
    policyVersion: 'delivery-policy-v1',
    requirementDigest: DIGEST_A,
    mergeRepositoryId: '3001',
    mergeCommitOid: FIXTURE_MERGE_COMMIT,
    pullRequestNumber: FIXTURE_PR_NUMBER,
    mergeObservationId: 'obs_pr_42',
    reviewObservationId: 'obs_review_5001',
    reviewHeadOid: FIXTURE_DELIVERY_COMMIT,
    reviewerGitHubUserId: FIXTURE_REVIEWER_GITHUB_ID,
    requiredTestVerificationIds: ['ve_tests'],
    independentReviewVerificationId: 've_review',
    postMergeVerificationId: 've_post',
    checklistDigest: DIGEST_B,
    createdByGitHubUserId: '101',
    createdAt: '2026-10-16T10:05:00Z',
    mergedByGitHubUserId: FIXTURE_REVIEWER_GITHUB_ID,
    mergedByType: 'User',
  };
  return withoutUndefined({ ...base, ...overrides });
}

/** The four synchronous gate rows plus the two auxiliary ones the owner freezes. */
export function deliveryVerificationFixture(): VerificationRecord[] {
  return [
    verificationFixture(),
    verificationFixture({
      verificationId: 've_review',
      kind: 'independent_review',
      producerId: 'task_2',
      configurationVersion: 'review-v1',
      observedAt: '2026-10-16T09:10:00Z',
      createdAt: '2026-10-16T09:10:00Z',
    }),
    verificationFixture({
      verificationId: 've_security',
      kind: 'security_review',
      producerId: 'task_3',
      configurationVersion: 'security-v1',
      observedAt: '2026-10-16T09:15:00Z',
      createdAt: '2026-10-16T09:15:00Z',
    }),
    verificationFixture({
      verificationId: 've_post',
      kind: 'post_merge_integration',
      commitOid: FIXTURE_MERGE_COMMIT,
      producerId: 'task_4',
      configurationVersion: 'integration-v1',
      observedAt: '2026-10-16T10:02:00Z',
      createdAt: '2026-10-16T10:02:00Z',
    }),
    verificationFixture({
      verificationId: 've_suggestion',
      kind: 'knowledge_suggestion',
      subjectKind: 'artifact',
      commitOid: undefined,
      artifactId: 'art_suggestion',
      artifactDigest: DIGEST_D,
      repositoryId: undefined,
      producerKind: 'control_plane',
      producerId: 'suggestion_1',
      configurationVersion: 'knowledge-v1',
      result: 'failed',
      observedAt: '2026-10-16T10:04:00Z',
      createdAt: '2026-10-16T10:04:00Z',
    }),
    verificationFixture({
      verificationId: 've_offline',
      kind: 'offline_evaluation',
      subjectKind: 'artifact',
      commitOid: undefined,
      artifactId: 'art_eval',
      artifactDigest: DIGEST_D,
      repositoryId: undefined,
      producerKind: 'control_plane',
      producerId: 'eval_1',
      configurationVersion: 'eval-v1',
      result: 'failed',
      observedAt: '2026-10-16T10:06:00Z',
      createdAt: '2026-10-16T10:06:00Z',
    }),
  ];
}

/**
 * A closed delivery: the PR merged, a review applies to the head that merged,
 * the gate rows passed, and the owner committed a closure record. Variants
 * override exactly the fact under test.
 */
export function deliveryViewFixture(overrides: Partial<DeliveryViewResponse> = {}): DeliveryViewResponse {
  const base: DeliveryViewResponse = {
    schemaVersion: 'kowa.delivery-governance.v1',
    kind: 'deliveryView',
    workItem: workItemFixture({ state: 'CLOSED', version: 4, currentRunId: FIXTURE_RUN_ID }),
    runBindings: [
      { workItemId: FIXTURE_WORK_ITEM_ID, workflowRunId: FIXTURE_RUN_ID, kind: 'initial', boundByGitHubUserId: '101', boundAt: '2026-10-16T08:05:00Z' },
    ],
    operations: [externalOperationFixture()],
    facts: [pullRequestObservationFixture(), reviewObservationFixture(), checksObservationFixture()],
    verification: deliveryVerificationFixture(),
    closure: closureRecordFixture(),
  };
  return withoutUndefined({ ...base, ...overrides });
}

// ---- kowa.observability.v1 (observability.watermarks) ----------------------

export function watermarkViewFixture(overrides: Partial<WatermarkView> = {}): WatermarkView {
  const base: WatermarkView = {
    schemaVersion: 'kowa.observability.v1',
    kind: 'observabilityWatermarks',
    observedAt: '2026-10-16T10:10:00Z',
    workspaceId: 'workspace-a1b2c3d4',
    workItemId: FIXTURE_WORK_ITEM_ID,
    deployment: { commit: '6d88be15052683e0807e7a363f33000565608066', migrationVersion: '000006', goVersion: 'go1.26.9' },
    definition: { name: 'kowa-fixed-workflow', version: 1, digest: DIGEST_C },
    executionConfigVersion: 3,
    run: {
      workflowRunId: FIXTURE_RUN_ID,
      state: 'SUCCEEDED',
      version: 9,
      updatedAt: '2026-10-16T10:05:00Z',
      nodeRounds: 6,
      attempts: 7,
      openHumanTasks: 0,
    },
    observations: [
      { subjectKind: 'pull_request', subjectKey: FIXTURE_PR_NUMBER, lastAttemptAt: '2026-10-16T10:00:00Z', lastAttemptKnown: true, freshness: 'fresh', supersededByFailure: false },
      { subjectKind: 'pull_request_review', subjectKey: `${FIXTURE_PR_NUMBER}/5001`, lastAttemptAt: '2026-10-16T09:40:00Z', lastAttemptKnown: true, freshness: 'fresh', supersededByFailure: false },
      { subjectKind: 'pull_request_checks', subjectKey: FIXTURE_DELIVERY_COMMIT, lastAttemptAt: '2026-10-16T09:45:00Z', lastAttemptKnown: true, freshness: 'fresh', supersededByFailure: false },
    ],
    runtimes: [
      { runtimeId: 'runtime-mac-claude', providerId: 'claude', providerVersion: '2.1.299', capabilities: ['code.generate@1'], authenticated: true, observedAt: '2026-10-16T10:09:00Z', freshness: 'fresh', contactable: true },
    ],
    progress: {
      kind: 'business_progress',
      subject: FIXTURE_RUN_ID,
      lastSuccessAt: '2026-10-16T10:05:00Z',
      lastSuccessDetail: 'run advanced',
      lastAttemptAt: '2026-10-16T10:05:00Z',
      successes: 1,
      failures: 0,
      freshness: 'fresh',
      contactable: false,
      progressing: true,
    },
    artifacts: { namespace: DIGEST_D, published: 3 },
    freshnessWindowSeconds: 900,
  };
  return withoutUndefined({ ...base, ...overrides });
}
