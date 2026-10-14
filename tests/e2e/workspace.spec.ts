import { expect, test } from '@playwright/test';
import { actor, isControlPlane } from './support/mode';
import { workspaceConfigFixture } from '../fixtures/contracts';
import { installApi, type FixtureServer } from './support/fixtures';

async function signIn(page: import('@playwright/test').Page, fixtures?: FixtureServer) {
  await page.goto('/login');
  await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
  await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
  return fixtures;
}

test('a member creates a workspace and configures the dual repositories', async ({ page }) => {
  const fixtures = await installApi(page);
  await signIn(page, fixtures);
  await page.getByLabel('名称').fill('产品团队');
  await page.getByRole('button', { name: '创建并进入设置' }).click();
  await expect(page.getByRole('heading', { name: '产品团队' })).toBeVisible();
  const settings = page.getByRole('region', { name: '项目仓与知识仓' });
  await expect(settings.getByLabel('Repository ID', { exact: true }).first()).toBeVisible();
  await expect(page.getByText(/项目仓是唯一可写绑定/)).toBeVisible();
  await expect(page.getByText(/知识仓始终只读/)).toBeVisible();
});

test('a workspace can be opened by a known id and is authorized by the server', async ({ page }) => {
  const fixtures = await installApi(page);
  await signIn(page, fixtures);
  await page.getByLabel('Workspace ID').fill('workspace-a1b2c3d4');
  await page.getByRole('button', { name: '打开' }).click();
  await expect(page.getByText(/当前版本 v2/)).toBeVisible();
  await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
  await expect(page.getByText(/我的 Workspace 角色/)).toBeVisible();
});

test('the runtime view exposes the read-only git remote projection', async ({ page }) => {
  const fixtures = await installApi(page);
  await signIn(page, fixtures);
  await page.goto('/workspaces/workspace-a1b2c3d4/runtimes');
  const remote = page.getByRole('region', { name: 'Git remote 可用性与权限缺口' });
  await expect(remote).toBeVisible();
  await expect(remote.getByRole('table', { name: /按仓库的只读观测/ })).toBeVisible();
});

test.describe('fixture-mode negative journeys', () => {
  test.skip(isControlPlane, 'fixture-mode scenario');

  test('a version conflict requires an explicit confirmation against the new version', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page, fixtures);
    let version = 2;
    let writes = 0;
    // Another writer advances the Workspace between the submit and the refetch.
    fixtures?.set('PUT', '/api/v1/workspaces/[^/]+/repositories', () => {
      writes += 1;
      if (writes === 1) {
        version = 3;
        return { status: 409, body: { error: 'VERSION_CONFLICT' } };
      }
      return { status: 200, body: workspaceConfigFixture({ version: 4 }) };
    });
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+$', () => ({
      status: 200,
      body: workspaceConfigFixture({ version }),
    }));
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    await expect(page.getByText('当前版本 v2')).toBeVisible();
    await page.getByRole('button', { name: /保存（基于 v2）/ }).click();
    await expect(page.getByText(/版本冲突：服务端当前为 v3/)).toBeVisible();
    // Nothing was replayed silently: exactly one write reached the server.
    expect(writes).toBe(1);
    await page.getByRole('button', { name: '确认并按 v3 重新提交' }).click();
    await expect(page.getByText(/已保存为版本 v4/)).toBeVisible();
    expect(writes).toBe(2);
  });

  test('a second writable project repository is refused as POLICY_BLOCKED', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page, fixtures);
    fixtures?.set('PUT', '/api/v1/workspaces/[^/]+/repositories', () => ({ status: 422, body: { error: 'POLICY_BLOCKED' } }));
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    await page.getByRole('button', { name: /保存（基于 v2）/ }).click();
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('POLICY_BLOCKED');
    await expect(alert).toContainText(/第二个可写项目仓/);
  });

  test('a non-member sees the server FORBIDDEN category, not an invented empty workspace', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page, fixtures);
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+$', () => ({ status: 403, body: { error: 'FORBIDDEN' } }));
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('FORBIDDEN');
    await expect(alert).toContainText(/没有该对象或 Workspace 的权限/);
  });

  test('a viewer is read-only while the server keeps enforcing writes', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page, fixtures);
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+$', () => ({
      status: 200,
      body: {
        workspaceId: 'workspace-a1b2c3d4',
        name: 'Kowa',
        version: 2,
        members: [
          { githubUserId: actor.githubUserId, role: 'viewer' },
          { githubUserId: '202', role: 'admin' },
        ],
      },
    }));
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    await expect(page.getByText('只读视图')).toBeVisible();
    await expect(page.getByRole('button', { name: /保存（基于/ })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '添加/更新成员' })).toHaveCount(0);
  });
});

test.describe('control-plane negative journeys', () => {
  test.skip(!isControlPlane, 'requires the real control plane');

  test('a workspace created by another member is refused for the current identity', async ({ page }) => {
    await signIn(page);
    await page.getByLabel('名称').fill('后端团队');
    await page.getByRole('button', { name: '创建并进入设置' }).click();
    const workspaceId = (await page.locator('code').first().textContent())?.trim() ?? '';
    expect(workspaceId.length).toBeGreaterThan(0);
    // Sign out, sign in as the second identity and open the same id.
    await page.getByRole('button', { name: '登出' }).click();
    await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
    await page.goto('/login?as=member-b');
    await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
    await page.goto(`/workspaces/${workspaceId}/settings`);
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('FORBIDDEN');
  });
});
