import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { workspaceConfigFixture } from '../fixtures/contracts';
import { createFakeFetch, renderApp, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
const CONFIG_PATH = `GET /api/v1/workspaces/${WORKSPACE}`;
const REPOS_PATH = `PUT /api/v1/workspaces/${WORKSPACE}/repositories`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function render(routes: Routes) {
  const { fetchImpl, calls } = createFakeFetch({ 'POST /api/v2/auth/session': sessionRoute(), ...routes });
  const user = userEvent.setup();
  renderApp({ fetch: fetchImpl, initialEntries: [`/workspaces/${WORKSPACE}/settings`] });
  return { calls, user };
}

async function renderLoaded(routes: Routes) {
  const app = render(routes);
  await screen.findByRole('heading', { name: 'Kowa' });
  return app;
}

describe('workspace settings page', () => {
  it('renders the server-projected configuration, members and dual repositories', async () => {
    await renderLoaded({ [CONFIG_PATH]: { status: 200, body: workspaceConfigFixture() } });
    expect(screen.getByRole('heading', { name: '项目仓与知识仓' })).toBeInTheDocument();
    const members = screen.getByRole('table', { name: /服务端返回的成员与角色/ });
    expect(within(members).getByText('202')).toBeInTheDocument();
    expect(screen.getByText(/installation 4001/)).toBeInTheDocument();
    expect(screen.getByText(/App 安装不等于登录身份或管理权限/)).toBeInTheDocument();
    expect(screen.getByText(/项目仓是唯一可写绑定/)).toBeInTheDocument();
  });

  it('refuses a non-member with the server FORBIDDEN category and keeps no stale data', async () => {
    render({ [CONFIG_PATH]: { status: 403, body: { error: 'FORBIDDEN' } } });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('FORBIDDEN');
    expect(alert).toHaveTextContent(/没有该对象或 Workspace 的权限/);
    expect(screen.queryByText(/团队\/project/)).not.toBeInTheDocument();
  });

  it('requires explicit re-confirmation on a version conflict and uses a fresh idempotency key', async () => {
    let version = 2;
    const writes: { expectedVersion: number; key: string }[] = [];
    const { user } = await renderLoaded({
      [CONFIG_PATH]: () => ({ status: 200, body: workspaceConfigFixture({ version }) }),
      [REPOS_PATH]: async (request) => {
        const body = (await request.json()) as { expectedVersion: number };
        writes.push({ expectedVersion: body.expectedVersion, key: request.headers.get('Idempotency-Key') ?? '' });
        if (body.expectedVersion === 2) {
          version = 3; // another writer advanced the Workspace
          return { status: 409, body: { error: 'VERSION_CONFLICT' } };
        }
        version = 4;
        return { status: 200, body: workspaceConfigFixture({ version: 4 }) };
      },
    });

    await user.click(screen.getByRole('button', { name: /保存（基于 v2）/ }));
    expect(await screen.findByText(/版本冲突：服务端当前为 v3/)).toBeInTheDocument();
    // Nothing was replayed automatically and the draft is preserved.
    expect(writes).toHaveLength(1);
    const repositoryId = screen.getByLabelText('Repository ID', { selector: '#project-repository-id' }) as HTMLInputElement;
    expect(repositoryId.value).toBe('3001');

    await user.click(screen.getByRole('button', { name: '确认并按 v3 重新提交' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]?.expectedVersion).toBe(3);
    expect(writes[1]?.key).not.toBe(writes[0]?.key);
    expect(await screen.findByText(/已保存为版本 v4/)).toBeInTheDocument();
  });

  it('reports POLICY_BLOCKED without retrying, keeps the input and accepts a later legal write', async () => {
    let refuse = true;
    const { user, calls } = await renderLoaded({
      [CONFIG_PATH]: () => ({ status: 200, body: workspaceConfigFixture() }),
      [REPOS_PATH]: () => (refuse
        ? { status: 422, body: { error: 'POLICY_BLOCKED' } }
        : { status: 200, body: workspaceConfigFixture({ version: 3 }) }),
    });
    const repositoryId = screen.getByLabelText('Repository ID', { selector: '#project-repository-id' }) as HTMLInputElement;
    await user.clear(repositoryId);
    await user.type(repositoryId, '3999');
    await user.click(screen.getByRole('button', { name: /保存（基于 v2）/ }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('POLICY_BLOCKED');
    expect(alert).toHaveTextContent(/第二个可写项目仓/);
    // Nothing was retried automatically: exactly one write reached the server.
    const writes = calls.filter((call) => call.method === 'PUT');
    expect(writes).toHaveLength(1);
    expect(repositoryId.value).toBe('3999');

    // The refusal did not poison the form: a corrected submission succeeds,
    // and changed content always carries a new idempotency key.
    refuse = false;
    const displayName = screen.getByLabelText('显示名', { selector: '#project-display-name' });
    await user.clear(displayName);
    await user.type(displayName, 'team/project-fixed');
    await user.click(screen.getByRole('button', { name: /保存（基于 v2）/ }));
    await waitFor(() => expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(2));
    expect(await screen.findByText(/已保存为版本 v3/)).toBeInTheDocument();
    const keys = calls.filter((call) => call.method === 'PUT').map((call) => call.headers.get('Idempotency-Key'));
    expect(new Set(keys).size).toBe(2);
  });

  it('presents RESOURCE_UNAVAILABLE as an external fact, not as a missing object', async () => {
    const { user } = await renderLoaded({
      [CONFIG_PATH]: { status: 200, body: workspaceConfigFixture() },
      [REPOS_PATH]: { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } },
    });
    await user.click(screen.getByRole('button', { name: /保存（基于 v2）/ }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('RESOURCE_UNAVAILABLE');
    expect(alert).toHaveTextContent(/这不表示对象不存在/);
  });

  it('hides write controls for a non-admin member but still shows server facts', async () => {
    const config = workspaceConfigFixture({
      members: [
        { githubUserId: '101', role: 'viewer' },
        { githubUserId: '202', role: 'admin' },
      ],
    });
    await renderLoaded({ [CONFIG_PATH]: { status: 200, body: config } });
    expect(screen.queryByRole('button', { name: /保存（基于/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '添加/更新成员' })).not.toBeInTheDocument();
    const members = screen.getByRole('table', { name: /服务端返回的成员与角色/ });
    expect(within(members).getByText('viewer')).toBeInTheDocument();
  });

  it('sends CSRF and an idempotency key on a member write and refetches the version', async () => {
    let version = 2;
    const { user, calls } = await renderLoaded({
      [CONFIG_PATH]: () => ({ status: 200, body: workspaceConfigFixture({ version }) }),
      [`PUT /api/v1/workspaces/${WORKSPACE}/members/303`]: () => {
        version = 3;
        return { status: 204 };
      },
    });
    await user.type(screen.getByLabelText('GitHub 用户 ID'), '303');
    await user.click(screen.getByRole('button', { name: '添加/更新成员' }));
    await waitFor(() => expect(calls.some((call) => call.method === 'PUT' && call.url.endsWith('/members/303'))).toBe(true));
    const write = calls.find((call) => call.url.endsWith('/members/303'));
    expect(write?.headers.get('X-CSRF-Token')).toBe('B'.repeat(43));
    expect(write?.headers.get('Idempotency-Key')).toBeTruthy();
    expect(write?.body).toEqual({ expectedVersion: 2, role: 'developer' });
    await waitFor(() => expect(screen.getByText(/当前版本 v3/)).toBeInTheDocument());
  });
});
