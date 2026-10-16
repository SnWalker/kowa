import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { installApi, type FixtureServer } from './support/fixtures';
import {
  FIXTURE_DELIVERY_COMMIT,
  FIXTURE_MERGE_COMMIT,
  FIXTURE_RUN_ID,
  FIXTURE_WORK_ITEM_ID,
  FIXTURE_PR_NUMBER,
  closureRecordFixture,
  deliveryViewFixture,
  pullRequestObservationFixture,
  reviewObservationFixture,
  checksObservationFixture,
  workItemFixture,
} from '../fixtures/contracts';
import {
  ensureWorkspace,
  fillDualRepositories,
  harnessFixtures,
  publishExecutionConfig,
  registerFixedFlowRuntime,
  signIn,
  type HarnessFixtures,
} from './support/journey';
import { isControlPlane } from './support/mode';

const HARNESS_WORKSPACE_HINT = 'workspace-a1b2c3d4';
const RUNTIME_EPOCH = 'epoch-e2e-delivery';

/**
 * Opens one work item whose delivery evidence the browser can read. Fixture mode
 * starts the fixed flow against the contract-bound double; control-plane mode
 * binds real repositories, registers its own scripted runtime and starts a real
 * run, so the delivery projection the page reads is the owner's own.
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
  // Each stage admits runs under its own runtime id: the harness binds a token
  // to exactly one runtime, so reusing another stage's would move its generation.
  await registerFixedFlowRuntime(
    request,
    {
      ...harness.runtime,
      runtimeId: `${harness.runtime.runtimeId}-${browserName}-s06`,
      epoch: `${RUNTIME_EPOCH}-${browserName}`,
      token: `${baseToken}-${browserName}-s06`,
    },
    harness.capabilities,
  );
  await publishExecutionConfig(page, workspaceId, `${harness.runtime.runtimeId}-${browserName}-s06`, harness.capabilities);
  return { workspaceId, harness };
}

async function startRun(page: Page, workspaceId: string): Promise<void> {
  const title = `Delivery journey ${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
  await page.goto(`/workspaces/${workspaceId}/work-items/new`);
  await page.getByLabel('标题', { exact: true }).fill(title);
  await page.getByLabel('需求', { exact: true }).fill('Add a helper that greets deterministically.');
  await page.getByLabel('验收标准', { exact: true }).fill('The helper has a passing unit test.');
  await page.getByRole('button', { name: '创建工作项' }).click();
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await page.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
  await expect(page.getByRole('region', { name: '知识绑定' })).toBeVisible();
}

function deliveryRegion(page: Page) {
  return page.getByRole('region', { name: '交付证据与关闭' });
}

test('the delivery evidence names every identity separately and offers no merge control', async ({ page, request, browserName }) => {
  test.slow();
  // The populated panel needs the reconciled GitHub facts the double serves;
  // the control-plane journey below covers the same region on the real root.
  test.skip(isControlPlane, 'the fixture double serves the reconciled facts');
  const { workspaceId } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);

  const panel = deliveryRegion(page);
  await expect(panel).toBeVisible();
  await expect(panel.getByText('署名与身份分层')).toBeVisible();

  // Five layers, five rows: the initiator never stands in for the writer.
  await expect(panel.getByText('Web 发起人（当前 WorkflowRun）', { exact: true })).toBeVisible();
  await expect(panel.getByText('Runner 机器 gh 账号（运行准入时冻结）', { exact: true })).toBeVisible();
  await expect(panel.getByText('GitHub PR 实际创建者', { exact: true })).toBeVisible();
  await expect(panel.getByText('远端 commit author / committer', { exact: true })).toBeVisible();
  await expect(panel.getByText('未在任何已冻结的浏览器消费合同里携带')).toBeVisible();

  // No second approval, merge or close exists on this page.
  await expect(panel.getByRole('button', { name: /合并/ })).toHaveCount(0);
  await expect(panel.getByRole('button', { name: /批准/ })).toHaveCount(0);
  await expect(panel.getByText(/没有第二个合并批准按钮/)).toBeVisible();

  const siteLinks = panel.getByRole('link');
  for (const href of await siteLinks.evaluateAll((anchors) => anchors.map((anchor) => anchor.getAttribute('href') ?? ''))) {
    if (href.startsWith('http')) expect(href.startsWith('https://github.com/')).toBe(true);
  }
});

test('the quality evidence and the closure checklist are read, not decided by the page', async ({ page, request, browserName }) => {
  test.slow();
  test.skip(isControlPlane, 'the fixture double serves the reconciled facts');
  const { workspaceId } = await prepareWorkspace(page, request, browserName);
  await startRun(page, workspaceId);
  const panel = deliveryRegion(page);
  await expect(panel.getByText('质量证据（只读）')).toBeVisible();
  // The gate rows and the auxiliary rows are always told apart.
  await expect(panel.getByText(/属同步门禁/)).toBeVisible();
  await expect(panel.getByText(/失败不阻塞交付关闭/)).toBeVisible();
  await expect(panel.getByText(/本页不代替它做决定/)).toBeVisible();
});

test('fixture-mode delivery states: merged but open, unknown reads and head drift', async ({ page }) => {
  test.skip(isControlPlane, 'the fixture double serves these states faithfully');
  const server = await installApi(page);
  await signIn(page);
  // A real work item and a real admitted run: the panel's states are then
  // swapped through the double, which is what a different GitHub outcome looks
  // like from the browser's side.
  await startRun(page, HARNESS_WORKSPACE_HINT);
  const itemPath = new URL(page.url()).pathname;
  expect(server).toBeDefined();
  const serveDelivery = (body: unknown) =>
    server!.set('GET', '/api/v1/workspaces/[^/]+/work-items/[^/]+/delivery', () => ({ status: 200, body }));

  // A merged PR with no closure record: the merge stays visible next to the
  // missing closure, and the work item is not presented as closed.
  serveDelivery(deliveryViewFixture({
    workItem: workItemFixture({ state: 'OPEN', currentRunId: undefined }),
    closure: undefined,
  }));
  await page.goto(itemPath);
  const panel = deliveryRegion(page);
  await expect(panel.getByText('GitHub 已报告合并，但工作项未关闭')).toBeVisible();
  await expect(panel.getByText(FIXTURE_MERGE_COMMIT).first()).toBeVisible();
  await expect(panel.getByText(/服务端还没有为该工作项提交关闭记录/)).toBeVisible();

  // A failed read is its own answer, never a fact and never a pass.
  serveDelivery(deliveryViewFixture({
    workItem: workItemFixture({ state: 'OPEN', currentRunId: undefined }),
    facts: [
      pullRequestObservationFixture(),
      reviewObservationFixture(),
      checksObservationFixture({ observationId: 'obs_checks_unknown', unknown: true, checks: undefined, unknownReason: 'gh exited non-zero' }),
    ],
  }));
  await page.goto(itemPath);
  await expect(panel.getByText(/未成功读取的 GitHub 观察 1 条/)).toBeVisible();
  await expect(panel.getByText(/原因“gh exited non-zero”/)).toBeVisible();

  // A review bound to another commit never reads as applying to the head.
  serveDelivery(deliveryViewFixture({
    workItem: workItemFixture({ state: 'OPEN', currentRunId: undefined }),
    facts: [
      pullRequestObservationFixture({ pullRequest: { ...pullRequestObservationFixture().pullRequest!, headOid: FIXTURE_MERGE_COMMIT } }),
      reviewObservationFixture(),
      checksObservationFixture(),
    ],
  }));
  await page.goto(itemPath);
  await expect(panel.getByText('不适用于当前 head')).toBeVisible();
  await expect(panel.getByText(new RegExp(`head 已经前进到 ${FIXTURE_MERGE_COMMIT}`))).toBeVisible();

  // Every item of the closure record is re-read against another fact.
  // The closure names the same run the item points at, so the only bindings
  // that disagree are the two that name the merge commit.
  serveDelivery(deliveryViewFixture({
    workItem: workItemFixture({ state: 'CLOSED', currentRunId: FIXTURE_RUN_ID }),
    runBindings: [{ workItemId: FIXTURE_WORK_ITEM_ID, workflowRunId: FIXTURE_RUN_ID, kind: 'initial', boundByGitHubUserId: '101', boundAt: '2026-10-16T08:05:00Z' }],
    closure: closureRecordFixture({ workflowRunId: FIXTURE_RUN_ID, mergeCommitOid: FIXTURE_DELIVERY_COMMIT }),
  }));
  await page.goto(itemPath);
  await expect(panel.getByText('关闭清单逐项重读')).toBeVisible();
  await expect(panel.getByText('2 项没有重读一致：实际合并、合并后验证。')).toBeVisible();
});

test.describe('control-plane journeys', () => {
  test.skip(!isControlPlane, 'requires the real control plane');

  test('the real control plane answers the delivery projection without inventing a delivery', async ({ page, request, browserName }) => {
    test.slow();
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    await startRun(page, workspaceId);

    const panel = deliveryRegion(page);
    await expect(panel).toBeVisible();
    // No external operation has run in this harness, and the page says exactly
    // that instead of showing a pass.
    await expect(panel.getByText(/服务端还没有为这个工作项登记任何外部交付操作/)).toBeVisible();
    await expect(panel.getByText(/这不是「已通过」/)).toBeVisible();
    await expect(panel.getByText(/没有已核对的 PR、Review 或 CI 事实/)).toBeVisible();
    await expect(panel.getByRole('button', { name: /合并/ })).toHaveCount(0);

    // The watermark projection is the owner's own: either it answered with
    // watermarks, or it answered with a failure — both are rendered as
    // themselves and neither becomes "everything is fresh".
    const watermarkSection = panel.getByText('最后成功观察与水位');
    await expect(watermarkSection).toBeVisible();
    await expect(panel.getByText(/最后一次成功观察（本投影）/)).toBeVisible();
  });
});

for (const theme of ['light', 'dark'] as const) {
  test(`the delivery panel is accessible in ${theme} (axe, 320px/200%)`, async ({ page, request, browserName }) => {
    test.slow();
    test.skip(isControlPlane, 'the fixture double serves the populated panel');
    const { workspaceId } = await prepareWorkspace(page, request, browserName);
    await startRun(page, workspaceId);
    await page.emulateMedia({ colorScheme: theme });
    const panel = deliveryRegion(page);
    await expect(panel.getByText('质量证据（只读）')).toBeVisible();

    const result = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice'])
      .analyze();
    expect(result.violations, `delivery panel ${theme}`).toEqual([]);

    await page.setViewportSize({ width: 320, height: 640 });
    await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
    await expect(panel.getByText('署名与身份分层')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
  });
}

test('a work item without a run has no delivery facts and says so', async ({ page }) => {
  test.skip(isControlPlane, 'the fixture double serves the empty projection');
  const fixtures = await installApi(page);
  await signIn(page);
  await page.goto(`/workspaces/${HARNESS_WORKSPACE_HINT}/work-items/new`);
  await page.getByLabel('标题', { exact: true }).fill('No run yet');
  await page.getByLabel('需求', { exact: true }).fill('Nothing has run.');
  await page.getByLabel('验收标准', { exact: true }).fill('It stays untouched.');
  await page.getByRole('button', { name: '创建工作项' }).click();
  await expect(page.getByRole('heading', { name: 'No run yet' })).toBeVisible();
  const panel = deliveryRegion(page);
  await expect(panel.getByText(/服务端还没有为这个工作项登记任何外部交付操作/)).toBeVisible();
  // Reading the panel never starts anything: opening the page issues no run
  // command, and no delivery command at all.
  expect(fixtures?.calls.filter((call) => call.method === 'POST' && (call.path.endsWith('/runs') || call.path.endsWith('/delivery'))) ?? [])
    .toHaveLength(0);
});

test('the PR facts stay separated from the merge fact they did not produce', async ({ page }) => {
  test.skip(isControlPlane, 'the fixture double serves the populated panel');
  const server = await installApi(page);
  await signIn(page);
  await startRun(page, HARNESS_WORKSPACE_HINT);
  const itemPath = new URL(page.url()).pathname;
  await server!.set('GET', '/api/v1/workspaces/[^/]+/work-items/[^/]+/delivery', () => ({
    status: 200,
    body: deliveryViewFixture({ workItem: workItemFixture({ state: 'OPEN', currentRunId: undefined }), closure: undefined }),
  }));
  await page.goto(itemPath);
  const panel = deliveryRegion(page);
  // The PR is presented with its author, its head and its merge, and the page
  // links out to GitHub rather than restating GitHub's answer.
  const prLink = panel.getByRole('link', { name: `Pull Request ${FIXTURE_PR_NUMBER}（在 GitHub 新标签页打开）` }).first();
  await expect(prLink).toHaveAttribute('href', `https://github.com/team/project/pull/${FIXTURE_PR_NUMBER}`);
  await expect(prLink).toHaveAttribute('rel', 'noreferrer noopener');
  await expect(panel.getByText(/不证明人亲自操作/)).toBeVisible();
});
