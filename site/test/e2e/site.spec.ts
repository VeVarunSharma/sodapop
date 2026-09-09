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
  const title = 'Sodapop - Your bubbly terminal coding companion';
  const description = 'An independent terminal coding companion powered by the GitHub Copilot SDK. Explore code, compare approaches, taste-test changes, and inspect the diff.';
  await expect(page.getByRole('heading', { level: 1 })).toHaveText("A little can of let's build that.");
  await expect(page.getByRole('heading', { name: 'Ideas, checks, settings. One vending machine.', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Taste-test it. Then take a look.', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'How fizzy are we feeling?', exact: true })).toBeVisible();
  await expect(page).toHaveTitle(title);
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', title);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', description);
  await expect(page.locator('meta[property="og:description"]')).toHaveAttribute('content', description);
  await expect(page.getByRole('link', { name: 'See the built-in workflows' }))
    .toHaveAttribute('href', `${base}docs/commands/#built-in-workflows`);
  await expect(page.getByRole('link', { name: 'See how the evidence works' }))
    .toHaveAttribute('href', `${base}docs/commands/#conversation-model-and-change-controls`);
  await expect(page.locator('footer')).toContainText(/A little can of "let's build that\."/);
  await expect(page.locator('.demo-figure figcaption')).toHaveCount(0);
  for (const retired of [
    'A fresh take on your terminal', 'Real tools, clear approvals',
    'Comfortably familiar.', 'Refreshingly different.', 'Good tools. A little joy.',
  ]) {
    await expect(page.locator('body')).not.toContainText(retired);
  }
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

test('recordings autoplay when visible and stop back on their posters', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('./');
  const frame = page.locator('sodapop-demo').first();
  await frame.scrollIntoViewIfNeeded();
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
  await expect(page.getByRole('heading', { level: 1 })).toHaveText("Let's get Sodapop into your terminal.");
  await expect(page).toHaveTitle('Get Sodapop - Downloads and installation');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content',
    'Start here to install Sodapop, check platform and release availability, and open your first project.');
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
  const context = await browser.newContext({ javaScriptEnabled: false, baseURL, colorScheme: 'light', viewport: { width: 320, height: 844 } });
  const page = await context.newPage();
  await page.goto('./');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await page.getByRole('link', { name: 'Read the docs' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await page.goto('docs/customization/');
  const table = page.getByRole('table');
  await expect(table).toHaveAttribute('tabindex', '0');
  await page.getByRole('link', { name: /Section titled .Themes./ }).focus();
  await page.keyboard.press('Tab');
  await expect(table).toBeFocused();
  expect(await table.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  await page.keyboard.press('ArrowRight');
  await expect.poll(() => table.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await page.goto('download/');
  await expect(page.getByText('All available installation commands:')).toBeVisible();
  await context.close();
});

test('documentation keeps its bubbly welcome and literal guide names', async ({ page }) => {
  await page.goto('docs/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sodapop docs');
  await expect(page.getByRole('main')).toContainText('First sip? Let\u2019s get Sodapop running in your project.');
  await expect(page.getByRole('link', { name: 'download page', exact: true }))
    .toHaveAttribute('href', `${base}download/`);
  for (const [name, slug] of [
    ['Getting started', 'getting-started'], ['Installation', 'installation'],
    ['Commands and shortcuts', 'commands'], ['Customization', 'customization'],
    ['Permissions and privacy', 'permissions-and-privacy'], ['Troubleshooting', 'troubleshooting'],
  ]) {
    await expect(page.getByRole('main').getByRole('link', { name, exact: true }).first())
      .toHaveAttribute('href', `${base}docs/${slug}/`);
  }
  await page.getByRole('main').getByRole('link', { name: 'Customization', exact: true }).first().click();
  await expect(page).toHaveURL(/docs\/customization\/$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Customization');
  await expect(page.getByRole('main')).toContainText('How fizzy are we feeling? Open /theme');
  await expect(page.getByRole('table').first()).toContainText('/theme arcade');
  await expect(page.getByRole('main')).toContainText(
    'Personality changes UI copy and decoration, not the model\u2019s capabilities, Copilot access, or tool-approval policy.');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content',
    'How fizzy are we feeling? Choose themes, personality, motion, and terminal options.');
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
    for (const route of ['./', 'download/', 'docs/', 'docs/getting-started/', 'docs/customization/']) {
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
