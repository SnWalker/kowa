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
  MACHINE_IDENTITY_STATES,
  PUSH_PERMISSION_STATES,
  REMOTE_READ_STATES,
  RUNTIME_FRESHNESS,
  RUNTIME_STALE_REASONS,
  WORKSPACE_ROLES,
  type ApiErrorCategory,
  type Capability,
  type ExecutionConfig,
  type ExecutionConfigResponse,
  type GitRemoteMachineIdentity,
  type GitRemoteRepositoryStatus,
  type GitRemoteStatus,
  type GitRemoteStatusResponse,
  type LoginResponse,
  type RepositoryBinding,
  type RuntimeListResponse,
  type RuntimeObservation,
  type RuntimeView,
  type RuntimeViewResponse,
  type SessionBootstrapResponse,
  type WebIdentity,
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

export function decodeLoginResponse(value: unknown): LoginResponse {
  const object = asExactObject(value, SESSION_CONTRACT, ['authorizationUrl']);
  return { authorizationUrl: asString(required(object, 'authorizationUrl', SESSION_CONTRACT), SESSION_CONTRACT, 'authorizationUrl') };
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
