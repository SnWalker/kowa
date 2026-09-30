import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';

test('production shell supports keyboard navigation and skip focus', async ({ page, browserName }) => {
  const tabKey = browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Tab' : 'Tab';
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '欢迎使用 Kowa' })).toBeVisible();
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: '跳至主要内容' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('main')).toBeFocused();
  await page.reload();
  await page.keyboard.press(tabKey);
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: 'Kowa 首页' })).toBeFocused();
  await page.keyboard.press(tabKey);
  await expect(page.getByLabel('外观')).toBeFocused();
  await page.keyboard.press(tabKey);
  await expect(page.getByRole('link', { name: '首页', exact: true })).toBeFocused();
  expect(errors).toEqual([]);
});

test('deep links refresh, recover and support browser history', async ({ page }) => {
  await page.goto('/missing/nested?source=test');
  await page.reload();
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible();
  await page.getByRole('link', { name: '返回首页' }).click();
  await expect(page).toHaveTitle('首页 · Kowa');
  await expect(page.getByRole('main')).toBeFocused();
  await page.goBack();
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible();
  await expect(page.getByRole('main')).toBeFocused();
  await page.goForward();
  await expect(page.getByRole('heading', { name: '欢迎使用 Kowa' })).toBeVisible();
});

for (const theme of ['light', 'dark'] as const) {
  test(`shell and unknown route have no axe violations in ${theme} theme`, async ({ page }) => {
    await page.goto('/');
    await page.getByLabel('外观').selectOption(theme);
    for (const path of ['/', '/missing']) {
      if (path !== '/') {
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
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto('/');
  await expect(page.getByLabel('外观')).toHaveValue('system');
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('html')).toHaveCSS('color-scheme', 'light');
});

test('320px layout and 200% text remain usable without horizontal scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto('/');
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect(page.getByRole('heading', { name: '欢迎使用 Kowa' })).toBeVisible();
  await expect(page.getByLabel('外观')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByLabel('外观').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('isolated fixture announces loading, contains an error and restores content', async ({ page, browserName }) => {
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
