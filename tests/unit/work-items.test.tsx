import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import {
  FIXTURE_HUMAN_TASK_ID,
  FIXTURE_RUN_ID,
  FIXTURE_WORK_ITEM_ID,
  admissionResponseFixture,
  humanTaskFixture,
  humanTaskListFixture,
  humanTaskResponseFixture,
  knowledgeRecommendationFixture,
  resolvedHumanTaskFixture,
  runDetailFixture,
  sessionBootstrapFixture,
  workItemFixture,
  workItemListFixture,
  workItemResponseFixture,
  workspaceConfigFixture,
} from '../fixtures/contracts';
import { createFakeFetch, renderApp, sessionRoute, type FakeResponse } from './render';

const WORKSPACE = 'workspace-a1b2c3d4';
const CONFIG_PATH = `/api/v1/workspaces/${WORKSPACE}`;
const ITEMS_PATH = `${CONFIG_PATH}/work-items`;
const ITEM_PATH = `${ITEMS_PATH}/${FIXTURE_WORK_ITEM_ID}`;
const RUNS_PATH = `${ITEM_PATH}/runs`;
const RUN_PATH = `${RUNS_PATH}/${FIXTURE_RUN_ID}`;
const TASKS_PATH = `${CONFIG_PATH}/human-tasks`;
const TASK_PATH = `${TASKS_PATH}/${FIXTURE_HUMAN_TASK_ID}`;
const ANSWERS_PATH = `${TASK_PATH}/responses`;
const CONFIG = `GET ${CONFIG_PATH}`;
const ITEMS = `GET ${ITEMS_PATH}`;
const ITEM = `GET ${ITEM_PATH}`;
const RUN = `GET ${RUN_PATH}`;
const TASKS = `GET ${TASKS_PATH}`;
const TASK = `GET ${TASK_PATH}`;
const CREATE = `POST ${ITEMS_PATH}`;
const START = `POST ${RUNS_PATH}`;
const ANSWERS = `POST ${ANSWERS_PATH}`;

type Routes = Record<string, FakeResponse | ((request: Request) => FakeResponse | Promise<FakeResponse>)>;

function render(routes: Routes, path: string) {
  const { fetchImpl, calls } = createFakeFetch({ 'POST /api/v2/auth/session': sessionRoute(), ...routes });
  // `delay: null` dispatches the same events without userEvent's artificial
  // pause between them: the assertions stay identical while a loaded machine
  // cannot push an interaction test past its budget.
  const user = userEvent.setup({ delay: null });
  const view = renderApp({ fetch: fetchImpl, initialEntries: [path] });
  return { calls, user, ...view };
}

async function renderListed(routes: Routes) {
  const app = render(routes, `/workspaces/${WORKSPACE}/work-items`);
  await screen.findByRole('heading', { name: '工作项列表' });
  return app;
}

describe('work item list', () => {
  it('renders the server projection with the pending count of open tasks', async () => {
    await renderListed({
      [ITEMS]: { status: 200, body: workItemListFixture([workItemFixture({ version: 3, currentRunId: FIXTURE_RUN_ID })]) },
      [TASKS]: { status: 200, body: humanTaskListFixture([humanTaskFixture(), humanTaskFixture({ humanTaskId: 'ht_other', workItemId: 'wi_other' })]) },
    });
    const table = screen.getByRole('table', { name: /服务端返回的工作项/ });
    expect(within(table).getByText('Add a deterministic greeting helper')).toBeInTheDocument();
    expect(within(table).getByText(FIXTURE_RUN_ID)).toBeInTheDocument();
    // Only the open task of this work item counts; another item's task does not.
    expect(within(table).getByText('待处理 1')).toBeInTheDocument();
  });

  it('shows a successful empty list instead of an error', async () => {
    await renderListed({
      [ITEMS]: { status: 200, body: workItemListFixture([]) },
      [TASKS]: { status: 200, body: humanTaskListFixture([]) },
    });
    expect(screen.getByRole('heading', { name: '还没有工作项' })).toBeInTheDocument();
  });

  it('reports an unavailable pending count without inventing zero', async () => {
    await renderListed({
      [ITEMS]: { status: 200, body: workItemListFixture([workItemFixture()]) },
      [TASKS]: { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } },
    });
    expect(screen.getByRole('alert')).toHaveTextContent('RESOURCE_UNAVAILABLE');
    expect(screen.getByText('待处理数不可用')).toBeInTheDocument();
  });
});

describe('work item intake', () => {
  const path = `/workspaces/${WORKSPACE}/work-items/new`;

  it('names the single writable repository and offers no template or second repository control', async () => {
    render({ [CONFIG]: { status: 200, body: workspaceConfigFixture() } }, path);
    await screen.findByRole('heading', { name: '需求与验收标准' });
    expect(screen.getByText(/team\/project（3001，read_write）/)).toBeInTheDocument();
    expect(screen.getByText(/唯一固定内置流程/)).toBeInTheDocument();
    // The MVP has one fixed workflow and one writable repository: no selector
    // for templates and no second target repository field exists.
    const main = screen.getByRole('main');
    expect(within(main).queryByRole('combobox')).not.toBeInTheDocument();
    expect(within(main).queryByLabelText(/第二个|模板/)).not.toBeInTheDocument();
    expect(within(main).queryByText(/向量/)).not.toBeInTheDocument();
  });

  it('posts the frozen intake command without a target repository or an actor', async () => {
    const writes: { body: Record<string, unknown>; csrf: string | null }[] = [];
    const { user } = render(
      {
        [CONFIG]: { status: 200, body: workspaceConfigFixture() },
        [CREATE]: async (request) => {
          writes.push({
            body: (await request.json()) as Record<string, unknown>,
            csrf: request.headers.get('X-CSRF-Token'),
          });
          return { status: 201, body: workItemResponseFixture() };
        },
        [ITEM]: { status: 200, body: workItemResponseFixture() },
        // The created work item is opened next; the run read is not needed yet.
      },
      path,
    );
    await screen.findByRole('heading', { name: '需求与验收标准' });
    await user.type(screen.getByLabelText('标题'), 'Add a greeting helper');
    await user.type(screen.getByLabelText('需求'), 'Add a deterministic greeting helper.');
    await user.type(screen.getByLabelText('验收标准'), 'The helper has a passing unit test.');
    await user.click(screen.getByRole('button', { name: '创建工作项' }));

    await waitFor(() => expect(writes).toHaveLength(1));
    const body = writes[0]!.body;
    expect(Object.keys(body).sort()).toEqual(['acceptance', 'requestKey', 'requirement', 'title']);
    expect(body.requestKey).toMatch(/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/);
    expect(body.requirement).toEqual({ version: 1, content: 'Add a deterministic greeting helper.' });
    expect(body.acceptance).toEqual({ version: 1, content: 'The helper has a passing unit test.' });
    // The owner decides the target repository and the actor; a browser that
    // sends either is refused by the frozen schema.
    expect(body.targetRepositoryId).toBeUndefined();
    expect(body.createdByGitHubUserId).toBeUndefined();
    expect(writes[0]!.csrf).toBe(sessionBootstrapFixture().csrfToken);
  });

  it('blocks the form when the workspace has no project binding', async () => {
    render({ [CONFIG]: { status: 200, body: workspaceConfigFixture({ project: undefined }) } }, path);
    expect(await screen.findByRole('heading', { name: '缺少项目仓绑定' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '暂时无法创建' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '创建工作项' })).not.toBeInTheDocument();
  });

  it('never sends a write without a confirmed session', async () => {
    const { calls } = render({ 'POST /api/v2/auth/session': { status: 401, body: { error: 'UNAUTHORIZED' } } }, path);
    await waitFor(() => expect(calls.some((call) => call.method === 'POST' && call.url === '/api/v2/auth/session')).toBe(true));
    expect(screen.queryByRole('heading', { name: '需求与验收标准' })).not.toBeInTheDocument();
    expect(calls.some((call) => call.method === 'POST' && call.url === '/api/v1/workspaces')).toBe(false);
  });
});

describe('fixed workflow entry', () => {
  const path = `/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`;

  function detailRoutes(overrides: Routes = {}): Routes {
    return {
      [ITEM]: { status: 200, body: workItemResponseFixture() },
      [CONFIG]: { status: 200, body: workspaceConfigFixture() },
      ...overrides,
    };
  }

  it('starts the single fixed flow with the server-issued version and a body request key', async () => {
    const starts: { body: Record<string, unknown>; csrf: string | null }[] = [];
    const { user } = render(
      detailRoutes({
        [START]: async (request) => {
          starts.push({ body: (await request.json()) as Record<string, unknown>, csrf: request.headers.get('X-CSRF-Token') });
          return { status: 201, body: admissionResponseFixture() };
        },
        [RUN]: { status: 200, body: runDetailFixture() },
        [TASK]: { status: 200, body: humanTaskResponseFixture() },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /启动固定流程（基于 v1）/ }));
    await waitFor(() => expect(starts).toHaveLength(1));
    expect(Object.keys(starts[0]!.body).sort()).toEqual(['expectedVersion', 'requestKey']);
    expect(starts[0]!.body.expectedVersion).toBe(1);
    expect(starts[0]!.body.requestKey).toMatch(/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/);
    expect(starts[0]!.csrf).toBe(sessionBootstrapFixture().csrfToken);
    expect(await screen.findByText(/已启动固定流程/)).toBeInTheDocument();
  });

  it('presents a replayed admission as an idempotent replay, not a new run', async () => {
    const { user } = render(
      detailRoutes({
        [START]: { status: 201, body: admissionResponseFixture({ replayed: true, workItemVersion: 2 }) },
        [RUN]: { status: 200, body: runDetailFixture() },
        [TASK]: { status: 200, body: humanTaskResponseFixture() },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /启动固定流程/ }));
    expect(await screen.findByText(/幂等重放：没有创建新的运行/)).toBeInTheDocument();
  });

  it('re-reads the work item on a version conflict and requires explicit re-confirmation', async () => {
    let version = 1;
    const starts: { expectedVersion: number; key: string }[] = [];
    const { user, calls } = render(
      detailRoutes({
        [ITEM]: () => ({ status: 200, body: workItemResponseFixture(workItemFixture({ version })) }),
        [START]: async (request) => {
          const body = (await request.json()) as { expectedVersion: number; requestKey: string };
          starts.push({ expectedVersion: body.expectedVersion, key: body.requestKey });
          if (body.expectedVersion === 1) {
            version = 2; // someone else advanced the item
            return { status: 409, body: { error: 'VERSION_CONFLICT' } };
          }
          return { status: 201, body: admissionResponseFixture({ workItemVersion: 3 }) };
        },
        [RUN]: { status: 200, body: runDetailFixture() },
        [TASK]: { status: 200, body: humanTaskResponseFixture() },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /启动固定流程（基于 v1）/ }));
    expect(await screen.findByText(/版本冲突：你提交时基于 v1，服务端当前为 v2/)).toBeInTheDocument();
    // No silent replay: exactly one start was sent, the item was re-read.
    expect(starts).toHaveLength(1);
    expect(calls.filter((call) => call.method === 'GET' && call.url === `/api/v1/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`).length).toBeGreaterThan(1);

    await user.click(screen.getByRole('button', { name: '确认并按 v2 重新提交' }));
    await waitFor(() => expect(starts).toHaveLength(2));
    expect(starts[1]!.expectedVersion).toBe(2);
    expect(starts[1]!.key).not.toBe(starts[0]!.key);
    expect(await screen.findByText(/已启动固定流程/)).toBeInTheDocument();
  });

  it('keeps an unavailable admission a failure and never claims there is no knowledge', async () => {
    const { user } = render(
      detailRoutes({
        [START]: { status: 503, body: { error: 'RESOURCE_UNAVAILABLE' } },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /启动固定流程/ }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('RESOURCE_UNAVAILABLE');
    expect(alert).toHaveTextContent(/外部依赖或配置当前不可用/);
    expect(screen.queryByText(/无知识|没有知识/)).not.toBeInTheDocument();
  });

  it('shows a read-only notice instead of the start control for a viewer', async () => {
    render(
      detailRoutes({
        [CONFIG]: { status: 200, body: workspaceConfigFixture({ members: [{ githubUserId: '101', role: 'viewer' }] }) },
      }),
      path,
    );
    expect(await screen.findByRole('heading', { name: '启动固定流程' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /启动固定流程/ })).not.toBeInTheDocument();
    expect(screen.getByText(/需要服务端判定的 developer\/admin 角色/)).toBeInTheDocument();
  });
});

describe('knowledge binding', () => {
  const path = `/workspaces/${WORKSPACE}/work-items/${FIXTURE_WORK_ITEM_ID}`;

  function bindingRoutes(recommendation = knowledgeRecommendationFixture(), overrides: Routes = {}): Routes {
    return {
      [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ version: 2, currentRunId: FIXTURE_RUN_ID })) },
      [CONFIG]: { status: 200, body: workspaceConfigFixture() },
      [RUN]: { status: 200, body: runDetailFixture({ knowledgeRecommendation: recommendation }) },
      [TASK]: { status: 200, body: humanTaskResponseFixture() },
      ...overrides,
    };
  }

  it('shows the frozen index, locks required entries and keeps the selection while filtering', async () => {
    const { user } = render(bindingRoutes(), path);
    expect(await screen.findByRole('heading', { name: '知识绑定' })).toBeInTheDocument();
    expect(screen.getByText(/等待人工确认（RECOMMENDED）/)).toBeInTheDocument();
    const required = screen.getByLabelText('必需条目 docs/design.md');
    expect(required).toBeDisabled();
    expect(required).toBeChecked();
    expect(screen.getByText(/required 1 条不可移除/)).toBeInTheDocument();

    // Dropping an optional entry is an index-internal edit; filtering is a
    // pure view over the loaded index and never changes the selection.
    await user.click(screen.getByLabelText('选择 docs/api.md'));
    await user.type(screen.getByLabelText(/在冻结索引内筛选/), 'runbook');
    expect(screen.queryByLabelText('选择 docs/api.md')).not.toBeInTheDocument();
    expect(screen.getByLabelText('选择 ops/runbook.md')).toBeInTheDocument();
    expect(screen.getByText(/已选 2 条/)).toBeInTheDocument();
  });

  it('confirms with the request version and object binding read from human.detail', async () => {
    // The confirmation request deliberately binds a different object version
    // than the recommendation: rebuilding the binding from the recommendation
    // is exactly what the owner refuses.
    const task = humanTaskFixture({
      request: {
        ...humanTaskFixture().request,
        version: 4,
        object: {
          kind: 'knowledge_recommendation',
          id: FIXTURE_RUN_ID,
          version: `sha256:${'9'.repeat(64)}`,
          digest: `sha256:${'9'.repeat(64)}`,
        },
      },
    });
    const answers: Record<string, unknown>[] = [];
    let recommendation = knowledgeRecommendationFixture();
    const { user } = render(
      bindingRoutes(knowledgeRecommendationFixture(), {
        [TASK]: { status: 200, body: humanTaskResponseFixture(task) },
        [RUN]: () => ({ status: 200, body: runDetailFixture({ knowledgeRecommendation: recommendation }) }),
        [ANSWERS]: async (request) => {
          answers.push((await request.json()) as Record<string, unknown>);
          // The control plane consumes the accepted continuation and freezes the
          // binding; the page observes that server transition by re-reading.
          recommendation = { ...recommendation, state: 'CONFIRMED', snapshotId: 'snap_x', bindingEventId: 'be_x', confirmedAt: '2026-10-16T08:10:01Z' };
          return { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture('docs/design.md\ndocs/api.md')) };
        },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /确认冻结知识选择（基于请求 v4）/ }));
    await waitFor(() => expect(answers).toHaveLength(1));
    expect(answers[0]!.requestVersion).toBe(4);
    expect(answers[0]!.object).toEqual(task.request.object);
    expect(answers[0]!.decision).toBe('approved');
    // The frozen selection format is newline-separated index paths in index order.
    expect(answers[0]!.values).toEqual({
      decision: 'approved',
      selection: 'docs/design.md\ndocs/api.md\nops/runbook.md',
    });
    expect(await screen.findByText(/确认已提交/)).toBeInTheDocument();
  });

  it('keeps the selection and re-reads the request on a refused confirmation', async () => {
    const conflicts: Record<string, unknown>[] = [];
    let refused = true;
    let recommendation = knowledgeRecommendationFixture();
    const { user } = render(
      bindingRoutes(knowledgeRecommendationFixture(), {
        [TASK]: { status: 200, body: humanTaskResponseFixture() },
        [RUN]: () => ({ status: 200, body: runDetailFixture({ knowledgeRecommendation: recommendation }) }),
        [ANSWERS]: async (request) => {
          const body = (await request.json()) as Record<string, unknown>;
          conflicts.push(body);
          if (refused) {
            refused = false;
            return { status: 422, body: { error: 'POLICY_BLOCKED' } };
          }
          recommendation = { ...recommendation, state: 'CONFIRMED', snapshotId: 'snap_y', bindingEventId: 'be_y', confirmedAt: '2026-10-16T08:10:01Z' };
          return { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture()) };
        },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /确认冻结知识选择/ }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('POLICY_BLOCKED');
    expect(screen.getByRole('heading', { name: '如何恢复' })).toBeInTheDocument();
    // The selection survives the refusal and a later legal confirmation succeeds.
    expect(screen.getByLabelText('必需条目 docs/design.md')).toBeChecked();
    await user.click(screen.getByRole('button', { name: /确认冻结知识选择/ }));
    await waitFor(() => expect(conflicts).toHaveLength(2));
    expect(await screen.findByText(/确认已提交/)).toBeInTheDocument();
  });

  it('re-reads the request and requires explicit confirmation on a request version conflict', async () => {
    const answers: { requestVersion: number; key: string }[] = [];
    let version = 1;
    const { user } = render(
      bindingRoutes(knowledgeRecommendationFixture(), {
        [TASK]: () => ({ status: 200, body: humanTaskResponseFixture(humanTaskFixture({ request: { ...humanTaskFixture().request, version } })) }),
        [ANSWERS]: async (request) => {
          const body = (await request.json()) as { requestVersion: number; requestKey: string };
          answers.push({ requestVersion: body.requestVersion, key: body.requestKey });
          if (body.requestVersion === 1) {
            version = 2; // the server re-asked under a new request version
            return { status: 409, body: { error: 'VERSION_CONFLICT' } };
          }
          return { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture()) };
        },
      }),
      path,
    );
    await user.click(await screen.findByRole('button', { name: /确认冻结知识选择（基于请求 v1）/ }));
    expect(await screen.findByText(/请求版本冲突：你提交时基于 v1，服务端当前为 v2/)).toBeInTheDocument();
    expect(answers).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: '确认并按 v2 重新提交' }));
    await waitFor(() => expect(answers).toHaveLength(2));
    expect(answers[1]!.requestVersion).toBe(2);
    expect(answers[1]!.key).not.toBe(answers[0]!.key);
  });

  it('treats a successfully read empty index as an empty selection that still needs confirmation', async () => {
    const answers: Record<string, unknown>[] = [];
    let confirmedEmpty = knowledgeRecommendationFixture({ candidates: [] });
    const { user } = render(
      bindingRoutes(knowledgeRecommendationFixture({ candidates: [] }), {
        [RUN]: () => ({ status: 200, body: runDetailFixture({ knowledgeRecommendation: confirmedEmpty }) }),
        [ANSWERS]: async (request) => {
          answers.push((await request.json()) as Record<string, unknown>);
          confirmedEmpty = { ...confirmedEmpty, state: 'CONFIRMED', snapshotId: 'snap_e', bindingEventId: 'be_e', confirmedAt: '2026-10-16T08:10:01Z' };
          return { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture('')) };
        },
      }),
      path,
    );
    expect(await screen.findByRole('heading', { name: '冻结索引内没有条目' })).toBeInTheDocument();
    expect(screen.getByText(/成功读取后的空结果，不是读取失败/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /确认冻结知识选择/ }));
    await waitFor(() => expect(answers).toHaveLength(1));
    // An empty selection carries no `selection` value at all; the required
    // decision field still travels.
    expect(answers[0]!.values).toEqual({ decision: 'approved' });
  });

  it('never renders execution progress while the binding is unconfirmed', async () => {
    render(bindingRoutes(), path);
    expect(await screen.findByRole('heading', { name: '知识绑定' })).toBeInTheDocument();
    expect(screen.getByText(/未确认期间服务端不会派发任何任务/)).toBeInTheDocument();
    // The run projection carries node rounds, but the unconfirmed page shows
    // the knowledge step only: no node row, verdict or progress rendering.
    expect(screen.queryByText('knowledge_recommend')).not.toBeInTheDocument();
    expect(screen.queryByText('WAITING_HUMAN')).not.toBeInTheDocument();
    expect(screen.queryByText('RUNNING')).not.toBeInTheDocument();
  });

  it('shows the confirmed binding with its accepted human action and no respond control', async () => {
    const confirmed = knowledgeRecommendationFixture({
      state: 'CONFIRMED',
      snapshotId: 'snap_1',
      bindingEventId: 'be_1',
      effectiveInputId: 'ei_1',
      confirmedAt: '2026-10-16T08:10:01Z',
    });
    render(
      bindingRoutes(confirmed, {
        [TASK]: { status: 200, body: humanTaskResponseFixture(resolvedHumanTaskFixture()) },
      }),
      path,
    );
    expect(await screen.findByText(/已确认（CONFIRMED）/)).toBeInTheDocument();
    expect(screen.getByText('snap_1')).toBeInTheDocument();
    expect(screen.getByText('be_1')).toBeInTheDocument();
    expect(screen.getByText(/docs\/design.md、docs\/api.md/)).toBeInTheDocument();
    expect(screen.getByText(/同一内容再次确认会产生新的人工动作记录/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /确认冻结知识选择/ })).not.toBeInTheDocument();
  });

  it('distinguishes a missing recommendation from an empty index', async () => {
    render(
      {
        [ITEM]: { status: 200, body: workItemResponseFixture(workItemFixture({ version: 2, currentRunId: FIXTURE_RUN_ID })) },
        [CONFIG]: { status: 200, body: workspaceConfigFixture() },
        [RUN]: { status: 200, body: runDetailFixture({ knowledgeRecommendation: undefined }) },
      },
      path,
    );
    expect(await screen.findByRole('heading', { name: '知识推荐尚未生成' })).toBeInTheDocument();
    expect(screen.getByText(/读取失败不会被显示成“无知识”/)).toBeInTheDocument();
  });

  it('does not render the confirmation control when the server grants no respond action', async () => {
    const task = humanTaskFixture({ availableActions: [] });
    render(
      bindingRoutes(knowledgeRecommendationFixture(), {
        [TASK]: { status: 200, body: humanTaskResponseFixture(task) },
      }),
      path,
    );
    expect(await screen.findByRole('heading', { name: '当前身份无法答复该请求' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /确认冻结知识选择/ })).not.toBeInTheDocument();
  });
});
