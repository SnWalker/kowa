/**
 * Wire types for the frozen contracts frontend:S01 consumes.
 *
 * Owners and pinned digests are recorded in
 * `doc/Kowa前端设计/record/S01-消费准入证据.md`; these types mirror the
 * official schemas field by field and must not add, rename or reinterpret
 * business fields. Every response is decoded by `decode.ts`, which fails
 * closed on unknown fields and shapes.
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

export const API_ERROR_CATEGORIES = [
  'VALIDATION_ERROR',
  'UNAUTHORIZED',
  'FORBIDDEN',
  'VERSION_CONFLICT',
  'RESOURCE_UNAVAILABLE',
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
