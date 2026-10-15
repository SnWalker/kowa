import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { installApi, type FixtureServer } from './support/fixtures';
import { admissionResponseFixture, humanTaskFixture } from '../fixtures/contracts';
import {
  ensureWorkspace,
  expectNoRunTask,
  fillDualRepositories,
  harnessFixtures,
  leaseRunTask,
  publishExecutionConfig,
  pageCsrf,
  pageRequest,
  registerFixedFlowRuntime,
  signIn,
  type HarnessFixtures,
  type HarnessRuntime,
} from './support/journey';
import { controlPlaneURL, isControlPlane } from './support/mode';

const RUNTIME_EPOCH = 'epoch-e2e-work-items';
const HARNESS_WORKSPACE_HINT = 'workspace-a1b2c3d4';

/**
 * Prepares a workspace that can really start the fixed flow.
 *
 * Fixture mode serves the frozen projections from the contract-bound double;
 * control-plane mode binds the harness's local bare repositories, registers a
 * scripted runtime covering the compiled definition's capabilities and
 * publishes the execution configuration through the real page.
 */
async function prepareWorkspace(
  page: Page,
  request: APIRequestContext,
  browserName: string,
): Promise<{ workspaceId: string; fixtures?: FixtureServer; harness?: HarnessFixtures & { runtime: HarnessRuntime } }> {
  if (!isControlPlane) {
    const fixtures = await installApi(page);
    await signIn(page);
    return { workspaceId: HARNESS_WORKSPACE_HINT, fixtures };
  }
  const workspaceId = await ensureWorkspace(page);
  const harness = await harnessFixtures(request);
  await page.goto(`/workspaces/${workspaceId}/settings`);
  await fillDualRepositories(page, harness.project.repositoryId, harness.knowledge.repositoryId);
  await page.getByRole('button', { name: /保存（基于/ }).click();
  await expect(page.getByText(/已保存为版本/)).toBeVisible();
  // Each browser project drives its own runtime and token, and S02 keeps its
  // own runtime id: its repeated registrations must never advance the epoch of
  // the runtime frontend:S01's journey asserts on in the same harness.
  const baseToken = process.env.KOWA_E2E_RUNTIME_TOKEN ?? 'harness-runtime-token-0123456789';
  const runtime = {
    ...harness.runtime,
    runtimeId: `${harness.runtime.runtimeId}-${browserName}-s02`,
    epoch: `${RUNTIME_EPOCH}-${browserName}`,
    token: `${baseToken}-${browserName}-s02`,
  };
  await registerFixedFlowRuntime(request, runtime, harness.capabilities);
  await publishExecutionConfig(page, workspaceId, runtime.runtimeId, harness.capabilities);
  return { workspaceId, harness: { ...harness, runtime } };
}

async function createWorkItem(page: Page, workspaceId: string, title: string): Promise<void> {
  await page.goto(`/workspaces/${workspaceId}/work-items/new`);
  await page.getByLabel('标题', { exact: true }).fill(title);
  await page.getByLabel('需求', { exact: true }).fill('Add a helper that greets deterministically.');
  await page.getByLabel('验收标准', { exact: true }).fill('The helper has a passing unit test.');
  await page.getByRole('button', { name: '创建工作项' }).click();
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
}

/** The run the server really bound to this work item. */
async function currentRunId(page: Page, workspaceId: string, workItemId: string): Promise<string> {
  const item = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/work-items/${workItemId}`);
  expect(item.status).toBe(200);
  const runId = (item.body as { workItem: { currentRunId?: string } }).workItem.currentRunId;
  expect(runId, 'the admitted work item must carry its delivery run').toBeTruthy();
  return runId as string;
}

/** The open knowledge confirmation the server really created, read from the app origin. */
async function openKnowledgeTask(page: Page, workspaceId: string) {
  const response = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/human-tasks`);
  expect(response.status).toBe(200);
  const body = response.body as {
    humanTasks: {
      humanTaskId: string;
      state: string;
      request: { version: number; kind: string; object: { kind: string; id: string; version: string } };
    }[];
  };
  const task = body.humanTasks.find((entry) => entry.request.kind === 'knowledge_confirmation' && entry.state === 'OPEN');
  expect(task, 'the server must expose the open knowledge confirmation').toBeTruthy();
  return task!;
}

test('a member creates a work item, starts the single fixed flow and confirms the frozen knowledge selection', async ({ page, request, browserName }) => {
  // These journeys drive a real server with several round trips per step;
  // the budget is widened instead of trimming what is verified.
  test.slow();
  const { workspaceId, fixtures, harness } = await prepareWorkspace(page, request, browserName);
  const title = `Grow the greeting helper ${Date.now().toString(36)}`;
  await createWorkItem(page, workspaceId, title);

  // Exactly one fixed entry: no template choice, no second writable
  // repository, and the knowledge set is the frozen index (no search service).
  const startSection = page.getByRole('region', { name: '启动固定流程' });
  await expect(startSection.getByText(/只有一条固定、内置、版本化的工作流/)).toBeVisible();
  await expect(startSection.getByRole('button', { name: /启动固定流程（基于 v1）/ })).toBeVisible();
  await startSection.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();

  // Admission opens the versioned confirmation and claims no progress.
  const knowledge = page.getByRole('region', { name: '知识绑定' });
  await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();
  await expect(knowledge.getByText(/未确认期间服务端不会派发任何任务/)).toBeVisible();
  const required = knowledge.getByLabel(/必需条目/).first();
  await expect(required).toBeDisabled();
  await expect(required).toBeChecked();

  const workItemId = page.url().split('/').pop() as string;
  const runId = isControlPlane ? await currentRunId(page, workspaceId, workItemId) : '';
  if (harness) {
    // The real control plane dispatches nothing of this run before the confirmation.
    await expectNoRunTask(request, harness.runtime, runId);
  }

  // Dropping an optional index entry is allowed; a required one has no control.
  const optional = knowledge.getByLabel(/^选择 /).first();
  await expect(optional).toBeEnabled();
  await optional.uncheck();
  await optional.check();
  await knowledge.getByRole('button', { name: /确认冻结知识选择/ }).click();

  // The accepted answer freezes the binding; the projection comes from the server.
  await expect(knowledge.getByText('已确认（CONFIRMED）')).toBeVisible();
  await expect(knowledge.getByText('冻结结果')).toBeVisible();
  await expect(knowledge.getByRole('heading', { name: '人工动作' })).toBeVisible();

  if (harness) {
    // Only now may the flow dispatch the first capability task of this run.
    const after = await leaseRunTask(request, harness.runtime, runId);
    expect(after.status).toBe(200);
    expect(after.capability).toBe('plan.create');
  }

  // Work item list projection refresh: the item advanced and the pending count
  // is back to zero because the confirmation was resolved.
  await page.goto(`/workspaces/${workspaceId}/work-items`);
  const row = page.getByRole('row', { name: new RegExp(title) });
  await expect(row).toContainText('未关闭（OPEN）');
  await expect(row).not.toContainText('尚未启动');
  await expect(row).not.toContainText('待处理 1');

  if (fixtures !== undefined) {
    // The double is a stub, never the authority: the write still carried the
    // in-memory CSRF token and a frozen request key.
    const write = fixtures.calls.find((call) => call.method === 'POST' && call.path.endsWith('/runs'));
    expect(write?.headers['x-csrf-token']).toBeTruthy();
    expect((write?.body as { requestKey?: string }).requestKey).toMatch(/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/);
  }
});

test('the knowledge confirmation is keyboard-operable and the pages hold 320px at 200% text', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId } = await prepareWorkspace(page, request, browserName);
  const title = `Keyboard confirmation ${Date.now().toString(36)}`;
  await createWorkItem(page, workspaceId, title);
  await page.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
  const knowledge = page.getByRole('region', { name: '知识绑定' });
  await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();

  // The required entry has no keyboard path that could drop it.
  const required = knowledge.getByLabel(/必需条目/).first();
  await expect(required).toBeDisabled();
  // An optional entry can be toggled with the keyboard alone.
  const optional = knowledge.getByLabel(/^选择 /).first();
  await optional.focus();
  await expect(optional).toBeFocused();
  await page.keyboard.press('Space');
  await expect(optional).not.toBeChecked();
  await page.keyboard.press('Space');
  await expect(optional).toBeChecked();

  // Narrow screen with doubled text on the three S02 routes.
  await page.setViewportSize({ width: 320, height: 640 });
  for (const path of [`/workspaces/${workspaceId}/work-items`, `/workspaces/${workspaceId}/work-items/new`, page.url()]) {
    await page.goto(path);
    await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
    await expect(page.getByRole('main')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), path).toBe(true);
  }

  // The confirmation view stays operable while narrow and after the resize.
  await page.goto(page.url());
  await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();
  const confirm = knowledge.getByRole('button', { name: /确认冻结知识选择/ });
  await confirm.scrollIntoViewIfNeeded();
  await confirm.focus();
  if (browserName === 'webkit') {
    // WebKit does not activate a button with Enter; Space is the keyboard path.
    await page.keyboard.press('Space');
  } else {
    await page.keyboard.press('Enter');
  }
  await expect(knowledge.getByText('已确认（CONFIRMED）')).toBeVisible({ timeout: 15_000 });
});

test.describe('fixture-mode knowledge binding states', () => {
  test.skip(isControlPlane, 'fixture-mode scenarios');

  test('an unreadable knowledge source is a failure, never an empty knowledge set', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('POST', '/api/v1/workspaces/[^/]+/work-items/[^/]+/runs', () => ({
      status: 503,
      body: { error: 'RESOURCE_UNAVAILABLE' },
    }));
    await createWorkItem(page, HARNESS_WORKSPACE_HINT, 'Unavailable knowledge');
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('RESOURCE_UNAVAILABLE');
    await expect(alert).toContainText(/外部依赖或配置当前不可用/);
    expect(await page.getByText(/没有条目/).count()).toBe(0);
  });

  test('a successfully read empty index still requires an explicit confirmation', async ({ page }) => {
    await installApi(page, undefined, { emptyKnowledge: true });
    await signIn(page);
    await createWorkItem(page, HARNESS_WORKSPACE_HINT, 'Empty index');
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    const knowledge = page.getByRole('region', { name: '知识绑定' });
    await expect(knowledge.getByRole('heading', { name: '冻结索引内没有条目' })).toBeVisible();
    await expect(knowledge.getByText(/不是读取失败/)).toBeVisible();
    await knowledge.getByRole('button', { name: /确认冻结知识选择/ }).click();
    await expect(knowledge.getByText('已确认（CONFIRMED）')).toBeVisible();
  });

  test('a replayed start is presented as an idempotent replay, not a new run', async ({ page }) => {
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('POST', '/api/v1/workspaces/[^/]+/work-items/[^/]+/runs', () => ({
      status: 201,
      body: admissionResponseFixture({ replayed: true }),
    }));
    await createWorkItem(page, HARNESS_WORKSPACE_HINT, 'Replayed start');
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    await expect(page.getByText(/幂等重放：没有创建新的运行/)).toBeVisible();
  });

  test('a POLICY_BLOCKED answer refusal is presented and the page sends no silent retry', async ({ page }) => {
    // Presentation branch for the frozen error envelope: whatever the owner
    // refuses with, the page keeps the selection, names the category and never
    // retries on its own.
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('POST', '/api/v1/workspaces/[^/]+/human-tasks/[^/]+/responses', () => ({
      status: 422,
      body: { error: 'POLICY_BLOCKED' },
    }));
    await createWorkItem(page, HARNESS_WORKSPACE_HINT, 'Refused selection');
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    const knowledge = page.getByRole('region', { name: '知识绑定' });
    await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();
    await knowledge.getByRole('button', { name: /确认冻结知识选择/ }).click();
    await expect(page.getByRole('alert')).toContainText('POLICY_BLOCKED');
    await expect(knowledge.getByRole('heading', { name: '如何恢复' })).toBeVisible();
    // The selection is preserved and nothing was retried automatically.
    await expect(knowledge.getByLabel(/必需条目/).first()).toBeChecked();
    const answers = fixtures?.calls.filter((call) => call.method === 'POST' && call.path.endsWith('/responses'));
    expect(answers?.length).toBe(1);
  });

  test('an accepted answer that binds nothing is never shown as a confirmed binding', async ({ page }) => {
    // The frozen acceptance is decoupled from the binding: a task answer can be
    // accepted while the flow refuses to freeze that selection. The page must
    // then report the unconfirmed binding instead of inventing a success.
    const fixtures = await installApi(page);
    await signIn(page);
    fixtures?.set('POST', '/api/v1/workspaces/[^/]+/human-tasks/[^/]+/responses', () => ({
      status: 200,
      body: { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTask', humanTask: { ...humanTaskFixture(), state: 'RESOLVED', resolutionKind: 'kowa_answer', availableActions: [] } },
    }));
    await createWorkItem(page, HARNESS_WORKSPACE_HINT, 'Unbindable selection');
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    const knowledge = page.getByRole('region', { name: '知识绑定' });
    await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();
    await knowledge.getByRole('button', { name: /确认冻结知识选择/ }).click();
    await expect(page.getByRole('alert')).toContainText(/冻结绑定尚未建立/);
    await expect(knowledge.getByText('已确认（CONFIRMED）')).toHaveCount(0);
    await expect(knowledge.getByLabel(/必需条目/).first()).toBeChecked();
    const answers = fixtures?.calls.filter((call) => call.method === 'POST' && call.path.endsWith('/responses'));
    expect(answers?.length).toBe(1);
  });
});

test.describe('control-plane negative journeys', () => {
  test.skip(!isControlPlane, 'requires the real control plane');

  test('the real server refuses a stale answer and the legal confirmation still succeeds', async ({ page, request, browserName }) => {
    test.slow();
    const { workspaceId, harness } = await prepareWorkspace(page, request, browserName);
    await createWorkItem(page, workspaceId, `Stale answer ${Date.now().toString(36)}`);
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    const knowledge = page.getByRole('region', { name: '知识绑定' });
    await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();
    await expect(knowledge.getByLabel(/必需条目/).first()).toBeDisabled();

    // The request goes out from the page's own origin with the real session and
    // the real CSRF proof, so the refusal is the owner's, not a missing one.
    const task = await openKnowledgeTask(page, workspaceId);
    // Without the CSRF proof the same write is unauthorized, not a half-applied
    // answer: the browser-side refusal and the server-side gate are separate.
    const unproven = await pageRequest(
      page,
      'POST',
      `/api/v1/workspaces/${workspaceId}/human-tasks/${task.humanTaskId}/responses`,
      {
        requestKey: `e2e-no-csrf-${Date.now().toString(36)}`,
        requestVersion: task.request.version,
        object: task.request.object,
        decision: 'approved',
        values: { decision: 'approved', selection: 'docs/design.md' },
      },
    );
    expect(unproven.status).toBe(401);
    expect(unproven.body).toEqual({ error: 'UNAUTHORIZED' });
    const csrf = await pageCsrf(page);
    const refused = await pageRequest(
      page,
      'POST',
      `/api/v1/workspaces/${workspaceId}/human-tasks/${task.humanTaskId}/responses`,
      {
        requestKey: `e2e-stale-${Date.now().toString(36)}`,
        requestVersion: task.request.version + 7,
        object: task.request.object,
        decision: 'approved',
        values: { decision: 'approved', selection: 'docs/design.md' },
      },
      csrf,
    );
    expect(refused.status).toBe(409);
    expect(refused.body).toEqual({ error: 'VERSION_CONFLICT' });

    // The refusal changed nothing: the request is still open and the legal
    // confirmation from the page freezes the binding and releases the flow.
    const stillOpen = await openKnowledgeTask(page, workspaceId);
    expect(stillOpen.humanTaskId).toBe(task.humanTaskId);
    await knowledge.getByRole('button', { name: /确认冻结知识选择/ }).click();
    await expect(knowledge.getByText('已确认（CONFIRMED）')).toBeVisible();
    if (harness) {
      const itemId = page.url().split('/').pop() as string;
      const dispatched = await leaseRunTask(request, harness.runtime, await currentRunId(page, workspaceId, itemId));
      expect(dispatched.status).toBe(200);
      expect(dispatched.capability).toBe('plan.create');
    }
  });

  test('an accepted answer that leaves the frozen index binds nothing and dispatches nothing', async ({ page, request, browserName }) => {
    const { workspaceId, harness } = await prepareWorkspace(page, request, browserName);
    await createWorkItem(page, workspaceId, `Unbindable ${Date.now().toString(36)}`);
    await page.getByRole('button', { name: /启动固定流程/ }).click();
    const knowledge = page.getByRole('region', { name: '知识绑定' });
    await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();

    // The frozen delivery contract accepts the answer as a task answer; the
    // knowledge binding is a separate step and must not have happened for a
    // selection the frozen index refuses.
    const task = await openKnowledgeTask(page, workspaceId);
    const csrf = await pageCsrf(page);
    const accepted = await pageRequest(
      page,
      'POST',
      `/api/v1/workspaces/${workspaceId}/human-tasks/${task.humanTaskId}/responses`,
      {
        requestKey: `e2e-outside-index-${Date.now().toString(36)}`,
        requestVersion: task.request.version,
        object: task.request.object,
        decision: 'approved',
        values: { decision: 'approved', selection: 'not-in-the-index.md' },
      },
      csrf,
    );
    expect(accepted.status).toBe(200);
    const resolved = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/human-tasks/${task.humanTaskId}`);
    expect((resolved.body as { humanTask: { state: string } }).humanTask.state).toBe('RESOLVED');
    if (harness) {
      const itemId = page.url().split('/').pop() as string;
      await expectNoRunTask(request, harness.runtime, await currentRunId(page, workspaceId, itemId));
    }

    // The re-read projection still reports an unconfirmed binding: the page
    // never turns a refused selection into a confirmed one.
    await page.reload();
    await expect(knowledge.getByText('等待人工确认（RECOMMENDED）')).toBeVisible();
    await expect(knowledge.getByText('已确认（CONFIRMED）')).toHaveCount(0);
  });

  test('the real server refuses the same intake request key with different content', async ({ page, request, browserName }) => {
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    const csrf = await pageCsrf(page);
    const requestKey = `e2e-intake-conflict-${Date.now().toString(36)}`;
    const command = {
      requestKey,
      title: 'Intake conflict',
      requirement: { version: 1, content: 'Add a helper that greets deterministically.' },
      acceptance: { version: 1, content: 'The helper has a passing unit test.' },
    };
    const first = await pageRequest(page, 'POST', `/api/v1/workspaces/${workspaceId}/work-items`, command, csrf);
    expect(first.status).toBe(201);
    // The same key with different content is a conflict, not a silent replay:
    // the content digest is inside the request identity.
    const second = await pageRequest(
      page,
      'POST',
      `/api/v1/workspaces/${workspaceId}/work-items`,
      { ...command, title: 'Intake conflict, changed' },
      csrf,
    );
    expect(second.status).toBe(409);
    expect(second.body).toEqual({ error: 'VERSION_CONFLICT' });
    const list = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/work-items`);
    const items = (list.body as { workItems: unknown[] }).workItems;
    expect(items, 'the refused retry must not create a second work item').toHaveLength(1);
  });

  test('a stale start survives a real version conflict and reports the resulting policy refusal', async ({ page, browser, request, browserName }) => {
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    const title = `Stale start ${Date.now().toString(36)}`;
    await createWorkItem(page, workspaceId, title);

    // A second session of the same member starts the run first.
    const otherContext = await browser.newContext({ ignoreHTTPSErrors: true, baseURL: controlPlaneURL });
    const other = await otherContext.newPage();
    await signIn(other);
    await other.goto(page.url());
    await other.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
    await expect(other.getByRole('region', { name: '知识绑定' })).toBeVisible();
    await otherContext.close();

    // The stale page still holds v1: the real command refuses it, the page
    // re-reads the server projection and names both versions instead of
    // replaying silently. Since the MVP admits one advancement run per work
    // item, the re-read replaces the start entry with the run that really
    // exists — the refusal stays visible as the explanation.
    await page.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
    await expect(page.getByText(/启动被拒绝：你提交时基于 v1，服务端当前为 v2/)).toBeVisible();
    await expect(page.getByRole('region', { name: '知识绑定' })).toBeVisible();
    await expect(page.getByText(/一个工作项同时只有一条推进运行/)).toBeVisible();
    await expect(page.getByRole('button', { name: /启动固定流程/ })).toHaveCount(0);
  });

  test('a workspace where the session is not a member refuses reads and starts', async ({ page, request, browserName }) => {
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    await createWorkItem(page, workspaceId, `Cross workspace denial ${Date.now().toString(36)}`);
    const itemId = page.url().split('/').pop() as string;
    const foreign = 'ws_not_a_member';

    // The server re-decides per request: the same session is not a member of
    // another workspace, so reading its object is FORBIDDEN — not an empty page.
    const read = await pageRequest(page, 'GET', `/api/v1/workspaces/${foreign}/work-items/${itemId}`);
    expect(read.status).toBe(403);
    expect(read.body).toEqual({ error: 'FORBIDDEN' });
    const started = await pageRequest(
      page,
      'POST',
      `/api/v1/workspaces/${foreign}/work-items/${itemId}/runs`,
      { requestKey: `cross-workspace-${Date.now().toString(36)}`, expectedVersion: 1 },
      await pageCsrf(page),
    );
    expect(started.status).toBe(403);

    // The browser journey shows the same refusal and offers no start control.
    await page.goto(`/workspaces/${foreign}/work-items/${itemId}`);
    await expect(page.getByRole('alert')).toContainText('FORBIDDEN');
    await expect(page.getByRole('button', { name: /启动固定流程/ })).toHaveCount(0);
  });

  test('an anonymous visitor never sees work item content', async ({ page }) => {
    await page.goto('/workspaces/workspace-a1b2c3d4/work-items');
    await expect(page.getByRole('button', { name: '使用 GitHub 登录' })).toBeVisible();
    expect(await page.getByRole('heading', { name: '工作项列表' }).count()).toBe(0);
  });
});
