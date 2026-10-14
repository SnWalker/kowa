import { expect, type Page, type APIRequestContext } from '@playwright/test';
import { installApi } from './fixtures';
import { actor, isControlPlane } from './mode';

/**
 * Journeys shared by both targets. Fixture mode reads the workspace the double
 * serves; control-plane mode creates a real one through the real command, so
 * the same assertions hold against real state instead of invented state.
 */
export const FIXTURE_WORKSPACE = 'workspace-a1b2c3d4';

export async function signIn(page: Page, target = '/login'): Promise<void> {
  await page.goto(target);
  const login = page.getByRole('button', { name: '使用 GitHub 登录' });
  if (await login.count() === 0) {
    await page.goto('/workspaces');
    return;
  }
  await login.click();
  await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
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

export async function switchIdentity(stubOrigin: string, login: string): Promise<void> {
  const response = await fetch(`${stubOrigin}/stub/identity`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login }),
  });
  expect(response.status).toBe(200);
}
