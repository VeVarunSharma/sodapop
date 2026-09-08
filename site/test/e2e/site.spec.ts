import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { readFile } from 'node:fs/promises';
import { gzipSync } from 'node:zlib';
import path from 'node:path';
import { siteConfiguration } from '../../scripts/site-config.mjs';
import { readCatalog } from '../../scripts/release-catalog.mjs';

const { base } = siteConfiguration();

test('homepage delivers the original brand without eager GIFs or console failures', async ({ page }, testInfo) => {
  const errors: string[] = [];
  const gifRequests: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('request', (request) => { if (new URL(request.url()).pathname.endsWith('.gif')) gifRequests.push(request.url()); });
  await page.goto('./');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('more pop.');
  await page.waitForLoadState('networkidle');
  expect(gifRequests).toEqual([]);
  expect(errors).toEqual([]);
  const initial = await page.evaluate(() =>
    performance.getEntriesByType('resource').map((entry) => {
      const resource = entry as PerformanceResourceTiming;
      return { name: resource.name, bytes: resource.encodedBodySize };
    }),
  );
  let javascriptBytes = 0;
  for (const resource of initial) {
    const url = new URL(resource.name);
    if (url.pathname.endsWith('.js') && url.pathname.startsWith(base)) {
      const file = path.join(process.cwd(), 'dist', decodeURIComponent(url.pathname.slice(base.length)));
      javascriptBytes += gzipSync(await readFile(file)).length;
    }
  }
  expect(javascriptBytes).toBeLessThanOrEqual(150 * 1024);
  expect(initial.reduce((sum, resource) => sum + resource.bytes, 0)).toBeLessThanOrEqual(1024 * 1024);
  for (const frame of await page.locator('sodapop-demo').all()) {
    await frame.scrollIntoViewIfNeeded();
    await expect.poll(() => frame.locator('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBeGreaterThan(0);
  }
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('homepage-desktop.png'), fullPage: true });
});

test('recordings play only on request and stop back on their posters', async ({ page }) => {
  await page.goto('./');
  const frame = page.locator('sodapop-demo').first();
  await frame.scrollIntoViewIfNeeded();
  await frame.getByRole('button', { name: 'Play A peek inside Sodapop demo' }).click();
  await expect(frame.locator('img')).toHaveAttribute('src', /\.gif$/);
  await frame.getByRole('button', { name: 'Stop A peek inside Sodapop demo' }).click();
  await expect(frame.locator('img')).toHaveAttribute('src', /\.webp$/);
  await expect(frame.getByRole('button')).toHaveAttribute('aria-pressed', 'false');
});

test('availability is honest and denied clipboard access has a visible fallback', async ({ page }) => {
  const catalog = await readCatalog();
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { value: {
      writeText: () => Promise.reject(new DOMException('Denied', 'NotAllowedError')),
    } });
  });
  await page.goto('download/');
  if (catalog.mode === 'pre-release') {
    await expect(page.getByRole('heading', { name: 'Still in the soda lab.' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Download archive' })).toHaveCount(0);
    await expect(page.locator('code').filter({ hasText: 'npm install' })).toHaveCount(0);
  } else {
    await expect(page.getByRole('heading', { name: `Sodapop ${catalog.version}`, exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Download archive' })).toHaveCount(catalog.artifacts.length);
  }
  await page.getByRole('button', { name: 'Copy installation command' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Could not copy' })).toBeVisible();
  await expect(page.getByText('Copied. Your terminal is next.')).toHaveCount(0);
});

test('successful copy reports success and theme choice follows into documentation', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: () => Promise.resolve() } });
  });
  await page.goto('download/');
  await page.getByRole('button', { name: 'Copy installation command' }).click();
  await expect(page.getByText('Copied. Your terminal is next.')).toBeVisible();
  await page.getByRole('button', { name: 'Switch color theme' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.goto('docs/getting-started/');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  const darkResults = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze();
  expect(darkResults.violations).toEqual([]);
});

test('essential content remains usable without JavaScript', async ({ browser, baseURL }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, baseURL, colorScheme: 'light', viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto('./');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await page.getByRole('link', { name: 'Read the docs' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await page.goto('download/');
  await expect(page.getByText('All available installation commands:')).toBeVisible();
  await context.close();
});

test('documentation search works in the built site and custom 404 provides recovery', async ({ page }) => {
  await page.goto('docs/');
  await page.getByRole('button', { name: /search/i }).click();
  const dialog = page.getByRole('dialog', { name: 'Search', exact: true });
  const input = dialog.getByRole('textbox', { name: 'Search', exact: true });
  await expect(input).toBeVisible();
  await input.fill('permissions');
  const result = dialog.getByRole('link', { name: /permissions and privacy/i }).first();
  await expect(result).toBeVisible();
  await result.click();
  await expect(page).toHaveURL(/docs\/permissions-and-privacy\//);
  await page.goto('404.html');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('That page has fizzled.');
  await page.getByRole('link', { name: 'Find the docs' }).click();
  await expect(page).toHaveURL(new RegExp(`${base.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}docs/`));
});

for (const width of [320, 390, 768, 1440]) {
  test(`responsive layout and accessibility at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    for (const route of ['./', 'download/', 'docs/getting-started/']) {
      await page.goto(route);
      await page.waitForLoadState('networkidle');
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze();
      expect(results.violations).toEqual([]);
    }
    if (width === 390) {
      await page.goto('./');
      await page.getByRole('button', { name: 'Open navigation' }).click();
      await expect(page.getByRole('dialog')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(page.getByRole('button', { name: 'Open navigation' })).toBeFocused();
      for (const frame of await page.locator('sodapop-demo').all()) {
        await frame.scrollIntoViewIfNeeded();
        await expect.poll(() => frame.locator('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBeGreaterThan(0);
      }
      await page.evaluate(() => scrollTo(0, 0));
      await page.screenshot({ path: testInfo.outputPath('homepage-mobile.png'), fullPage: true });
    }
  });
}
