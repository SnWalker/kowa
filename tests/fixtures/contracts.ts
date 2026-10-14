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
  ExecutionConfig,
  GitRemoteStatus,
  RuntimeListResponse,
  RuntimeView,
  SessionBootstrapResponse,
  WorkspaceConfig,
} from '../../src/api/contracts';

const TOKEN_43 = 'B'.repeat(43);

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
