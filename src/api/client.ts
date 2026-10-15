/**
 * Same-origin HTTP client for the frozen operations consumed by S01 and S02
 * (WorkItem intake/list/detail, run admission and run detail, HumanTask
 * list/detail/response).
 *
 * The browser holds one opaque `__Host-` session cookie; the CSRF token stays
 * in memory and is sent only as `X-CSRF-Token` on the mutating operations the
 * contracts declare as CSRF-protected. `Origin` is set by the browser for
 * same-origin writes, so the app must be served from the trusted origin (no
 * CORS credential grant exists). HTTP status alone never decides retry or
 * success: every failure is classified through the frozen error envelope, and
 * an unrecognised body fails closed.
 */
import {
  decodeAdmissionResponse,
  decodeApiErrorCategory,
  decodeExecutionConfigResponse,
  decodeGitRemoteStatusResponse,
  decodeHumanTaskListResponse,
  decodeHumanTaskResponse,
  decodeLoginResponse,
  decodeRunDetail,
  decodeRuntimeListResponse,
  decodeRuntimeViewResponse,
  decodeSessionBootstrapResponse,
  decodeWorkItemListResponse,
  decodeWorkItemResponse,
  decodeWorkspaceConfig,
} from './decode';
import type {
  AdmissionResponse,
  AnswerHumanTaskCommand,
  ApiErrorCategory,
  ConfigureRepositoriesCommand,
  CreateWorkItemCommand,
  ExecutionConfigResponse,
  GitRemoteStatusResponse,
  HumanTaskListResponse,
  HumanTaskResponse,
  LoginResponse,
  RunDetail,
  RuntimeListResponse,
  RuntimeViewResponse,
  SessionBootstrapResponse,
  StartRunCommand,
  UpdateExecutionConfigRequest,
  WorkItemListResponse,
  WorkItemResponse,
  WorkspaceConfig,
  WorkspaceRole,
} from './contracts';

export class ApiError extends Error {
  readonly status: number;
  readonly category?: ApiErrorCategory;

  constructor(status: number, category: ApiErrorCategory | undefined, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.category = category;
  }
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  csrf?: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
};

export type ApiClient = ReturnType<typeof createApiClient>;

export function createApiClient(clientOptions: { fetch?: typeof fetch; onUnauthorized?: () => void } = {}) {
  const doFetch = clientOptions.fetch ?? globalThis.fetch.bind(globalThis);

  async function request(path: string, options: RequestOptions = {}): Promise<{ status: number; body: unknown }> {
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (options.body !== undefined) headers['Content-Type'] = 'application/json';
    if (options.csrf) headers['X-CSRF-Token'] = options.csrf;
    if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey;

    const response = await doFetch(path, {
      method: options.method ?? 'GET',
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    });

    let body: unknown;
    if (response.status === 204) {
      body = undefined;
    } else {
      const text = await response.text();
      try {
        body = text === '' ? undefined : JSON.parse(text);
      } catch {
        if (response.ok) {
          throw new ApiError(response.status, undefined, '响应不是合法 JSON，已按契约违规处理');
        }
        body = undefined;
      }
    }

    if (!response.ok) {
      const category = classifyErrorBody(body);
      // An unauthorized answer discards authentication state for every
      // consumer; the session store increments its generation and clears
      // caches before any further request. An answer to a request the caller
      // already superseded is not evidence about the current session, so it
      // never invalidates one.
      if (category === 'UNAUTHORIZED' && !options.signal?.aborted) clientOptions.onUnauthorized?.();
      throw new ApiError(response.status, category, `请求被拒绝（HTTP ${response.status}）`);
    }
    return { status: response.status, body };
  }

  function classifyErrorBody(body: unknown): ApiErrorCategory | undefined {
    if (body === undefined) return undefined;
    try {
      return decodeApiErrorCategory(body);
    } catch {
      return undefined;
    }
  }

  return {
    /** POST /api/v2/auth/github/login — returns the owner-produced authorization URL. */
    async beginLogin(returnTo: string, signal?: AbortSignal): Promise<LoginResponse> {
      const { body } = await request('/api/v2/auth/github/login', { method: 'POST', body: { returnTo }, signal });
      return decodeLoginResponse(body);
    },

    /** POST /api/v2/auth/session — read-only snapshot; no CSRF is required. */
    async bootstrapSession(requestId: string, signal?: AbortSignal): Promise<SessionBootstrapResponse> {
      const { body } = await request('/api/v2/auth/session', { method: 'POST', body: { requestId }, signal });
      return decodeSessionBootstrapResponse(body);
    },

    /** POST /api/v2/auth/logout — durable revocation; requires CSRF. */
    async logout(csrf: string, signal?: AbortSignal): Promise<void> {
      await request('/api/v2/auth/logout', { method: 'POST', csrf, body: {}, signal });
    },

    async getWorkspace(workspaceId: string, signal?: AbortSignal): Promise<WorkspaceConfig> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}`, { signal });
      return decodeWorkspaceConfig(body);
    },

    async bootstrapWorkspace(name: string, csrf: string, idempotencyKey: string, signal?: AbortSignal): Promise<WorkspaceConfig> {
      const { body } = await request('/api/v1/workspaces', {
        method: 'POST',
        body: { name },
        csrf,
        idempotencyKey,
        signal,
      });
      return decodeWorkspaceConfig(body);
    },

    async configureRepositories(
      workspaceId: string,
      command: ConfigureRepositoriesCommand,
      csrf: string,
      idempotencyKey: string,
      signal?: AbortSignal,
    ): Promise<WorkspaceConfig> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/repositories`, {
        method: 'PUT',
        body: command,
        csrf,
        idempotencyKey,
        signal,
      });
      return decodeWorkspaceConfig(body);
    },

    async upsertMember(
      workspaceId: string,
      githubUserId: string,
      command: { expectedVersion: number; role: WorkspaceRole },
      csrf: string,
      idempotencyKey: string,
      signal?: AbortSignal,
    ): Promise<void> {
      await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/members/${encodeURIComponent(githubUserId)}`, {
        method: 'PUT',
        body: command,
        csrf,
        idempotencyKey,
        signal,
      });
    },

    async revokeMember(
      workspaceId: string,
      githubUserId: string,
      command: { expectedVersion: number },
      csrf: string,
      idempotencyKey: string,
      signal?: AbortSignal,
    ): Promise<void> {
      await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/members/${encodeURIComponent(githubUserId)}`, {
        method: 'DELETE',
        body: command,
        csrf,
        idempotencyKey,
        signal,
      });
    },

    async listRuntimes(workspaceId: string, signal?: AbortSignal): Promise<RuntimeListResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/runtimes`, { signal });
      return decodeRuntimeListResponse(body);
    },

    async getRuntime(workspaceId: string, runtimeId: string, signal?: AbortSignal): Promise<RuntimeViewResponse> {
      const { body } = await request(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/runtimes/${encodeURIComponent(runtimeId)}`,
        { signal },
      );
      return decodeRuntimeViewResponse(body);
    },

    async getExecutionConfig(workspaceId: string, signal?: AbortSignal): Promise<ExecutionConfigResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/execution-config`, { signal });
      return decodeExecutionConfigResponse(body);
    },

    async updateExecutionConfig(
      workspaceId: string,
      command: UpdateExecutionConfigRequest,
      csrf: string,
      signal?: AbortSignal,
    ): Promise<ExecutionConfigResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/execution-config`, {
        method: 'PUT',
        body: command,
        csrf,
        signal,
      });
      return decodeExecutionConfigResponse(body);
    },

    async getGitRemoteStatus(workspaceId: string, signal?: AbortSignal): Promise<GitRemoteStatusResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/git-remote-status`, { signal });
      return decodeGitRemoteStatusResponse(body);
    },

    // ---- kowa.workitem.v1 --------------------------------------------------
    // The intake command carries its idempotency key in the body (`requestKey`),
    // exactly as the owner freezes it; `Idempotency-Key` stays the S01 header
    // channel and is not sent here.

    async createWorkItem(workspaceId: string, command: CreateWorkItemCommand, csrf: string, signal?: AbortSignal): Promise<WorkItemResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/work-items`, {
        method: 'POST',
        body: command,
        csrf,
        signal,
      });
      return decodeWorkItemResponse(body);
    },

    async listWorkItems(workspaceId: string, signal?: AbortSignal): Promise<WorkItemListResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/work-items`, { signal });
      return decodeWorkItemListResponse(body);
    },

    async getWorkItem(workspaceId: string, workItemId: string, signal?: AbortSignal): Promise<WorkItemResponse> {
      const { body } = await request(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(workItemId)}`,
        { signal },
      );
      return decodeWorkItemResponse(body);
    },

    // ---- kowa.web-application.v1 ------------------------------------------

    async startRun(
      workspaceId: string,
      workItemId: string,
      command: StartRunCommand,
      csrf: string,
      signal?: AbortSignal,
    ): Promise<AdmissionResponse> {
      const { body } = await request(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(workItemId)}/runs`,
        { method: 'POST', body: command, csrf, signal },
      );
      return decodeAdmissionResponse(body);
    },

    async getRunDetail(workspaceId: string, workItemId: string, runId: string, signal?: AbortSignal): Promise<RunDetail> {
      const { body } = await request(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(workItemId)}/runs/${encodeURIComponent(runId)}`,
        { signal },
      );
      return decodeRunDetail(body);
    },

    // ---- kowa.delivery-governance.v1 (human tasks) -------------------------

    async listHumanTasks(workspaceId: string, signal?: AbortSignal): Promise<HumanTaskListResponse> {
      const { body } = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}/human-tasks`, { signal });
      return decodeHumanTaskListResponse(body);
    },

    async getHumanTask(workspaceId: string, humanTaskId: string, signal?: AbortSignal): Promise<HumanTaskResponse> {
      const { body } = await request(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/human-tasks/${encodeURIComponent(humanTaskId)}`,
        { signal },
      );
      return decodeHumanTaskResponse(body);
    },

    async answerHumanTask(
      workspaceId: string,
      humanTaskId: string,
      command: AnswerHumanTaskCommand,
      csrf: string,
      signal?: AbortSignal,
    ): Promise<HumanTaskResponse> {
      const { body } = await request(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/human-tasks/${encodeURIComponent(humanTaskId)}/responses`,
        { method: 'POST', body: command, csrf, signal },
      );
      return decodeHumanTaskResponse(body);
    },
  };
}
