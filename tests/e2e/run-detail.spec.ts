import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { installApi, type FixtureServer } from './support/fixtures';
import { nodeHistoryFixture, runDetailFixture } from '../fixtures/contracts';
import {
  ensureWorkspace,
  fillDualRepositories,
  harnessFixtures,
  publishExecutionConfig,
  registerFixedFlowRuntime,
  pageRequest,
  signIn,
  type HarnessFixtures,
} from './support/journey';
import { isControlPlane } from './support/mode';

const HARNESS_WORKSPACE_HINT = 'workspace-a1b2c3d4';
const RUNTIME_EPOCH = 'epoch-e2e-run-detail';

/**
 * Opens the detail page of a work item that really has a delivery run.
 *
 * Fixture mode serves the frozen projections from the contract-bound double;
 * control-plane mode binds local bare repositories, registers a scripted
 * runtime and publishes the execution configuration through the real page, so
 * the DAG and the run projection the browser reads are the owner's own.
 */
async function prepareRun(
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
      runtimeId: `${harness.runtime.runtimeId}-${browserName}-s04`,
      epoch: `${RUNTIME_EPOCH}-${browserName}`,
      token: `${baseToken}-${browserName}-s04`,
    },
    harness.capabilities,
  );
  await publishExecutionConfig(page, workspaceId, `${harness.runtime.runtimeId}-${browserName}-s04`, harness.capabilities);
  return { workspaceId, harness };
}

async function createWorkItemAndStart(page: Page, workspaceId: string): Promise<void> {
  const title = `DAG journey ${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
  await page.goto(`/workspaces/${workspaceId}/work-items/new`);
  await page.getByLabel('标题', { exact: true }).fill(title);
  await page.getByLabel('需求', { exact: true }).fill('Add a helper that greets deterministically.');
  await page.getByLabel('验收标准', { exact: true }).fill('The helper has a passing unit test.');
  await page.getByRole('button', { name: '创建工作项' }).click();
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await page.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
  await expect(page.getByRole('region', { name: '固定流程 DAG' })).toBeVisible();
}

/** The run the server really bound to this work item (the URL names the item). */
async function boundRunId(page: Page, workspaceId: string): Promise<string> {
  const workItemId = page.url().split('/').pop() as string;
  const item = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/work-items/${workItemId}`);
  expect(item.status).toBe(200);
  const runId = (item.body as { workItem: { currentRunId?: string } }).workItem.currentRunId;
  expect(runId, 'the admitted work item must carry its delivery run').toBeTruthy();
  return runId as string;
}

test('the run view draws the frozen DAG and keeps state, verdict and history apart', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId } = await prepareRun(page, request, browserName);
  await createWorkItemAndStart(page, workspaceId);

  const dag = page.getByRole('region', { name: '固定流程 DAG' });
  // The definition identity the run was admitted with is on the section, and
  // the graph states whether the definition view still matches it.
  await expect(dag.getByText(/kowa-fixed-workflow@1/)).toBeVisible();
  await expect(dag.getByText(/与运行冻结的定义一致/)).toBeVisible();

  // A node the run has reached carries its own state and the owner's verdict
  // token; a node it has not reached yet is drawn as unmaterialised rather than
  // omitted, so the picture never implies more progress than the run has.
  const confirmed = dag.getByRole('button', { name: /^knowledge_confirm / });
  const downstream = dag.getByRole('button', { name: /^plan / });
  await expect(downstream).toContainText('未物化');
  await expect(downstream).toContainText('裁决 未记录');

  // Selecting a node is recoverable from the URL, and the detail panel then
  // reads the node back out of the frozen definition.
  await downstream.click();
  await expect(page).toHaveURL(/\?node=plan/);
  const panel = page.getByRole('region', { name: '节点详情' });
  await expect(panel.getByText('plan', { exact: true })).toBeVisible();
  await expect(panel.getByText('plan.create@1')).toBeVisible();

  // The same view survives a reload: the URL is the selection.
  await page.reload();
  await expect(page.getByRole('region', { name: '节点详情' }).getByText('plan', { exact: true })).toBeVisible();

  // Keyboard: the graph is walked with the arrow keys, and Enter opens the node.
  await confirmed.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(dag.getByRole('button', { name: /^knowledge_recommend / })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\?node=knowledge_recommend/);
  await expect(page.getByRole('region', { name: '节点详情' }).getByText('knowledge.recommend')).toBeVisible();

  // Counts are counts of server rounds: they name skipped and failed states
  // even at zero and never turn into a completion claim.
  const stats = page.getByRole('region', { name: '本轮状态统计' });
  await expect(stats).toContainText('跳过（SKIPPED）');
  await expect(stats).toContainText(/不是完成度/);
});

test('an earlier iteration is labelled as history and never as the current result', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareRun(page, request, browserName);
  if (fixtures === undefined) {
    // The re-round belongs to the frozen projection's own shape, which the real
    // control plane only produces when a revision really happens; that journey
    // is the owner's, and is not fabricated here.
    test.skip(true, 'the re-round projection is exercised against the contract-bound double');
    return;
  }
  await createWorkItemAndStart(page, workspaceId);
  const runId = await boundRunId(page, workspaceId);

  fixtures.set('GET', '/api/v1/workspaces/[^/]+/work-items/[^/]+/runs/[^/]+', () => ({
    status: 200,
    body: runDetailFixture({
      workflowRunId: runId,
      knowledgeRecommendation: undefined,
      nodes: [
        nodeHistoryFixture({ nodeId: 'plan', iteration: 1, state: 'DECIDED', verdict: 'rejected' }),
        nodeHistoryFixture({ nodeId: 'plan', iteration: 2, state: 'DECIDED', verdict: 'needs_revision' }),
        nodeHistoryFixture({ nodeId: 'design_tests', iteration: 1, state: 'SKIPPED', verdict: undefined }),
      ],
    }),
  }));
  await page.reload();

  const graph = page.getByRole('region', { name: '固定流程 DAG' });
  const plan = graph.getByRole('button', { name: /^plan / });
  await expect(plan).toContainText('needs_revision');
  await expect(plan).toContainText(/历史轮次 1/);

  const rounds = page.getByRole('region', { name: '运行轮次与历史' });
  await expect(rounds.getByText(/当前轮次（第 2 轮）/)).toBeVisible();
  await expect(rounds.getByText(/历史轮次（第 1 轮）/)).toBeVisible();
  // The earlier verdict is still readable, and it is not the current one.
  await expect(rounds.getByText('rejected', { exact: true })).toBeVisible();
  await expect(rounds.getByText('needs_revision', { exact: true })).toBeVisible();

  // A skipped node stays on the graph and is counted under its own name.
  await expect(graph.getByRole('button', { name: /^design_tests / })).toContainText('SKIPPED');
  await expect(page.getByRole('region', { name: '本轮状态统计' })).toContainText(/跳过（SKIPPED）\s*1 条/);
});

test('a broken live read is stated as stale instead of passing off the last read as current', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareRun(page, request, browserName);
  if (fixtures === undefined) {
    test.skip(true, 'the disconnect belongs to the browser read, exercised against the double');
    return;
  }
  await createWorkItemAndStart(page, workspaceId);
  await expect(page.getByText(/最后观测/)).toBeVisible();

  // The read starts failing while the run is still live: the page keeps the
  // projection it really read, and says that nothing after it is known.
  fixtures.set('GET', '/api/v1/workspaces/[^/]+/work-items/[^/]+/runs/[^/]+', () => ({
    status: 503,
    body: { error: 'RESOURCE_UNAVAILABLE' },
  }));
  await expect(page.getByText(/实时更新已断开/)).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(/此后的事实未知/)).toBeVisible();
  await expect(page.getByRole('button', { name: /立即重读/ })).toBeVisible();
  // The retained projection is still on screen — and it is still labelled as
  // the last observation, not as the present fact.
  await expect(page.getByRole('region', { name: '固定流程 DAG' })).toBeVisible();
});

test('the artifacts of a work item are references with no invented content link', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareRun(page, request, browserName);
  await createWorkItemAndStart(page, workspaceId);

  const artifacts = page.getByRole('region', { name: '产物与证据' });
  // No browser-facing operation serves artifact content, so the section never
  // offers a link to it: everything here is an identity and a digest.
  await expect(artifacts.getByRole('link')).toHaveCount(0);

  if (fixtures !== undefined) {
    // The double publishes one deliverable for the admitted run, and it is
    // shown as a reference with its own identity and digest.
    await expect(artifacts.getByText(/^art_fixture_/)).toBeVisible();
    await expect(artifacts.getByText(/已发布（PUBLISHED）/)).toBeVisible();
    await expect(artifacts.getByRole('code').first()).toBeVisible();
  } else {
    // Against the real control plane nothing has been produced yet at the
    // knowledge gate, and the empty list is reported as a successful empty
    // read rather than as a failure.
    await expect(artifacts.getByRole('heading', { name: '还没有已发布产物' })).toBeVisible();
    await expect(artifacts.getByText(/空清单是成功读取后的结果/)).toBeVisible();
  }
});

test('the frozen definition is read from the owner, not inferred from the run projection', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId, fixtures } = await prepareRun(page, request, browserName);
  await createWorkItemAndStart(page, workspaceId);

  if (fixtures !== undefined) {
    await expect
      .poll(() => fixtures.calls.filter((call) => call.path.endsWith('/fixed-workflow')).length)
      .toBeGreaterThan(0);
    await expect
      .poll(() => fixtures.calls.filter((call) => call.path.endsWith('/artifacts')).length)
      .toBeGreaterThan(0);
    // A definition the browser cannot place is refused, not drawn approximately:
    // the edges are the owner's, and a view without them is not a graph.
    fixtures.set('GET', '/api/v1/workspaces/[^/]+/fixed-workflow', () => ({
      status: 503,
      body: { error: 'RESOURCE_UNAVAILABLE' },
    }));
    await page.reload();
    const dag = page.getByRole('region', { name: '固定流程 DAG' });
    await expect(dag.getByRole('alert')).toContainText('RESOURCE_UNAVAILABLE');
    await expect(dag.getByRole('button', { name: /^plan / })).toHaveCount(0);
  } else {
    // Against the real composition root the same read is the owner's own view.
    const response = await page.request.get(`/api/v1/workspaces/${workspaceId}/fixed-workflow`);
    expect(response.status()).toBe(200);
    const view = (await response.json()) as { kind: string; order: string[]; nodes: { id: string }[] };
    expect(view.kind).toBe('fixedWorkflowView');
    expect(view.order.length).toBeGreaterThan(0);
    expect(view.nodes.length).toBe(view.order.length);
  }
});

test('the run view has no axe violations in both themes and keeps 320px without horizontal scrolling', async ({ page, request, browserName }) => {
  test.slow();
  const { workspaceId } = await prepareRun(page, request, browserName);
  await createWorkItemAndStart(page, workspaceId);

  for (const theme of ['light', 'dark'] as const) {
    // A node is opened as well, so the analysis covers the detail panel and not
    // only the graph: the two are the states an operator really reads.
    await page.getByRole('region', { name: '固定流程 DAG' }).getByRole('button', { name: /^knowledge_confirm / }).click();
    await expect(page.getByRole('region', { name: '节点详情' })).toBeVisible();
    await page.getByLabel('外观').selectOption(theme);
    await expect(page.getByRole('main')).toBeVisible();
    const result = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice'])
      .analyze();
    expect(result.violations, `run detail ${theme}`).toEqual([]);
  }

  // The graph is wider than a phone on purpose; it scrolls inside its own
  // region and must not push the document itself sideways.
  await page.setViewportSize({ width: 320, height: 640 });
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(page.getByRole('region', { name: '固定流程 DAG' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
});
