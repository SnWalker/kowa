import AxeBuilder from '@axe-core/playwright';
import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import {
  bindRepositories,
  createWorkItemThroughPage,
  openHumanTaskThroughPage,
  prepareRunnableWorkspace,
  signInForS07,
  startRunThroughPage,
} from './support/runnable-workspace';

/**
 * The whole-app audit of frontend:S07.
 *
 * frontend:S01—S06 each proved their own pages; this spec is the single place
 * that walks *every* route the application declares, in both themes and at
 * 320px with doubled text, against whichever target the run is pointed at (the
 * contract-bound fixture double, or the real control plane with isolated
 * external adapters). Two rules make the sweep honest rather than best-effort:
 *
 *  - the catalogue is checked against `src/app/App.tsx`, so adding a route
 *    without auditing it fails the suite instead of silently shrinking cover;
 *  - a route is analysed only once every region its state owns rendered, so a
 *    half-painted page can neither invent violations nor hide real ones.
 */

const DEFAULT_VIEWPORT = { width: 1280, height: 720 };
const NARROW_VIEWPORT = { width: 320, height: 640 };
const WIDE_VIEWPORT = { width: 2048, height: 1200 };

type AuditContext = {
  workspaceId: string;
  workItemId: string;
  humanTaskId: string;
  runtimeId: string;
};

type AuditedPage = {
  /** The `<Route path>` in `src/app/App.tsx` this page instantiates. */
  route: string;
  /**
   * The page state this entry audits. One route can have more than one (the
   * work item detail page before and after a run exists); each state is its own
   * entry rather than pretending the two are the same page.
   */
  state: string;
  url: (context: AuditContext) => string;
  /**
   * The landmark that must be visible before the route is analysed: a named
   * region for a page whose facts live in titled sections, or a level-one
   * heading for a page that is its own title.
   */
  landmark: { region: string } | { heading: string };
  /** Further regions that belong to this state and must be rendered too. */
  also?: string[];
};

const CATALOGUE: AuditedPage[] = [
  { route: 'login', state: 'anonymous', url: () => '/login', landmark: { heading: '登录 Kowa' } },
  // The catch-all route, answered by the shell itself.
  { route: '*', state: 'unknown path', url: () => '/missing/nested', landmark: { heading: '页面不存在' } },
  { route: 'workspaces', state: 'entry', url: () => '/workspaces', landmark: { region: '创建工作区' } },
  {
    route: 'workspaces/:workspaceId/settings',
    state: 'configured workspace',
    url: (context) => `/workspaces/${context.workspaceId}/settings`,
    landmark: { region: '项目仓与知识仓' },
  },
  {
    route: 'workspaces/:workspaceId/work-items',
    state: 'list',
    url: (context) => `/workspaces/${context.workspaceId}/work-items`,
    landmark: { region: '工作项列表' },
  },
  {
    route: 'workspaces/:workspaceId/work-items/new',
    state: 'intake form',
    url: (context) => `/workspaces/${context.workspaceId}/work-items/new`,
    landmark: { region: '目标与流程' },
  },
  {
    // The work item detail page *before* a run exists: the intake facts and the
    // single fixed-workflow entry. A real page state, not a loading state.
    route: 'workspaces/:workspaceId/work-items/:workItemId',
    state: 'no run admitted yet',
    url: (context) => `/workspaces/${context.workspaceId}/work-items/${context.workItemId}`,
    landmark: { region: '需求与验收' },
    also: ['启动固定流程'],
  },
  {
    // The same route once the delivery run exists: DAG, node panel, rounds,
    // recovery, artifacts, knowledge binding and the delivery evidence panel.
    route: 'workspaces/:workspaceId/work-items/:workItemId',
    state: 'run admitted, node selected',
    url: (context) => `/workspaces/${context.workspaceId}/work-items/${context.workItemId}?node=plan`,
    landmark: { region: '固定流程 DAG' },
    also: ['节点详情', '交付运行', '运行轮次与历史', '失败与恢复', '产物与证据', '知识绑定', '交付证据与关闭'],
  },
  {
    route: 'workspaces/:workspaceId/human-tasks',
    state: 'list',
    url: (context) => `/workspaces/${context.workspaceId}/human-tasks`,
    landmark: { region: '待办清单' },
  },
  {
    route: 'workspaces/:workspaceId/human-tasks/:humanTaskId',
    state: 'versioned request',
    url: (context) => `/workspaces/${context.workspaceId}/human-tasks/${context.humanTaskId}`,
    landmark: { region: '所审版本' },
    also: ['依据', '答复'],
  },
  {
    route: 'workspaces/:workspaceId/runtimes',
    state: 'list and execution config',
    url: (context) => `/workspaces/${context.workspaceId}/runtimes`,
    landmark: { region: 'Runtime 列表' },
    also: ['执行配置', 'Git remote 可用性与权限缺口'],
  },
  {
    route: 'workspaces/:workspaceId/runtimes/:runtimeId',
    state: 'registration and last observation',
    url: (context) => `/workspaces/${context.workspaceId}/runtimes/${context.runtimeId}`,
    landmark: { region: 'Runtime 状态' },
    also: ['最近观测'],
  },
];

/** The routes `App.tsx` declares, read from the file that owns them. */
function declaredRoutes(): string[] {
  const source = readFileSync(new URL('../../src/app/App.tsx', import.meta.url), 'utf8');
  return [...source.matchAll(/<Route\s+path="([^"]+)"/g)].map((match) => match[1] as string);
}

/** The one catalogue entry for a route + state pair. */
function entryFor(route: string, state: string): AuditedPage {
  const entry = CATALOGUE.find((candidate) => candidate.route === route && candidate.state === state);
  expect(entry, `the catalogue has no ${route} entry for state ${state}`).toBeTruthy();
  return entry as AuditedPage;
}

test('every route the application declares is covered by the page audit', () => {
  const declared = declaredRoutes();
  const audited = [...new Set(CATALOGUE.map((page) => page.route))];
  expect(declared.filter((route) => !audited.includes(route)), 'declared but never audited').toEqual([]);
  expect(audited.filter((route) => !declared.includes(route)), 'audited but not declared').toEqual([]);
  const states = CATALOGUE.map((page) => `${page.route} @ ${page.state}`);
  expect(new Set(states).size, 'duplicate route state in the catalogue').toBe(states.length);
});

/**
 * Analyses one route in both themes and probes 320px with doubled text. The
 * theme is chosen through the real control, so the analysed document is the one
 * an operator sees after picking that theme.
 */
async function auditOne(page: Page, entry: AuditedPage, context: AuditContext): Promise<void> {
  const url = entry.url(context);
  const where = `${entry.route} (${entry.state})`;
  // A route is analysed only once every region this state owns has rendered:
  // analysing a half-painted page would report violations the operator never
  // sees, and pass over the ones they do.
  const ready = [
    'region' in entry.landmark
      ? page.getByRole('region', { name: entry.landmark.region })
      : page.getByRole('heading', { level: 1, name: entry.landmark.heading }),
    ...(entry.also ?? []).map((name) => page.getByRole('region', { name })),
  ];

  await page.setViewportSize(DEFAULT_VIEWPORT);
  for (const theme of ['light', 'dark'] as const) {
    await page.goto(url);
    for (const landmark of ready) await expect(landmark, `${where} did not render its ${theme} projection`).toBeVisible();
    await page.getByLabel('外观').selectOption(theme);
    await expect(page.getByRole('main')).toBeVisible();
    const result = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice'])
      .analyze();
    expect(result.violations, `${where} ${theme}`).toEqual([]);
  }

  // Doubled text at a 320px viewport: the document itself must never scroll
  // sideways. A region that is legitimately wider than a phone (the DAG) scrolls
  // inside itself instead.
  await page.setViewportSize(NARROW_VIEWPORT);
  await page.goto(url);
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(ready[0] as ReturnType<typeof page.getByRole>, `${where} did not render at 320px`).toBeVisible();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
    `${where} 320px/200%`,
  ).toBe(true);

  // The other end of the range the stage contract names: on a wide screen the
  // page still fits, and the content column stays bounded rather than stretching
  // a machine fact across two thousand pixels.
  await page.setViewportSize(WIDE_VIEWPORT);
  await page.goto(url);
  await expect(ready[0] as ReturnType<typeof page.getByRole>, `${where} did not render on a wide screen`).toBeVisible();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
    `${where} 2048px`,
  ).toBe(true);
  await page.setViewportSize(DEFAULT_VIEWPORT);
}

test('the public pages have no axe violations in either theme and hold the full width range', async ({ page }) => {
  test.slow();
  const context: AuditContext = { workspaceId: '', workItemId: '', humanTaskId: '', runtimeId: '' };
  for (const entry of CATALOGUE.filter((candidate) => candidate.route === 'login' || candidate.route === '*')) {
    await auditOne(page, entry, context);
  }
});

test('the workspace and runtime pages hold both themes and the full width range', async ({ page, request, browserName }) => {
  test.slow();
  const workspaceId = await signInForS07(page);
  const runtimeId = await prepareRunnableWorkspace(page, request, browserName, workspaceId);
  const context: AuditContext = { workspaceId, workItemId: '', humanTaskId: '', runtimeId };
  for (const [route, state] of [
    ['workspaces', 'entry'],
    ['workspaces/:workspaceId/settings', 'configured workspace'],
    ['workspaces/:workspaceId/runtimes', 'list and execution config'],
    ['workspaces/:workspaceId/runtimes/:runtimeId', 'registration and last observation'],
  ] as const) {
    await auditOne(page, entryFor(route, state), context);
  }
});

test('the work item pages, including the state before a run exists, hold both themes and the full width range', async ({ page, request }) => {
  test.slow();
  const workspaceId = await signInForS07(page);
  // Intake needs the workspace's project repository; without it the form is
  // disabled (correctly), and the audit would be measuring a refusal page.
  await bindRepositories(page, request, workspaceId);
  const item = await createWorkItemThroughPage(page, workspaceId, 'Page audit');
  const context: AuditContext = { workspaceId, workItemId: item.workItemId, humanTaskId: '', runtimeId: '' };
  for (const [route, state] of [
    ['workspaces/:workspaceId/work-items', 'list'],
    ['workspaces/:workspaceId/work-items/new', 'intake form'],
    ['workspaces/:workspaceId/work-items/:workItemId', 'no run admitted yet'],
  ] as const) {
    await auditOne(page, entryFor(route, state), context);
  }
});

test('the run, human-task and delivery pages hold both themes and the full width range', async ({ page, request, browserName }) => {
  test.slow();
  const workspaceId = await signInForS07(page);
  const runtimeId = await prepareRunnableWorkspace(page, request, browserName, workspaceId);
  const item = await createWorkItemThroughPage(page, workspaceId, 'Page audit');
  await page.goto(`/workspaces/${workspaceId}/work-items/${item.workItemId}`);
  await startRunThroughPage(page);
  const humanTaskId = await openHumanTaskThroughPage(page, workspaceId);

  const context: AuditContext = { workspaceId, workItemId: item.workItemId, humanTaskId, runtimeId };
  for (const [route, state] of [
    ['workspaces/:workspaceId/work-items/:workItemId', 'run admitted, node selected'],
    ['workspaces/:workspaceId/human-tasks', 'list'],
    ['workspaces/:workspaceId/human-tasks/:humanTaskId', 'versioned request'],
  ] as const) {
    await auditOne(page, entryFor(route, state), context);
  }
});
