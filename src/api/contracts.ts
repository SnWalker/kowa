/**
 * Wire types for the frozen contracts the frontend consumes.
 *
 * Owners and pinned digests are recorded in
 * `doc/Kowa前端设计/record/S01-消费准入证据.md` (S01 operations),
 * `doc/Kowa前端设计/record/S02-消费准入证据.md` (S02 operations) and
 * `doc/Kowa前端设计/record/S04-消费准入证据.md` (S04 operations); these types
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

/**
 * The five recovery commands the owner freezes, and nothing more: the frontend
 * never extends the set, the retry semantics or the error vocabulary. `retry`
 * keeps the node's frozen input, `rerun` opens one further round in the current
 * run, `cancel` asks the run to end, `repair` admits a repair run bound to the
 * merged delivery, and `retry-knowledge-suggestion` re-attempts the auxiliary
 * suggestion branch of a closed delivery.
 */
export const RECOVERY_KINDS = ['retry', 'rerun', 'cancel', 'repair', 'retry-knowledge-suggestion'] as const;
export type RecoveryKind = (typeof RECOVERY_KINDS)[number];

export type RecoveryRequest = {
  kind: RecoveryKind;
  nodeId?: string;
  reason?: string;
  /** The version this command is decided against; which version is checked depends on `kind`. */
  expectedVersion: number;
  requestKey: string;
};

/** The server's own answer: `applied` is the fact, `detail` its own wording. */
export type RecoveryResponse = {
  workflowRunId: string;
  kind: RecoveryKind;
  applied: boolean;
  state: RunState;
  version: number;
  detail?: string;
  recordedAt: string;
};

// ---- kowa.fixed-workflow.v1 (fixed.workflow) ------------------------------

/**
 * The frozen definition view the browser draws the DAG from. It describes the
 * *definition* only: `order` is the server's topological sequence and
 * `dependsOn` carries the edges, so the graph is never inferred locally. Node
 * rounds and verdicts are a different fact and come from `RunDetail`.
 */
export const FIXED_WORKFLOW_VALUE_TYPES = ['string', 'boolean', 'integer', 'artifact_ref'] as const;
export type FixedWorkflowValueType = (typeof FIXED_WORKFLOW_VALUE_TYPES)[number];

export type FixedWorkflowInputBinding = {
  key: string;
  producerNodeId: string;
  output: string;
  type: FixedWorkflowValueType;
  required: boolean;
};

export type FixedWorkflowRevisionPolicy = {
  targetNodeId: string;
  maxAutoRevisions: number;
};

export type FixedWorkflowNode = {
  id: string;
  type: NodeType;
  dependsOn: string[];
  inputs: FixedWorkflowInputBinding[];
  capability?: Capability;
  operation?: string;
  humanTask?: string;
  revision?: FixedWorkflowRevisionPolicy;
  mandatory: boolean;
};

export type FixedWorkflowView = {
  schemaVersion: 'kowa.fixed-workflow.v1';
  kind: 'fixedWorkflowView';
  name: string;
  version: number;
  definitionSchemaVersion: 'kowa.workflow-execution.v1';
  definitionDigest: string;
  order: string[];
  nodes: FixedWorkflowNode[];
};

// ---- kowa.web-application.v1 (delivery.artifact-fixrun) --------------------

export const ARTIFACT_PRODUCER_KINDS = ['operation', 'task'] as const;
export type ArtifactProducerKind = (typeof ARTIFACT_PRODUCER_KINDS)[number];

export const ARTIFACT_STATES = ['STAGING', 'UPLOADED', 'PUBLISHED', 'DELETING', 'DELETED'] as const;
export type ArtifactState = (typeof ARTIFACT_STATES)[number];

/** One addressable, versioned artifact row: identity, digest and lifecycle. */
export type ArtifactRecord = {
  artifactId: string;
  subjectId: string;
  kind: string;
  digest: string;
  size: number;
  mediaType: string;
  producerKind: ArtifactProducerKind;
  producerId: string;
  requiresApproval: boolean;
  state: ArtifactState;
  createdAt: string;
};

export type ArtifactListResponse = {
  schemaVersion: 'kowa.web-application.v1';
  kind: 'artifactList';
  workItemId: string;
  artifacts: ArtifactRecord[];
  runBindings: RunBinding[];
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

// ---- kowa.delivery-governance.v1 (delivery.query) --------------------------

/**
 * The read-only delivery evidence projection of one WorkItem. It is the page's
 * only source of GitHub reconciliation facts: the operations the Runner's
 * controlled channels actually performed, the observations the control plane
 * recorded, the verification evidence rows and the closure record.
 *
 * The projection never carries a verdict the browser may act on: there is no
 * merge operation, no approval and no lifecycle authority here. `closure` is
 * present only once the owner committed one in the same transaction as the
 * delivery run's success.
 */
export const EXTERNAL_OPERATION_KINDS = ['verify_change', 'publish_pr'] as const;
export type ExternalOperationKind = (typeof EXTERNAL_OPERATION_KINDS)[number];

export const EXTERNAL_OPERATION_STATES = ['PENDING', 'ATTEMPTED', 'SUCCEEDED', 'FAILED', 'UNKNOWN'] as const;
export type ExternalOperationState = (typeof EXTERNAL_OPERATION_STATES)[number];

/** What the operation asked the Runner's machine identity to do. */
export type ExternalOperationRequest = {
  repositoryId: string;
  role: 'project';
  operation: ExternalOperationKind;
  ref: string;
  baseRef?: string;
  commitOid: string;
  /** The machine account the write had to come from, frozen at admission. */
  expectedGitHubUserId: string;
  /** The Web actor who asked for the run; never the GitHub writer. */
  requestedByGitHubUserId: string;
  titleArtifactId?: string;
};

/** What was reconciled against GitHub; `reconciled` is the owner's own fact. */
export type ExternalOperationResult = {
  repositoryId: string;
  ref: string;
  commitOid: string;
  pullRequestNumber?: string;
  /** The GitHub account that actually created the PR: the machine identity. */
  pullRequestAuthorId?: string;
  baseRef?: string;
  reconciled: boolean;
  observationId: string;
  observedAt: string;
  observedByGitHubUserId?: string;
};

export type ExternalOperation = {
  operationId: string;
  workspaceId: string;
  workItemId: string;
  workflowRunId: string;
  nodeRunId?: string;
  taskId?: string;
  kind: ExternalOperationKind;
  state: ExternalOperationState;
  request: ExternalOperationRequest;
  requestDigest: string;
  result?: ExternalOperationResult;
  unknownReason?: string;
  version: number;
  createdAt: string;
  updatedAt: string;
  settledAt?: string;
};

export const OBSERVATION_SUBJECT_KINDS = [
  'pull_request',
  'pull_request_review',
  'pull_request_checks',
  'branch_head',
] as const;
export type ObservationSubjectKind = (typeof OBSERVATION_SUBJECT_KINDS)[number];

/** GitHub's own check vocabulary; `none` is not a pass. */
export const CHECK_STATES = ['none', 'pending', 'failed', 'passed'] as const;
export type CheckState = (typeof CHECK_STATES)[number];

export type PullRequestFact = {
  number: string;
  authorId: string;
  headOid: string;
  headRef?: string;
  baseRef?: string;
  state: string;
  merged: boolean;
  mergeCommitOid?: string;
  mergedById?: string;
  mergedAt?: string;
  mergedByType?: string;
};

export type ReviewFact = {
  reviewId: string;
  reviewerId: string;
  state: string;
  commitOid?: string;
  submittedAt?: string;
  authorAssociation?: string;
  reviewerLogin?: string;
  reviewerType: string;
  reviewerPermission?: 'admin' | 'write' | 'read' | 'none';
};

export type ChecksFact = {
  commitOid: string;
  state: CheckState;
  total: number;
  failed: number;
  pending: number;
};

/**
 * One recorded GitHub read. `unknown` is a real, frozen answer: a read that did
 * not succeed. It carries no payload, so an unknown row can never be presented
 * as a fact, and the last successful read is reported separately by the
 * observability watermark.
 */
export type GitHubObservation = {
  observationId: string;
  workspaceId: string;
  repositoryId: string;
  subjectKind: ObservationSubjectKind;
  subjectKey: string;
  observedAt: string;
  /**
   * The GitHub account that performed the read. The owner omits it when the
   * read never reached an authenticated identity, so it is not a required
   * fact and "who read this" may legitimately be unknown.
   */
  actorId?: string;
  unknown: boolean;
  unknownReason?: string;
  pullRequest?: PullRequestFact;
  review?: ReviewFact;
  checks?: ChecksFact;
  ref?: { commitOid: string };
};

export const VERIFICATION_KINDS = [
  'required_tests',
  'independent_review',
  'security_review',
  'post_merge_integration',
  'knowledge_suggestion',
  'offline_evaluation',
] as const;
export type VerificationKind = (typeof VERIFICATION_KINDS)[number];

export const VERIFICATION_SUBJECT_KINDS = ['commit', 'artifact'] as const;
export type VerificationSubjectKind = (typeof VERIFICATION_SUBJECT_KINDS)[number];

export const VERIFICATION_PRODUCER_KINDS = ['task', 'external_ci', 'control_plane'] as const;
export type VerificationProducerKind = (typeof VERIFICATION_PRODUCER_KINDS)[number];

export const VERIFICATION_SOURCES = ['real', 'simulated'] as const;
export type VerificationSource = (typeof VERIFICATION_SOURCES)[number];

export const VERIFICATION_RESULTS = ['passed', 'failed'] as const;
export type VerificationResult = (typeof VERIFICATION_RESULTS)[number];

export type VerificationRecord = {
  verificationId: string;
  workspaceId: string;
  workItemId: string;
  workflowRunId?: string;
  nodeRunId?: string;
  kind: VerificationKind;
  subjectKind: VerificationSubjectKind;
  repositoryId?: string;
  commitOid?: string;
  artifactId?: string;
  artifactDigest?: string;
  configurationVersion: string;
  producerKind: VerificationProducerKind;
  producerId: string;
  source: VerificationSource;
  result: VerificationResult;
  reportArtifactId?: string;
  reportDigest?: string;
  observedAt: string;
  createdAt: string;
};

export type ClosureRecord = {
  closureId: string;
  workspaceId: string;
  workItemId: string;
  workflowRunId: string;
  definitionName: string;
  definitionVersion: number;
  definitionDigest: string;
  policyVersion: string;
  requirementDigest: string;
  knowledgeSnapshotId?: string;
  knowledgeBindingEventId?: string;
  mergeRepositoryId: string;
  mergeCommitOid: string;
  pullRequestNumber: string;
  mergeObservationId: string;
  reviewObservationId: string;
  reviewHeadOid: string;
  reviewerGitHubUserId: string;
  requiredTestVerificationIds: string[];
  independentReviewVerificationId: string;
  postMergeVerificationId: string;
  checklistDigest: string;
  createdByGitHubUserId: string;
  createdAt: string;
  mergedByGitHubUserId: string;
  mergedByType: string;
};

export type DeliveryViewResponse = {
  schemaVersion: 'kowa.delivery-governance.v1';
  kind: 'deliveryView';
  workItem: WorkItem;
  runBindings: RunBinding[];
  operations: ExternalOperation[];
  facts: GitHubObservation[];
  verification: VerificationRecord[];
  closure?: ClosureRecord;
};

// ---- kowa.observability.v1 (observability.watermarks) ----------------------

export const FRESHNESS_STATES = ['fresh', 'stale', 'unknown'] as const;
export type FreshnessState = (typeof FRESHNESS_STATES)[number];

export const FRESHNESS_REASONS = ['expired', 'superseded-by-failure', 'never-established'] as const;
export type FreshnessReason = (typeof FRESHNESS_REASONS)[number];

/**
 * The owner's own watermark of one observed GitHub subject. Its success time
 * only moves on a successful read, so a failed read degrades the freshness
 * immediately: `supersededByFailure` says a known fact was displaced by a
 * later failure, `never-established` says there never was one. The browser
 * never invents a staleness threshold — the window is the owner's.
 */
export type ObservationWatermark = {
  subjectKind: string;
  subjectKey: string;
  lastAttemptAt: string;
  lastAttemptKnown: boolean;
  unknownReason?: string;
  freshness: FreshnessState;
  freshnessReason?: FreshnessReason;
  supersededByFailure: boolean;
};

export type ObservabilityDefinitionInfo = {
  name?: string;
  version?: number;
  digest?: string;
};

export type ObservabilityDeployment = {
  commit?: string;
  migrationVersion?: string;
  goVersion?: string;
};

export type ObservabilityRunFacts = {
  workflowRunId: string;
  state: string;
  version: number;
  updatedAt: string;
  nodeRounds: number;
  attempts: number;
  openHumanTasks: number;
};

export type ObservabilitySnapshot = {
  kind: 'contactability' | 'business_progress' | 'external_observation';
  subject: string;
  lastSuccessAt?: string;
  lastSuccessDetail?: string;
  lastAttemptAt?: string;
  lastFailureAt?: string;
  lastFailureReason?: string;
  successes: number;
  failures: number;
  freshness: FreshnessState;
  staleReason?: FreshnessReason;
  contactable: boolean;
  progressing: boolean;
};

export type ObservabilityRuntime = {
  runtimeId: string;
  providerId?: string;
  providerVersion?: string;
  capabilities: string[];
  authenticated: boolean;
  observedAt: string;
  freshness: FreshnessState;
  staleReason?: FreshnessReason;
  contactable: boolean;
};

export type ObservabilityArtifactFacts = {
  namespace?: string;
  published: number;
};

export type WatermarkView = {
  schemaVersion: 'kowa.observability.v1';
  kind: 'observabilityWatermarks';
  observedAt: string;
  workspaceId: string;
  workItemId: string;
  deployment: ObservabilityDeployment;
  definition: ObservabilityDefinitionInfo;
  executionConfigVersion: number;
  run?: ObservabilityRunFacts;
  observations: ObservationWatermark[];
  runtimes: ObservabilityRuntime[];
  progress?: ObservabilitySnapshot;
  artifacts: ObservabilityArtifactFacts;
  freshnessWindowSeconds: number;
};
