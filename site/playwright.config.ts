import { defineConfig, devices } from '@playwright/test';
import { siteConfiguration } from './scripts/site-config.mjs';

const { base } = siteConfiguration();
const port = process.env.SODAPOP_SITE_PORT || '4321';
const channel = process.env.SODAPOP_SITE_BROWSER_CHANNEL;
if (channel && channel !== 'chrome') throw new Error('SODAPOP_SITE_BROWSER_CHANNEL must be chrome or unset');

export default defineConfig({
  testDir: './test/e2e',
  outputDir: './test-results',
  fullyParallel: true,
  workers: 2,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}${base}`,
    colorScheme: 'light',
    reducedMotion: 'reduce',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], channel } }],
  webServer: {
    command: `npm run preview -- --host 127.0.0.1 --port ${port}`,
    url: `http://127.0.0.1:${port}${base}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60000,
  },
});
