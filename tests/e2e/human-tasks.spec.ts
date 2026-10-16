import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { installApi, type FixtureServer } from './support/fixtures';
import { externalFactHumanTaskFixture, humanTaskFixture, recoveryResponseFixture } from '../fixtures/contracts';
import {
  ensureWorkspace,
  fillDualRepositories,
  harnessFixtures,
  pageCsrf,
  pageRequest,
  publishExecutionConfig,
  registerFixedFlowRuntime,
  signIn,
  type HarnessFixtures,
} from './support/journey';
import { isControlPlane } from './support/mode';

const HARNESS_WORKSPACE_HINT = 'workspace-a1b2c3d4';
const RUNTIME_EPOCH = 'epoch-e2e-human-tasks';

/**
 * Opens the workspace's human requests without inventing one: fixture mode
 * starts the fixed flow against the contract-bound double, control-plane mode
 * binds real repositories, registers its own scripted runtime and starts a real
 * run, so the request the browser reads is the owner's own.
 */
async function prepareWorkspace(
  page: Page,
  request: APIRequestContext,
  browserName: string,
): Promise<{ workspaceId: string; fixtures?: FixtureServer; harness?: HarnessFixtures }> {
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
  const baseToken = process.env.KOWA_E2E_RUNTIME_TOKEN ?? 'harness-runtime-token-0123456789';
  await registerFixedFlowRuntime(
    request,
    {
      ...harness.runtime,
      runtimeId: `${harness.runtime.runtimeId}-${browserName}-s05`,
      epoch: `${RUNTIME_EPOCH}-${browserName}`,
      token: `${baseToken}-${browserName}-s05`,
    },
    harness.capabilities,
  );
  await publishExecutionConfig(page, workspaceId, `${harness.runtime.runtimeId}-${browserName}-s05`, harness.capabilities);
  return { workspaceId, harness };
}

async function startRun(page: Page, workspaceId: string): Promise<void> {
  const title = `Human task journey ${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
  await page.goto(`/workspaces/${workspaceId}/work-items/new`);
  await page.getByLabel('标题', { exact: true }).fill(title);
  await page.getByLabel('需求', { exact: true }).fill('Add a helper that greets deterministically.');
  await page.getByLabel('验收标准', { exact: true }).fill('The helper has a passing unit test.');
  await page.getByRole('button', { name: '创建工作项' }).click();
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await page.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
  await expect(page.getByRole('region', { name: '知识绑定' })).toBeVisible();
}

/** The request the server really opened for this run, read from the page's origin. */
type OpenTask = {
  humanTaskId: string;
  workItemId: string;
  workflowRunId: string;
  state: string;
  request: { version: number; object: { kind: string; id: string; version: string; digest?: string } };
};

async function firstOpenTask(page: Page, workspaceId: string): Promise<OpenTask> {
  const listed = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/human-tasks?state=open`);
  expect(listed.status).toBe(200);
  const tasks = (listed.body as { humanTasks: OpenTask[] }).humanTasks;
  expect(tasks.length).toBeGreaterThan(0);
  return tasks[0] as OpenTask;
}

test('the human requests of a workspace are listed, versioned and answerable', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);

  // The list is reached from the shell, and every row carries the request, the
  // item and run it belongs to, and the server's own action judgement.
  await page.getByRole('link', { name: '人工待办', exact: true }).click();
  const list = page.getByRole('region', { name: '待办清单' });
  await expect(list.getByText('Confirm the frozen knowledge selection')).toBeVisible();
  await expect(list.getByText(/knowledge_confirmation/)).toBeVisible();
  await expect(list.getByText(/服务端为当前身份返回了 respond 动作|服务端没有为当前身份返回/)).toBeVisible();

  await page.getByRole('link', { name: 'Confirm the frozen knowledge selection' }).click();
  const detail = page.getByRole('region', { name: '人工待办' });
  await expect(detail).toBeVisible();
  await expect(detail.getByText(/Kowa 内答复（kowa_answer）/)).toBeVisible();

  // The reviewed version is the one the request froze, and the page states that
  // it will not re-bind it.
  const object = page.getByRole('region', { name: '所审版本' });
  await expect(object.getByText('knowledge_recommendation')).toBeVisible();
  await expect(object.getByText(/不会改绑/)).toBeVisible();
  await expect(object.getByText(/与该运行上的冻结索引一致/)).toBeVisible();

  // The basis is read from the run projection, not assembled here.
  const basis = page.getByRole('region', { name: '依据' });
  await expect(basis.getByText('docs/design.md')).toBeVisible();
  await expect(basis.getByText(/冻结索引条目共/)).toBeVisible();

  // The frozen fields are rendered, the required decision starts unset, and
  // opening the page wrote nothing.
  const answer = page.getByRole('region', { name: '答复' });
  await expect(answer.getByText('decision', { exact: true })).toBeVisible();
  await expect(answer.getByRole('button', { name: /提交答复/ })).toBeDisabled();
  expect(fixtures?.calls.filter((call) => call.method === 'POST' && call.path.endsWith('/responses')) ?? []).toHaveLength(0);

  await answer.getByRole('radio', { name: /批准/ }).click();
  await expect(answer.getByRole('button', { name: /提交答复/ })).toBeEnabled();
  await answer.getByRole('button', { name: /提交答复/ }).click();

  const accepted = page.getByRole('region', { name: '答复记录' });
  await expect(accepted).toBeVisible();
  await expect(accepted.getByText(/人工动作/)).toBeVisible();
  await expect(accepted.getByText(/PENDING/).first()).toBeVisible();
});

test('a request the server settled by an external fact offers no answer control', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);
  if (fixtures === undefined) {
    // The real control plane has no reconciled GitHub fact in this harness: the
    // external-fact shape is exercised where it can be served faithfully.
    test.skip(true, 'the external-fact request is exercised against the contract-bound double');
    return;
  }
  const external = externalFactHumanTaskFixture();
  fixtures.set('GET', '/api/v1/workspaces/[^/]+/human-tasks/[^/]+$', () => ({ status: 200, body: { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTask', humanTask: external } }));

  await page.goto(`/workspaces/${workspaceId}/human-tasks/${external.humanTaskId}`);
  const answer = page.getByRole('region', { name: '答复' });
  await expect(answer.getByText(/只有已核对的 GitHub 事实/)).toBeVisible();
  await expect(answer.getByRole('button', { name: /批准|驳回|提交答复/ })).toHaveCount(0);
  expect(fixtures.calls.filter((call) => call.method === 'POST' && call.path.endsWith('/responses'))).toHaveLength(0);
});

test('an expired request guides a re-review and never retries the decision', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);
  if (fixtures === undefined) {
    test.skip(true, 'the expiry is exercised against the contract-bound double');
    return;
  }
  const expired = humanTaskFixture({ state: 'EXPIRED', availableActions: [], expiredReason: 'the bound object moved to a newer version' });
  fixtures.set('GET', '/api/v1/workspaces/[^/]+/human-tasks/[^/]+$', () => ({ status: 200, body: { schemaVersion: 'kowa.delivery-governance.v1', kind: 'humanTask', humanTask: expired } }));

  await page.goto(`/workspaces/${workspaceId}/human-tasks/${expired.humanTaskId}`);
  const answer = page.getByRole('region', { name: '答复' });
  await expect(answer.getByText(/the bound object moved to a newer version/)).toBeVisible();
  await expect(answer.getByText(/重新审阅/)).toBeVisible();
  await expect(answer.getByRole('button', { name: /提交答复/ })).toHaveCount(0);
  expect(fixtures.calls.filter((call) => call.method === 'POST' && call.path.endsWith('/responses'))).toHaveLength(0);
});

test('a refused answer names both versions and is never resubmitted by itself', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);
  if (fixtures === undefined) {
    test.skip(true, 'the conflict path is exercised against the contract-bound double');
    return;
  }
  fixtures.set('POST', '/responses$', () => ({ status: 409, body: { error: 'VERSION_CONFLICT' } }));

  await page.goto(`/workspaces/${workspaceId}/human-tasks/ht_5d2a`);
  const answer = page.getByRole('region', { name: '答复' });
  await answer.getByRole('radio', { name: /批准/ }).click();
  await answer.getByRole('button', { name: /提交答复/ }).click();

  await expect(answer.getByText(/请求版本冲突：你提交时基于 v1，服务端当前为 v1/)).toBeVisible();
  await expect(answer.getByRole('button', { name: /按服务端当前版本 v1 提交答复/ })).toBeVisible();
  // One refused attempt, and the decision is still the operator's to re-issue.
  expect(fixtures.calls.filter((call) => call.method === 'POST' && call.path.endsWith('/responses'))).toHaveLength(1);
});

test('the run view offers the frozen recovery commands and submits the one chosen', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);

  // Loading (and re-reading) a run writes nothing.
  const recovery = page.getByRole('region', { name: '失败与恢复' });
  await expect(recovery).toBeVisible();
  await expect(recovery.getByText(/是否接受由服务端裁决/)).toBeVisible();
  await expect(recovery.getByRole('button', { name: '取消运行（cancel）' })).toBeVisible();
  await expect(recovery.getByRole('button', { name: '创建修复运行（repair）' })).toBeDisabled();
  await expect(recovery.getByText(/先选择一个节点/)).toBeVisible();

  await recovery.getByRole('button', { name: '取消运行（cancel）' }).click();
  const applied = recovery.getByText(/服务端已应用该命令（cancel）|命令未被应用（cancel）/);
  await expect(applied).toBeVisible();
  await expect(recovery.getByText(/the double recorded cancel|状态/).first()).toBeVisible();
});

test('the human-task pages have no axe violations in both themes and keep 320px', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);

  for (const theme of ['light', 'dark'] as const) {
    await page.goto(`/workspaces/${workspaceId}/human-tasks`);
    await expect(page.getByRole('region', { name: '待办清单' })).toBeVisible();
    await page.getByLabel('外观').selectOption(theme);
    const listResult = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice'])
      .analyze();
    expect(listResult.violations, `human task list ${theme}`).toEqual([]);

    await page.getByRole('link', { name: 'Confirm the frozen knowledge selection' }).click();
    await expect(page.getByRole('region', { name: '答复' })).toBeVisible();
    const detailResult = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice'])
      .analyze();
    expect(detailResult.violations, `human task detail ${theme}`).toEqual([]);
  }

  await page.setViewportSize({ width: 320, height: 640 });
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(page.getByRole('region', { name: '答复' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
});

test.describe('control-plane journeys', () => {
  test.skip(!isControlPlane, 'requires the real control plane');

  test('the real control plane lists the request, serves its basis and accepts an answer', async ({ page, request, browserName }) => {
    test.slow();
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    await startRun(page, workspaceId);

    const task = await firstOpenTask(page, workspaceId);
    await page.goto(`/workspaces/${workspaceId}/human-tasks`);
    const list = page.getByRole('region', { name: '待办清单' });
    await expect(list.getByText('Confirm the frozen knowledge selection')).toBeVisible();

    await page.goto(`/workspaces/${workspaceId}/human-tasks/${task.humanTaskId}`);
    // The reviewed version is the owner's index digest, and the page's own basis
    // read (the run projection) agrees with it.
    const object = page.getByRole('region', { name: '所审版本' });
    await expect(object.getByText(task.request.object.version).first()).toBeVisible();
    await expect(object.getByText(/与该运行上的冻结索引一致/).first()).toBeVisible();

    // The frozen index the basis shows is the owner's own recommendation.
    const basis = page.getByRole('region', { name: '依据' });
    await expect(basis.getByText(/冻结索引条目共/)).toBeVisible();
    const required = await requiredIndexPaths(page, workspaceId, task);

    // The generic form submits the frozen request version and object binding
    // together with the selection the frozen index declares.
    const answer = page.getByRole('region', { name: '答复' });
    await answer.getByLabel('selection').fill(required.join('\n'));
    await answer.getByRole('radio', { name: /批准/ }).click();
    await answer.getByRole('button', { name: /提交答复/ }).click();
    const accepted = page.getByRole('region', { name: '答复记录' });
    await expect(accepted).toBeVisible();
    await expect(accepted.getByText(/hr_|humanResponseId|回答|人工动作/).first()).toBeVisible();

    // The accepted answer really moved the frozen binding on the run: the page
    // reads that from the server, not from the answer it submitted.
    const bound = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/human-tasks/${task.humanTaskId}`);
    expect((bound.body as { humanTask: { state: string; response?: { decision: string } } }).humanTask.state).toBe('RESOLVED');
  });

  test('the real server version-guards a cancel and applies the one issued from the run view', async ({ page, request, browserName }) => {
    test.slow();
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    await startRun(page, workspaceId);
    const itemId = page.url().split('/').pop() as string;
    const item = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/work-items/${itemId}`);
    const runId = (item.body as { workItem: { currentRunId?: string } }).workItem.currentRunId;
    expect(runId).toBeTruthy();
    const run = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/work-items/${itemId}/runs/${runId}`);
    const runVersion = (run.body as { version: number }).version;
    expect(runVersion).toBeGreaterThan(0);

    // A stale version is refused, exactly as the page states for `cancel`.
    const csrf = await pageCsrf(page);
    const stale = await pageRequest(
      page,
      'POST',
      `/api/v1/workspaces/${workspaceId}/work-items/${itemId}/runs/${runId}/recovery`,
      { kind: 'cancel', expectedVersion: runVersion + 5, requestKey: `e2e-stale-cancel-${Date.now().toString(36)}` },
      csrf,
    );
    expect(stale.status).toBe(409);
    expect(stale.body).toEqual({ error: 'VERSION_CONFLICT' });

    // The same command from the page carries the version the page read.
    const recovery = page.getByRole('region', { name: '失败与恢复' });
    await recovery.getByRole('button', { name: '取消运行（cancel）' }).click();
    await expect(recovery.getByText(/服务端已应用该命令（cancel）/)).toBeVisible();
    await expect(recovery.getByText(/cancellation was requested/)).toBeVisible();

    // The run really moved: the re-read projection reports the server's state.
    const after = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/work-items/${itemId}/runs/${runId}`);
    // A run with no leased attempt settles to CANCELED; with one in flight it
    // first reports CANCELING. Both are cancellations the server recorded.
    expect((after.body as { state: string }).state).toMatch(/^CANCEL(ED|ING)$/);
  });
});

/** The `required` paths of the run's frozen index, read from the run projection. */
async function requiredIndexPaths(page: Page, workspaceId: string, task: OpenTask): Promise<string[]> {
  const run = await pageRequest(
    page,
    'GET',
    `/api/v1/workspaces/${workspaceId}/work-items/${task.workItemId}/runs/${task.workflowRunId}`,
  );
  expect(run.status).toBe(200);
  const recommendation = (run.body as { knowledgeRecommendation?: { candidates: { path: string; required: boolean }[] } }).knowledgeRecommendation;
  const paths = (recommendation?.candidates ?? []).filter((candidate) => candidate.required).map((candidate) => candidate.path);
  expect(paths.length).toBeGreaterThan(0);
  return paths;
}

test('the recovery response renders what the owner answered', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);
  if (fixtures === undefined) {
    test.skip(true, 'the recorded answer is exercised against the contract-bound double');
    return;
  }
  // An `applied: false` answer is a fact about a command that did not take
  // effect, and the page never presents it as recovery.
  fixtures.set('POST', '/recovery$', () => ({ status: 200, body: recoveryResponseFixture({ applied: false, state: 'ACTIVE', version: 2, detail: 'the run was not canceled' }) }));
  const recovery = page.getByRole('region', { name: '失败与恢复' });
  await recovery.getByRole('button', { name: '取消运行（cancel）' }).click();
  await expect(recovery.getByText(/命令未被应用（cancel）/)).toBeVisible();
  await expect(recovery.getByText(/the run was not canceled/)).toBeVisible();
});
