import { test, expect } from '@playwright/test';
import { chQuery } from './fixtures/chOracle';
import { primeActiveSite } from './fixtures/session';

test.describe('site switcher + multi-tenant isolation', () => {
  const SITE_A = process.env.STATNIVE_E2E_SITE_A!;
  const SITE_B = process.env.STATNIVE_E2E_SITE_B!;
  const HOST_A = process.env.STATNIVE_E2E_HOST_A!;
  const HOST_B = process.env.STATNIVE_E2E_HOST_B!;

  test.beforeEach(async ({ page }) => {
    await primeActiveSite(page, SITE_A);
    const overviewResp = page.waitForResponse(
      (r) => r.url().includes('/api/stats/overview') && r.url().includes(`site=${SITE_A}`),
      { timeout: 10_000 },
    );
    await page.goto('/app/');
    await overviewResp;
    await expect(page.getByTestId('kpi-primary')).toBeVisible({ timeout: 10_000 });
  });

  test('SiteSwitcher lists both seeded sites as <option>s', async ({ page }) => {
    const select = page.getByTestId('site-select');
    await expect(select).toBeVisible();

    const optionTexts = await select.locator('option').allInnerTexts();
    const joined = optionTexts.join(' | ');
    expect(joined).toContain(HOST_A);
    expect(joined).toContain(HOST_B);
  });

  test('switching sites triggers a refetch and CH-oracle parity on siteB', async ({ page }) => {
    await page.getByTestId('site-select').selectOption(SITE_B);
    // Wait for /api/stats/overview to fire with site=SITE_B and the
    // panel to re-render. The refetch is triggered by activeSiteSignal
    // flipping — Overview's useEffect depends on siteSignal via the
    // signal reactivity system.
    await page.waitForResponse(
      (r) => r.url().includes(`/api/stats/overview`) && r.url().includes(`site=${SITE_B}`),
      { timeout: 10_000 },
    );
    await expect(page.getByTestId('kpi-primary')).toBeVisible();

    const uiVisitorsB = parseInt(
      (await page
        .locator('[data-kpi="visitors"] .statnive-num-primary')
        .innerText()).replace(/[^0-9]/g, ''),
      10,
    );

    const oracle = chQuery<{ visitors: string }>(
      `SELECT toUInt64(uniqCombined64Merge(visitors_state)) AS visitors
       FROM statnive.hourly_visitors
       WHERE site_id = ${SITE_B}
       AND hour >= now() - INTERVAL 7 DAY`,
    );
    const oracleB = oracle ? Number(oracle.visitors) : 0;

    const tolerance = Math.max(2, Math.round(0.01 * oracleB));
    expect(
      Math.abs(uiVisitorsB - oracleB),
      `site B visitors: ui=${uiVisitorsB} oracle=${oracleB}`,
    ).toBeLessThanOrEqual(tolerance);
  });
});
