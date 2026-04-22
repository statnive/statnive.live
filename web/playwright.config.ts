import { defineConfig, devices } from '@playwright/test';

// Playwright config for the dashboard e2e suite. The binary is spawned
// once by globalSetup against docker-compose ClickHouse; every spec
// runs against the same live instance. Canonical reference for the
// spawn + seed logic is test/smoke/harness.sh.
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/globalSetup.ts',
  globalTeardown: './e2e/globalTeardown.ts',

  // Tests don't run in parallel against the single shared binary: a
  // Chromium instance per worker would blow the /api/realtime poll
  // assertions and fight over the same docker-exec lock. The suite is
  // small + fast (~30 tests, <5 min) so single-threaded is fine.
  fullyParallel: false,
  workers: 1,

  timeout: 30_000,
  expect: { timeout: 5_000 },
  retries: process.env.CI ? 1 : 0,

  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'playwright-report' }],
  ],

  use: {
    baseURL: process.env.STATNIVE_E2E_BASEURL ?? 'http://127.0.0.1:18299',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
