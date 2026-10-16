/**
 * The recovery surface of the run view.
 *
 * The owner freezes five commands and no action list: the page therefore shows
 * the server fact each candidate rests on, submits only the frozen `kind`
 * tokens, and takes the server's answer as the fact — an `applied: false`, a
 * refusal or a conflict is never dressed up as recovery.
 */
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import {
  DIGEST_A,
  FIXTURE_RUN_ID,
  FIXTURE_WORK_ITEM_ID,
  artifactListFixture,
  fixedWorkflowFixture,
  humanTaskResponseFixture,
  nodeHistoryFixture,
  runDetailFixture,
  sessionBootstrapFixture,
  workItemFixture,
  workItemResponseFixture,
  workspaceConfigFixture,
} from '../fixtures/contracts';
import { createFakeFetch, renderApp, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
const CONFIG = `GET /api/v1/workspaces/${WORKSPACE}`;
const ITEM = `GET /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`;
const RUN = `GET /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}/runs/${FIXTURE_RUN_ID}`;
const WORKFLOW = `GET /api/v1/workspaces/${WORKSPACE}/fixed-workflow`;
const ARTIFACTS = `GET /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}/artifacts`;
const TASK = `GET /api/v1/workspaces/${WORKSPACE}/human-tasks/ht_5d2a`;
const RECOVERY = `POST /api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}/runs/${FIXTURE_RUN_ID}/recovery`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function pageRoutes(overrides: Routes = {}): Routes {
  return {
    'POST /api/v2/auth/session': sessionRoute(sessionBootstrapFixture()),
    [CONFIG]: { status: 200, body: workspaceConfigFixture() },
    [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ version: 2, currentRunId: FIXTURE_RUN_ID })) },
    [RUN]: { status: 200, body: runDetailFixture() },
    [WORKFLOW]: { status: 200, body: fixedWorkflowFixture() },
    [ARTIFACTS]: { status: 200, body: artifactListFixture() },
    [TASK]: { status: 200, body: humanTaskResponseFixture() },
    ...overrides,
  };
}

function renderPage(routes: Routes = pageRoutes()) {
  const { fetchImpl, calls } = createFakeFetch(routes);
  const user = userEvent.setup({ delay: null });
  const view = renderApp({ fetch: fetchImpl, initialEntries: [`/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`] });
  return { calls, user, ...view };
}

async function recoverySection(): Promise<HTMLElement> {
  return await screen.findByRole('region', { name: '失败与恢复' });
}

function failedAttemptNode(nodeId: string) {
  return nodeHistoryFixture({
    nodeId,
    state: 'EXECUTION_FAILED',
    attempts: [{ taskId: `${nodeId}-1`, attempt: 1, attemptKind: 'initial', state: 'FAILED', inputDigest: DIGEST_A, artifactIds: [] }],
  });
}

const completedNode = (nodeId: string) =>
  nodeHistoryFixture({
    nodeId,
    attempts: [{ taskId: `${nodeId}-1`, attempt: 1, attemptKind: 'initial', state: 'COMPLETED', inputDigest: DIGEST_A, artifactIds: [] }],
  });

describe('loading a run never writes', () => {
  it('issues no recovery command on its own', async () => {
    const { calls } = renderPage();
    await recoverySection();
    // The only write the app makes by itself is its own session bootstrap; the
    // run and recovery paths are read-only until an operator commands otherwise.
    expect(calls.filter((call) => call.method !== 'GET' && call.url.includes('/runs'))).toHaveLength(0);
    expect(calls.filter((call) => call.url.endsWith('/recovery'))).toHaveLength(0);
  });

  it('offers every command under its frozen kind token, with the fact it rests on', async () => {
    renderPage();
    const section = await recoverySection();
    expect(within(section).getByRole('button', { name: /取消运行（cancel）/ })).toBeInTheDocument();
    expect(within(section).getByText(/运行状态为 ACTIVE/)).toBeInTheDocument();
    expect(within(section).getByText(/是否接受由服务端裁决/)).toBeInTheDocument();
    // The reason is required by the owner for a repair run, and the page says so.
    expect(within(section).getByRole('button', { name: '创建修复运行（repair）' })).toBeInTheDocument();
    expect(within(section).getByText(/必须填写原因/)).toBeInTheDocument();
  });
});

describe('cancel is submitted against the run version the page really read', () => {
  it('posts the frozen kind, the observed version and a request key, then re-reads the run', async () => {
    const { user, calls } = renderPage(
      pageRoutes({
        [RUN]: { status: 200, body: runDetailFixture({ version: 9 }) },
        [RECOVERY]: {
          status: 200,
          body: {
            workflowRunId: FIXTURE_RUN_ID,
            kind: 'cancel',
            applied: true,
            state: 'CANCELING',
            version: 10,
            detail: 'cancellation was requested; in-flight attempts settle before the run ends',
            recordedAt: '2026-10-16T09:30:00Z',
          },
        },
      }),
    );
    const section = await recoverySection();
    await user.click(within(section).getByRole('button', { name: /取消运行（cancel）/ }));
    await waitFor(() => expect(calls.filter((call) => call.url.endsWith('/recovery'))).toHaveLength(1));
    const posted = calls.find((call) => call.url.endsWith('/recovery')) as { body: Record<string, unknown>; headers: Headers };
    expect(posted.body.kind).toBe('cancel');
    expect(posted.body.expectedVersion).toBe(9);
    expect(posted.body.nodeId).toBeUndefined();
    expect(String(posted.body.requestKey)).toMatch(/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/);
    expect(posted.headers.get('X-CSRF-Token')).toBe('B'.repeat(43));
    // The server's own answer is shown, and the run is re-read instead of being
    // rewritten locally.
    expect(await within(section).findByText(/cancellation was requested/)).toBeInTheDocument();
    await waitFor(() => expect(calls.filter((call) => call.url === `/api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}/runs/${FIXTURE_RUN_ID}`).length).toBeGreaterThan(1));
  });

  it('reports an unapplied command as not applied', async () => {
    const { user } = renderPage(
      pageRoutes({
        [RECOVERY]: {
          status: 200,
          body: { workflowRunId: FIXTURE_RUN_ID, kind: 'cancel', applied: false, state: 'ACTIVE', version: 2, detail: 'the run was not canceled', recordedAt: '2026-10-16T09:30:00Z' },
        },
      }),
    );
    const section = await recoverySection();
    await user.click(within(section).getByRole('button', { name: /取消运行（cancel）/ }));
    expect(await within(section).findByText(/未被应用/)).toBeInTheDocument();
    expect(within(section).getByText(/the run was not canceled/)).toBeInTheDocument();
    expect(within(section).queryByText(/已应用/)).not.toBeInTheDocument();
  });

  it('surfaces a version conflict, keeps the operator’s own words and waits for a re-submission', async () => {
    const { user, calls } = renderPage(
      pageRoutes({
        [RUN]: { status: 200, body: runDetailFixture({ state: 'FAILED', nodes: [failedAttemptNode('plan')] }) },
        [RECOVERY]: { status: 409, body: { error: 'VERSION_CONFLICT' } },
      }),
    );
    const section = await recoverySection();
    const reason = within(section).getByLabelText(/修复运行的原因/);
    await user.type(reason, 'the merged plan needs a follow-up change');
    await user.click(within(section).getByRole('button', { name: /创建修复运行（repair）/ }));
    expect(await within(section).findByText(/你提交时基于 v2/)).toBeInTheDocument();
    expect(within(section).getByRole('button', { name: /按 v2 重新提交相同命令/ })).toBeInTheDocument();
    // One refused attempt, no automatic second one, and the draft survives.
    expect(calls.filter((call) => call.url.endsWith('/recovery'))).toHaveLength(1);
    expect((reason as HTMLTextAreaElement).value).toBe('the merged plan needs a follow-up change');
  });

  it('names the version the command’s own guard holds when a repair is refused', async () => {
    // `repair` is decided against the WorkItem version, so the conflict notice
    // reports that projection and re-reads it — never the run's version.
    const { user, calls } = renderPage(
      pageRoutes({
        [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ version: 4, currentRunId: FIXTURE_RUN_ID })) },
        [RUN]: { status: 200, body: runDetailFixture({ version: 7 }) },
        [RECOVERY]: { status: 409, body: { error: 'VERSION_CONFLICT' } },
      }),
    );
    const section = await recoverySection();
    await user.type(within(section).getByLabelText(/修复运行的原因/), 'follow-up after the merge');
    await user.click(within(section).getByRole('button', { name: /创建修复运行（repair）/ }));
    expect(await within(section).findByText(/服务端工作项当前为 v4/)).toBeInTheDocument();
    expect(within(section).getByRole('button', { name: /按 v4 重新提交相同命令/ })).toBeInTheDocument();
    await waitFor(() => expect(calls.filter((call) => call.url === `/api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`).length).toBeGreaterThan(1));
  });

  it('shows a policy refusal as a refusal, not as an unavailable object', async () => {
    const { user } = renderPage(
      pageRoutes({
        [RECOVERY]: { status: 409, body: { error: 'POLICY_BLOCKED' } },
      }),
    );
    const section = await recoverySection();
    await user.click(within(section).getByRole('button', { name: /取消运行（cancel）/ }));
    expect(await within(section).findByRole('alert')).toHaveTextContent('POLICY_BLOCKED');
  });
});

describe('node-level recovery is offered only where the server recorded the fact', () => {
  it('offers retry for a node whose latest attempt failed, and posts the node it names', async () => {
    const { user, calls } = renderPage(
      pageRoutes({
        [RUN]: {
          status: 200,
          body: runDetailFixture({ nodes: [failedAttemptNode('plan'), completedNode('knowledge_confirm')] }),
        },
        [RECOVERY]: {
          status: 200,
          body: {
            workflowRunId: FIXTURE_RUN_ID,
            kind: 'retry',
            applied: true,
            state: 'ACTIVE',
            version: 3,
            detail: 'attempt 2 of node plan was queued with the frozen input of attempt 1',
            recordedAt: '2026-10-16T09:40:00Z',
          },
        },
      }),
    );
    const section = await recoverySection();
    await user.selectOptions(within(section).getByLabelText('恢复动作作用的节点'), 'plan');
    expect(await within(section).findByText(/最新一次尝试为 FAILED/)).toBeInTheDocument();
    await user.click(within(section).getByRole('button', { name: /重试该尝试（retry）/ }));
    await waitFor(() => expect(calls.filter((call) => call.url.endsWith('/recovery'))).toHaveLength(1));
    const posted = calls.find((call) => call.url.endsWith('/recovery')) as { body: Record<string, unknown> };
    expect(posted.body.kind).toBe('retry');
    expect(posted.body.nodeId).toBe('plan');
    expect(await within(section).findByText(/attempt 2 of node plan/)).toBeInTheDocument();
  });

  it('does not offer a retry where no attempt failed', async () => {
    const { user } = renderPage(
      pageRoutes({
        [RUN]: { status: 200, body: runDetailFixture({ nodes: [completedNode('plan')] }) },
      }),
    );
    const section = await recoverySection();
    await user.selectOptions(within(section).getByLabelText('恢复动作作用的节点'), 'plan');
    await waitFor(() => expect(within(section).getByText(/没有失败或过期的尝试/)).toBeInTheDocument());
    expect(within(section).queryByRole('button', { name: /重试该尝试（retry）/ })).not.toBeInTheDocument();
  });

  it('prefers an explicit node over a guess when nothing is selected yet', async () => {
    renderPage();
    const section = await recoverySection();
    expect(within(section).getByText(/先选择一个节点/)).toBeInTheDocument();
    expect(within(section).queryByRole('button', { name: /重试该尝试（retry）/ })).not.toBeInTheDocument();
  });
});
