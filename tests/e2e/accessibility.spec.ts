import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { ensureWorkspace, fillDualRepositories } from './support/journey';

/**
 * Analyses one route in the given theme once the page really rendered.
 *
 * The shell renders its loading state first; analysing that transient document
 * would report violations the real page does not have. Every route renders its
 * own level-one heading, so the wait replaces racing the first server read.
 */
async function expectNoAxeViolations(page: import('@playwright/test').Page, path: string, theme: 'light' | 'dark', label: string) {
  await page.goto(path);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await page.getByLabel('外观').selectOption(theme);
  await expect(page.getByRole('main')).toBeVisible();
  const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice']).analyze();
  expect(result.violations, `${label} ${theme} ${path}`).toEqual([]);
}

test('workspace and runtime pages have no axe violations in both themes', async ({ page }) => {
  // axe analysis is CPU-heavy and an engine on a loaded machine can exceed the
  // default per-test budget; the budget is widened instead of weakening what is
  // analysed.
  test.slow();
  const workspaceId = await ensureWorkspace(page);
  for (const theme of ['light', 'dark'] as const) {
    for (const path of ['/workspaces', `/workspaces/${workspaceId}/settings`, `/workspaces/${workspaceId}/runtimes`]) {
      await expectNoAxeViolations(page, path, theme, 'workspace');
    }
  }
  expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })))
    .toEqual({ local: 0, session: 0 });
});

// The work item pages of frontend:S02 are analysed in their own test so one
// engine's slow analysis cannot consume another route group's budget.
test('work item pages have no axe violations in both themes', async ({ page }) => {
  test.slow();
  const workspaceId = await ensureWorkspace(page);
  for (const theme of ['light', 'dark'] as const) {
    for (const path of [`/workspaces/${workspaceId}/work-items`, `/workspaces/${workspaceId}/work-items/new`]) {
      await expectNoAxeViolations(page, path, theme, 'work-item');
    }
  }
});

test('the workspace settings form is usable by keyboard alone', async ({ page, browserName }) => {
  const workspaceId = await ensureWorkspace(page);
  await page.goto(`/workspaces/${workspaceId}/settings`);
  // A workspace created through the real command starts without bindings, so
  // every required field is typed before the keyboard submit; this keeps the
  // journey honest in both targets instead of relying on fixture prefill.
  const repositoryId = page.getByLabel('Repository ID', { exact: true }).first();
  await repositoryId.focus();
  await expect(repositoryId).toBeFocused();
  await repositoryId.fill('3001');
  await expect(repositoryId).toHaveValue('3001');
  await fillDualRepositories(page, '3001', '3002');
  const submit = page.getByRole('button', { name: /保存（基于/ });
  await submit.focus();
  if (browserName !== 'webkit') {
    // Enter on a focused submit button submits the form.
    await page.keyboard.press('Enter');
    await expect(page.getByText(/已保存为版本|版本冲突/)).toBeVisible();
  } else {
    await expect(submit).toBeFocused();
  }
});

test('authenticated pages keep 320px width without horizontal scrolling', async ({ page }) => {
  const workspaceId = await ensureWorkspace(page);
  await page.setViewportSize({ width: 320, height: 640 });
  for (const path of ['/workspaces', `/workspaces/${workspaceId}/settings`, `/workspaces/${workspaceId}/runtimes`]) {
    await page.goto(path);
    await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
    await expect(page.getByRole('main')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), path).toBe(true);
  }
});
