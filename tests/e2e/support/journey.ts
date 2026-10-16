import { expect, type BrowserContext, type Page, type APIRequestContext } from '@playwright/test';
import { installApi } from './fixtures';
import { actor, controlPlaneStub, isControlPlane } from './mode';

/**
 * Journeys shared by both targets. Fixture mode reads the workspace the double
 * serves; control-plane mode creates a real one through the real command, so
 * the same assertions hold against real state instead of invented state.
 */
export const FIXTURE_WORKSPACE = 'workspace-a1b2c3d4';

export async function signIn(page: Page, target = '/login', expected: { login: string } = actor): Promise<void> {
  await page.goto(target);
  const login = page.getByRole('button', { name: '使用 GitHub 登录' });
  const identity = page.getByText(`登录身份：${expected.login}`);
  if (await login.count() === 0) {
    await page.goto('/workspaces');
    await expect(identity).toBeVisible();
    return;
  }
  // The owner decides where an authenticated browser goes: /login redirects to
  // the return target as soon as the session snapshot lands, so the button this
  // helper just saw can be replaced mid-click. A click the redirect wins means
  // a browser that is already signed in — the identity assertion below is what
  // this helper really claims, and it stays strict (an unauthenticated or
  // broken login still fails here, which is why only the click is guarded).
  await login.click({ timeout: 5_000 }).catch(() => undefined);
  await expect(identity).toBeVisible();
}

/** Signs in when needed and returns a workspace the actor may read. */
export async function ensureWorkspace(page: Page): Promise<string> {
  if (!isControlPlane) {
    await installApi(page);
    await signIn(page);
    return FIXTURE_WORKSPACE;
  }
  await signIn(page);
  await page.goto('/workspaces');
  await page.getByLabel('名称').fill(`验收工作区 ${Date.now().toString(36)}`);
  await page.getByRole('button', { name: '创建并进入设置' }).click();
  await expect(page.getByRole('region', { name: '项目仓与知识仓' })).toBeVisible();
  const workspaceId = (await page.locator('code').first().textContent())?.trim() ?? '';
  expect(workspaceId).not.toBe('');
  return workspaceId;
}

/** Drives the real Runner transport for the control-plane runtime journey. */
export async function registerRuntime(request: APIRequestContext, runtimeId: string, token: string, epoch: string): Promise<void> {
  const response = await request.post('/api/v1/runtime/register', {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      schemaVersion: 'kowa.workflow-execution.v1',
      kind: 'runtimeRegistration',
      runtimeId,
      runtimeEpoch: epoch,
      gitHubUserId: '135722937',
      provider: { id: 'claude', version: '2.1.296 (Claude Code)' },
      providerAuthenticated: true,
      capabilities: [{ id: 'plan.create', version: 1 }],
      registeredAt: new Date().toISOString(),
    },
  });
  expect(response.status()).toBe(200);
}

export async function reportObservation(request: APIRequestContext, runtimeId: string, token: string, epoch: string): Promise<void> {
  const response = await request.post('/api/v1/runtime/observations', {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      runtimeId,
      runtimeEpoch: epoch,
      observedAt: new Date().toISOString(),
      executable: true,
      cliVersion: '2.1.296 (Claude Code)',
      versionCompatible: true,
      authenticated: true,
      canExecute: true,
    },
  });
  expect(response.status()).toBe(200);
}

/**
 * Selects the identity the OAuth stub issues for this browser context only.
 * The stub reads the cookie on the stub origin, so one journey can never change
 * another journey's login — including when a test fails mid-switch.
 */
/**
 * Fills both repository groups with distinct, contract-shaped values. A
 * workspace created through the real command starts without bindings, so a
 * submission needs every required field typed first.
 */
export async function fillDualRepositories(page: Page, projectId: string, knowledgeId: string): Promise<void> {
  const repositoryIds = page.getByLabel('Repository ID', { exact: true });
  await repositoryIds.first().fill(projectId);
  await repositoryIds.nth(1).fill(knowledgeId);
  await page.getByLabel('Installation ID').first().fill('4001');
  await page.getByLabel('Installation ID').nth(1).fill('4001');
  await page.getByLabel('显示名').first().fill('team/project');
  await page.getByLabel('显示名').nth(1).fill('team/knowledge');
}

export async function switchIdentity(context: BrowserContext, login: string): Promise<void> {
  expect(controlPlaneStub, 'control-plane stub origin is required').not.toBe('');
  await context.addCookies([{ name: 'kowa-harness-login', value: login, url: controlPlaneStub }]);
}

/** The controlled repositories, plan identity and runtime facts the harness publishes. */
export type HarnessFixtures = {
  project: { repositoryId: string; installationId: string; displayName: string };
  knowledge: { repositoryId: string; installationId: string; displayName: string };
  knowledgeEmpty: { repositoryId: string; installationId: string; displayName: string };
  knowledgeUnavailable: { repositoryId: string; installationId: string; displayName: string };
  definition: { name: string; version: number; digest: string };
  capabilities: { id: string; version: number }[];
  runtime: { runtimeId: string; epoch: string; providerId: string; providerVersion: string };
};

/**
 * One browser project's runtime: the harness publishes the identity, and the
 * token stays in the acceptance process environment (the harness binds a token
 * to exactly one engine-scoped runtime).
 */
export type HarnessRuntime = HarnessFixtures['runtime'] & { token: string };

/**
 * Reads the harness facts instead of hardcoding ids in the journey: the local
 * bare repositories, the compiled fixed definition and the scripted runtime
 * are produced by the harness itself.
 */
export async function harnessFixtures(request: APIRequestContext): Promise<HarnessFixtures> {
  expect(controlPlaneStub, 'control-plane stub origin is required').not.toBe('');
  const response = await request.get(`${controlPlaneStub}/stub/harness/fixtures`);
  expect(response.status()).toBe(200);
  return (await response.json()) as HarnessFixtures;
}

/**
 * Registers the scripted runtime with exactly the capabilities the frozen
 * definition needs, over the real Runner transport.
 */
export async function registerFixedFlowRuntime(
  request: APIRequestContext,
  runtime: HarnessRuntime,
  capabilities: { id: string; version: number }[],
): Promise<void> {
  const response = await request.post('/api/v1/runtime/register', {
    headers: { Authorization: `Bearer ${runtime.token}` },
    data: {
      schemaVersion: 'kowa.workflow-execution.v1',
      kind: 'runtimeRegistration',
      runtimeId: runtime.runtimeId,
      runtimeEpoch: runtime.epoch,
      gitHubUserId: '135722937',
      provider: { id: runtime.providerId, version: runtime.providerVersion },
      providerAuthenticated: true,
      capabilities,
      registeredAt: new Date().toISOString(),
    },
  });
  expect(response.status()).toBe(200);
}

/** Publishes the execution configuration the admission checks, through the real page. */
export async function publishExecutionConfig(
  page: Page,
  workspaceId: string,
  runtimeId: string,
  capabilities: { id: string; version: number }[],
): Promise<void> {
  await page.goto(`/workspaces/${workspaceId}/runtimes`);
  const section = page.getByRole('region', { name: '执行配置' });
  await section.getByLabel('能力列表').fill(capabilities.map((capability) => `${capability.id}@${capability.version}`).join('\n'));
  await section.getByLabel('按 ID 授权 Runtime').fill(runtimeId);
  await section.getByRole('button', { name: '添加 Runtime' }).click();
  await section.getByRole('button', { name: /保存执行配置（基于 v/ }).click();
  await expect(section.getByText(/已保存为版本|已保存执行配置/)).toBeVisible();
}

/**
 * Sends one request from the page's own origin, so the browser attaches the
 * `\_\_Host-` session cookie and the Origin header exactly as the application
 * does. The acceptance runner uses it to read projections it asserts against
 * and to send the negative writes the UI never composes; it never fabricates a
 * credential.
 */
export async function pageRequest(
  page: Page,
  method: string,
  path: string,
  payload?: unknown,
  csrf?: string,
): Promise<{ status: number; body: unknown }> {
  return await page.evaluate(
    async (input: { method: string; path: string; payload?: unknown; csrf?: string }) => {
      const headers: Record<string, string> = { Accept: 'application/json' };
      if (input.payload !== undefined) headers['Content-Type'] = 'application/json';
      if (input.csrf) headers['X-CSRF-Token'] = input.csrf;
      const response = await fetch(input.path, {
        method: input.method,
        headers,
        credentials: 'same-origin',
        cache: 'no-store',
        body: input.payload === undefined ? undefined : JSON.stringify(input.payload),
      });
      const text = await response.text();
      let body: unknown;
      try {
        body = text === '' ? undefined : JSON.parse(text);
      } catch {
        body = text;
      }
      return { status: response.status, body };
    },
    { method, path, payload, csrf },
  );
}

/** The CSRF token of the page's own session, read from the frozen recovery snapshot. */
export async function pageCsrf(page: Page): Promise<string> {
  const result = await pageRequest(page, 'POST', '/api/v2/auth/session', { requestId: `e2e-csrf-${Date.now()}` });
  expect(result.status).toBe(200);
  const csrf = (result.body as { csrfToken?: string }).csrfToken;
  expect(csrf).toBeTruthy();
  return csrf as string;
}

/**
 * A dispatch assertion is scoped to the run under test: one scripted runtime
 * serves every workspace of this harness, so a task of another run must never
 * be read as this run's dispatch. Task identities embed their run id.
 */
export function taskOfRun(task: unknown, runId: string): boolean {
  return typeof (task as { taskId?: string } | null)?.taskId === 'string'
    && ((task as { taskId: string }).taskId.startsWith(`${runId}:task:`));
}

/** Leases until a task of this run appears, ignoring (never claiming) other runs' tasks. */
export async function leaseRunTask(
  request: APIRequestContext,
  runtime: HarnessRuntime,
  runId: string,
  attempts = 20,
): Promise<{ status: number; capability?: string }> {
  for (let index = 0; index < attempts; index += 1) {
    const lease = await leaseTask(request, runtime);
    if (lease.status === 204) continue;
    if (taskOfRun(lease.task, runId)) return { status: 200, capability: lease.capability };
  }
  return { status: 204 };
}

/** Proves the transport cannot obtain any task of this run. */
export async function expectNoRunTask(
  request: APIRequestContext,
  runtime: HarnessRuntime,
  runId: string,
  attempts = 3,
): Promise<void> {
  for (let index = 0; index < attempts; index += 1) {
    const lease = await leaseTask(request, runtime);
    if (lease.status === 204) continue;
    expect(taskOfRun(lease.task, runId), `run ${runId} dispatched ${JSON.stringify(lease.task)}`).toBe(false);
  }
}

/** Leases one task over the frozen Runner transport; 204 means nothing is dispatched. */
export async function leaseTask(
  request: APIRequestContext,
  runtime: HarnessRuntime,
): Promise<{ status: number; capability?: string; task?: unknown }> {
  const response = await request.post('/api/v1/runtime/tasks/lease', {
    headers: { Authorization: `Bearer ${runtime.token}` },
    data: { runtimeId: runtime.runtimeId, runtimeEpoch: runtime.epoch, waitSeconds: 1, leaseSeconds: 30 },
  });
  if (response.status() === 204) return { status: 204 };
  const body = (await response.json()) as { task?: { capability?: { id?: string } } };
  return { status: response.status(), capability: body.task?.capability?.id, task: body.task };
}
