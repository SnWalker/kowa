import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { installApi } from './fixtures';
import {
  ensureWorkspace,
  fillDualRepositories,
  harnessFixtures,
  pageRequest,
  publishExecutionConfig,
  registerFixedFlowRuntime,
  signIn,
} from './journey';
import { isControlPlane } from './mode';

/**
 * The shared preparations of the frontend:S07 whole-app suite.
 *
 * frontend:S01—S06 each carried their own copy of these journeys. S07 audits
 * every page, so the same preparation would otherwise be written four more
 * times; it lives here instead, with the S07 runtime identity (`{id}-{engine}-s07`)
 * that the harness requires so a repeated registration never advances the epoch
 * of a runtime another stage's journey still asserts on.
 */

export const HARNESS_WORKSPACE_HINT = 'workspace-a1b2c3d4';
const RUNTIME_EPOCH = 'epoch-e2e-s07';

/** Signs in against whichever target this run is pointed at, and returns a usable workspace. */
export async function signInForS07(page: Page): Promise<string> {
  if (!isControlPlane) {
    await installApi(page);
    await signIn(page);
    return HARNESS_WORKSPACE_HINT;
  }
  return ensureWorkspace(page);
}

/**
 * Binds the workspace's dual repositories through the real settings page.
 *
 * A workspace created through the real command has no bindings, and the intake
 * form is correctly disabled until it does (`WorkItemCreatePage` reads the
 * projection, not a local guess). Fixture mode already serves a configured
 * workspace, so this is a no-op there.
 */
export async function bindRepositories(page: Page, request: APIRequestContext, workspaceId: string): Promise<void> {
  if (!isControlPlane) return;
  const harness = await harnessFixtures(request);
  await page.goto(`/workspaces/${workspaceId}/settings`);
  await fillDualRepositories(page, harness.project.repositoryId, harness.knowledge.repositoryId);
  await page.getByRole('button', { name: /保存（基于/ }).click();
  await expect(page.getByText(/已保存为版本/)).toBeVisible();
}

/**
 * Makes the workspace able to really admit a run: dual repositories bound,
 * one scripted runtime registered over the frozen transport, and the execution
 * configuration publishing its capabilities through the real page.
 *
 * Returns the runtime id this browser project may read, which is also the id
 * whose detail page the audit visits.
 */
export async function prepareRunnableWorkspace(
  page: Page,
  request: APIRequestContext,
  browserName: string,
  workspaceId: string,
): Promise<string> {
  if (!isControlPlane) return 'runtime-mac-codex';
  const harness = await harnessFixtures(request);
  await bindRepositories(page, request, workspaceId);
  const baseToken = process.env.KOWA_E2E_RUNTIME_TOKEN ?? 'harness-runtime-token-0123456789';
  const runtimeId = `${harness.runtime.runtimeId}-${browserName}-s07`;
  await registerFixedFlowRuntime(
    request,
    { ...harness.runtime, runtimeId, epoch: `${RUNTIME_EPOCH}-${browserName}`, token: `${baseToken}-${browserName}-s07` },
    harness.capabilities,
  );
  await publishExecutionConfig(page, workspaceId, runtimeId, harness.capabilities);
  return runtimeId;
}

/** Creates one work item through the real intake page and returns its identity. */
export async function createWorkItemThroughPage(
  page: Page,
  workspaceId: string,
  prefix: string,
): Promise<{ workItemId: string; title: string }> {
  const title = `${prefix} ${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
  await page.goto(`/workspaces/${workspaceId}/work-items/new`);
  await page.getByLabel('标题', { exact: true }).fill(title);
  await page.getByLabel('需求', { exact: true }).fill('Audit every MVP page against the real projection.');
  await page.getByLabel('验收标准', { exact: true }).fill('Each page renders its own facts in both themes and at 320px.');
  await page.getByRole('button', { name: '创建工作项' }).click();
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  return { workItemId: page.url().split('/').pop() as string, title };
}

/**
 * Admits the fixed flow from the work item detail page and waits for the DAG.
 * The caller must already be on `/workspaces/{id}/work-items/{workItemId}`.
 */
export async function startRunThroughPage(page: Page): Promise<void> {
  await page.getByRole('button', { name: /启动固定流程（基于 v1）/ }).click();
  await expect(page.getByRole('region', { name: '固定流程 DAG' })).toBeVisible();
}

/**
 * The knowledge confirmation the admitted run opened, read from the page's own
 * origin — fixture mode answers `/api/**` at the browser boundary, so a request
 * issued outside the page would reach the static server instead of the double.
 */
export async function openHumanTaskThroughPage(page: Page, workspaceId: string): Promise<string> {
  const result = await pageRequest(page, 'GET', `/api/v1/workspaces/${workspaceId}/human-tasks?state=open`);
  expect(result.status).toBe(200);
  const first = (result.body as { humanTasks: { humanTaskId: string }[] }).humanTasks[0]?.humanTaskId;
  expect(first, 'the admitted run must have opened one human request').toBeTruthy();
  return first as string;
}
