import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import {
  executionConfigFixture,
  gitRemoteStatusFixture,
  runtimeListFixture,
  runtimeViewFixture,
} from '../fixtures/contracts';
import type { GitRemoteStatus } from '../../src/api/contracts';
import { createFakeFetch, renderApp, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
const LIST_PATH = `GET /api/v1/workspaces/${WORKSPACE}/runtimes`;
const VIEW_PATH = `GET /api/v1/workspaces/${WORKSPACE}/runtimes/runtime-mac-claude`;
const CONFIG_PATH = `GET /api/v1/workspaces/${WORKSPACE}/execution-config`;
const CONFIG_WRITE = `PUT /api/v1/workspaces/${WORKSPACE}/execution-config`;
const GIT_REMOTE_PATH = `GET /api/v1/workspaces/${WORKSPACE}/git-remote-status`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function render(entry: string, routes: Routes) {
  const { fetchImpl, calls } = createFakeFetch({ 'POST /api/v2/auth/session': sessionRoute(), ...routes });
  const user = userEvent.setup();
  renderApp({ fetch: fetchImpl, initialEntries: [entry] });
  return { calls, user };
}

const baseRoutes = (): Routes => ({
  [LIST_PATH]: { status: 200, body: runtimeListFixture() },
  [CONFIG_PATH]: { status: 200, body: executionConfigFixture() },
  [GIT_REMOTE_PATH]: { status: 200, body: gitRemoteStatusFixture() },
});

describe('runtime pages', () => {
  it('keeps the three freshness states separate and never shows stale or unknown as usable', async () => {
    render(`/workspaces/${WORKSPACE}/runtimes`, baseRoutes());
    expect(await screen.findByRole('link', { name: 'runtime-mac-codex' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'runtime-mac-unobserved' })).toBeInTheDocument();
    expect(screen.getByText('新鲜')).toBeInTheDocument();
    expect(screen.getByText('陈旧（expired）')).toBeInTheDocument();
    expect(screen.getByText('未知')).toBeInTheDocument();
    expect(screen.getByText(/该 Runtime 从未上报观测；未知不是可用/)).toBeInTheDocument();
    expect(screen.getByText(/观测水位已过期；仅保留审计，不表示可用/)).toBeInTheDocument();
    // Provider identity is a separate fact from the machine GitHub identity.
    expect(screen.getAllByText('登记机器 GitHub 用户 ID').length).toBeGreaterThan(0);
  });

  it('shows the execution configuration with its version and authorized runtimes', async () => {
    render(`/workspaces/${WORKSPACE}/runtimes`, baseRoutes());
    const config = await screen.findByRole('region', { name: '执行配置' });
    expect(within(config).getByText(/当前版本/)).toBeInTheDocument();
    expect(within(config).getByText('v3')).toBeInTheDocument();
    expect(within(config).getByText(/runtime-mac-claude、runtime-mac-codex/)).toBeInTheDocument();
  });

  it('presents the read-only git remote projection with unknown states intact', async () => {
    const unknown: Partial<GitRemoteStatus> = {
      probeOutcome: 'unknown',
      probeReason: 'no verified machine identity observation',
      machineIdentity: { runtimeIds: [], registeredGitHubUserIds: [], state: 'unknown', reason: 'not observed yet' },
      repositories: [
        {
          repositoryId: '3001',
          role: 'project',
          remoteRead: { state: 'unknown', reason: 'no machine identity' },
          pushPermission: { state: 'unknown' },
          writeObservation: 'none',
          observedAt: '2026-10-14T16:12:04Z',
        },
      ],
    };
    render(`/workspaces/${WORKSPACE}/runtimes`, { ...baseRoutes(), [GIT_REMOTE_PATH]: { status: 200, body: gitRemoteStatusFixture(unknown) } });
    const section = await screen.findByRole('region', { name: 'Git remote 可用性与权限缺口' });
    expect(within(section).getByText(/探测未完成/)).toBeInTheDocument();
    expect(within(section).getByText(/机器 GitHub 身份未知/)).toBeInTheDocument();
    expect(within(section).getByText(/未探测/)).toBeInTheDocument();
    expect(within(section).getAllByText('无（writeObservation=none）').length).toBeGreaterThan(0);
    expect(within(section).getByText(/登录、Runtime 注册或 CLI 存在都不构成可用证据/)).toBeInTheDocument();
  });

  it('fails closed when the projection claims a write observation or an unknown field', async () => {
    const claimed = gitRemoteStatusFixture();
    const tampered = {
      ...claimed,
      status: {
        ...claimed.status,
        repositories: [{ ...claimed.status.repositories[0], writeObservation: 'succeeded' }],
      },
    };
    render(`/workspaces/${WORKSPACE}/runtimes`, { ...baseRoutes(), [GIT_REMOTE_PATH]: { status: 200, body: tampered } });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/写入观测|writeObservation|合同不一致/);
  });

  it('refuses a runtime the workspace configuration does not authorize', async () => {
    render(`/workspaces/${WORKSPACE}/runtimes/runtime-mac-claude`, {
      ...baseRoutes(),
      [VIEW_PATH]: { status: 403, body: { error: 'FORBIDDEN' } },
    });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('FORBIDDEN');
  });

  it('renders the runtime detail with separated capability layers', async () => {
    render(`/workspaces/${WORKSPACE}/runtimes/runtime-mac-claude`, {
      ...baseRoutes(),
      [VIEW_PATH]: { status: 200, body: runtimeViewFixture() },
    });
    const state = await screen.findByRole('region', { name: 'Runtime 状态' });
    expect(within(state).getByText(/code.review@1/)).toBeInTheDocument();
    const observation = screen.getByRole('region', { name: '最近观测' });
    expect(within(observation).getByText('可执行（观测结论）')).toBeInTheDocument();
    expect(within(observation).getByText(/probe execution did not complete/)).toBeInTheDocument();
  });

  it('writes the execution configuration with requestKey, expectedVersion and CSRF, then follows a conflict', async () => {
    let version = 3;
    const writes: { expectedVersion: number; requestKey: string }[] = [];
    const { user } = render(`/workspaces/${WORKSPACE}/runtimes`, {
      ...baseRoutes(),
      [CONFIG_PATH]: () => ({ status: 200, body: executionConfigFixture({ version }) }),
      [CONFIG_WRITE]: async (request) => {
        const body = (await request.json()) as { expectedVersion: number; requestKey: string };
        writes.push({ expectedVersion: body.expectedVersion, requestKey: body.requestKey });
        if (body.expectedVersion === 3) {
          version = 4;
          return { status: 409, body: { error: 'VERSION_CONFLICT' } };
        }
        return { status: 200, body: executionConfigFixture({ version: 5 }) };
      },
    });
    const section = await screen.findByRole('region', { name: '执行配置' });
    await user.click(within(section).getByRole('button', { name: /保存执行配置（基于 v3）/ }));
    expect(await screen.findByText(/版本冲突：服务端当前为 v4/)).toBeInTheDocument();
    expect(writes).toHaveLength(1);
    expect(writes[0]?.requestKey).toBeTruthy();

    await user.click(screen.getByRole('button', { name: '确认并重新提交' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]?.expectedVersion).toBe(4);
    expect(writes[1]?.requestKey).not.toBe(writes[0]?.requestKey);
    expect(await screen.findByText(/已保存为版本 v5/)).toBeInTheDocument();
  });

  it('refuses to submit an invalid capability list locally', async () => {
    const { user, calls } = render(`/workspaces/${WORKSPACE}/runtimes`, baseRoutes());
    const section = await screen.findByRole('region', { name: '执行配置' });
    const textarea = within(section).getByLabelText('能力列表');
    await user.clear(textarea);
    await user.type(textarea, 'not-a-capability');
    await user.click(within(section).getByRole('button', { name: /保存执行配置/ }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/能力格式必须是 id@version/);
    expect(calls.filter((call) => call.method === 'PUT' && call.url.endsWith('/execution-config'))).toHaveLength(0);
  });
});
