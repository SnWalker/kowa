import { expect, test } from '@playwright/test';
import { actor, isControlPlane } from './support/mode';
import { installApi } from './support/fixtures';
import { ensureWorkspace } from './support/journey';

test.describe('session and guards', () => {
  test('an anonymous visitor is sent to the login page and never sees workspace content', async ({ page }) => {
    await installApi(page);
    await page.goto('/workspaces');
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
    await expect(page).toHaveURL(/\/login\?returnTo=/);
    await expect(page.getByRole('heading', { name: 'Workspace' })).toHaveCount(0);
  });

  test('opening a deep workspace link while anonymous returns there after login', async ({ page }) => {
    const workspaceId = await ensureWorkspace(page);
    // Return to an anonymous visitor through the real logout command.
    await page.getByRole('button', { name: '登出' }).click();
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
    const target = `/workspaces/${workspaceId}/runtimes`;
    const fixtures = await installApi(page);
    await page.goto(target);
    await expect(page).toHaveURL(new RegExp(`returnTo=${encodeURIComponent(target)}`));
    await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
    await expect(page.getByText(`登录身份：${actor.login}（${actor.githubUserId}）`)).toBeVisible();
    expect(new URL(page.url()).pathname).toBe(target);
    await expect(page.getByRole('heading', { name: 'Runtime 与执行环境' })).toBeVisible();
    if (fixtures) expect(fixtures.calls.some((call) => call.path === '/api/v2/auth/session')).toBe(true);
  });

  test('the browser keeps no session credential outside memory', async ({ page }) => {
    await installApi(page);
    await page.goto('/login');
    await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
    await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
    expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length }))).toEqual({ local: 0, session: 0 });
    expect(page.url()).not.toContain('csrf');
  });

  test('logout revokes the session and returns to the login page', async ({ page }) => {
    await installApi(page);
    await page.goto('/workspaces');
    await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
    await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
    await page.getByRole('button', { name: '登出' }).click();
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
    await page.goto('/workspaces');
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
  });

  test('a second tab reacts to a logout in the first tab without sharing credentials', async ({ context, page }) => {
    const second = await context.newPage();
    const shared = { authenticated: false };
    await installApi(page, shared);
    await installApi(second, shared);
    await page.goto('/workspaces');
    await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
    // The first tab must have completed the handoff before the second one
    // reads the shared session state.
    await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
    await second.goto('/workspaces');
    await expect(second.getByText(`登录身份：${actor.login}`)).toBeVisible();
    // Logging out must not leave the other tab showing authenticated content.
    await page.getByRole('button', { name: '登出' }).click();
    await expect(second.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
  });
});

test.describe('session failure handling', () => {
  test.skip(isControlPlane, 'fixture-mode scenario: the control plane owns real revocation');

  test('an expired or revoked session drops authenticated state on the next request', async ({ page }) => {
    const fixtures = await installApi(page);
    await page.goto('/workspaces');
    await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
    await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    await expect(page.getByText('当前版本 v2')).toBeVisible();
    fixtures?.signOut();
    await page.reload();
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
    await expect(page.getByText(`登录身份：${actor.login}`)).toHaveCount(0);
  });

  test('an unavailable identity service is not presented as a logged-out success', async ({ page }) => {
    await page.route('**/api/v2/auth/session', (route) => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'RESOURCE_UNAVAILABLE' }) }));
    await page.goto('/workspaces');
    await expect(page.getByText('会话状态暂时无法确认')).toBeVisible();
    await expect(page.getByText(/身份\/外部依赖当前不可用/)).toBeVisible();
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toHaveCount(0);
  });
});
