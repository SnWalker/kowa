import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { isControlPlane } from './support/mode';
import { installApi } from './support/fixtures';

test('shell supports keyboard navigation and skip focus', async ({ page, browserName }) => {
  const tabKey = browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Tab' : 'Tab';
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await installApi(page);
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();

  // Skip link first, and activating it focuses the main region. A fresh
  // navigation (not a reload) resets the sequential focus starting point.
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: '跳至主要内容' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('main')).toBeFocused();

  // The tab order after the skip link is brand → theme → primary navigation.
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: '跳至主要内容' })).toBeFocused();
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: 'Kowa 首页' })).toBeFocused();
  await page.keyboard.press(tabKey);
  await expect(page.getByLabel('外观')).toBeFocused();
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: '工作区' })).toBeFocused();
  expect(errors).toEqual([]);
});

test('deep links refresh, recover and support browser history', async ({ page }) => {
  await installApi(page);
  await page.goto('/missing/nested?source=test');
  await page.reload();
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible();
  await page.getByRole('link', { name: '返回工作区' }).click();
  await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
  await expect(page).toHaveTitle('登录 · Kowa');
  await expect(page.getByRole('main')).toBeFocused();
  await page.goBack();
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible();
  await page.goForward();
  await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
});

for (const theme of ['light', 'dark'] as const) {
  test(`shell, login and unknown route have no axe violations in ${theme} theme`, async ({ page }) => {
    await installApi(page);
    await page.goto('/login');
    await page.getByLabel('外观').selectOption(theme);
    for (const path of ['/login', '/missing', '/workspaces']) {
      if (path !== '/login') {
        await page.goto(path);
        await page.getByLabel('外观').selectOption(theme);
      }
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations).toEqual([]);
    }
    expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })))
      .toEqual({ local: 0, session: 0 });
  });
}

test('system theme follows browser preference', async ({ page }) => {
  await installApi(page);
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto('/login');
  await expect(page.getByLabel('外观')).toHaveValue('system');
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light');
});

test('320px layout and 200% text remain usable without horizontal scrolling', async ({ page }) => {
  await installApi(page);
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto('/login');
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(page.getByRole('heading', { name: '登录 Kowa' })).toBeVisible();
  await expect(page.getByRole('button', { name: '使用 GitHub 登录' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByLabel('外观').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test.describe('isolated browser fixture', () => {
  test.skip(isControlPlane, 'the component fixture server only runs in fixture mode');

  test('announces loading, contains an error and restores content', async ({ page, browserName }) => {
    const tabKey = browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Tab' : 'Tab';
    await page.goto('http://127.0.0.1:4174');
    await expect(page.getByRole('status')).toContainText('正在加载');
    await page.getByRole('button', { name: '触发渲染异常' }).click();
    await expect(page.getByRole('alert')).toBeFocused();
    await expect(page.getByRole('alert')).toContainText('内容暂时无法显示');
    expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);
    await page.getByRole('button', { name: '重试' }).click();
    await expect(page.getByRole('button', { name: '触发渲染异常' })).toBeVisible();
    await page.keyboard.press(tabKey);
    await expect(page.getByRole('button', { name: '触发渲染异常' })).toBeFocused();
  });
});
