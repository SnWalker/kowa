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
  ExecutionConfig,
  FixedWorkflowNode,
  FixedWorkflowView,
  GitRemoteStatus,
  HumanTask,
  HumanTaskListResponse,
  HumanTaskResponse,
  KnowledgeRecommendation,
  NodeHistory,
  RunDetail,
  RuntimeListResponse,
  RuntimeView,
  SessionBootstrapResponse,
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
  return { ...base, ...overrides };
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
