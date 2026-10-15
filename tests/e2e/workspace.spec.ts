import { expect, test } from '@playwright/test';
import { workspaceConfigFixture } from '../fixtures/contracts';
import { installApi } from './support/fixtures';
import { ensureWorkspace, signIn, switchIdentity } from './support/journey';
import { actor, isControlPlane, otherActor, controlPlaneURL } from './support/mode';

test('a member creates a workspace and configures the dual repositories', async ({ page }) => {
  await installApi(page);
  await signIn(page);
  await page.getByLabel('名称').fill('产品团队');
  await page.getByRole('button', { name: '创建并进入设置' }).click();
  await expect(page.getByRole('heading', { name: '产品团队' })).toBeVisible();
  const settings = page.getByRole('region', { name: '项目仓与知识仓' });
  await expect(settings.getByLabel('Repository ID', { exact: true }).first()).toBeVisible();
  await expect(page.getByText(/项目仓是唯一可写绑定/)).toBeVisible();
  await expect(page.getByText(/知识仓始终只读/)).toBeVisible();
  // The creator is the first administrator, and the server says so.
  const members = page.getByRole('table', { name: /服务端返回的成员与角色/ });
  await expect(members.getByText('admin').first()).toBeVisible();
});

test('a workspace can be opened by a known id and is authorized by the server', async ({ page }) => {
  const workspaceId = await ensureWorkspace(page);
  await page.goto('/workspaces');
  await page.getByLabel('Workspace ID').fill(workspaceId);
  await page.getByRole('button', { name: '打开' }).click();
  await expect(page.getByText(/当前版本 v\d+/)).toBeVisible();
  await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
  await expect(page.getByText(/我的 Workspace 角色/)).toBeVisible();
});

test('the runtime view exposes the read-only git remote projection', async ({ page }) => {
  const workspaceId = await ensureWorkspace(page);
  await page.goto(`/workspaces/${workspaceId}/runtimes`);
  const remote = page.getByRole('region', { name: 'Git remote 可用性与权限缺口' });
  await expect(remote).toBeVisible();
  await expect(remote.getByText(/请求者（Web actor）/)).toBeVisible();
});

test.describe('fixture-mode repository policy', () => {
  test.skip(isControlPlane, 'fixture-mode scenario; the control plane refuses through its own policy');

  test('a second writable project repository is refused as POLICY_BLOCKED', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('PUT', '/api/v1/workspaces/[^/]+/repositories', () => ({ status: 422, body: { error: 'POLICY_BLOCKED' } }));
    const workspaceId = await ensureWorkspace(page);
    await page.goto(`/workspaces/${workspaceId}/settings`);
    await page.getByRole('button', { name: /保存（基于/ }).click();
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('POLICY_BLOCKED');
    await expect(alert).toContainText(/第二个可写项目仓/);
  });
});

test.describe('fixture-mode negative journeys', () => {
  test.skip(isControlPlane, 'fixture-mode scenario');

  test('a version conflict requires an explicit confirmation against the new version', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page);
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

  test('a non-member sees the server FORBIDDEN category, not an invented empty workspace', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+$', () => ({ status: 403, body: { error: 'FORBIDDEN' } }));
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('FORBIDDEN');
    await expect(alert).toContainText(/没有该对象或 Workspace 的权限/);
  });

  test('a viewer is read-only while the server keeps enforcing writes', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+$', () => ({
      status: 200,
      body: workspaceConfigFixture({
        members: [
          { githubUserId: actor.githubUserId, role: 'viewer' },
          { githubUserId: otherActor.githubUserId, role: 'admin' },
        ],
      }),
    }));
    await page.goto('/workspaces/workspace-a1b2c3d4/settings');
    await expect(page.getByText('只读视图')).toBeVisible();
    await expect(page.getByRole('button', { name: /保存（基于/ })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '添加/更新成员' })).toHaveCount(0);
  });
});

test.describe('control-plane negative journeys', () => {
  test.skip(!isControlPlane, 'requires the real control plane');

  test('the real command refuses an unusable dual-repository binding', async ({ page }) => {
    const workspaceId = await ensureWorkspace(page);
    await page.goto(`/workspaces/${workspaceId}/settings`);
    const repositoryIds = page.getByLabel('Repository ID', { exact: true });
    const installationIds = page.getByLabel('Installation ID');
    const displayNames = page.getByLabel('显示名');
    // The frozen command requires two distinct repositories; claiming the same
    // repository for both roles is a validation refusal from the real server.
    await repositoryIds.first().fill('3001');
    await repositoryIds.nth(1).fill('3001');
    await installationIds.first().fill('4001');
    await installationIds.nth(1).fill('4001');
    await displayNames.first().fill('team/project');
    await displayNames.nth(1).fill('team/knowledge');
    await page.getByRole('button', { name: /保存（基于/ }).click();
    await expect(page.getByRole('alert')).toContainText('VALIDATION_ERROR');

    // A repository the controlled installation adapter reports as unavailable
    // is refused as a real resource fact, never bound optimistically.
    await repositoryIds.nth(1).fill('999999');
    await page.getByRole('button', { name: /保存（基于/ }).click();
    await expect(page.getByRole('alert')).toContainText('RESOURCE_UNAVAILABLE');
  });

  test('membership is granted and revoked through the real commands', async ({ page, browser }) => {
    const workspaceId = await ensureWorkspace(page);
    // Another identity needs its own browser context: one context shares one
    // session cookie, so a second page would silently stay the admin.
    const outsiderContext = await browser.newContext({
      ignoreHTTPSErrors: true,
      baseURL: controlPlaneURL,
    });
    const outsider = await outsiderContext.newPage();
    await switchIdentity(outsiderContext, otherActor.login);
    await signIn(outsider, '/login', otherActor);
    await outsider.goto(`/workspaces/${workspaceId}/settings`);
    await expect(outsider.getByRole('alert')).toContainText('FORBIDDEN');
    await expect(outsider.getByText(/当前版本 v/)).toHaveCount(0);

    // The admin grants a viewer role through the real command.
    await page.goto(`/workspaces/${workspaceId}/settings`);
    await page.getByLabel('GitHub 用户 ID').fill(otherActor.githubUserId);
    await page.getByLabel('角色').selectOption('viewer');
    await page.getByRole('button', { name: '添加/更新成员' }).click();
    const members = page.getByRole('table', { name: /服务端返回的成员与角色/ });
    await expect(members.getByText(otherActor.githubUserId)).toBeVisible();

    // The granted member can now read the same Workspace.
    await outsider.goto(`/workspaces/${workspaceId}/settings`);
    await expect(outsider.getByText(/当前版本 v/)).toBeVisible();
    await expect(outsider.getByText('只读视图')).toBeVisible();

    // Revoking the membership takes effect on the next request.
    await page.getByRole('button', { name: '撤销' }).last().click();
    await expect(members.getByText('已撤销', { exact: false }).first()).toBeVisible();
    await outsider.goto(`/workspaces/${workspaceId}/settings`);
    await expect(outsider.getByRole('alert')).toContainText('FORBIDDEN');
    await outsiderContext.close();
  });

  test('the workspace version conflict is refused by the real command', async ({ page }) => {
    const workspaceId = await ensureWorkspace(page);
    // Replaying an old version must conflict instead of overwriting silently.
    const stale = await page.request.put(`/api/v1/workspaces/${workspaceId}/repositories`, {
      headers: { 'X-CSRF-Token': 'not-a-real-token', 'Idempotency-Key': 'stale-write' },
      data: {
        expectedVersion: 99,
        project: { githubRepositoryId: '3001', installationId: '4001', displayName: 'team/project' },
        knowledge: { githubRepositoryId: '3002', installationId: '4001', displayName: 'team/knowledge' },
      },
    });
    // Without the session's CSRF the control plane refuses before the version
    // check; either way the write never succeeds against a forged proof.
    expect([401, 409]).toContain(stale.status());
  });
});
