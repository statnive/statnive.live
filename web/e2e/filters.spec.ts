import { test, expect } from '@playwright/test';
import { primeActiveSite } from './fixtures/session';

test.describe('filter panel + date picker', () => {
  const SITE_A = process.env.STATNIVE_E2E_SITE_A!;

  test.beforeEach(async ({ page }) => {
    await primeActiveSite(page, SITE_A);
    await page.goto('/app/');
    await expect(page.getByTestId('kpi-primary')).toBeVisible({ timeout: 10_000 });
  });

  test('channel chip toggles URL hash + aria-pressed', async ({ page }) => {
    await page.goto('/app/#sources');
    const chip = page.getByRole('button', { name: 'Direct', exact: true });

    await expect(chip).toHaveAttribute('aria-pressed', 'false');
    await chip.click();
    await expect(chip).toHaveAttribute('aria-pressed', 'true');
    await expect(page).toHaveURL(/channel=Direct/);

    await chip.click();
    await expect(chip).toHaveAttribute('aria-pressed', 'false');
    await expect(page).not.toHaveURL(/channel=Direct/);
  });

  test('deep-link with channel=Direct preselects the chip', async ({ page }) => {
    await page.goto('/app/#sources?channel=Direct');
    const chip = page.getByRole('button', { name: 'Direct', exact: true });
    await expect(chip).toHaveAttribute('aria-pressed', 'true');
  });

  test('reload preserves active channel filter', async ({ page }) => {
    await page.goto('/app/#sources?channel=Organic%20Search');
    await page.reload();
    await expect(
      page.getByRole('button', { name: 'Organic Search', exact: true }),
    ).toHaveAttribute('aria-pressed', 'true');
  });

  test('Clear all button resets chips and URL params', async ({ page }) => {
    await page.goto('/app/#sources?channel=Direct');
    await expect(page.getByRole('button', { name: 'Clear all' })).toBeVisible();
    await page.getByRole('button', { name: 'Clear all' }).click();
    await expect(page).toHaveURL(/#sources$/);
    await expect(
      page.getByRole('button', { name: 'Direct', exact: true }),
    ).toHaveAttribute('aria-pressed', 'false');
  });

  test('preset Last 30 days updates URL', async ({ page }) => {
    await page.goto('/app/');
    await page.getByRole('button', { name: 'Last 30 days' }).click();
    await expect(page).toHaveURL(/from=\d{4}-\d{2}-\d{2}.*to=\d{4}-\d{2}-\d{2}/);
  });

  test('custom date range fires API call with from/to', async ({ page }) => {
    await page.goto('/app/#sources');

    const apiCalls: string[] = [];
    page.on('request', (req) => {
      const url = req.url();
      if (url.includes('/api/stats/sources')) apiCalls.push(url);
    });

    await page.getByRole('button', { name: 'Custom' }).click();
    await page.locator('#dp-from').fill('2026-01-01');
    await page.locator('#dp-to').fill('2026-01-08');
    await page.getByRole('button', { name: 'Apply' }).click();

    await page.waitForTimeout(500);

    const custom = apiCalls.find((u) => u.includes('from=2026-01-01') && u.includes('to=2026-01-08'));
    expect(custom, `no /api/stats/sources call with custom range; saw: ${apiCalls.join(' | ')}`).toBeDefined();
  });

  test('channel chip click fires a filtered /api/stats/sources request', async ({ page }) => {
    // End-to-end wire-up proof: before Phase 5d, chip clicks wrote the
    // URL hash but the backend ignored the filter. This test asserts
    // the filtered request actually reaches the server with
    // `channel=Direct`. The SQL-level narrowing is covered by
    // test/dashboard_http_test.go:TestDashboardHTTP_SourcesChannelFilter
    // (Go integration test, deterministic); the UI round-trip is
    // intentionally wire-level here because Preact reconcile timing
    // makes table-shape assertions flake-prone at the e2e tier.
    await page.goto('/app/#sources');
    await expect(page.getByRole('heading', { level: 2, name: 'Sources' })).toBeVisible();
    await page.waitForLoadState('networkidle');

    const filteredReq = page.waitForRequest(
      (r) => r.url().includes('/api/stats/sources') && r.url().includes('channel=Direct'),
      { timeout: 10_000 },
    );

    await page.getByRole('button', { name: 'Direct', exact: true }).click();
    await filteredReq;
  });
});
