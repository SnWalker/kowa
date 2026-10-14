import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { installApi } from './support/fixtures';
import { actor } from './support/mode';

async function signIn(page: import('@playwright/test').Page) {
  await installApi(page);
  await page.goto('/login');
  await page.getByRole('button', { name: '使用 GitHub 登录' }).click();
  await expect(page.getByText(`登录身份：${actor.login}`)).toBeVisible();
}

test('workspace and runtime pages have no axe violations in both themes', async ({ page }) => {
  await signIn(page);
  for (const theme of ['light', 'dark'] as const) {
    for (const path of ['/workspaces', '/workspaces/workspace-a1b2c3d4/settings', '/workspaces/workspace-a1b2c3d4/runtimes']) {
      await page.goto(path);
      await page.getByLabel('外观').selectOption(theme);
      await expect(page.getByRole('main')).toBeVisible();
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations, `${theme} ${path}`).toEqual([]);
    }
  }
  expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })))
    .toEqual({ local: 0, session: 0 });
});

test('the workspace settings form is usable by keyboard alone', async ({ page, browserName }) => {
  await signIn(page);
  await page.goto('/workspaces/workspace-a1b2c3d4/settings');
  const repositoryId = page.getByLabel('Repository ID', { exact: true }).first();
  await repositoryId.focus();
  await expect(repositoryId).toBeFocused();
  await repositoryId.fill('3001');
  await expect(repositoryId).toHaveValue('3001');
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
  await signIn(page);
  await page.setViewportSize({ width: 320, height: 640 });
  for (const path of ['/workspaces', '/workspaces/workspace-a1b2c3d4/settings', '/workspaces/workspace-a1b2c3d4/runtimes']) {
    await page.goto(path);
    await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
    await expect(page.getByRole('main')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), path).toBe(true);
  }
});
