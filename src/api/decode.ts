/**
 * Strict, fail-closed decoders for the frozen wire contracts.
 *
 * The official schemas reject unknown fields; a projection must never treat a
 * response whose shape it does not fully understand as usable. Every decoder
 * therefore checks the exact field set, the required fields, the enums and the
 * documented semantic invariants (for example the observation watermark order
 * and the `freshness` ↔ `observation`/`staleReason` linkage) and throws
 * `ContractViolationError` otherwise.
 */
import {
  API_ERROR_CATEGORIES,
  ARTIFACT_PRODUCER_KINDS,
  ARTIFACT_STATES,
  ATTEMPT_KINDS,
  ATTEMPT_STATES,
  CHECK_STATES,
  CONTINUATION_STATES,
  EXTERNAL_OPERATION_KINDS,
  EXTERNAL_OPERATION_STATES,
  FIXED_WORKFLOW_VALUE_TYPES,
  FRESHNESS_REASONS,
  FRESHNESS_STATES,
  HUMAN_COMPLETION_MODES,
  HUMAN_DECISIONS,
  HUMAN_FIELD_TYPES,
  HUMAN_TASK_ACTIONS,
  HUMAN_TASK_STATES,
  KNOWLEDGE_RECOMMENDATION_STATES,
  KNOWLEDGE_SUGGESTION_STATES,
  MACHINE_IDENTITY_STATES,
  NODE_ROUND_STATES,
  NODE_TYPES,
  OBSERVATION_SUBJECT_KINDS,
  PUSH_PERMISSION_STATES,
  REMOTE_READ_STATES,
  RUN_STATES,
  RECOVERY_KINDS,
  RUNTIME_FRESHNESS,
  RUNTIME_STALE_REASONS,
  VERIFICATION_KINDS,
  VERIFICATION_PRODUCER_KINDS,
  VERIFICATION_RESULTS,
  VERIFICATION_SOURCES,
  VERIFICATION_SUBJECT_KINDS,
  WORK_ITEM_STATES,
  WORKSPACE_ROLES,
  type Admission,
  type AdmissionResponse,
  type ApiErrorCategory,
  type ArtifactListResponse,
  type ArtifactRecord,
  type Capability,
  type ChecksFact,
  type ClosureRecord,
  type DeliveryViewResponse,
  type ExecutionConfig,
  type ExecutionConfigResponse,
  type ExternalOperation,
  type ExternalOperationRequest,
  type ExternalOperationResult,
  type FixedWorkflowInputBinding,
  type FixedWorkflowNode,
  type FixedWorkflowRevisionPolicy,
  type FixedWorkflowView,
  type GitHubObservation,
  type GitRemoteMachineIdentity,
  type GitRemoteRepositoryStatus,
  type GitRemoteStatus,
  type GitRemoteStatusResponse,
  type HumanContinuation,
  type HumanField,
  type HumanObjectBinding,
  type HumanResponse,
  type HumanTask,
  type HumanTaskListResponse,
  type HumanTaskRequest,
  type HumanTaskResponse,
  type KnowledgeCandidate,
  type KnowledgeRecommendation,
  type KnowledgeSuggestion,
  type KnowledgeSuggestionAttempt,
  type LoginResponse,
  type NodeAttempt,
  type ObservationWatermark,
  type ObservabilityArtifactFacts,
  type ObservabilityDeployment,
  type ObservabilityDefinitionInfo,
  type ObservabilityRunFacts,
  type ObservabilityRuntime,
  type ObservabilitySnapshot,
  type PullRequestFact,
  type RecoveryResponse,
  type ReviewFact,
  type NodeHistory,
  type RepositoryBinding,
  type RunBinding,
  type RunDetail,
  type RunWatermark,
  type RuntimeListResponse,
  type RuntimeObservation,
  type RuntimeView,
  type RuntimeViewResponse,
  type SessionBootstrapResponse,
  type VerificationRecord,
  type VersionedContent,
  type WatermarkView,
  type WebIdentity,
  type WorkItem,
  type WorkItemListResponse,
  type WorkItemResponse,
  type WorkspaceConfig,
  type WorkspaceMember,
} from './contracts';

export class ContractViolationError extends Error {
  constructor(contract: string, detail: string) {
    super(`${contract}: ${detail}`);
    this.name = 'ContractViolationError';
  }
}

const CSRF_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const GITHUB_ID_PATTERN = /^[1-9][0-9]*$/;
const RUNTIME_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const RUNTIME_WORKSPACE_ID_PATTERN = /^[A-Za-z0-9_-]{8,128}$/;
const CAPABILITY_ID_PATTERN = /^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$/;

function fail(contract: string, detail: string): never {
  throw new ContractViolationError(contract, detail);
}

function asObject(value: unknown, contract: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    fail(contract, 'expected an object');
  }
  return value as Record<string, unknown>;
}

/** Rejects any field outside the frozen shape. */
function asExactObject(value: unknown, contract: string, fields: readonly string[]): Record<string, unknown> {
  const object = asObject(value, contract);
  for (const key of Object.keys(object)) {
    if (!fields.includes(key)) fail(contract, `unexpected field ${key}`);
  }
  return object;
}

function required(object: Record<string, unknown>, key: string, contract: string): unknown {
  const value = object[key];
  if (value === undefined) fail(contract, `missing field ${key}`);
  return value;
}

function optional(object: Record<string, unknown>, key: string): unknown {
  return object[key];
}

function asString(value: unknown, contract: string, what: string, pattern?: RegExp, maxLength?: number): string {
  if (typeof value !== 'string' || value.length === 0) fail(contract, `${what} must be a non-empty string`);
  if (pattern && !pattern.test(value)) fail(contract, `${what} has an invalid format`);
  if (maxLength !== undefined && value.length > maxLength) fail(contract, `${what} is too long`);
  return value;
}

function asTimestamp(value: unknown, contract: string, what: string): string {
  const text = asString(value, contract, what);
  if (Number.isNaN(Date.parse(text))) fail(contract, `${what} is not a date-time`);
  return text;
}

function asBoolean(value: unknown, contract: string, what: string): boolean {
  if (typeof value !== 'boolean') fail(contract, `${what} must be a boolean`);
  return value;
}

function asInteger(value: unknown, contract: string, what: string, minimum: number, maximum?: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < minimum || (maximum !== undefined && value > maximum)) {
    fail(contract, `${what} must be an integer >= ${minimum}`);
  }
  return value;
}

function asEnum<T extends string>(value: unknown, contract: string, what: string, allowed: readonly T[]): T {
  if (typeof value !== 'string' || !allowed.includes(value as T)) {
    fail(contract, `${what} is not one of ${allowed.join(', ')}`);
  }
  return value as T;
}

function asArray<T>(value: unknown, contract: string, what: string, item: (entry: unknown, index: number) => T): T[] {
  if (!Array.isArray(value)) fail(contract, `${what} must be an array`);
  return value.map((entry, index) => item(entry, index));
}

function asOptional<T>(value: unknown, decode: (value: unknown) => T): T | undefined {
  return value === undefined ? undefined : decode(value);
}

// ---- shared ---------------------------------------------------------------

const WEB_IDENTITY_FIELDS = ['githubUserId', 'login'] as const;

export function decodeWebIdentity(value: unknown, contract = 'kowa.identity-workspace.v1'): WebIdentity {
  const object = asExactObject(value, contract, WEB_IDENTITY_FIELDS);
  return {
    githubUserId: asString(required(object, 'githubUserId', contract), contract, 'githubUserId', GITHUB_ID_PATTERN),
    login: asString(required(object, 'login', contract), contract, 'login'),
  };
}

/** Only the frozen error envelope is accepted; HTTP status alone is not authority. */
export function decodeApiErrorCategory(value: unknown, contract = 'kowa.api-error.v1'): ApiErrorCategory {
  const object = asExactObject(value, contract, ['error']);
  return asEnum(required(object, 'error', contract), contract, 'error', API_ERROR_CATEGORIES);
}

// ---- kowa.web-session.v1 --------------------------------------------------

const SESSION_CONTRACT = 'kowa.web-session.v1';

/**
 * The login response is a browser-navigation target. Defence in depth: only an
 * absolute http(s) URL is a navigation, so a compromised or misconfigured
 * producer can never hand the browser a script-bearing scheme
 * (`javascript:`, `data:`, `file:`, `blob:`). The approved local OAuth stub is
 * plain HTTP and stays valid.
 */
function asNavigationTarget(value: unknown, contract: string, what: string): string {
  const text = asString(value, contract, what);
  let parsed: URL;
  try {
    parsed = new URL(text);
  } catch {
    fail(contract, `${what} must be an absolute URL`);
  }
  if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
    fail(contract, `${what} must be an http(s) navigation target, got ${parsed.protocol}`);
  }
  return text;
}

export function decodeLoginResponse(value: unknown): LoginResponse {
  const object = asExactObject(value, SESSION_CONTRACT, ['authorizationUrl']);
  return {
    authorizationUrl: asNavigationTarget(
      required(object, 'authorizationUrl', SESSION_CONTRACT),
      SESSION_CONTRACT,
      'authorizationUrl',
    ),
  };
}

export function decodeSessionBootstrapResponse(value: unknown): SessionBootstrapResponse {
  const fields = ['identity', 'csrfToken', 'sessionId', 'expiresAt', 'requestId'] as const;
  const object = asExactObject(value, SESSION_CONTRACT, fields);
  return {
    identity: decodeWebIdentity(required(object, 'identity', SESSION_CONTRACT), SESSION_CONTRACT),
    csrfToken: asString(required(object, 'csrfToken', SESSION_CONTRACT), SESSION_CONTRACT, 'csrfToken', CSRF_PATTERN),
    sessionId: asString(required(object, 'sessionId', SESSION_CONTRACT), SESSION_CONTRACT, 'sessionId', CSRF_PATTERN),
    expiresAt: asTimestamp(required(object, 'expiresAt', SESSION_CONTRACT), SESSION_CONTRACT, 'expiresAt'),
    requestId: asString(required(object, 'requestId', SESSION_CONTRACT), SESSION_CONTRACT, 'requestId', undefined, 128),
  };
}

// ---- kowa.identity-workspace.v1 -------------------------------------------

const IDENTITY_CONTRACT = 'kowa.identity-workspace.v1';

function decodeWorkspaceMember(value: unknown): WorkspaceMember {
  const object = asExactObject(value, IDENTITY_CONTRACT, ['githubUserId', 'role', 'revokedAt']);
  return {
    githubUserId: asString(required(object, 'githubUserId', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'member.githubUserId', GITHUB_ID_PATTERN),
    role: asEnum(required(object, 'role', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'member.role', WORKSPACE_ROLES),
    revokedAt: asOptional(optional(object, 'revokedAt'), (entry) => asTimestamp(entry, IDENTITY_CONTRACT, 'member.revokedAt')),
  };
}

function decodeRepositoryBinding(value: unknown): RepositoryBinding {
  const fields = ['githubRepositoryId', 'installationId', 'displayName', 'role', 'access'] as const;
  const object = asExactObject(value, IDENTITY_CONTRACT, fields);
  const role = asEnum(required(object, 'role', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'repository.role', ['project', 'knowledge'] as const);
  const access = asEnum(required(object, 'access', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'repository.access', ['read', 'read_write'] as const);
  // A second writable repository is outside MVP: project is always read_write
  // and knowledge is always read (acceptance A02).
  const expected = role === 'project' ? 'read_write' : 'read';
  if (access !== expected) fail(IDENTITY_CONTRACT, `repository role ${role} must have access ${expected}`);
  return {
    githubRepositoryId: asString(required(object, 'githubRepositoryId', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'repository.githubRepositoryId', GITHUB_ID_PATTERN),
    installationId: asString(required(object, 'installationId', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'repository.installationId', GITHUB_ID_PATTERN),
    displayName: asString(required(object, 'displayName', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'repository.displayName'),
    role,
    access,
  };
}

export function decodeWorkspaceConfig(value: unknown): WorkspaceConfig {
  const fields = ['workspaceId', 'name', 'version', 'members', 'project', 'knowledge'] as const;
  const object = asExactObject(value, IDENTITY_CONTRACT, fields);
  const members = asArray(required(object, 'members', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'members', decodeWorkspaceMember);
  if (members.length === 0) fail(IDENTITY_CONTRACT, 'members must not be empty');
  return {
    workspaceId: asString(required(object, 'workspaceId', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'workspaceId'),
    name: asString(required(object, 'name', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'name'),
    version: asInteger(required(object, 'version', IDENTITY_CONTRACT), IDENTITY_CONTRACT, 'version', 1),
    members,
    project: asOptional(optional(object, 'project'), decodeRepositoryBinding),
    knowledge: asOptional(optional(object, 'knowledge'), decodeRepositoryBinding),
  };
}

// ---- kowa.runtime-web.v1 --------------------------------------------------

const RUNTIME_CONTRACT = 'kowa.runtime-web.v1';

function decodeCapability(value: unknown): Capability {
  const object = asExactObject(value, RUNTIME_CONTRACT, ['id', 'version']);
  return {
    id: asString(required(object, 'id', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'capability.id', CAPABILITY_ID_PATTERN),
    version: asInteger(required(object, 'version', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'capability.version', 1, 65535),
  };
}

function decodeObservation(value: unknown): RuntimeObservation {
  const fields = [
    'runtimeId', 'runtimeEpoch', 'observedAt', 'receivedAt', 'executable', 'cliVersion',
    'versionCompatible', 'authenticated', 'canExecute', 'detail',
  ] as const;
  const object = asExactObject(value, RUNTIME_CONTRACT, fields);
  const observedAt = asTimestamp(required(object, 'observedAt', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.observedAt');
  const receivedAt = asTimestamp(required(object, 'receivedAt', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.receivedAt');
  // The server watermark cannot precede the advisory Runner clock (the frozen
  // negative-semantic watermark-order case).
  if (Date.parse(receivedAt) < Date.parse(observedAt)) {
    fail(RUNTIME_CONTRACT, 'observation.receivedAt must not precede observation.observedAt');
  }
  return {
    runtimeId: asString(required(object, 'runtimeId', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.runtimeId', RUNTIME_ID_PATTERN),
    runtimeEpoch: asString(required(object, 'runtimeEpoch', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.runtimeEpoch', RUNTIME_ID_PATTERN),
    observedAt,
    receivedAt,
    executable: asBoolean(required(object, 'executable', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.executable'),
    cliVersion: asString(required(object, 'cliVersion', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.cliVersion', undefined, 256),
    versionCompatible: asBoolean(required(object, 'versionCompatible', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.versionCompatible'),
    authenticated: asBoolean(required(object, 'authenticated', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.authenticated'),
    canExecute: asBoolean(required(object, 'canExecute', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'observation.canExecute'),
    detail: asOptional(optional(object, 'detail'), (entry) => asString(entry, RUNTIME_CONTRACT, 'observation.detail', undefined, 4096)),
  };
}

function decodeRuntimeView(value: unknown): RuntimeView {
  const fields = [
    'runtimeId', 'gitHubUserId', 'providerId', 'providerVersion', 'providerAuthenticated',
    'capabilities', 'registeredAt', 'freshness', 'staleReason', 'observation',
  ] as const;
  const object = asExactObject(value, RUNTIME_CONTRACT, fields);
  const freshness = asEnum(required(object, 'freshness', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.freshness', RUNTIME_FRESHNESS);
  const staleReason = asOptional(optional(object, 'staleReason'), (entry) =>
    asEnum(entry, RUNTIME_CONTRACT, 'runtime.staleReason', RUNTIME_STALE_REASONS));
  const observation = asOptional(optional(object, 'observation'), decodeObservation);
  // The three freshness states are separate machine facts and never collapse
  // into one "healthy" answer.
  if (freshness === 'fresh' && (observation === undefined || staleReason !== undefined)) {
    fail(RUNTIME_CONTRACT, 'fresh runtime requires an observation and forbids staleReason');
  }
  if (freshness === 'stale' && staleReason === undefined) {
    fail(RUNTIME_CONTRACT, 'stale runtime requires staleReason');
  }
  if (freshness === 'unknown' && observation !== undefined) {
    fail(RUNTIME_CONTRACT, 'unknown runtime must not carry an observation');
  }
  return {
    runtimeId: asString(required(object, 'runtimeId', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.runtimeId', RUNTIME_ID_PATTERN),
    gitHubUserId: asString(required(object, 'gitHubUserId', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.gitHubUserId', GITHUB_ID_PATTERN),
    providerId: asString(required(object, 'providerId', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.providerId', undefined, 128),
    providerVersion: asString(required(object, 'providerVersion', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.providerVersion', undefined, 128),
    providerAuthenticated: asBoolean(required(object, 'providerAuthenticated', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.providerAuthenticated'),
    capabilities: asArray(required(object, 'capabilities', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.capabilities', decodeCapability),
    registeredAt: asTimestamp(required(object, 'registeredAt', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtime.registeredAt'),
    freshness,
    staleReason,
    observation,
  };
}

export function decodeRuntimeListResponse(value: unknown): RuntimeListResponse {
  const object = asExactObject(value, RUNTIME_CONTRACT, ['schemaVersion', 'kind', 'runtimes']);
  if (required(object, 'schemaVersion', RUNTIME_CONTRACT) !== 'kowa.runtime-web.v1') fail(RUNTIME_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', RUNTIME_CONTRACT) !== 'runtimeList') fail(RUNTIME_CONTRACT, 'unexpected kind');
  const runtimes = asArray(required(object, 'runtimes', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'runtimes', decodeRuntimeView);
  if (runtimes.length > 64) fail(RUNTIME_CONTRACT, 'too many runtimes');
  return { schemaVersion: 'kowa.runtime-web.v1', kind: 'runtimeList', runtimes };
}

export function decodeRuntimeViewResponse(value: unknown): RuntimeViewResponse {
  const object = asExactObject(value, RUNTIME_CONTRACT, ['schemaVersion', 'kind', 'runtime']);
  if (required(object, 'schemaVersion', RUNTIME_CONTRACT) !== 'kowa.runtime-web.v1') fail(RUNTIME_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', RUNTIME_CONTRACT) !== 'runtimeView') fail(RUNTIME_CONTRACT, 'unexpected kind');
  return { schemaVersion: 'kowa.runtime-web.v1', kind: 'runtimeView', runtime: decodeRuntimeView(required(object, 'runtime', RUNTIME_CONTRACT)) };
}

function decodeExecutionConfig(value: unknown): ExecutionConfig {
  const fields = ['workspaceId', 'version', 'capabilities', 'runtimes', 'updatedByGitHubUserId', 'updatedAt'] as const;
  const object = asExactObject(value, RUNTIME_CONTRACT, fields);
  return {
    workspaceId: asString(required(object, 'workspaceId', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'config.workspaceId', RUNTIME_WORKSPACE_ID_PATTERN),
    version: asInteger(required(object, 'version', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'config.version', 0),
    capabilities: asArray(required(object, 'capabilities', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'config.capabilities', decodeCapability),
    runtimes: asArray(required(object, 'runtimes', RUNTIME_CONTRACT), RUNTIME_CONTRACT, 'config.runtimes', (entry) =>
      asString(entry, RUNTIME_CONTRACT, 'config.runtimes entry', RUNTIME_ID_PATTERN)),
    updatedByGitHubUserId: asOptional(optional(object, 'updatedByGitHubUserId'), (entry) =>
      asString(entry, RUNTIME_CONTRACT, 'config.updatedByGitHubUserId', GITHUB_ID_PATTERN)),
    updatedAt: asOptional(optional(object, 'updatedAt'), (entry) => asTimestamp(entry, RUNTIME_CONTRACT, 'config.updatedAt')),
  };
}

export function decodeExecutionConfigResponse(value: unknown): ExecutionConfigResponse {
  const object = asExactObject(value, RUNTIME_CONTRACT, ['schemaVersion', 'kind', 'config']);
  if (required(object, 'schemaVersion', RUNTIME_CONTRACT) !== 'kowa.runtime-web.v1') fail(RUNTIME_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', RUNTIME_CONTRACT) !== 'executionConfig') fail(RUNTIME_CONTRACT, 'unexpected kind');
  return { schemaVersion: 'kowa.runtime-web.v1', kind: 'executionConfig', config: decodeExecutionConfig(required(object, 'config', RUNTIME_CONTRACT)) };
}

// ---- kowa.delivery-governance.v1 ------------------------------------------

const GOVERNANCE_CONTRACT = 'kowa.delivery-governance.v1';

function decodeGitRemoteMachineIdentity(value: unknown): GitRemoteMachineIdentity {
  const fields = ['runtimeIds', 'registeredGitHubUserIds', 'observedGitHubUserId', 'state', 'reason'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    runtimeIds: asArray(required(object, 'runtimeIds', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'machineIdentity.runtimeIds', (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'machineIdentity.runtimeIds entry', undefined, 200)),
    registeredGitHubUserIds: asArray(required(object, 'registeredGitHubUserIds', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'machineIdentity.registeredGitHubUserIds', (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'machineIdentity.registeredGitHubUserIds entry', GITHUB_ID_PATTERN)),
    observedGitHubUserId: asOptional(optional(object, 'observedGitHubUserId'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'machineIdentity.observedGitHubUserId', GITHUB_ID_PATTERN)),
    state: asEnum(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'machineIdentity.state', MACHINE_IDENTITY_STATES),
    reason: asOptional(optional(object, 'reason'), (entry) => asString(entry, GOVERNANCE_CONTRACT, 'machineIdentity.reason', undefined, 2000)),
  };
}

function decodeGitRemoteRepositoryStatus(value: unknown): GitRemoteRepositoryStatus {
  const fields = ['repositoryId', 'role', 'remoteRead', 'pushPermission', 'writeObservation', 'observedAt', 'observedByGitHubUserId'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  const remoteReadObject = asExactObject(required(object, 'remoteRead', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, ['state', 'transport', 'reason']);
  const pushPermissionObject = asExactObject(required(object, 'pushPermission', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, ['state', 'reason']);
  const writeObservation = required(object, 'writeObservation', GOVERNANCE_CONTRACT);
  // This stage records no real write observation; a claimed write is refused.
  if (writeObservation !== 'none') fail(GOVERNANCE_CONTRACT, 'writeObservation must be none');
  return {
    repositoryId: asString(required(object, 'repositoryId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'repository.repositoryId', GITHUB_ID_PATTERN),
    role: asEnum(required(object, 'role', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'repository.role', ['project', 'knowledge'] as const),
    remoteRead: {
      state: asEnum(required(remoteReadObject, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'remoteRead.state', REMOTE_READ_STATES),
      transport: asOptional(optional(remoteReadObject, 'transport'), (entry) =>
        asEnum(entry, GOVERNANCE_CONTRACT, 'remoteRead.transport', ['https', 'ssh'] as const)),
      reason: asOptional(optional(remoteReadObject, 'reason'), (entry) => asString(entry, GOVERNANCE_CONTRACT, 'remoteRead.reason', undefined, 2000)),
    },
    pushPermission: {
      state: asEnum(required(pushPermissionObject, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'pushPermission.state', PUSH_PERMISSION_STATES),
      reason: asOptional(optional(pushPermissionObject, 'reason'), (entry) => asString(entry, GOVERNANCE_CONTRACT, 'pushPermission.reason', undefined, 2000)),
    },
    writeObservation: 'none',
    observedAt: asTimestamp(required(object, 'observedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'repository.observedAt'),
    observedByGitHubUserId: asOptional(optional(object, 'observedByGitHubUserId'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'repository.observedByGitHubUserId', GITHUB_ID_PATTERN)),
  };
}

function decodeGitRemoteStatus(value: unknown): GitRemoteStatus {
  const fields = [
    'schemaVersion', 'kind', 'workspaceId', 'requestedByGitHubUserId', 'probeOutcome',
    'probeReason', 'observedAt', 'machineIdentity', 'repositories',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  if (required(object, 'schemaVersion', GOVERNANCE_CONTRACT) !== 'kowa.delivery-governance.v1') fail(GOVERNANCE_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', GOVERNANCE_CONTRACT) !== 'gitRemoteStatus') fail(GOVERNANCE_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.delivery-governance.v1',
    kind: 'gitRemoteStatus',
    workspaceId: asString(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'workspaceId', undefined, 200),
    requestedByGitHubUserId: asString(required(object, 'requestedByGitHubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'requestedByGitHubUserId', GITHUB_ID_PATTERN),
    probeOutcome: asEnum(required(object, 'probeOutcome', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'probeOutcome', ['completed', 'unknown'] as const),
    probeReason: asOptional(optional(object, 'probeReason'), (entry) => asString(entry, GOVERNANCE_CONTRACT, 'probeReason', undefined, 2000)),
    observedAt: asTimestamp(required(object, 'observedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observedAt'),
    machineIdentity: decodeGitRemoteMachineIdentity(required(object, 'machineIdentity', GOVERNANCE_CONTRACT)),
    repositories: asArray(required(object, 'repositories', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'repositories', decodeGitRemoteRepositoryStatus),
  };
}

export function decodeGitRemoteStatusResponse(value: unknown): GitRemoteStatusResponse {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, ['schemaVersion', 'kind', 'status']);
  if (required(object, 'schemaVersion', GOVERNANCE_CONTRACT) !== 'kowa.delivery-governance.v1') fail(GOVERNANCE_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', GOVERNANCE_CONTRACT) !== 'gitRemoteStatus') fail(GOVERNANCE_CONTRACT, 'unexpected kind');
  return { schemaVersion: 'kowa.delivery-governance.v1', kind: 'gitRemoteStatus', status: decodeGitRemoteStatus(required(object, 'status', GOVERNANCE_CONTRACT)) };
}

// ---- kowa.workitem.v1 -----------------------------------------------------

const WORK_ITEM_CONTRACT = 'kowa.workitem.v1';

const DIGEST_PATTERN = /^sha256:[0-9a-f]{64}$/;
const OID_PATTERN = /^[0-9a-f]{40}$/;
const LOWER_SNAKE_PATTERN = /^[a-z][a-z0-9_]*$/;
const BRANCH_REF_PATTERN = /^refs\/heads\/[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$/;
const CONTENT_MAX_LENGTH = 20000;

function asDigest(value: unknown, contract: string, what: string): string {
  return asString(value, contract, what, DIGEST_PATTERN);
}

function asOid(value: unknown, contract: string, what: string): string {
  return asString(value, contract, what, OID_PATTERN);
}

/** ID fields carry opaque Kowa identities; the contracts bound them at 200 chars. */
function asIdentifier(value: unknown, contract: string, what: string): string {
  return asString(value, contract, what, undefined, 200);
}

/** The decimal GitHub/repository-id form the workitem family freezes. */
function asDecimalId(value: unknown, contract: string, what: string): string {
  return asString(value, contract, what, GITHUB_ID_PATTERN);
}

function decodeVersionedContent(value: unknown): VersionedContent {
  const fields = ['version', 'digest', 'content'] as const;
  const object = asExactObject(value, WORK_ITEM_CONTRACT, fields);
  return {
    version: asInteger(required(object, 'version', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'content.version', 1),
    digest: asDigest(required(object, 'digest', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'content.digest'),
    content: asString(required(object, 'content', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'content.content', undefined, CONTENT_MAX_LENGTH),
  };
}

function decodeWorkItem(value: unknown): WorkItem {
  const fields = [
    'workItemId', 'workspaceId', 'title', 'targetRepositoryId', 'requirement', 'acceptance',
    'state', 'version', 'currentRunId', 'createdByGitHubUserId', 'createdAt', 'updatedAt',
    'closedAt', 'canceledAt',
  ] as const;
  const object = asExactObject(value, WORK_ITEM_CONTRACT, fields);
  return {
    workItemId: asIdentifier(required(object, 'workItemId', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'workItemId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'workspaceId'),
    title: asString(required(object, 'title', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'title', undefined, 300),
    targetRepositoryId: asDecimalId(required(object, 'targetRepositoryId', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'targetRepositoryId'),
    requirement: decodeVersionedContent(required(object, 'requirement', WORK_ITEM_CONTRACT)),
    acceptance: decodeVersionedContent(required(object, 'acceptance', WORK_ITEM_CONTRACT)),
    state: asEnum(required(object, 'state', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'state', WORK_ITEM_STATES),
    version: asInteger(required(object, 'version', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'version', 1),
    currentRunId: asOptional(optional(object, 'currentRunId'), (entry) => asIdentifier(entry, WORK_ITEM_CONTRACT, 'currentRunId')),
    createdByGitHubUserId: asDecimalId(required(object, 'createdByGitHubUserId', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'createdByGitHubUserId'),
    createdAt: asTimestamp(required(object, 'createdAt', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'createdAt'),
    updatedAt: asTimestamp(required(object, 'updatedAt', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'updatedAt'),
    closedAt: asOptional(optional(object, 'closedAt'), (entry) => asTimestamp(entry, WORK_ITEM_CONTRACT, 'closedAt')),
    canceledAt: asOptional(optional(object, 'canceledAt'), (entry) => asTimestamp(entry, WORK_ITEM_CONTRACT, 'canceledAt')),
  };
}

export function decodeWorkItemResponse(value: unknown): WorkItemResponse {
  const object = asExactObject(value, WORK_ITEM_CONTRACT, ['schemaVersion', 'kind', 'workItem']);
  if (required(object, 'schemaVersion', WORK_ITEM_CONTRACT) !== 'kowa.workitem.v1') fail(WORK_ITEM_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', WORK_ITEM_CONTRACT) !== 'workItem') fail(WORK_ITEM_CONTRACT, 'unexpected kind');
  return { schemaVersion: 'kowa.workitem.v1', kind: 'workItem', workItem: decodeWorkItem(required(object, 'workItem', WORK_ITEM_CONTRACT)) };
}

export function decodeWorkItemListResponse(value: unknown): WorkItemListResponse {
  const object = asExactObject(value, WORK_ITEM_CONTRACT, ['schemaVersion', 'kind', 'workItems']);
  if (required(object, 'schemaVersion', WORK_ITEM_CONTRACT) !== 'kowa.workitem.v1') fail(WORK_ITEM_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', WORK_ITEM_CONTRACT) !== 'workItemList') fail(WORK_ITEM_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.workitem.v1',
    kind: 'workItemList',
    workItems: asArray(required(object, 'workItems', WORK_ITEM_CONTRACT), WORK_ITEM_CONTRACT, 'workItems', decodeWorkItem),
  };
}

// ---- kowa.web-application.v1 ----------------------------------------------

export const WEB_APPLICATION_CONTRACT = 'kowa.web-application.v1';

/**
 * The web-application family freezes a 20-digit decimal id, slightly wider
 * than the workitem family; both stay decimal strings and neither accepts a
 * prefixed or opaque form.
 */
const WEB_DECIMAL_ID_PATTERN = /^[1-9][0-9]{0,19}$/;

function decodeAdmission(value: unknown): Admission {
  const fields = [
    'workflowRunId', 'workspaceId', 'workItemId', 'definitionName', 'definitionVersion', 'definitionDigest',
    'projectRepositoryId', 'knowledgeRepositoryId', 'projectCommitOid', 'knowledgeCommitOid',
    'projectBranchRef', 'workingBranchRef', 'providerId', 'providerVersion', 'policyVersion',
    'admittedByGitHubUserId', 'admittedAt',
  ] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    workflowRunId: asIdentifier(required(object, 'workflowRunId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.workflowRunId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.workspaceId'),
    workItemId: asIdentifier(required(object, 'workItemId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.workItemId'),
    definitionName: asIdentifier(required(object, 'definitionName', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.definitionName'),
    definitionVersion: asInteger(required(object, 'definitionVersion', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.definitionVersion', 1),
    definitionDigest: asDigest(required(object, 'definitionDigest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.definitionDigest'),
    projectRepositoryId: asString(required(object, 'projectRepositoryId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.projectRepositoryId', WEB_DECIMAL_ID_PATTERN),
    knowledgeRepositoryId: asString(required(object, 'knowledgeRepositoryId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.knowledgeRepositoryId', WEB_DECIMAL_ID_PATTERN),
    // The frozen baseline is a commit oid, never a branch: a branch name here is
    // the frozen negative case and must be refused before any display.
    projectCommitOid: asOid(required(object, 'projectCommitOid', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.projectCommitOid'),
    knowledgeCommitOid: asOid(required(object, 'knowledgeCommitOid', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.knowledgeCommitOid'),
    projectBranchRef: asString(required(object, 'projectBranchRef', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.projectBranchRef', BRANCH_REF_PATTERN),
    workingBranchRef: asString(required(object, 'workingBranchRef', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.workingBranchRef', BRANCH_REF_PATTERN),
    providerId: asString(required(object, 'providerId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.providerId', undefined, 128),
    providerVersion: asString(required(object, 'providerVersion', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.providerVersion', undefined, 128),
    policyVersion: asString(required(object, 'policyVersion', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.policyVersion', undefined, 128),
    admittedByGitHubUserId: asString(required(object, 'admittedByGitHubUserId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.admittedByGitHubUserId', WEB_DECIMAL_ID_PATTERN),
    admittedAt: asTimestamp(required(object, 'admittedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'admission.admittedAt'),
  };
}

function decodeKnowledgeCandidate(value: unknown): KnowledgeCandidate {
  const fields = ['path', 'digest', 'scope', 'required'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    path: asString(required(object, 'path', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'candidate.path', undefined, 1000),
    digest: asDigest(required(object, 'digest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'candidate.digest'),
    scope: asString(required(object, 'scope', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'candidate.scope', undefined, 200),
    required: asBoolean(required(object, 'required', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'candidate.required'),
  };
}

function decodeKnowledgeRecommendation(value: unknown): KnowledgeRecommendation {
  const fields = [
    'workflowRunId', 'workspaceId', 'workItemId', 'knowledgeRepositoryId', 'knowledgeCommitOid',
    'indexPath', 'indexDigest', 'state', 'humanTaskId', 'candidates', 'snapshotId', 'bindingEventId',
    'effectiveInputId', 'recommendedAt', 'confirmedAt',
  ] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  const candidates = asArray(required(object, 'candidates', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'candidates', decodeKnowledgeCandidate);
  const state = asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.state', KNOWLEDGE_RECOMMENDATION_STATES);
  const confirmedAt = asOptional(optional(object, 'confirmedAt'), (entry) =>
    asTimestamp(entry, WEB_APPLICATION_CONTRACT, 'recommendation.confirmedAt'));
  // A successful read of an empty index is a real recommendation: an empty
  // candidate list stays valid and still needs its explicit confirmation.
  if (state === 'CONFIRMED' && confirmedAt === undefined) {
    fail(WEB_APPLICATION_CONTRACT, 'a confirmed recommendation carries confirmedAt');
  }
  return {
    workflowRunId: asIdentifier(required(object, 'workflowRunId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.workflowRunId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.workspaceId'),
    workItemId: asIdentifier(required(object, 'workItemId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.workItemId'),
    knowledgeRepositoryId: asString(required(object, 'knowledgeRepositoryId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.knowledgeRepositoryId', WEB_DECIMAL_ID_PATTERN),
    knowledgeCommitOid: asOid(required(object, 'knowledgeCommitOid', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.knowledgeCommitOid'),
    indexPath: asString(required(object, 'indexPath', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.indexPath', undefined, 200),
    indexDigest: asDigest(required(object, 'indexDigest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.indexDigest'),
    state,
    humanTaskId: asOptional(optional(object, 'humanTaskId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'recommendation.humanTaskId')),
    candidates,
    snapshotId: asOptional(optional(object, 'snapshotId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'recommendation.snapshotId')),
    bindingEventId: asOptional(optional(object, 'bindingEventId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'recommendation.bindingEventId')),
    effectiveInputId: asOptional(optional(object, 'effectiveInputId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'recommendation.effectiveInputId')),
    recommendedAt: asTimestamp(required(object, 'recommendedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recommendation.recommendedAt'),
    confirmedAt,
  };
}

export function decodeAdmissionResponse(value: unknown): AdmissionResponse {
  const fields = ['admission', 'workItemVersion', 'knowledgeRecommendation', 'replayed'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    admission: decodeAdmission(required(object, 'admission', WEB_APPLICATION_CONTRACT)),
    workItemVersion: asInteger(required(object, 'workItemVersion', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'workItemVersion', 1),
    knowledgeRecommendation: asOptional(optional(object, 'knowledgeRecommendation'), decodeKnowledgeRecommendation),
    replayed: asBoolean(required(object, 'replayed', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'replayed'),
  };
}

function decodeRunBinding(value: unknown): RunBinding {
  const fields = ['workItemId', 'workflowRunId', 'kind', 'sourceRunId', 'reason', 'mergedBaselineOid', 'boundByGitHubUserId', 'boundAt'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    workItemId: asIdentifier(required(object, 'workItemId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'binding.workItemId'),
    workflowRunId: asIdentifier(required(object, 'workflowRunId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'binding.workflowRunId'),
    kind: asEnum(required(object, 'kind', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'binding.kind', ['initial', 'repair'] as const),
    sourceRunId: asOptional(optional(object, 'sourceRunId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'binding.sourceRunId')),
    reason: asOptional(optional(object, 'reason'), (entry) => asString(entry, WEB_APPLICATION_CONTRACT, 'binding.reason', undefined, 2000)),
    mergedBaselineOid: asOptional(optional(object, 'mergedBaselineOid'), (entry) => asOid(entry, WEB_APPLICATION_CONTRACT, 'binding.mergedBaselineOid')),
    boundByGitHubUserId: asString(required(object, 'boundByGitHubUserId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'binding.boundByGitHubUserId', WEB_DECIMAL_ID_PATTERN),
    boundAt: asTimestamp(required(object, 'boundAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'binding.boundAt'),
  };
}

function decodeNodeAttempt(value: unknown): NodeAttempt {
  const fields = [
    'taskId', 'attempt', 'attemptKind', 'state', 'inputDigest', 'resultDigest',
    'runtimeId', 'runtimeEpoch', 'fencingToken', 'leaseId', 'artifactIds',
  ] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    taskId: asIdentifier(required(object, 'taskId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'attempt.taskId'),
    attempt: asInteger(required(object, 'attempt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'attempt.attempt', 1),
    attemptKind: asEnum(required(object, 'attemptKind', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'attempt.attemptKind', ATTEMPT_KINDS),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'attempt.state', ATTEMPT_STATES),
    inputDigest: asDigest(required(object, 'inputDigest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'attempt.inputDigest'),
    resultDigest: asOptional(optional(object, 'resultDigest'), (entry) => asDigest(entry, WEB_APPLICATION_CONTRACT, 'attempt.resultDigest')),
    runtimeId: asOptional(optional(object, 'runtimeId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'attempt.runtimeId')),
    runtimeEpoch: asOptional(optional(object, 'runtimeEpoch'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'attempt.runtimeEpoch')),
    fencingToken: asOptional(optional(object, 'fencingToken'), (entry) => asInteger(entry, WEB_APPLICATION_CONTRACT, 'attempt.fencingToken', 0)),
    leaseId: asOptional(optional(object, 'leaseId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'attempt.leaseId')),
    artifactIds: asArray(required(object, 'artifactIds', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'attempt.artifactIds', (entry) =>
      asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'attempt.artifactIds entry')),
  };
}

function decodeNodeHistory(value: unknown): NodeHistory {
  const fields = ['nodeId', 'type', 'iteration', 'state', 'verdict', 'inputDigest', 'output', 'attempts'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  const output = optional(object, 'output');
  if (output !== undefined) asObject(output, WEB_APPLICATION_CONTRACT);
  return {
    nodeId: asIdentifier(required(object, 'nodeId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'node.nodeId'),
    type: asEnum(required(object, 'type', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'node.type', NODE_TYPES),
    iteration: asInteger(required(object, 'iteration', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'node.iteration', 1),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'node.state', NODE_ROUND_STATES),
    verdict: asOptional(optional(object, 'verdict'), (entry) => asString(entry, WEB_APPLICATION_CONTRACT, 'node.verdict', undefined, 200)),
    inputDigest: asDigest(required(object, 'inputDigest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'node.inputDigest'),
    output: output as Record<string, unknown> | undefined,
    attempts: asArray(required(object, 'attempts', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'node.attempts', decodeNodeAttempt),
  };
}

function decodeKnowledgeSuggestionAttempt(value: unknown): KnowledgeSuggestionAttempt {
  const fields = ['attemptId', 'attempt', 'state', 'inputDigest', 'artifactId', 'artifactDigest', 'failureReason', 'requestedAt', 'updatedAt'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    attemptId: asIdentifier(required(object, 'attemptId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.attemptId'),
    attempt: asInteger(required(object, 'attempt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.attempt', 1),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.state', KNOWLEDGE_SUGGESTION_STATES),
    inputDigest: asDigest(required(object, 'inputDigest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.inputDigest'),
    artifactId: asOptional(optional(object, 'artifactId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'suggestion.artifactId')),
    artifactDigest: asOptional(optional(object, 'artifactDigest'), (entry) => asDigest(entry, WEB_APPLICATION_CONTRACT, 'suggestion.artifactDigest')),
    failureReason: asOptional(optional(object, 'failureReason'), (entry) => asString(entry, WEB_APPLICATION_CONTRACT, 'suggestion.failureReason', undefined, 500)),
    requestedAt: asTimestamp(required(object, 'requestedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.requestedAt'),
    updatedAt: asTimestamp(required(object, 'updatedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.updatedAt'),
  };
}

function decodeKnowledgeSuggestion(value: unknown): KnowledgeSuggestion {
  const fields = ['workflowRunId', 'state', 'attempts', 'artifactId', 'failureReason', 'updatedAt', 'history'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    workflowRunId: asIdentifier(required(object, 'workflowRunId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.workflowRunId'),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.state', KNOWLEDGE_SUGGESTION_STATES),
    attempts: asInteger(required(object, 'attempts', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.attempts', 1),
    artifactId: asOptional(optional(object, 'artifactId'), (entry) => asIdentifier(entry, WEB_APPLICATION_CONTRACT, 'suggestion.artifactId')),
    failureReason: asOptional(optional(object, 'failureReason'), (entry) => asString(entry, WEB_APPLICATION_CONTRACT, 'suggestion.failureReason', undefined, 500)),
    updatedAt: asTimestamp(required(object, 'updatedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.updatedAt'),
    history: asArray(required(object, 'history', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'suggestion.history', decodeKnowledgeSuggestionAttempt),
  };
}

function decodeRunWatermark(value: unknown): RunWatermark {
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, ['observedAt', 'runUpdatedAt']);
  return {
    observedAt: asTimestamp(required(object, 'observedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'watermark.observedAt'),
    runUpdatedAt: asOptional(optional(object, 'runUpdatedAt'), (entry) => asTimestamp(entry, WEB_APPLICATION_CONTRACT, 'watermark.runUpdatedAt')),
  };
}

export function decodeRunDetail(value: unknown): RunDetail {
  const fields = [
    'schemaVersion', 'kind', 'workItemId', 'workflowRunId', 'state', 'version', 'definitionName',
    'definitionVersion', 'definitionDigest', 'startedByGitHubUserId', 'gitExecutionUserId', 'nodes',
    'runBindings', 'knowledgeRecommendation', 'knowledgeSuggestion', 'watermark',
  ] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  if (required(object, 'schemaVersion', WEB_APPLICATION_CONTRACT) !== 'kowa.web-application.v1') fail(WEB_APPLICATION_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', WEB_APPLICATION_CONTRACT) !== 'runDetail') fail(WEB_APPLICATION_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.web-application.v1',
    kind: 'runDetail',
    workItemId: asIdentifier(required(object, 'workItemId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.workItemId'),
    workflowRunId: asIdentifier(required(object, 'workflowRunId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.workflowRunId'),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.state', RUN_STATES),
    version: asInteger(required(object, 'version', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.version', 1),
    definitionName: asIdentifier(required(object, 'definitionName', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.definitionName'),
    definitionVersion: asInteger(required(object, 'definitionVersion', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.definitionVersion', 1),
    definitionDigest: asDigest(required(object, 'definitionDigest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.definitionDigest'),
    startedByGitHubUserId: asString(required(object, 'startedByGitHubUserId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.startedByGitHubUserId', WEB_DECIMAL_ID_PATTERN),
    gitExecutionUserId: asOptional(optional(object, 'gitExecutionUserId'), (entry) =>
      asString(entry, WEB_APPLICATION_CONTRACT, 'run.gitExecutionUserId', WEB_DECIMAL_ID_PATTERN)),
    nodes: asArray(required(object, 'nodes', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.nodes', decodeNodeHistory),
    runBindings: asArray(required(object, 'runBindings', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'run.runBindings', decodeRunBinding),
    knowledgeRecommendation: asOptional(optional(object, 'knowledgeRecommendation'), decodeKnowledgeRecommendation),
    knowledgeSuggestion: asOptional(optional(object, 'knowledgeSuggestion'), decodeKnowledgeSuggestion),
    watermark: decodeRunWatermark(required(object, 'watermark', WEB_APPLICATION_CONTRACT)),
  };
}

function decodeArtifactRecord(value: unknown): ArtifactRecord {
  const fields = [
    'artifactId', 'subjectId', 'kind', 'digest', 'size', 'mediaType',
    'producerKind', 'producerId', 'requiresApproval', 'state', 'createdAt',
  ] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    artifactId: asIdentifier(required(object, 'artifactId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.artifactId'),
    subjectId: asIdentifier(required(object, 'subjectId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.subjectId'),
    kind: asString(required(object, 'kind', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.kind', undefined, 200),
    digest: asDigest(required(object, 'digest', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.digest'),
    size: asInteger(required(object, 'size', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.size', 0, 16777216),
    mediaType: asString(required(object, 'mediaType', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.mediaType', undefined, 200),
    producerKind: asEnum(required(object, 'producerKind', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.producerKind', ARTIFACT_PRODUCER_KINDS),
    producerId: asIdentifier(required(object, 'producerId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.producerId'),
    requiresApproval: asBoolean(required(object, 'requiresApproval', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.requiresApproval'),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.state', ARTIFACT_STATES),
    createdAt: asTimestamp(required(object, 'createdAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifact.createdAt'),
  };
}

export function decodeArtifactListResponse(value: unknown): ArtifactListResponse {
  const fields = ['schemaVersion', 'kind', 'workItemId', 'artifacts', 'runBindings'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  if (required(object, 'schemaVersion', WEB_APPLICATION_CONTRACT) !== 'kowa.web-application.v1') fail(WEB_APPLICATION_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', WEB_APPLICATION_CONTRACT) !== 'artifactList') fail(WEB_APPLICATION_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.web-application.v1',
    kind: 'artifactList',
    workItemId: asIdentifier(required(object, 'workItemId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifactList.workItemId'),
    artifacts: asArray(required(object, 'artifacts', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifactList.artifacts', decodeArtifactRecord),
    runBindings: asArray(required(object, 'runBindings', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'artifactList.runBindings', decodeRunBinding),
  };
}

/**
 * The answer to one recovery command. Only the frozen five kinds are accepted,
 * and `applied` stays a boolean fact: the decoder never turns an unapplied or
 * partially applied command into a state change.
 */
export function decodeRecoveryResponse(value: unknown): RecoveryResponse {
  const fields = ['workflowRunId', 'kind', 'applied', 'state', 'version', 'detail', 'recordedAt'] as const;
  const object = asExactObject(value, WEB_APPLICATION_CONTRACT, fields);
  return {
    workflowRunId: asIdentifier(required(object, 'workflowRunId', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recovery.workflowRunId'),
    kind: asEnum(required(object, 'kind', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recovery.kind', RECOVERY_KINDS),
    applied: asBoolean(required(object, 'applied', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recovery.applied'),
    state: asEnum(required(object, 'state', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recovery.state', RUN_STATES),
    version: asInteger(required(object, 'version', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recovery.version', 1),
    detail: asOptional(optional(object, 'detail'), (entry) =>
      asString(entry, WEB_APPLICATION_CONTRACT, 'recovery.detail', undefined, 2000)),
    recordedAt: asTimestamp(required(object, 'recordedAt', WEB_APPLICATION_CONTRACT), WEB_APPLICATION_CONTRACT, 'recovery.recordedAt'),
  };
}

// ---- kowa.fixed-workflow.v1 -----------------------------------------------

export const FIXED_WORKFLOW_CONTRACT = 'kowa.fixed-workflow.v1';

/** The workflow family's identifier shape, narrower than the web-application id. */
const WORKFLOW_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;

function asWorkflowId(value: unknown, what: string): string {
  return asString(value, FIXED_WORKFLOW_CONTRACT, what, WORKFLOW_ID_PATTERN);
}

function decodeFixedWorkflowInputBinding(value: unknown): FixedWorkflowInputBinding {
  const fields = ['key', 'producerNodeId', 'output', 'type', 'required'] as const;
  const object = asExactObject(value, FIXED_WORKFLOW_CONTRACT, fields);
  return {
    key: asWorkflowId(required(object, 'key', FIXED_WORKFLOW_CONTRACT), 'input.key'),
    producerNodeId: asWorkflowId(required(object, 'producerNodeId', FIXED_WORKFLOW_CONTRACT), 'input.producerNodeId'),
    output: asWorkflowId(required(object, 'output', FIXED_WORKFLOW_CONTRACT), 'input.output'),
    type: asEnum(required(object, 'type', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'input.type', FIXED_WORKFLOW_VALUE_TYPES),
    required: asBoolean(required(object, 'required', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'input.required'),
  };
}

function decodeFixedWorkflowRevision(value: unknown): FixedWorkflowRevisionPolicy {
  const fields = ['targetNodeId', 'maxAutoRevisions'] as const;
  const object = asExactObject(value, FIXED_WORKFLOW_CONTRACT, fields);
  return {
    targetNodeId: asWorkflowId(required(object, 'targetNodeId', FIXED_WORKFLOW_CONTRACT), 'revision.targetNodeId'),
    maxAutoRevisions: asInteger(required(object, 'maxAutoRevisions', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'revision.maxAutoRevisions', 1, 3),
  };
}

function decodeFixedWorkflowNode(value: unknown): FixedWorkflowNode {
  const fields = ['id', 'type', 'dependsOn', 'inputs', 'capability', 'operation', 'humanTask', 'revision', 'mandatory'] as const;
  const object = asExactObject(value, FIXED_WORKFLOW_CONTRACT, fields);
  const dependsOn = asArray(required(object, 'dependsOn', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'node.dependsOn', (entry) =>
    asWorkflowId(entry, 'node.dependsOn[]'));
  // `uniqueItems` is a promise of the frozen schema; a doubled edge would be
  // drawn twice, so it is refused here instead.
  if (dependsOn.length !== new Set(dependsOn).size) fail(FIXED_WORKFLOW_CONTRACT, 'node.dependsOn carries a duplicate');
  return {
    id: asWorkflowId(required(object, 'id', FIXED_WORKFLOW_CONTRACT), 'node.id'),
    type: asEnum(required(object, 'type', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'node.type', NODE_TYPES),
    dependsOn,
    inputs: asArray(required(object, 'inputs', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'node.inputs', decodeFixedWorkflowInputBinding),
    capability: asOptional(optional(object, 'capability'), decodeCapability),
    operation: asOptional(optional(object, 'operation'), (entry) =>
      asString(entry, FIXED_WORKFLOW_CONTRACT, 'node.operation', undefined, 128)),
    humanTask: asOptional(optional(object, 'humanTask'), (entry) =>
      asString(entry, FIXED_WORKFLOW_CONTRACT, 'node.humanTask', undefined, 128)),
    revision: asOptional(optional(object, 'revision'), decodeFixedWorkflowRevision),
    mandatory: asBoolean(required(object, 'mandatory', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'node.mandatory'),
  };
}

export function decodeFixedWorkflowView(value: unknown): FixedWorkflowView {
  const fields = [
    'schemaVersion', 'kind', 'name', 'version', 'definitionSchemaVersion', 'definitionDigest', 'order', 'nodes',
  ] as const;
  const object = asExactObject(value, FIXED_WORKFLOW_CONTRACT, fields);
  if (required(object, 'schemaVersion', FIXED_WORKFLOW_CONTRACT) !== 'kowa.fixed-workflow.v1') fail(FIXED_WORKFLOW_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', FIXED_WORKFLOW_CONTRACT) !== 'fixedWorkflowView') fail(FIXED_WORKFLOW_CONTRACT, 'unexpected kind');
  const order = asArray(required(object, 'order', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'order', (entry) =>
    asWorkflowId(entry, 'order[]'));
  if (order.length !== new Set(order).size) fail(FIXED_WORKFLOW_CONTRACT, 'order carries a duplicate node');
  const nodes = asArray(required(object, 'nodes', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'nodes', decodeFixedWorkflowNode);
  if (new Set(nodes.map((node) => node.id)).size !== nodes.length) fail(FIXED_WORKFLOW_CONTRACT, 'nodes carry a duplicate id');
  // Whether `order` can actually place the graph is the layout's decision, not
  // the decoder's: the decoder holds the wire shape, `layoutDag` holds the
  // drawing.
  return {
    schemaVersion: 'kowa.fixed-workflow.v1',
    kind: 'fixedWorkflowView',
    name: asWorkflowId(required(object, 'name', FIXED_WORKFLOW_CONTRACT), 'name'),
    version: asInteger(required(object, 'version', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'version', 1),
    definitionSchemaVersion: 'kowa.workflow-execution.v1',
    definitionDigest: asDigest(required(object, 'definitionDigest', FIXED_WORKFLOW_CONTRACT), FIXED_WORKFLOW_CONTRACT, 'definitionDigest'),
    order,
    nodes,
  };
}

// ---- kowa.delivery-governance.v1 human tasks ------------------------------

function decodeHumanObjectBinding(value: unknown): HumanObjectBinding {
  const fields = ['kind', 'id', 'version', 'digest'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    kind: asString(required(object, 'kind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'object.kind', LOWER_SNAKE_PATTERN),
    id: asIdentifier(required(object, 'id', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'object.id'),
    version: asString(required(object, 'version', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'object.version', undefined, 200),
    digest: asOptional(optional(object, 'digest'), (entry) => asDigest(entry, GOVERNANCE_CONTRACT, 'object.digest')),
  };
}

function decodeHumanField(value: unknown): HumanField {
  const fields = ['name', 'type', 'required', 'options'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    name: asString(required(object, 'name', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'field.name', LOWER_SNAKE_PATTERN),
    type: asEnum(required(object, 'type', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'field.type', HUMAN_FIELD_TYPES),
    required: asBoolean(required(object, 'required', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'field.required'),
    options: asOptional(optional(object, 'options'), (entry) =>
      asArray(entry, GOVERNANCE_CONTRACT, 'field.options', (item) => asString(item, GOVERNANCE_CONTRACT, 'field.options entry', undefined, 200))),
  };
}

function decodeHumanTaskRequest(value: unknown): HumanTaskRequest {
  const fields = ['version', 'title', 'kind', 'completionMode', 'fields', 'object', 'candidates', 'roles'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    version: asInteger(required(object, 'version', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'request.version', 1),
    title: asString(required(object, 'title', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'request.title', undefined, 300),
    kind: asString(required(object, 'kind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'request.kind', LOWER_SNAKE_PATTERN),
    completionMode: asEnum(required(object, 'completionMode', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'request.completionMode', HUMAN_COMPLETION_MODES),
    fields: asArray(required(object, 'fields', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'request.fields', decodeHumanField),
    object: decodeHumanObjectBinding(required(object, 'object', GOVERNANCE_CONTRACT)),
    candidates: asOptional(optional(object, 'candidates'), (entry) =>
      asArray(entry, GOVERNANCE_CONTRACT, 'request.candidates', (item) => asDecimalId(item, GOVERNANCE_CONTRACT, 'request.candidates entry'))),
    roles: asOptional(optional(object, 'roles'), (entry) =>
      asArray(entry, GOVERNANCE_CONTRACT, 'request.roles', (item) => asEnum(item, GOVERNANCE_CONTRACT, 'request.roles entry', WORKSPACE_ROLES))),
  };
}

function decodeHumanResponse(value: unknown): HumanResponse {
  const fields = ['humanResponseId', 'humanTaskId', 'workspaceId', 'requestVersion', 'object', 'githubUserId', 'decision', 'values', 'payloadDigest', 'createdAt'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  const values = asObject(required(object, 'values', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT);
  for (const [key, entry] of Object.entries(values)) {
    if (typeof entry !== 'string') fail(GOVERNANCE_CONTRACT, `response.values.${key} must be a string`);
  }
  return {
    humanResponseId: asIdentifier(required(object, 'humanResponseId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.humanResponseId'),
    humanTaskId: asIdentifier(required(object, 'humanTaskId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.humanTaskId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.workspaceId'),
    requestVersion: asInteger(required(object, 'requestVersion', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.requestVersion', 1),
    object: decodeHumanObjectBinding(required(object, 'object', GOVERNANCE_CONTRACT)),
    githubUserId: asDecimalId(required(object, 'githubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.githubUserId'),
    decision: asEnum(required(object, 'decision', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.decision', HUMAN_DECISIONS),
    values: values as Record<string, string>,
    payloadDigest: asDigest(required(object, 'payloadDigest', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.payloadDigest'),
    createdAt: asTimestamp(required(object, 'createdAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'response.createdAt'),
  };
}

function decodeHumanContinuation(value: unknown): HumanContinuation {
  const fields = [
    'continuationId', 'humanResponseId', 'humanTaskId', 'workspaceId', 'workflowRunId', 'nodeRunId',
    'intent', 'state', 'createdAt', 'consumedAt', 'consumedByGitHubUserId',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  if (required(object, 'intent', GOVERNANCE_CONTRACT) !== 'resume_node') fail(GOVERNANCE_CONTRACT, 'unexpected continuation intent');
  return {
    continuationId: asIdentifier(required(object, 'continuationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'continuation.continuationId'),
    humanResponseId: asIdentifier(required(object, 'humanResponseId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'continuation.humanResponseId'),
    humanTaskId: asIdentifier(required(object, 'humanTaskId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'continuation.humanTaskId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'continuation.workspaceId'),
    workflowRunId: asOptional(optional(object, 'workflowRunId'), (entry) => asIdentifier(entry, GOVERNANCE_CONTRACT, 'continuation.workflowRunId')),
    nodeRunId: asOptional(optional(object, 'nodeRunId'), (entry) => asIdentifier(entry, GOVERNANCE_CONTRACT, 'continuation.nodeRunId')),
    intent: 'resume_node',
    state: asEnum(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'continuation.state', CONTINUATION_STATES),
    createdAt: asTimestamp(required(object, 'createdAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'continuation.createdAt'),
    consumedAt: asOptional(optional(object, 'consumedAt'), (entry) => asTimestamp(entry, GOVERNANCE_CONTRACT, 'continuation.consumedAt')),
    consumedByGitHubUserId: asOptional(optional(object, 'consumedByGitHubUserId'), (entry) =>
      asDecimalId(entry, GOVERNANCE_CONTRACT, 'continuation.consumedByGitHubUserId')),
  };
}

function decodeHumanTask(value: unknown): HumanTask {
  const fields = [
    'humanTaskId', 'workspaceId', 'workItemId', 'workflowRunId', 'nodeRunId', 'request', 'requestDigest',
    'state', 'version', 'createdAt', 'updatedAt', 'resolvedAt', 'resolvedByGitHubUserId', 'resolutionKind',
    'externalObservationId', 'expiredReason', 'response', 'continuation', 'availableActions',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  const state = asEnum(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.state', HUMAN_TASK_STATES);
  const availableActions = asArray(required(object, 'availableActions', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.availableActions', (entry) =>
    asEnum(entry, GOVERNANCE_CONTRACT, 'availableActions entry', HUMAN_TASK_ACTIONS));
  // The server decides the actions: a closed request never offers one, and the
  // page must not render a write control the owner already refuses.
  if (state !== 'OPEN' && availableActions.length > 0) {
    fail(GOVERNANCE_CONTRACT, `a ${state} task must not carry available actions`);
  }
  const resolutionKind = asOptional(optional(object, 'resolutionKind'), (entry) =>
    asEnum(entry, GOVERNANCE_CONTRACT, 'humanTask.resolutionKind', HUMAN_COMPLETION_MODES));
  if (state === 'RESOLVED' && resolutionKind === undefined) {
    fail(GOVERNANCE_CONTRACT, 'a RESOLVED task carries its resolutionKind');
  }
  return {
    humanTaskId: asIdentifier(required(object, 'humanTaskId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.humanTaskId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.workspaceId'),
    workItemId: asIdentifier(required(object, 'workItemId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.workItemId'),
    workflowRunId: asOptional(optional(object, 'workflowRunId'), (entry) => asIdentifier(entry, GOVERNANCE_CONTRACT, 'humanTask.workflowRunId')),
    nodeRunId: asOptional(optional(object, 'nodeRunId'), (entry) => asIdentifier(entry, GOVERNANCE_CONTRACT, 'humanTask.nodeRunId')),
    request: decodeHumanTaskRequest(required(object, 'request', GOVERNANCE_CONTRACT)),
    requestDigest: asDigest(required(object, 'requestDigest', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.requestDigest'),
    state,
    version: asInteger(required(object, 'version', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.version', 1),
    createdAt: asTimestamp(required(object, 'createdAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.createdAt'),
    updatedAt: asTimestamp(required(object, 'updatedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTask.updatedAt'),
    resolvedAt: asOptional(optional(object, 'resolvedAt'), (entry) => asTimestamp(entry, GOVERNANCE_CONTRACT, 'humanTask.resolvedAt')),
    resolvedByGitHubUserId: asOptional(optional(object, 'resolvedByGitHubUserId'), (entry) =>
      asDecimalId(entry, GOVERNANCE_CONTRACT, 'humanTask.resolvedByGitHubUserId')),
    resolutionKind,
    externalObservationId: asOptional(optional(object, 'externalObservationId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'humanTask.externalObservationId')),
    expiredReason: asOptional(optional(object, 'expiredReason'), (entry) => asString(entry, GOVERNANCE_CONTRACT, 'humanTask.expiredReason', undefined, 2000)),
    response: asOptional(optional(object, 'response'), decodeHumanResponse),
    continuation: asOptional(optional(object, 'continuation'), decodeHumanContinuation),
    availableActions,
  };
}

export function decodeHumanTaskResponse(value: unknown): HumanTaskResponse {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, ['schemaVersion', 'kind', 'humanTask']);
  if (required(object, 'schemaVersion', GOVERNANCE_CONTRACT) !== 'kowa.delivery-governance.v1') fail(GOVERNANCE_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', GOVERNANCE_CONTRACT) !== 'humanTask') fail(GOVERNANCE_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.delivery-governance.v1',
    kind: 'humanTask',
    humanTask: decodeHumanTask(required(object, 'humanTask', GOVERNANCE_CONTRACT)),
  };
}

export function decodeHumanTaskListResponse(value: unknown): HumanTaskListResponse {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, ['schemaVersion', 'kind', 'humanTasks']);
  if (required(object, 'schemaVersion', GOVERNANCE_CONTRACT) !== 'kowa.delivery-governance.v1') fail(GOVERNANCE_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', GOVERNANCE_CONTRACT) !== 'humanTaskList') fail(GOVERNANCE_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.delivery-governance.v1',
    kind: 'humanTaskList',
    humanTasks: asArray(required(object, 'humanTasks', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'humanTasks', decodeHumanTask),
  };
}

// ---- kowa.delivery-governance.v1 (delivery.query) --------------------------

function decodeExternalOperationRequest(value: unknown): ExternalOperationRequest {
  const fields = [
    'repositoryId', 'role', 'operation', 'ref', 'baseRef', 'commitOid',
    'expectedGitHubUserId', 'requestedByGitHubUserId', 'titleArtifactId',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    repositoryId: asString(required(object, 'repositoryId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.repositoryId', GITHUB_ID_PATTERN),
    role: asEnum(required(object, 'role', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.role', ['project'] as const),
    operation: asEnum(required(object, 'operation', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.operation', EXTERNAL_OPERATION_KINDS),
    ref: asString(required(object, 'ref', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.ref', undefined, 200),
    baseRef: asOptional(optional(object, 'baseRef'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'operation.request.baseRef', undefined, 200)),
    commitOid: asOid(required(object, 'commitOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.commitOid'),
    expectedGitHubUserId: asString(required(object, 'expectedGitHubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.expectedGitHubUserId', GITHUB_ID_PATTERN),
    requestedByGitHubUserId: asString(required(object, 'requestedByGitHubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.request.requestedByGitHubUserId', GITHUB_ID_PATTERN),
    titleArtifactId: asOptional(optional(object, 'titleArtifactId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'operation.request.titleArtifactId')),
  };
}

function decodeExternalOperationResult(value: unknown): ExternalOperationResult {
  const fields = [
    'repositoryId', 'ref', 'commitOid', 'pullRequestNumber', 'pullRequestAuthorId',
    'baseRef', 'reconciled', 'observationId', 'observedAt', 'observedByGitHubUserId',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    repositoryId: asString(required(object, 'repositoryId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.result.repositoryId', GITHUB_ID_PATTERN),
    ref: asString(required(object, 'ref', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.result.ref', undefined, 200),
    commitOid: asOid(required(object, 'commitOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.result.commitOid'),
    pullRequestNumber: asOptional(optional(object, 'pullRequestNumber'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'operation.result.pullRequestNumber', GITHUB_ID_PATTERN)),
    pullRequestAuthorId: asOptional(optional(object, 'pullRequestAuthorId'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'operation.result.pullRequestAuthorId', GITHUB_ID_PATTERN)),
    baseRef: asOptional(optional(object, 'baseRef'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'operation.result.baseRef', undefined, 200)),
    reconciled: asBoolean(required(object, 'reconciled', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.result.reconciled'),
    observationId: asIdentifier(required(object, 'observationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.result.observationId'),
    observedAt: asTimestamp(required(object, 'observedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.result.observedAt'),
    observedByGitHubUserId: asOptional(optional(object, 'observedByGitHubUserId'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'operation.result.observedByGitHubUserId', undefined, 64)),
  };
}

function decodeExternalOperation(value: unknown): ExternalOperation {
  const fields = [
    'operationId', 'workspaceId', 'workItemId', 'workflowRunId', 'nodeRunId', 'taskId',
    'kind', 'state', 'request', 'requestDigest', 'result', 'unknownReason',
    'version', 'createdAt', 'updatedAt', 'settledAt',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  const kind = asEnum(required(object, 'kind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.kind', EXTERNAL_OPERATION_KINDS);
  const request = decodeExternalOperationRequest(required(object, 'request', GOVERNANCE_CONTRACT));
  // The operation kind is the same fact in both halves; a mismatch would let a
  // `verify_change` be presented with a `publish_pr` request.
  if (request.operation !== kind) fail(GOVERNANCE_CONTRACT, 'operation.request.operation differs from operation.kind');
  return {
    operationId: asIdentifier(required(object, 'operationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.operationId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.workspaceId'),
    workItemId: asIdentifier(required(object, 'workItemId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.workItemId'),
    workflowRunId: asIdentifier(required(object, 'workflowRunId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.workflowRunId'),
    nodeRunId: asOptional(optional(object, 'nodeRunId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'operation.nodeRunId')),
    taskId: asOptional(optional(object, 'taskId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'operation.taskId')),
    kind,
    state: asEnum(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.state', EXTERNAL_OPERATION_STATES),
    request,
    requestDigest: asDigest(required(object, 'requestDigest', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.requestDigest'),
    result: asOptional(optional(object, 'result'), decodeExternalOperationResult),
    unknownReason: asOptional(optional(object, 'unknownReason'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'operation.unknownReason', undefined, 2000)),
    version: asInteger(required(object, 'version', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.version', 1),
    createdAt: asTimestamp(required(object, 'createdAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.createdAt'),
    updatedAt: asTimestamp(required(object, 'updatedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operation.updatedAt'),
    settledAt: asOptional(optional(object, 'settledAt'), (entry) =>
      asTimestamp(entry, GOVERNANCE_CONTRACT, 'operation.settledAt')),
  };
}

const PULL_REQUEST_FACT_FIELDS = [
  'number', 'authorId', 'headOid', 'headRef', 'baseRef', 'state', 'merged',
  'mergeCommitOid', 'mergedById', 'mergedAt', 'mergedByType',
] as const;

function decodePullRequestFact(value: unknown): PullRequestFact {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, PULL_REQUEST_FACT_FIELDS);
  return {
    number: asString(required(object, 'number', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'pullRequest.number', GITHUB_ID_PATTERN),
    authorId: asString(required(object, 'authorId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'pullRequest.authorId', GITHUB_ID_PATTERN),
    headOid: asOid(required(object, 'headOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'pullRequest.headOid'),
    headRef: asOptional(optional(object, 'headRef'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'pullRequest.headRef', undefined, 300)),
    baseRef: asOptional(optional(object, 'baseRef'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'pullRequest.baseRef', undefined, 300)),
    state: asString(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'pullRequest.state', undefined, 32),
    merged: asBoolean(required(object, 'merged', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'pullRequest.merged'),
    mergeCommitOid: asOptional(optional(object, 'mergeCommitOid'), (entry) =>
      asOid(entry, GOVERNANCE_CONTRACT, 'pullRequest.mergeCommitOid')),
    mergedById: asOptional(optional(object, 'mergedById'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'pullRequest.mergedById', GITHUB_ID_PATTERN)),
    mergedAt: asOptional(optional(object, 'mergedAt'), (entry) =>
      asTimestamp(entry, GOVERNANCE_CONTRACT, 'pullRequest.mergedAt')),
    mergedByType: asOptional(optional(object, 'mergedByType'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'pullRequest.mergedByType', undefined, 64)),
  };
}

const REVIEW_FACT_FIELDS = [
  'reviewId', 'reviewerId', 'state', 'commitOid', 'submittedAt',
  'authorAssociation', 'reviewerLogin', 'reviewerType', 'reviewerPermission',
] as const;

function decodeReviewFact(value: unknown): ReviewFact {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, REVIEW_FACT_FIELDS);
  return {
    reviewId: asIdentifier(required(object, 'reviewId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'review.reviewId'),
    reviewerId: asString(required(object, 'reviewerId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'review.reviewerId', GITHUB_ID_PATTERN),
    state: asString(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'review.state', undefined, 32),
    commitOid: asOptional(optional(object, 'commitOid'), (entry) =>
      asOid(entry, GOVERNANCE_CONTRACT, 'review.commitOid')),
    submittedAt: asOptional(optional(object, 'submittedAt'), (entry) =>
      asTimestamp(entry, GOVERNANCE_CONTRACT, 'review.submittedAt')),
    authorAssociation: asOptional(optional(object, 'authorAssociation'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'review.authorAssociation', undefined, 64)),
    reviewerLogin: asOptional(optional(object, 'reviewerLogin'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'review.reviewerLogin', undefined, 200)),
    reviewerType: asString(required(object, 'reviewerType', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'review.reviewerType', undefined, 64),
    reviewerPermission: asOptional(optional(object, 'reviewerPermission'), (entry) =>
      asEnum(entry, GOVERNANCE_CONTRACT, 'review.reviewerPermission', ['admin', 'write', 'read', 'none'] as const)),
  };
}

function decodeChecksFact(value: unknown): ChecksFact {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, ['commitOid', 'state', 'total', 'failed', 'pending']);
  return {
    commitOid: asOid(required(object, 'commitOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'checks.commitOid'),
    state: asEnum(required(object, 'state', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'checks.state', CHECK_STATES),
    total: asInteger(required(object, 'total', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'checks.total', 0),
    failed: asInteger(required(object, 'failed', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'checks.failed', 0),
    pending: asInteger(required(object, 'pending', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'checks.pending', 0),
  };
}

/** The one payload a subject kind may carry. */
const OBSERVATION_PAYLOAD = {
  pull_request: 'pullRequest',
  pull_request_review: 'review',
  pull_request_checks: 'checks',
  branch_head: 'ref',
} as const;

const OBSERVATION_FIELDS = [
  'observationId', 'workspaceId', 'repositoryId', 'subjectKind', 'subjectKey',
  'observedAt', 'actorId', 'unknown', 'unknownReason',
  'pullRequest', 'review', 'checks', 'ref',
] as const;

function decodeGitHubObservation(value: unknown): GitHubObservation {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, OBSERVATION_FIELDS);
  const subjectKind = asEnum(required(object, 'subjectKind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.subjectKind', OBSERVATION_SUBJECT_KINDS);
  const unknown = asBoolean(required(object, 'unknown', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.unknown');
  const payloadKey = OBSERVATION_PAYLOAD[subjectKind];
  for (const key of ['pullRequest', 'review', 'checks', 'ref'] as const) {
    if (key !== payloadKey && optional(object, key) !== undefined) {
      fail(GOVERNANCE_CONTRACT, `observation.${key} does not belong to subject kind ${subjectKind}`);
    }
  }
  const payload = optional(object, payloadKey);
  // A failed read is a real answer and carries no fact; a successful one always
  // carries the fact it read. Presenting either as the other would invent a
  // GitHub state the owner never recorded.
  if (unknown && payload !== undefined) fail(GOVERNANCE_CONTRACT, 'an unknown observation must not carry a fact');
  if (!unknown && payload === undefined) fail(GOVERNANCE_CONTRACT, `a ${subjectKind} observation must carry its fact`);
  // A failed read carries no payload at all, so the payload decoders only run
  // when the owner actually published the fact.
  const pullRequest = subjectKind === 'pull_request' && payload !== undefined ? decodePullRequestFact(payload) : undefined;
  const review = subjectKind === 'pull_request_review' && payload !== undefined ? decodeReviewFact(payload) : undefined;
  const checks = subjectKind === 'pull_request_checks' && payload !== undefined ? decodeChecksFact(payload) : undefined;
  const ref = subjectKind === 'branch_head' && payload !== undefined ? decodeBranchHeadFact(payload) : undefined;
  return {
    observationId: asIdentifier(required(object, 'observationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.observationId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.workspaceId'),
    repositoryId: asString(required(object, 'repositoryId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.repositoryId', GITHUB_ID_PATTERN),
    subjectKind,
    subjectKey: asString(required(object, 'subjectKey', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.subjectKey', undefined, 300),
    observedAt: asTimestamp(required(object, 'observedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'observation.observedAt'),
    actorId: asOptional(optional(object, 'actorId'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'observation.actorId', GITHUB_ID_PATTERN)),
    unknown,
    unknownReason: asOptional(optional(object, 'unknownReason'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'observation.unknownReason', undefined, 2000)),
    pullRequest,
    review,
    checks,
    ref,
  };
}

function decodeBranchHeadFact(value: unknown): { commitOid: string } {
  const object = asExactObject(value, GOVERNANCE_CONTRACT, ['commitOid']);
  return { commitOid: asOid(required(object, 'commitOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'ref.commitOid') };
}

function decodeVerificationRecord(value: unknown): VerificationRecord {
  const fields = [
    'verificationId', 'workspaceId', 'workItemId', 'workflowRunId', 'nodeRunId', 'kind',
    'subjectKind', 'repositoryId', 'commitOid', 'artifactId', 'artifactDigest',
    'configurationVersion', 'producerKind', 'producerId', 'source', 'result',
    'reportArtifactId', 'reportDigest', 'observedAt', 'createdAt',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  const subjectKind = asEnum(required(object, 'subjectKind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.subjectKind', VERIFICATION_SUBJECT_KINDS);
  const commitOid = optional(object, 'commitOid');
  const artifactId = optional(object, 'artifactId');
  const artifactDigest = optional(object, 'artifactDigest');
  // The subject kind decides which subject the evidence is about; a row that
  // contradicts itself would be shown against the wrong commit or artifact.
  if (subjectKind === 'commit' && artifactId !== undefined) fail(GOVERNANCE_CONTRACT, 'a commit verification must not carry an artifact subject');
  if (subjectKind === 'artifact' && commitOid !== undefined) fail(GOVERNANCE_CONTRACT, 'an artifact verification must not carry a commit subject');
  return {
    verificationId: asIdentifier(required(object, 'verificationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.verificationId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.workspaceId'),
    workItemId: asIdentifier(required(object, 'workItemId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.workItemId'),
    workflowRunId: asOptional(optional(object, 'workflowRunId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'verification.workflowRunId')),
    nodeRunId: asOptional(optional(object, 'nodeRunId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'verification.nodeRunId')),
    kind: asEnum(required(object, 'kind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.kind', VERIFICATION_KINDS),
    subjectKind,
    repositoryId: asOptional(optional(object, 'repositoryId'), (entry) =>
      asString(entry, GOVERNANCE_CONTRACT, 'verification.repositoryId', GITHUB_ID_PATTERN)),
    commitOid: asOptional(commitOid, (entry) => asOid(entry, GOVERNANCE_CONTRACT, 'verification.commitOid')),
    artifactId: asOptional(artifactId, (entry) => asIdentifier(entry, GOVERNANCE_CONTRACT, 'verification.artifactId')),
    artifactDigest: asOptional(artifactDigest, (entry) => asDigest(entry, GOVERNANCE_CONTRACT, 'verification.artifactDigest')),
    configurationVersion: asString(required(object, 'configurationVersion', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.configurationVersion', undefined, 200),
    producerKind: asEnum(required(object, 'producerKind', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.producerKind', VERIFICATION_PRODUCER_KINDS),
    producerId: asIdentifier(required(object, 'producerId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.producerId'),
    source: asEnum(required(object, 'source', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.source', VERIFICATION_SOURCES),
    result: asEnum(required(object, 'result', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.result', VERIFICATION_RESULTS),
    reportArtifactId: asOptional(optional(object, 'reportArtifactId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'verification.reportArtifactId')),
    reportDigest: asOptional(optional(object, 'reportDigest'), (entry) =>
      asDigest(entry, GOVERNANCE_CONTRACT, 'verification.reportDigest')),
    observedAt: asTimestamp(required(object, 'observedAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.observedAt'),
    createdAt: asTimestamp(required(object, 'createdAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification.createdAt'),
  };
}

function decodeClosureRecord(value: unknown): ClosureRecord {
  const fields = [
    'closureId', 'workspaceId', 'workItemId', 'workflowRunId', 'definitionName',
    'definitionVersion', 'definitionDigest', 'policyVersion', 'requirementDigest',
    'knowledgeSnapshotId', 'knowledgeBindingEventId', 'mergeRepositoryId',
    'mergeCommitOid', 'pullRequestNumber', 'mergeObservationId', 'reviewObservationId',
    'reviewHeadOid', 'reviewerGitHubUserId', 'requiredTestVerificationIds',
    'independentReviewVerificationId', 'postMergeVerificationId', 'checklistDigest',
    'createdByGitHubUserId', 'createdAt', 'mergedByGitHubUserId', 'mergedByType',
  ] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  return {
    closureId: asIdentifier(required(object, 'closureId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.closureId'),
    workspaceId: asIdentifier(required(object, 'workspaceId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.workspaceId'),
    workItemId: asIdentifier(required(object, 'workItemId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.workItemId'),
    workflowRunId: asIdentifier(required(object, 'workflowRunId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.workflowRunId'),
    definitionName: asIdentifier(required(object, 'definitionName', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.definitionName'),
    definitionVersion: asInteger(required(object, 'definitionVersion', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.definitionVersion', 1),
    definitionDigest: asDigest(required(object, 'definitionDigest', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.definitionDigest'),
    policyVersion: asString(required(object, 'policyVersion', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.policyVersion', undefined, 200),
    requirementDigest: asDigest(required(object, 'requirementDigest', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.requirementDigest'),
    knowledgeSnapshotId: asOptional(optional(object, 'knowledgeSnapshotId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'closure.knowledgeSnapshotId')),
    knowledgeBindingEventId: asOptional(optional(object, 'knowledgeBindingEventId'), (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'closure.knowledgeBindingEventId')),
    mergeRepositoryId: asString(required(object, 'mergeRepositoryId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.mergeRepositoryId', GITHUB_ID_PATTERN),
    mergeCommitOid: asOid(required(object, 'mergeCommitOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.mergeCommitOid'),
    pullRequestNumber: asString(required(object, 'pullRequestNumber', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.pullRequestNumber', GITHUB_ID_PATTERN),
    mergeObservationId: asIdentifier(required(object, 'mergeObservationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.mergeObservationId'),
    reviewObservationId: asIdentifier(required(object, 'reviewObservationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.reviewObservationId'),
    reviewHeadOid: asOid(required(object, 'reviewHeadOid', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.reviewHeadOid'),
    reviewerGitHubUserId: asString(required(object, 'reviewerGitHubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.reviewerGitHubUserId', GITHUB_ID_PATTERN),
    requiredTestVerificationIds: asArray(required(object, 'requiredTestVerificationIds', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.requiredTestVerificationIds', (entry) =>
      asIdentifier(entry, GOVERNANCE_CONTRACT, 'closure.requiredTestVerificationIds entry')),
    independentReviewVerificationId: asIdentifier(required(object, 'independentReviewVerificationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.independentReviewVerificationId'),
    postMergeVerificationId: asIdentifier(required(object, 'postMergeVerificationId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.postMergeVerificationId'),
    checklistDigest: asDigest(required(object, 'checklistDigest', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.checklistDigest'),
    createdByGitHubUserId: asString(required(object, 'createdByGitHubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.createdByGitHubUserId', GITHUB_ID_PATTERN),
    createdAt: asTimestamp(required(object, 'createdAt', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.createdAt'),
    mergedByGitHubUserId: asString(required(object, 'mergedByGitHubUserId', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.mergedByGitHubUserId', GITHUB_ID_PATTERN),
    mergedByType: asString(required(object, 'mergedByType', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'closure.mergedByType', undefined, 64),
  };
}

export function decodeDeliveryView(value: unknown): DeliveryViewResponse {
  const fields = ['schemaVersion', 'kind', 'workItem', 'runBindings', 'operations', 'facts', 'verification', 'closure'] as const;
  const object = asExactObject(value, GOVERNANCE_CONTRACT, fields);
  if (required(object, 'schemaVersion', GOVERNANCE_CONTRACT) !== 'kowa.delivery-governance.v1') fail(GOVERNANCE_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', GOVERNANCE_CONTRACT) !== 'deliveryView') fail(GOVERNANCE_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.delivery-governance.v1',
    kind: 'deliveryView',
    workItem: decodeWorkItem(required(object, 'workItem', GOVERNANCE_CONTRACT)),
    runBindings: asArray(required(object, 'runBindings', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'runBindings', decodeRunBinding),
    operations: asArray(required(object, 'operations', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'operations', decodeExternalOperation),
    facts: asArray(required(object, 'facts', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'facts', decodeGitHubObservation),
    verification: asArray(required(object, 'verification', GOVERNANCE_CONTRACT), GOVERNANCE_CONTRACT, 'verification', decodeVerificationRecord),
    closure: asOptional(optional(object, 'closure'), decodeClosureRecord),
  };
}

// ---- kowa.observability.v1 (observability.watermarks) ----------------------

const OBSERVABILITY_CONTRACT = 'kowa.observability.v1';

/** The owner's `SafeText`: a diagnostic string that must not carry a credential. */
const CREDENTIAL_PATTERN = /(?:gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|(?:pk|sk)-lf-[A-Za-z0-9]{8,}|\bsk-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}|:\/\/[^\s/]*@)/;

function asSafeText(value: unknown, what: string): string {
  const text = asString(value, OBSERVABILITY_CONTRACT, what, undefined, 300);
  // The contract refuses a credential shape; the reader must not accept one
  // either, or a leaked token would be rendered as a diagnostic reason.
  if (CREDENTIAL_PATTERN.test(text)) fail(OBSERVABILITY_CONTRACT, `${what} still carries a credential shape`);
  return text;
}

function decodeObservabilityDefinition(value: unknown): ObservabilityDefinitionInfo {
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, ['name', 'version', 'digest']);
  return {
    name: asOptional(optional(object, 'name'), (entry) => asString(entry, OBSERVABILITY_CONTRACT, 'definition.name', undefined, 128)),
    version: asOptional(optional(object, 'version'), (entry) => asInteger(entry, OBSERVABILITY_CONTRACT, 'definition.version', 0)),
    digest: asOptional(optional(object, 'digest'), (entry) => asDigest(entry, OBSERVABILITY_CONTRACT, 'definition.digest')),
  };
}

function decodeObservabilityDeployment(value: unknown): ObservabilityDeployment {
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, ['commit', 'migrationVersion', 'goVersion']);
  return {
    commit: asOptional(optional(object, 'commit'), (entry) => asString(entry, OBSERVABILITY_CONTRACT, 'deployment.commit', /^[0-9a-f]{7,64}$/)),
    migrationVersion: asOptional(optional(object, 'migrationVersion'), (entry) => asString(entry, OBSERVABILITY_CONTRACT, 'deployment.migrationVersion', /^[0-9]{6}$/)),
    goVersion: asOptional(optional(object, 'goVersion'), (entry) => asString(entry, OBSERVABILITY_CONTRACT, 'deployment.goVersion', undefined, 64)),
  };
}

function decodeObservationWatermark(value: unknown): ObservationWatermark {
  const fields = [
    'subjectKind', 'subjectKey', 'lastAttemptAt', 'lastAttemptKnown', 'unknownReason',
    'freshness', 'freshnessReason', 'supersededByFailure',
  ] as const;
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, fields);
  return {
    subjectKind: asString(required(object, 'subjectKind', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observation.subjectKind', undefined, 64),
    subjectKey: asString(required(object, 'subjectKey', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observation.subjectKey', undefined, 200),
    lastAttemptAt: asTimestamp(required(object, 'lastAttemptAt', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observation.lastAttemptAt'),
    lastAttemptKnown: asBoolean(required(object, 'lastAttemptKnown', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observation.lastAttemptKnown'),
    unknownReason: asOptional(optional(object, 'unknownReason'), (entry) => asSafeText(entry, 'observation.unknownReason')),
    freshness: asEnum(required(object, 'freshness', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observation.freshness', FRESHNESS_STATES),
    freshnessReason: asOptional(optional(object, 'freshnessReason'), (entry) =>
      asEnum(entry, OBSERVABILITY_CONTRACT, 'observation.freshnessReason', FRESHNESS_REASONS)),
    supersededByFailure: asBoolean(required(object, 'supersededByFailure', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observation.supersededByFailure'),
  };
}

function decodeObservabilityRunFacts(value: unknown): ObservabilityRunFacts {
  const fields = ['workflowRunId', 'state', 'version', 'updatedAt', 'nodeRounds', 'attempts', 'openHumanTasks'] as const;
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, fields);
  return {
    workflowRunId: asString(required(object, 'workflowRunId', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.workflowRunId', undefined, 128),
    state: asString(required(object, 'state', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.state', undefined, 32),
    version: asInteger(required(object, 'version', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.version', 0),
    updatedAt: asTimestamp(required(object, 'updatedAt', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.updatedAt'),
    nodeRounds: asInteger(required(object, 'nodeRounds', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.nodeRounds', 0),
    attempts: asInteger(required(object, 'attempts', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.attempts', 0),
    openHumanTasks: asInteger(required(object, 'openHumanTasks', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'run.openHumanTasks', 0),
  };
}

function decodeObservabilitySnapshot(value: unknown): ObservabilitySnapshot {
  const fields = [
    'kind', 'subject', 'lastSuccessAt', 'lastSuccessDetail', 'lastAttemptAt',
    'lastFailureAt', 'lastFailureReason', 'successes', 'failures', 'freshness',
    'staleReason', 'contactable', 'progressing',
  ] as const;
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, fields);
  return {
    kind: asEnum(required(object, 'kind', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.kind', ['contactability', 'business_progress', 'external_observation'] as const),
    subject: asString(required(object, 'subject', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.subject', undefined, 200),
    lastSuccessAt: asOptional(optional(object, 'lastSuccessAt'), (entry) => asTimestamp(entry, OBSERVABILITY_CONTRACT, 'progress.lastSuccessAt')),
    lastSuccessDetail: asOptional(optional(object, 'lastSuccessDetail'), (entry) => asSafeText(entry, 'progress.lastSuccessDetail')),
    lastAttemptAt: asOptional(optional(object, 'lastAttemptAt'), (entry) => asTimestamp(entry, OBSERVABILITY_CONTRACT, 'progress.lastAttemptAt')),
    lastFailureAt: asOptional(optional(object, 'lastFailureAt'), (entry) => asTimestamp(entry, OBSERVABILITY_CONTRACT, 'progress.lastFailureAt')),
    lastFailureReason: asOptional(optional(object, 'lastFailureReason'), (entry) => asSafeText(entry, 'progress.lastFailureReason')),
    successes: asInteger(required(object, 'successes', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.successes', 0),
    failures: asInteger(required(object, 'failures', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.failures', 0),
    freshness: asEnum(required(object, 'freshness', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.freshness', FRESHNESS_STATES),
    staleReason: asOptional(optional(object, 'staleReason'), (entry) => asEnum(entry, OBSERVABILITY_CONTRACT, 'progress.staleReason', FRESHNESS_REASONS)),
    contactable: asBoolean(required(object, 'contactable', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.contactable'),
    progressing: asBoolean(required(object, 'progressing', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'progress.progressing'),
  };
}

function decodeObservabilityRuntime(value: unknown): ObservabilityRuntime {
  const fields = ['runtimeId', 'providerId', 'providerVersion', 'capabilities', 'authenticated', 'observedAt', 'freshness', 'staleReason', 'contactable'] as const;
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, fields);
  return {
    runtimeId: asString(required(object, 'runtimeId', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtime.runtimeId', undefined, 128),
    providerId: asOptional(optional(object, 'providerId'), (entry) => asString(entry, OBSERVABILITY_CONTRACT, 'runtime.providerId', undefined, 64)),
    providerVersion: asOptional(optional(object, 'providerVersion'), (entry) => asString(entry, OBSERVABILITY_CONTRACT, 'runtime.providerVersion', undefined, 64)),
    capabilities: asArray(required(object, 'capabilities', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtime.capabilities', (entry) =>
      asString(entry, OBSERVABILITY_CONTRACT, 'runtime.capabilities entry', undefined, 128)),
    authenticated: asBoolean(required(object, 'authenticated', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtime.authenticated'),
    observedAt: asTimestamp(required(object, 'observedAt', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtime.observedAt'),
    freshness: asEnum(required(object, 'freshness', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtime.freshness', FRESHNESS_STATES),
    staleReason: asOptional(optional(object, 'staleReason'), (entry) => asEnum(entry, OBSERVABILITY_CONTRACT, 'runtime.staleReason', FRESHNESS_REASONS)),
    contactable: asBoolean(required(object, 'contactable', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtime.contactable'),
  };
}

function decodeObservabilityArtifacts(value: unknown): ObservabilityArtifactFacts {
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, ['namespace', 'published']);
  return {
    namespace: asOptional(optional(object, 'namespace'), (entry) => asDigest(entry, OBSERVABILITY_CONTRACT, 'artifacts.namespace')),
    published: asInteger(required(object, 'published', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'artifacts.published', 0),
  };
}

export function decodeWatermarkView(value: unknown): WatermarkView {
  const fields = [
    'schemaVersion', 'kind', 'observedAt', 'workspaceId', 'workItemId', 'deployment',
    'definition', 'executionConfigVersion', 'run', 'observations', 'runtimes',
    'progress', 'artifacts', 'freshnessWindowSeconds',
  ] as const;
  const object = asExactObject(value, OBSERVABILITY_CONTRACT, fields);
  if (required(object, 'schemaVersion', OBSERVABILITY_CONTRACT) !== 'kowa.observability.v1') fail(OBSERVABILITY_CONTRACT, 'unexpected schemaVersion');
  if (required(object, 'kind', OBSERVABILITY_CONTRACT) !== 'observabilityWatermarks') fail(OBSERVABILITY_CONTRACT, 'unexpected kind');
  return {
    schemaVersion: 'kowa.observability.v1',
    kind: 'observabilityWatermarks',
    observedAt: asTimestamp(required(object, 'observedAt', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observedAt'),
    workspaceId: asString(required(object, 'workspaceId', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'workspaceId', undefined, 128),
    workItemId: asString(required(object, 'workItemId', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'workItemId', undefined, 128),
    deployment: decodeObservabilityDeployment(required(object, 'deployment', OBSERVABILITY_CONTRACT)),
    definition: decodeObservabilityDefinition(required(object, 'definition', OBSERVABILITY_CONTRACT)),
    executionConfigVersion: asInteger(required(object, 'executionConfigVersion', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'executionConfigVersion', 0),
    run: asOptional(optional(object, 'run'), decodeObservabilityRunFacts),
    observations: asArray(required(object, 'observations', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'observations', decodeObservationWatermark),
    runtimes: asArray(required(object, 'runtimes', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'runtimes', decodeObservabilityRuntime),
    progress: asOptional(optional(object, 'progress'), decodeObservabilitySnapshot),
    artifacts: decodeObservabilityArtifacts(required(object, 'artifacts', OBSERVABILITY_CONTRACT)),
    freshnessWindowSeconds: asInteger(required(object, 'freshnessWindowSeconds', OBSERVABILITY_CONTRACT), OBSERVABILITY_CONTRACT, 'freshnessWindowSeconds', 0),
  };
}
