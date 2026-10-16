import { expect, test, type Page } from '@playwright/test';
import {
  createWorkItemThroughPage,
  prepareRunnableWorkspace,
  signInForS07,
  startRunThroughPage,
} from './support/runnable-workspace';

/**
 * The keyboard contract of frontend:S07.
 *
 * Accessibility is not only what a static analysis finds: the product has to be
 * operable without a pointer. That means every stop the Tab key lands on has to
 * be *visible* as the focused stop, and no region may swallow focus so the rest
 * of the page becomes unreachable. Both depend on the rendered document, so
 * both are driven here rather than asserted over the source.
 *
 * `foundation.spec.ts` already pins the shell's own tab order (skip link →
 * brand → appearance → navigation); this spec covers the pages inside it and
 * the one composite widget the product draws by hand — the DAG.
 */

const tabKey = (browserName: string) => (browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Tab' : 'Tab');

type FocusStop = { tag: string; label: string; hasIndicator: boolean; outline: string };

/** The focused element, and whether it paints any focus indicator at all. */
async function focusStop(page: Page): Promise<FocusStop | null> {
  return page.evaluate(() => {
    const element = document.activeElement as HTMLElement | null;
    if (element === null || element === document.body) return null;
    const style = getComputedStyle(element);
    const width = Number.parseFloat(style.outlineWidth);
    return {
      tag: element.tagName,
      label: (element.getAttribute('aria-label') ?? element.textContent ?? '').trim().slice(0, 48),
      // The product's focus indicator is the outline ring `global.css` declares,
      // and that is what is measured: a decorative `shadow-*` on the control
      // must not be able to stand in for a focus stop, which is exactly how a
      // missing ring went unnoticed before this stage.
      hasIndicator: style.outlineStyle !== 'none' && !Number.isNaN(width) && width > 0,
      outline: `${style.outlineStyle} ${style.outlineWidth}`,
    };
  });
}

/** Walks `stops` tab stops from the top of the document and returns those with no indicator. */
async function indicatorlessStops(page: Page, browserName: string, stops: number): Promise<FocusStop[]> {
  const offenders: FocusStop[] = [];
  for (let index = 0; index < stops; index += 1) {
    await page.keyboard.press(tabKey(browserName));
    const stop = await focusStop(page);
    if (stop === null) break; // the document ended: nothing further to check
    if (!stop.hasIndicator) offenders.push(stop);
  }
  return offenders;
}

/** Whether focus currently sits inside the DAG region of the run view. */
async function focusIsInsideDag(page: Page): Promise<boolean> {
  return page.evaluate(() => document.activeElement?.closest('[aria-labelledby="dag"]') !== null);
}

test('every tab stop on the workspace pages paints a visible focus indicator', async ({ page, browserName }) => {
  test.slow();
  const workspaceId = await signInForS07(page);
  for (const path of [`/workspaces/${workspaceId}/settings`, `/workspaces/${workspaceId}/work-items/new`, '/workspaces']) {
    await page.goto(path);
    await expect(page.getByRole('main')).toBeVisible();
    const offenders = await indicatorlessStops(page, browserName, 40);
    expect(offenders.map((stop) => `${stop.tag}:${stop.label} (outline ${stop.outline})`), `${path}: tab stops without a focus indicator`).toEqual([]);
  }
});

test('focus leaves the page content again: the shell is never cut off behind a trap', async ({ page, browserName }) => {
  test.slow();
  const workspaceId = await signInForS07(page);
  await page.goto(`/workspaces/${workspaceId}/settings`);
  await expect(page.getByRole('main')).toBeVisible();

  // Shift+Tab from the first field inside the page must walk back out into the
  // shell's primary navigation — that is what "the content has no trap" means.
  const firstField = page.getByLabel('Repository ID', { exact: true }).first();
  await firstField.focus();
  await expect(firstField).toBeFocused();
  let escaped = false;
  for (let index = 0; index < 25 && !escaped; index += 1) {
    await page.keyboard.press(`Shift+${tabKey(browserName)}`);
    escaped = await page.evaluate(() => document.activeElement?.closest('nav[aria-label="主导航"]') !== null);
  }
  expect(escaped, 'focus could not leave the page content by keyboard').toBe(true);
});

test('the DAG is reachable, movable and escapable with the keyboard alone', async ({ page, request, browserName }) => {
  test.slow();
  const workspaceId = await signInForS07(page);
  await prepareRunnableWorkspace(page, request, browserName, workspaceId);
  const item = await createWorkItemThroughPage(page, workspaceId, 'Keyboard journey');
  await page.goto(`/workspaces/${workspaceId}/work-items/${item.workItemId}`);
  await startRunThroughPage(page);

  const dag = page.getByRole('region', { name: '固定流程 DAG' });
  const nodes = dag.getByRole('button');
  const nodeCount = await nodes.count();
  expect(nodeCount, 'the frozen definition must draw more than one node').toBeGreaterThan(1);

  // The graph is reached the way an operator reaches it — by Tab, which is also
  // what makes the browser paint its focus indicator; a scripted `.focus()`
  // would prove the element is focusable, not that it is *visible* as focused.
  const first = nodes.first();
  await first.focus();
  await expect(first).toBeFocused();
  let reached = false;
  for (let index = 0; index < 120 && !reached; index += 1) {
    await page.keyboard.press(tabKey(browserName));
    reached = await focusIsInsideDag(page);
  }
  expect(reached, 'the DAG nodes were never reached by Tab').toBe(true);
  const stop = await focusStop(page);
  expect(stop?.hasIndicator, 'a focused DAG node must paint a focus indicator').toBe(true);
  expect(stop?.label, 'the focused node names itself for a screen reader').toMatch(/裁决/);

  // On top of the tab order the graph answers the arrow keys the way it is
  // drawn: sideways is one column, so the accessible name changes.
  const before = stop?.label;
  await page.keyboard.press('ArrowRight');
  const after = await page.evaluate<string | null>(() => document.activeElement?.getAttribute('aria-label') ?? null);
  expect(after, 'ArrowRight must move focus along the graph').not.toBe(before);
  expect(after, 'focus must still be on a node').toMatch(/裁决/);

  // …and Tab always walks back out: the drawing never becomes a keyboard trap.
  let escaped = false;
  for (let index = 0; index <= nodeCount && !escaped; index += 1) {
    await page.keyboard.press(tabKey(browserName));
    escaped = !(await focusIsInsideDag(page));
  }
  expect(escaped, 'the DAG trapped the keyboard').toBe(true);
  await expect(page.getByRole('region', { name: '节点详情' })).toBeVisible();
});
