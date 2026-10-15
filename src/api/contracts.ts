/**
 * Wire types for the frozen contracts the frontend consumes.
 *
 * Owners and pinned digests are recorded in
 * `doc/Kowa前端设计/record/S01-消费准入证据.md` (S01 operations) and
 * `doc/Kowa前端设计/record/S02-消费准入证据.md` (S02 operations); these types
 * mirror the official schemas field by field and must not add, rename or
 * reinterpret business fields. Every response is decoded by `decode.ts`, which
 * fails closed on unknown fields and shapes.
 */

// ---- kowa.identity-workspace.v1 -------------------------------------------

export const WORKSPACE_ROLES = ['admin', 'developer', 'viewer'] as const;
export type WorkspaceRole = (typeof WORKSPACE_ROLES)[number];

export type WebIdentity = {
  githubUserId: string;
  login: string;
};

export type WorkspaceMember = {
  githubUserId: string;
  role: WorkspaceRole;
  revokedAt?: string;
};

export type RepositoryBinding = {
  githubRepositoryId: string;
  installationId: string;
  displayName: string;
  role: 'project' | 'knowledge';
  access: 'read' | 'read_write';
};

export type WorkspaceConfig = {
  workspaceId: string;
  name: string;
  version: number;
  members: WorkspaceMember[];
  project?: RepositoryBinding;
  knowledge?: RepositoryBinding;
};

export type RepositoryInput = {
  githubRepositoryId: string;
  installationId: string;
  displayName: string;
};

export type ConfigureRepositoriesCommand = {
  expectedVersion: number;
  project: RepositoryInput;
  knowledge: RepositoryInput;
};

// ---- kowa.web-session.v1 ---------------------------------------------------

export type LoginResponse = {
  authorizationUrl: string;
};

export type SessionBootstrapResponse = {
  identity: WebIdentity;
  csrfToken: string;
  sessionId: string;
  expiresAt: string;
  requestId: string;
};

// ---- shared error envelope -------------------------------------------------

/**
 * The frozen categories the frontend classifies. `EXTERNAL_UNKNOWN` is part of
 * the shared cross-end semantics and of every S02-consumed family's frozen
 * `APIError` enum (`kowa.workitem.v1`, `kowa.web-application.v1`,
 * `kowa.delivery-governance.v1`); presenting it as "envelope not understood"
 * would misstate a real, frozen answer. `kowa.identity-workspace.v1` and
 * `kowa.runtime-web.v1` freeze a narrower reachable face, which stays a subset.
 */
export const API_ERROR_CATEGORIES = [
  'VALIDATION_ERROR',
  'UNAUTHORIZED',
  'FORBIDDEN',
  'VERSION_CONFLICT',
  'RESOURCE_UNAVAILABLE',
  'EXTERNAL_UNKNOWN',
  'POLICY_BLOCKED',
  'INTERNAL_ERROR',
] as const;
export type ApiErrorCategory = (typeof API_ERROR_CATEGORIES)[number];

// ---- kowa.runtime-web.v1 ---------------------------------------------------

export type Capability = {
  id: string;
  version: number;
};

export type RuntimeObservation = {
  runtimeId: string;
  runtimeEpoch: string;
  observedAt: string;
  receivedAt: string;
  executable: boolean;
  cliVersion: string;
  versionCompatible: boolean;
  authenticated: boolean;
  canExecute: boolean;
  detail?: string;
};

export const RUNTIME_FRESHNESS = ['fresh', 'stale', 'unknown'] as const;
export type RuntimeFreshness = (typeof RUNTIME_FRESHNESS)[number];

export const RUNTIME_STALE_REASONS = ['expired', 'superseded-epoch'] as const;
export type RuntimeStaleReason = (typeof RUNTIME_STALE_REASONS)[number];

export type RuntimeView = {
  runtimeId: string;
  gitHubUserId: string;
  providerId: string;
  providerVersion: string;
  providerAuthenticated: boolean;
  capabilities: Capability[];
  registeredAt: string;
  freshness: RuntimeFreshness;
  staleReason?: RuntimeStaleReason;
  observation?: RuntimeObservation;
};

export type RuntimeListResponse = {
  schemaVersion: 'kowa.runtime-web.v1';
  kind: 'runtimeList';
  runtimes: RuntimeView[];
};

export type RuntimeViewResponse = {
  schemaVersion: 'kowa.runtime-web.v1';
  kind: 'runtimeView';
  runtime: RuntimeView;
};

export type ExecutionConfig = {
  workspaceId: string;
  version: number;
  capabilities: Capability[];
  runtimes: string[];
  updatedByGitHubUserId?: string;
  updatedAt?: string;
};

export type ExecutionConfigResponse = {
  schemaVersion: 'kowa.runtime-web.v1';
  kind: 'executionConfig';
  config: ExecutionConfig;
};

export type UpdateExecutionConfigRequest = {
  requestKey: string;
  expectedVersion: number;
  capabilities: Capability[];
  runtimes: string[];
};

// ---- kowa.delivery-governance.v1 (gitRemote.status) ------------------------

export const MACHINE_IDENTITY_STATES = ['matched', 'drifted', 'unknown'] as const;
export type MachineIdentityState = (typeof MACHINE_IDENTITY_STATES)[number];

export const REMOTE_READ_STATES = ['readable', 'unreadable', 'unknown'] as const;
export type RemoteReadState = (typeof REMOTE_READ_STATES)[number];

export const PUSH_PERMISSION_STATES = ['granted', 'denied', 'unknown'] as const;
export type PushPermissionState = (typeof PUSH_PERMISSION_STATES)[number];

export type GitRemoteMachineIdentity = {
  runtimeIds: string[];
  registeredGitHubUserIds: string[];
  observedGitHubUserId?: string;
  state: MachineIdentityState;
  reason?: string;
};

export type GitRemoteRepositoryStatus = {
  repositoryId: string;
  role: 'project' | 'knowledge';
  remoteRead: { state: RemoteReadState; transport?: 'https' | 'ssh'; reason?: string };
  pushPermission: { state: PushPermissionState; reason?: string };
  writeObservation: 'none';
  observedAt: string;
  observedByGitHubUserId?: string;
};

export type GitRemoteStatus = {
  schemaVersion: 'kowa.delivery-governance.v1';
  kind: 'gitRemoteStatus';
  workspaceId: string;
  requestedByGitHubUserId: string;
  probeOutcome: 'completed' | 'unknown';
  probeReason?: string;
  observedAt: string;
  machineIdentity: GitRemoteMachineIdentity;
  repositories: GitRemoteRepositoryStatus[];
};

export type GitRemoteStatusResponse = {
  schemaVersion: 'kowa.delivery-governance.v1';
  kind: 'gitRemoteStatus';
  status: GitRemoteStatus;
};

// ---- kowa.workitem.v1 (workitem.intake / workitem.list / workitem.detail) ---

export const WORK_ITEM_STATES = ['OPEN', 'CLOSED', 'CANCELED'] as const;
export type WorkItemState = (typeof WORK_ITEM_STATES)[number];

/** Server-issued content identity: version, digest and exact bytes. */
export type VersionedContent = {
  version: number;
  digest: string;
  content: string;
};

/** Browser-supplied content: the owner freezes the digest, the client never does. */
export type VersionedContentInput = {
  version: number;
  content: string;
};

export type WorkItem = {
  workItemId: string;
  workspaceId: string;
  title: string;
  targetRepositoryId: string;
  requirement: VersionedContent;
  acceptance: VersionedContent;
  state: WorkItemState;
  version: number;
  currentRunId?: string;
  createdByGitHubUserId: string;
  createdAt: string;
  updatedAt: string;
  closedAt?: string;
  canceledAt?: string;
};

export type WorkItemResponse = {
  schemaVersion: 'kowa.workitem.v1';
  kind: 'workItem';
  workItem: WorkItem;
};

export type WorkItemListResponse = {
  schemaVersion: 'kowa.workitem.v1';
  kind: 'workItemList';
  workItems: WorkItem[];
};

/**
 * The intake command carries no target repository and no actor: the workspace
 * project binding decides the target and the session decides the actor, so a
 * browser cannot smuggle either (frozen negative `negative-schema-smuggled-actor`).
 */
export type CreateWorkItemCommand = {
  requestKey: string;
  title: string;
  requirement: VersionedContentInput;
  acceptance: VersionedContentInput;
};

// ---- kowa.web-application.v1 (intake.start-knowledge / run.detail-history) --

export const RUN_STATES = ['ACTIVE', 'PAUSED', 'CANCELING', 'SUCCEEDED', 'FAILED', 'CANCELED'] as const;
export type RunState = (typeof RUN_STATES)[number];

export const NODE_ROUND_STATES = [
  'PENDING', 'READY', 'RUNNING', 'WAITING_HUMAN', 'DECIDED', 'EXECUTION_FAILED', 'SKIPPED', 'CANCELED',
] as const;
export type NodeRoundState = (typeof NODE_ROUND_STATES)[number];

export const NODE_TYPES = ['capability', 'human_task', 'system', 'join', 'end'] as const;
export type NodeType = (typeof NODE_TYPES)[number];

export const ATTEMPT_KINDS = ['initial', 'retry', 'resume'] as const;
export type AttemptKind = (typeof ATTEMPT_KINDS)[number];

export const ATTEMPT_STATES = [
  'QUEUED', 'LEASED', 'RUNNING', 'AWAITING_INPUT', 'COMPLETED', 'FAILED', 'EXPIRED', 'CANCELED',
] as const;
export type AttemptState = (typeof ATTEMPT_STATES)[number];

export const KNOWLEDGE_RECOMMENDATION_STATES = ['RECOMMENDED', 'CONFIRMED'] as const;
export type KnowledgeRecommendationState = (typeof KNOWLEDGE_RECOMMENDATION_STATES)[number];

export const KNOWLEDGE_SUGGESTION_STATES = ['PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED'] as const;
export type KnowledgeSuggestionState = (typeof KNOWLEDGE_SUGGESTION_STATES)[number];

export type StartRunCommand = {
  requestKey: string;
  /** The WorkItem version the start was decided against; issued by the server. */
  expectedVersion: number;
};

export type Admission = {
  workflowRunId: string;
  workspaceId: string;
  workItemId: string;
  definitionName: string;
  definitionVersion: number;
  definitionDigest: string;
  projectRepositoryId: string;
  knowledgeRepositoryId: string;
  projectCommitOid: string;
  knowledgeCommitOid: string;
  projectBranchRef: string;
  workingBranchRef: string;
  providerId: string;
  providerVersion: string;
  policyVersion: string;
  admittedByGitHubUserId: string;
  admittedAt: string;
};

/** One frozen index entry the human may keep or drop; `required` entries cannot. */
export type KnowledgeCandidate = {
  path: string;
  digest: string;
  scope: string;
  required: boolean;
};

export type KnowledgeRecommendation = {
  workflowRunId: string;
  workspaceId: string;
  workItemId: string;
  knowledgeRepositoryId: string;
  knowledgeCommitOid: string;
  indexPath: string;
  indexDigest: string;
  state: KnowledgeRecommendationState;
  humanTaskId?: string;
  candidates: KnowledgeCandidate[];
  snapshotId?: string;
  bindingEventId?: string;
  effectiveInputId?: string;
  recommendedAt: string;
  confirmedAt?: string;
};

export type AdmissionResponse = {
  admission: Admission;
  /** The WorkItem version this admission advanced to. */
  workItemVersion: number;
  knowledgeRecommendation?: KnowledgeRecommendation;
  replayed: boolean;
};

export type RunBinding = {
  workItemId: string;
  workflowRunId: string;
  kind: 'initial' | 'repair';
  sourceRunId?: string;
  reason?: string;
  mergedBaselineOid?: string;
  boundByGitHubUserId: string;
  boundAt: string;
};

export type NodeAttempt = {
  taskId: string;
  attempt: number;
  attemptKind: AttemptKind;
  state: AttemptState;
  inputDigest: string;
  resultDigest?: string;
  runtimeId?: string;
  runtimeEpoch?: string;
  fencingToken?: number;
  leaseId?: string;
  artifactIds: string[];
};

export type NodeHistory = {
  nodeId: string;
  type: NodeType;
  iteration: number;
  state: NodeRoundState;
  verdict?: string;
  inputDigest: string;
  output?: Record<string, unknown>;
  attempts: NodeAttempt[];
};

export type KnowledgeSuggestionAttempt = {
  attemptId: string;
  attempt: number;
  state: KnowledgeSuggestionState;
  inputDigest: string;
  artifactId?: string;
  artifactDigest?: string;
  failureReason?: string;
  requestedAt: string;
  updatedAt: string;
};

export type KnowledgeSuggestion = {
  workflowRunId: string;
  state: KnowledgeSuggestionState;
  attempts: number;
  artifactId?: string;
  failureReason?: string;
  updatedAt: string;
  history: KnowledgeSuggestionAttempt[];
};

export type RunWatermark = {
  observedAt: string;
  runUpdatedAt?: string;
};

/**
 * The composite run projection. frontend:S02 reads only the run identity and
 * `knowledgeRecommendation` (the knowledge binding state and its confirmation
 * target); node rounds, attempts and the knowledge-suggestion branch are
 * decoded strictly but are not rendered before frontend:S03/S04.
 */
export type RunDetail = {
  schemaVersion: 'kowa.web-application.v1';
  kind: 'runDetail';
  workItemId: string;
  workflowRunId: string;
  state: RunState;
  version: number;
  definitionName: string;
  definitionVersion: number;
  definitionDigest: string;
  startedByGitHubUserId: string;
  gitExecutionUserId?: string;
  nodes: NodeHistory[];
  runBindings: RunBinding[];
  knowledgeRecommendation?: KnowledgeRecommendation;
  knowledgeSuggestion?: KnowledgeSuggestion;
  watermark: RunWatermark;
};

// ---- kowa.delivery-governance.v1 (human.list / human.detail / human.response)

export const HUMAN_TASK_STATES = ['OPEN', 'RESOLVED', 'EXPIRED', 'CANCELED'] as const;
export type HumanTaskState = (typeof HUMAN_TASK_STATES)[number];

export const HUMAN_COMPLETION_MODES = ['kowa_answer', 'external_fact'] as const;
export type HumanCompletionMode = (typeof HUMAN_COMPLETION_MODES)[number];

export const HUMAN_DECISIONS = ['approved', 'rejected', 'submitted'] as const;
export type HumanDecision = (typeof HUMAN_DECISIONS)[number];

export const HUMAN_TASK_ACTIONS = ['respond', 'await-external-fact'] as const;
export type HumanTaskAction = (typeof HUMAN_TASK_ACTIONS)[number];

export const HUMAN_FIELD_TYPES = ['text', 'decision', 'choice', 'comment'] as const;
export type HumanFieldType = (typeof HUMAN_FIELD_TYPES)[number];

export const CONTINUATION_STATES = ['PENDING', 'CONSUMED'] as const;
export type ContinuationState = (typeof CONTINUATION_STATES)[number];

/**
 * The exact object version an answer applies to. It is taken from
 * `human.detail`, never rebuilt from the recommendation: a re-asked
 * confirmation has a different request/object version and an answer against an
 * older binding is refused.
 */
export type HumanObjectBinding = {
  kind: string;
  id: string;
  version: string;
  digest?: string;
};

export type HumanField = {
  name: string;
  type: HumanFieldType;
  required: boolean;
  options?: string[];
};

export type HumanTaskRequest = {
  version: number;
  title: string;
  kind: string;
  completionMode: HumanCompletionMode;
  fields: HumanField[];
  object: HumanObjectBinding;
  candidates?: string[];
  roles?: WorkspaceRole[];
};

export type HumanResponse = {
  humanResponseId: string;
  humanTaskId: string;
  workspaceId: string;
  requestVersion: number;
  object: HumanObjectBinding;
  githubUserId: string;
  decision: HumanDecision;
  values: Record<string, string>;
  payloadDigest: string;
  createdAt: string;
};

export type HumanContinuation = {
  continuationId: string;
  humanResponseId: string;
  humanTaskId: string;
  workspaceId: string;
  workflowRunId?: string;
  nodeRunId?: string;
  intent: 'resume_node';
  state: ContinuationState;
  createdAt: string;
  consumedAt?: string;
  consumedByGitHubUserId?: string;
};

export type HumanTask = {
  humanTaskId: string;
  workspaceId: string;
  workItemId: string;
  workflowRunId?: string;
  nodeRunId?: string;
  request: HumanTaskRequest;
  requestDigest: string;
  state: HumanTaskState;
  version: number;
  createdAt: string;
  updatedAt: string;
  resolvedAt?: string;
  resolvedByGitHubUserId?: string;
  resolutionKind?: HumanCompletionMode;
  externalObservationId?: string;
  expiredReason?: string;
  response?: HumanResponse;
  continuation?: HumanContinuation;
  /** Server-decided actions for the current member; the browser never decides. */
  availableActions: HumanTaskAction[];
};

export type HumanTaskResponse = {
  schemaVersion: 'kowa.delivery-governance.v1';
  kind: 'humanTask';
  humanTask: HumanTask;
};

export type HumanTaskListResponse = {
  schemaVersion: 'kowa.delivery-governance.v1';
  kind: 'humanTaskList';
  humanTasks: HumanTask[];
};

export type AnswerHumanTaskCommand = {
  requestKey: string;
  /** From `human.detail`: `request.version`. */
  requestVersion: number;
  /** From `human.detail`: `request.object`. */
  object: HumanObjectBinding;
  decision: HumanDecision;
  values: Record<string, string>;
};
