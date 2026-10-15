import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { sessionBootstrapFixture, workspaceConfigFixture } from '../fixtures/contracts';
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
    // Each repository group is its own named group: one fieldset never carries
    // two legends, so assistive technology hears the right group name.
    expect(screen.getByRole('group', { name: '项目仓（可写）' })).toBeInTheDocument();
    expect(screen.getByRole('group', { name: '知识仓（只读）' })).toBeInTheDocument();
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
    // The notice separates the baseline the operator submitted against from the
    // version the server really holds; conflating them would hide what is being
    // abandoned (F2).
    expect(await screen.findByText(/版本冲突：你提交时基于 v2，服务端当前为 v3/)).toBeInTheDocument();
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

  it('re-reads and re-confirms after a member-write conflict instead of repeating a stale version', async () => {
    let version = 2;
    const memberWrites: { expectedVersion: number; key: string }[] = [];
    const { user } = await renderLoaded({
      [CONFIG_PATH]: () => ({ status: 200, body: workspaceConfigFixture({ version }) }),
      [`PUT /api/v1/workspaces/${WORKSPACE}/members/303`]: async (request) => {
        const body = (await request.json()) as { expectedVersion: number; role: string };
        memberWrites.push({ expectedVersion: body.expectedVersion, key: request.headers.get('Idempotency-Key') ?? '' });
        if (body.expectedVersion === 2) {
          version = 3; // another administrator wrote first
          return { status: 409, body: { error: 'VERSION_CONFLICT' } };
        }
        version = 4;
        return { status: 204 };
      },
    });

    await user.type(screen.getByLabelText('GitHub 用户 ID'), '303');
    await user.click(screen.getByRole('button', { name: '添加/更新成员' }));
    expect(await screen.findByText(/版本冲突：你提交时基于 v2，服务端当前为 v3/)).toBeInTheDocument();
    // The attempt is preserved and nothing was replayed against the old version.
    expect(memberWrites).toHaveLength(1);
    expect(screen.getByLabelText('GitHub 用户 ID')).toHaveValue('303');
    // Repeated clicks against the stale baseline cannot be sent: the write
    // controls stay disabled until the operator confirms the new version.
    expect(screen.getByRole('button', { name: '添加/更新成员' })).toBeDisabled();

    await user.click(screen.getByRole('button', { name: '确认并按 v3 重新提交' }));
    await waitFor(() => expect(memberWrites).toHaveLength(2));
    expect(memberWrites[1]?.expectedVersion).toBe(3);
    expect(memberWrites[1]?.key).not.toBe(memberWrites[0]?.key);
    expect(await screen.findByText(/已设置 303 的角色为 developer/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(/当前版本 v4/)).toBeInTheDocument());
  });

  it('reuses the member-write idempotency key when the same intent is retried after a lost response', async () => {
    const keys: string[] = [];
    let attempt = 0;
    const { user } = await renderLoaded({
      [CONFIG_PATH]: () => ({ status: 200, body: workspaceConfigFixture() }),
      [`PUT /api/v1/workspaces/${WORKSPACE}/members/303`]: (request) => {
        attempt += 1;
        keys.push(String(request.headers.get('Idempotency-Key')));
        if (attempt === 1) throw new Error('link went down after the request left the browser');
        return { status: 204 };
      },
    });
    await user.type(screen.getByLabelText('GitHub 用户 ID'), '303');
    await user.click(screen.getByRole('button', { name: '添加/更新成员' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/link went down/);
    // Same intent, same content, same baseline: the retry must be the same
    // delivery so an owner that already applied it can replay the answer.
    await user.click(screen.getByRole('button', { name: '添加/更新成员' }));
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[1]).toBe(keys[0]);
  });

  it('keeps the unsubmitted draft across a focus refresh and refuses writes until it completes', async () => {
    let bootstrapCalls = 0;
    const held: (() => void)[] = [];
    const { fetchImpl, calls } = createFakeFetch({
      'POST /api/v2/auth/session': async (request) => {
        bootstrapCalls += 1;
        const payload = (await request.json()) as { requestId: string };
        if (bootstrapCalls > 1) {
          await new Promise<void>((resolve) => held.push(resolve));
        }
        return { status: 200, body: sessionBootstrapFixture({ requestId: payload.requestId }) };
      },
      [CONFIG_PATH]: { status: 200, body: workspaceConfigFixture() },
      [REPOS_PATH]: { status: 200, body: workspaceConfigFixture({ version: 3 }) },
    });
    let focus: (() => void) | undefined;
    const user = userEvent.setup();
    renderApp({
      fetch: fetchImpl,
      initialEntries: [`/workspaces/${WORKSPACE}/settings`],
      onFocusSubscribed: (trigger) => {
        focus = trigger;
      },
    });
    await screen.findByRole('heading', { name: 'Kowa' });

    const projectId = screen.getByLabelText('Repository ID', { selector: '#project-repository-id' }) as HTMLInputElement;
    await user.clear(projectId);
    await user.type(projectId, '3999');
    focus?.();
    await waitFor(() => expect(bootstrapCalls).toBe(2));

    // The same identity keeps its draft: no remount, no silent reset.
    expect(screen.getByLabelText('Repository ID', { selector: '#project-repository-id' })).toHaveValue('3999');
    // The contract says bootstrap again *before writes*: a write attempted
    // while the refresh is in flight is refused locally and never sent.
    await user.click(screen.getByRole('button', { name: /保存（基于 v2）/ }));
    expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(0);
    expect(await screen.findByRole('alert')).toHaveTextContent(/会话正在刷新/);

    held.forEach((release) => release());
    await waitFor(() => expect(screen.getByLabelText('Repository ID', { selector: '#project-repository-id' })).toHaveValue('3999'));
    await user.click(screen.getByRole('button', { name: /保存（基于 v2）/ }));
    await waitFor(() => expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(1));
  });

  it('never lets a read that predates a focus refresh overwrite the newer projection', async () => {
    const pending: ((response: FakeResponse) => void)[] = [];
    let reads = 0;
    const { fetchImpl } = createFakeFetch({
      'POST /api/v2/auth/session': sessionRoute(),
      [CONFIG_PATH]: () => {
        reads += 1;
        if (reads === 1) {
          // The read issued before the focus refresh stays in flight (a fetch
          // that ignores its abort signal) and answers last.
          return new Promise<FakeResponse>((resolve) => pending.push(resolve));
        }
        return { status: 200, body: workspaceConfigFixture({ version: 3 }) };
      },
    });
    let focus: (() => void) | undefined;
    renderApp({
      fetch: fetchImpl,
      initialEntries: [`/workspaces/${WORKSPACE}/settings`],
      onFocusSubscribed: (trigger) => {
        focus = trigger;
      },
    });
    await waitFor(() => expect(reads).toBe(1));
    focus?.();
    await waitFor(() => expect(reads).toBe(2));
    expect(await screen.findByText(/当前版本 v3/)).toBeInTheDocument();

    // The superseded read answers late with older data; the newer projection
    // must stand.
    pending.forEach((resolve) => resolve({ status: 200, body: workspaceConfigFixture({ version: 9 }) }));
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.getByText(/当前版本 v3/)).toBeInTheDocument();
    expect(screen.queryByText(/当前版本 v9/)).not.toBeInTheDocument();
  });

  it('clears the draft when a refresh recovers a different identity', async () => {
    let bootstrapCalls = 0;
    const { fetchImpl } = createFakeFetch({
      'POST /api/v2/auth/session': async (request) => {
        bootstrapCalls += 1;
        const payload = (await request.json()) as { requestId: string };
        const identity = bootstrapCalls === 1
          ? { githubUserId: '101', login: 'member-a' }
          : { githubUserId: '202', login: 'member-b' };
        return { status: 200, body: sessionBootstrapFixture({ identity, requestId: payload.requestId }) };
      },
      [CONFIG_PATH]: { status: 200, body: workspaceConfigFixture() },
    });
    let focus: (() => void) | undefined;
    const user = userEvent.setup();
    renderApp({
      fetch: fetchImpl,
      initialEntries: [`/workspaces/${WORKSPACE}/settings`],
      onFocusSubscribed: (trigger) => {
        focus = trigger;
      },
    });
    await screen.findByRole('heading', { name: 'Kowa' });

    const projectId = screen.getByLabelText('Repository ID', { selector: '#project-repository-id' }) as HTMLInputElement;
    await user.clear(projectId);
    await user.type(projectId, '3999');
    focus?.();
    await waitFor(() => expect(screen.getByText(/登录身份：member-b/)).toBeInTheDocument());
    // Another identity must never inherit the previous one's draft.
    await waitFor(() =>
      expect(screen.getByLabelText('Repository ID', { selector: '#project-repository-id' })).toHaveValue('3001'),
    );
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
