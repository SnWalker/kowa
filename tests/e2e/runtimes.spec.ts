import { expect, test } from '@playwright/test';
import { installApi, type FixtureServer } from './support/fixtures';
import { ensureWorkspace, registerRuntime, reportObservation, signIn } from './support/journey';
import { actor, isControlPlane } from './support/mode';

async function signedInWithDouble(page: import('@playwright/test').Page): Promise<FixtureServer | undefined> {
  const fixtures = await installApi(page);
  await signIn(page);
  return fixtures;
}

test('the runtime page separates freshness, provider, capabilities and the git remote projection', async ({ page }) => {
  const workspaceId = await ensureWorkspace(page);
  await page.goto(`/workspaces/${workspaceId}/runtimes`);
  await expect(page.getByRole('region', { name: 'Runtime 列表' })).toBeVisible();
  await expect(page.getByRole('region', { name: '执行配置' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Git remote 可用性与权限缺口' })).toBeVisible();
});

test('a runtime detail keeps the capability ladder separate', async ({ page }) => {
  const workspaceId = await ensureWorkspace(page);
  const runtimeId = isControlPlane ? (process.env.KOWA_E2E_RUNTIME_ID ?? 'runtime-harness') : 'runtime-mac-codex';
  await page.goto(`/workspaces/${workspaceId}/runtimes/${runtimeId}`);
  // Fixture mode renders the authorized runtime; the control plane reports the
  // real refusal (no authorized runtime) instead of inventing a detail view.
  await expect(page.getByRole('heading', { level: 1 })).toContainText(runtimeId);
});

test.describe('fixture-mode runtime states', () => {
  test.skip(isControlPlane, 'fixture-mode scenario');

  test('the execution configuration is shown verbatim with its capabilities', async ({ page }) => {
    await signedInWithDouble(page);
    await page.goto('/workspaces/workspace-a1b2c3d4/runtimes');
    const config = page.getByRole('region', { name: '执行配置' });
    await expect(config.getByText('code.change@2、plan.create@1')).toBeVisible();
    await expect(config.getByText('v3', { exact: true })).toBeVisible();
  });

  test('fresh, stale and unknown runtime states never collapse into one healthy answer', async ({ page }) => {
    await signedInWithDouble(page);
    await page.goto('/workspaces/workspace-a1b2c3d4/runtimes');
    const list = page.getByRole('region', { name: 'Runtime 列表' });
    await expect(list.getByText('新鲜')).toBeVisible();
    await expect(list.getByText('陈旧（expired）')).toBeVisible();
    await expect(list.getByText('未知', { exact: true })).toBeVisible();
    await expect(list.getByText(/未知不是可用/)).toBeVisible();
    await expect(list.getByText(/不表示可用/).first()).toBeVisible();
  });

  test('the git remote projection shows unknown states, drift and the read-only write observation', async ({ page }) => {
    const fixtures = await signedInWithDouble(page);
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+/git-remote-status', () => ({
      status: 200,
      body: {
        schemaVersion: 'kowa.delivery-governance.v1',
        kind: 'gitRemoteStatus',
        status: {
          schemaVersion: 'kowa.delivery-governance.v1',
          kind: 'gitRemoteStatus',
          workspaceId: 'workspace-a1b2c3d4',
          requestedByGitHubUserId: actor.githubUserId,
          probeOutcome: 'completed',
          observedAt: '2026-10-15T02:00:00Z',
          machineIdentity: {
            runtimeIds: ['runtime-mac-codex'],
            registeredGitHubUserIds: ['135722937'],
            observedGitHubUserId: '999999',
            state: 'drifted',
            reason: 'the GitHub query identity is not a registered machine identity',
          },
          repositories: [
            {
              repositoryId: '3001',
              role: 'project',
              remoteRead: { state: 'readable', transport: 'ssh' },
              pushPermission: { state: 'denied', reason: 'GitHub reports read-only access' },
              writeObservation: 'none',
              observedAt: '2026-10-15T02:00:00Z',
              observedByGitHubUserId: '999999',
            },
          ],
        },
      },
    }));
    await page.goto('/workspaces/workspace-a1b2c3d4/runtimes');
    const remote = page.getByRole('region', { name: 'Git remote 可用性与权限缺口' });
    await expect(remote.getByText(/身份漂移：登记 135722937，实际查询 999999/)).toBeVisible();
    await expect(remote.getByText(/只读探测成功（ssh）/)).toBeVisible();
    await expect(remote.getByText(/GitHub 报告无 push 权限/)).toBeVisible();
    await expect(remote.getByText('无（writeObservation=none）')).toBeVisible();
  });

  test('a projection that claims a write observation is refused as a contract violation', async ({ page }) => {
    const fixtures = await signedInWithDouble(page);
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+/git-remote-status', () => ({
      status: 200,
      body: {
        schemaVersion: 'kowa.delivery-governance.v1',
        kind: 'gitRemoteStatus',
        status: {
          schemaVersion: 'kowa.delivery-governance.v1',
          kind: 'gitRemoteStatus',
          workspaceId: 'workspace-a1b2c3d4',
          requestedByGitHubUserId: actor.githubUserId,
          probeOutcome: 'completed',
          observedAt: '2026-10-15T02:00:00Z',
          machineIdentity: { runtimeIds: [], registeredGitHubUserIds: [], state: 'unknown' },
          repositories: [
            {
              repositoryId: '3001',
              role: 'project',
              remoteRead: { state: 'readable', transport: 'https' },
              pushPermission: { state: 'granted' },
              writeObservation: 'succeeded',
              observedAt: '2026-10-15T02:00:00Z',
            },
          ],
        },
      },
    }));
    await page.goto('/workspaces/workspace-a1b2c3d4/runtimes');
    await expect(page.getByRole('alert')).toContainText(/合同|writeObservation|契约违规/);
    await expect(page.getByText('只读探测成功')).toHaveCount(0);
  });

  test('the execution configuration write follows a version conflict explicitly', async ({ page }) => {
    const fixtures = await signedInWithDouble(page);
    let writes = 0;
    fixtures?.set('PUT', '/api/v1/workspaces/[^/]+/execution-config', () => {
      writes += 1;
      return writes === 1
        ? { status: 409, body: { error: 'VERSION_CONFLICT' } }
        : { status: 200, body: { schemaVersion: 'kowa.runtime-web.v1', kind: 'executionConfig', config: { workspaceId: 'workspace-a1b2c3d4', version: 5, capabilities: [{ id: 'plan.create', version: 1 }], runtimes: ['runtime-mac-codex'] } } };
    });
    fixtures?.set('GET', '/api/v1/workspaces/[^/]+/execution-config', () => ({
      status: 200,
      body: { schemaVersion: 'kowa.runtime-web.v1', kind: 'executionConfig', config: { workspaceId: 'workspace-a1b2c3d4', version: 4, capabilities: [], runtimes: [] } },
    }));
    await page.goto('/workspaces/workspace-a1b2c3d4/runtimes');
    const config = page.getByRole('region', { name: '执行配置' });
    await config.getByRole('button', { name: /保存执行配置（基于 v4）/ }).click();
    await expect(page.getByText(/版本冲突：服务端当前为 v4/)).toBeVisible();
    expect(writes).toBe(1);
    await page.getByRole('button', { name: '确认并重新提交' }).click();
    await expect(page.getByText(/已保存为版本 v5/)).toBeVisible();
    expect(writes).toBe(2);
  });
});

test.describe('control-plane runtime journey', () => {
  test.skip(!isControlPlane, 'requires the real control plane');

  test('a real runtime registration becomes visible only through the authorized execution configuration', async ({ page, request }) => {
    const runtimeId = process.env.KOWA_E2E_RUNTIME_ID ?? 'runtime-harness';
    const token = process.env.KOWA_E2E_RUNTIME_TOKEN ?? '';
    const epoch = `epoch-web-e2e-${Date.now().toString(36)}`;
    expect(token).not.toBe('');
    const workspaceId = await ensureWorkspace(page);
    await registerRuntime(request, runtimeId, token, epoch);

    // Without authorization the runtime must not appear in the workspace.
    await page.goto(`/workspaces/${workspaceId}/runtimes`);
    const list = page.getByRole('region', { name: 'Runtime 列表' });
    await expect(list.getByText(/没有授权任何 Runtime/)).toBeVisible();

    // Authorize it through the real admin execution-configuration write.
    const config = page.getByRole('region', { name: '执行配置' });
    await expect(config.getByText('v0', { exact: true })).toBeVisible();
    await config.getByRole('checkbox', { name: runtimeId }).check();
    await config.getByLabel('能力列表').fill('plan.create@1');
    await config.getByRole('button', { name: /保存执行配置（基于 v0）/ }).click();
    await expect(page.getByText(/已保存为版本 v1/)).toBeVisible();
    await expect(list.getByText(runtimeId)).toBeVisible();
    // Registered but unobserved stays unknown, and unknown is not usable.
    await expect(list.getByText('未知', { exact: true })).toBeVisible();

    // A real observation over the transport turns the projection fresh.
    await reportObservation(request, runtimeId, token, epoch);
    await page.reload();
    await expect(page.getByRole('region', { name: 'Runtime 列表' }).getByText('新鲜')).toBeVisible();

    // The authorized runtime has a real detail view.
    await page.goto(`/workspaces/${workspaceId}/runtimes/${runtimeId}`);
    await expect(page.getByRole('region', { name: 'Runtime 状态' })).toBeVisible();
    await expect(page.getByRole('region', { name: '最近观测' })).toBeVisible();

    // The Git remote projection answers for the same real workspace.
    await page.goto(`/workspaces/${workspaceId}/runtimes`);
    const remote = page.getByRole('region', { name: 'Git remote 可用性与权限缺口' });
    await expect(remote.getByText(/请求者（Web actor）/)).toBeVisible();
  });
});
