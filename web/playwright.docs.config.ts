import { defineConfig, devices } from '@playwright/test';

// Standalone Playwright config for the offline (air-gap) API-reference proof.
// Separate from playwright.config.ts so it does NOT trigger the ClickHouse
// globalSetup — this test only needs a static file server at the repo root.
export default defineConfig({
  testDir: './docs-e2e',
  fullyParallel: true,
  reporter: 'line',
  use: { baseURL: 'http://127.0.0.1:8088' },
  webServer: {
    command: 'python3 -m http.server 8088 --bind 127.0.0.1',
    cwd: '..', // repo root (web/ is one level below)
    url: 'http://127.0.0.1:8088/docs/api/index.html',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
