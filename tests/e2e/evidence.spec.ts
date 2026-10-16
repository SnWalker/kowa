import { expect, test, type Page } from '@playwright/test';
import {
  createWorkItemThroughPage,
  openHumanTaskThroughPage,
  prepareRunnableWorkspace,
  signInForS07,
  startRunThroughPage,
} from './support/runnable-workspace';
import { isControlPlane } from './support/mode';

/**
 * The stage-evidence capture of frontend:S07.
 *
 * It is deliberately not part of any acceptance run: it drives the same
 * fixture-mode journeys the audits do and writes the PNGs the record links to,
 * so the record shows the states its claims are about. It only runs when asked
 * for by name, so `pnpm exec playwright test` never writes into `doc/`:
 *
 *     KOWA_E2E_CAPTURE_EVIDENCE=1 \
 *       bash scripts/web-env.sh pnpm exec playwright test \
 *       tests/e2e/evidence.spec.ts --project=chromium
 *
 * Desktop states are 1280x1400 viewport captures; the narrow states are region
 * captures at 320px with doubled text, because a 320px viewport shot of those
 * pages would show only the page header.
 */

const OUT = 'doc/Kowa前端设计/record/evidence';
const DESKTOP = { width: 1280, height: 1400 };
const NARROW = { width: 320, height: 900 };
const WIDE = { width: 2048, height: 1200 };

test.skip(isControlPlane, 'the evidence capture replays fixture-mode states');
test.skip(
  process.env.KOWA_E2E_CAPTURE_EVIDENCE !== '1',
  'set KOWA_E2E_CAPTURE_EVIDENCE=1 to rewrite doc/Kowa前端设计/record/evidence/s07-*.png',
);

async function shot(page: Page, name: string): Promise<void> {
  await page.screenshot({ path: `${OUT}/${name}.png` });
}

test('capture the page states of frontend:S07', async ({ page, request, browserName }) => {
  test.slow();
  await page.setViewportSize(DESKTOP);

  // The public pages, in both themes, including the focus ring the keyboard
  // suite proves: the skip link is the first stop and is captured focused.
  await page.goto('/login');
  await shot(page, 's07-login-light');
  await page.keyboard.press(browserName === 'webkit' ? 'Alt+Tab' : 'Tab');
  await shot(page, 's07-login-focus-ring-light');
  await page.getByLabel('外观').selectOption('dark');
  await shot(page, 's07-login-dark');
  await page.getByLabel('外观').selectOption('light');

  await page.goto('/missing/nested');
  await shot(page, 's07-not-found-light');

  const workspaceId = await signInForS07(page);
  const runtimeId = await prepareRunnableWorkspace(page, request, browserName, workspaceId);

  await page.goto('/workspaces');
  await shot(page, 's07-workspaces-light');

  await page.goto(`/workspaces/${workspaceId}/settings`);
  await expect(page.getByRole('region', { name: '项目仓与知识仓' })).toBeVisible();
  await shot(page, 's07-settings-light');

  await page.goto(`/workspaces/${workspaceId}/runtimes`);
  await expect(page.getByRole('region', { name: '执行配置' })).toBeVisible();
  await shot(page, 's07-runtimes-light');
  await page.getByLabel('外观').selectOption('dark');
  await shot(page, 's07-runtimes-dark');
  await page.getByLabel('外观').selectOption('light');

  await page.goto(`/workspaces/${workspaceId}/runtimes/${runtimeId}`);
  await expect(page.getByRole('region', { name: '最近观测' })).toBeVisible();
  await shot(page, 's07-runtime-detail-light');

  // The wide end of the range the stage contract names.
  await page.setViewportSize(WIDE);
  await page.goto(`/workspaces/${workspaceId}/runtimes/${runtimeId}`);
  await expect(page.getByRole('region', { name: '最近观测' })).toBeVisible();
  await shot(page, 's07-runtime-detail-wide');
  await page.goto(`/workspaces/${workspaceId}/work-items`);
  await expect(page.getByRole('region', { name: '工作项列表' })).toBeVisible();
  await shot(page, 's07-work-items-wide');
  await page.setViewportSize(DESKTOP);

  const item = await createWorkItemThroughPage(page, workspaceId, 'S07 evidence');
  await page.goto(`/workspaces/${workspaceId}/work-items`);
  await shot(page, 's07-work-items-light');

  await page.goto(`/workspaces/${workspaceId}/work-items/new`);
  await expect(page.getByRole('region', { name: '目标与流程' })).toBeVisible();
  await shot(page, 's07-work-item-new-light');

  // The work item detail page before a run exists, at both ends of the range.
  await page.goto(`/workspaces/${workspaceId}/work-items/${item.workItemId}`);
  await expect(page.getByRole('region', { name: '启动固定流程' })).toBeVisible();
  await shot(page, 's07-work-item-detail-prerun-light');

  // Widths that are not 1280 are captured as the region itself: a 320px
  // viewport shot of these pages would show only the page header, which is the
  // same in both states and would prove nothing about the narrow layout.
  await page.setViewportSize(NARROW);
  await page.goto(`/workspaces/${workspaceId}/work-items/${item.workItemId}`);
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(page.getByRole('region', { name: '启动固定流程' })).toBeVisible();
  await page.getByRole('region', { name: '启动固定流程' }).screenshot({ path: `${OUT}/s07-work-item-prerun-320-200-light.png` });

  await page.setViewportSize(DESKTOP);
  await page.goto(`/workspaces/${workspaceId}/work-items/${item.workItemId}`);
  await startRunThroughPage(page);

  // The run view: the DAG with a node selected, the node panel, the recovery
  // area and the delivery evidence panel, in both themes.
  await page.getByRole('region', { name: '固定流程 DAG' }).getByRole('button', { name: /^knowledge_confirm / }).click();
  await expect(page.getByRole('region', { name: '交付证据与关闭' })).toBeVisible();
  await shot(page, 's07-work-item-detail-run-light');
  await page.getByLabel('外观').selectOption('dark');
  await shot(page, 's07-work-item-detail-run-dark');
  await page.getByLabel('外观').selectOption('light');

  await page.setViewportSize(NARROW);
  await page.goto(`/workspaces/${workspaceId}/work-items/${item.workItemId}?node=plan`);
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(page.getByRole('region', { name: '交付证据与关闭' })).toBeVisible();
  await page.getByRole('region', { name: '固定流程 DAG' }).screenshot({ path: `${OUT}/s07-dag-320-200-light.png` });
  // The DAG is legitimately wider than a phone and scrolls inside its own
  // region; that it does not push the document sideways is what the audit
  // asserts, and this capture is the delivery panel below it.
  await page.getByRole('region', { name: '交付证据与关闭' }).screenshot({ path: `${OUT}/s07-delivery-320-200-light.png` });
  await page.setViewportSize(DESKTOP);

  const humanTaskId = await openHumanTaskThroughPage(page, workspaceId);
  await page.goto(`/workspaces/${workspaceId}/human-tasks`);
  await expect(page.getByRole('region', { name: '待办清单' })).toBeVisible();
  await shot(page, 's07-human-tasks-light');

  await page.goto(`/workspaces/${workspaceId}/human-tasks/${humanTaskId}`);
  await expect(page.getByRole('region', { name: '所审版本' })).toBeVisible();
  await shot(page, 's07-human-task-detail-light');
  await page.getByLabel('外观').selectOption('dark');
  await shot(page, 's07-human-task-detail-dark');
});
