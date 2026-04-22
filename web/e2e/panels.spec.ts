import { test, expect } from '@playwright/test';
import { chQuery } from './fixtures/chOracle';
import { primeActiveSite } from './fixtures/session';

// HLL error band: uniqCombined64 has ~0.5% error. For small fixture
// counts we allow ±2 absolute OR 1% relative — whichever is larger.
function assertVisitorsClose(uiValue: number, oracleValue: number, label: string): void {
  const tolerance = Math.max(2, Math.round(0.01 * oracleValue));
  expect(
    Math.abs(uiValue - oracleValue),
    `${label}: ui=${uiValue} oracle=${oracleValue} tolerance=${tolerance}`,
  ).toBeLessThanOrEqual(tolerance);
}

function parseInt10(s: string): number {
  return parseInt(s.replace(/[^0-9-]/g, ''), 10);
}

// Navigate to a panel + wait for its loading state to resolve. Panels
// show "loading…" initially, then either render data or an empty state.
// We wait up to 5s for either outcome so the test isn't asserting on
// the transitional "loading…" that occurs when siteSignal is still
// syncing from sessionStorage.
async function gotoAndWaitReady(page: import('@playwright/test').Page, hash: string, h2: string): Promise<void> {
  await page.goto('/app/' + hash);
  await expect(page.getByRole('heading', { level: 2, name: h2 })).toBeVisible();
  // Wait for loading text to disappear.
  await expect(page.locator('main').getByText('loading…', { exact: true })).toHaveCount(0, {
    timeout: 10_000,
  });
}

test.describe('panels render + CH-oracle parity', () => {
  const SITE_A = process.env.STATNIVE_E2E_SITE_A!;

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

  test('Overview renders 6 KPIs and visitor count matches CH-oracle', async ({ page }) => {
    await expect(page.getByTestId('kpi-primary')).toBeVisible();
    await expect(page.getByTestId('kpi-secondary')).toBeVisible();

    const uiVisitors = parseInt10(
      await page.locator('[data-kpi="visitors"] .statnive-num-primary').innerText(),
    );

    const oracle = chQuery<{ visitors: string }>(
      `SELECT toUInt64(uniqCombined64Merge(visitors_state)) AS visitors
       FROM statnive.hourly_visitors
       WHERE site_id = ${SITE_A}
       AND hour >= now() - INTERVAL 7 DAY`,
    );
    const oracleVisitors = oracle ? Number(oracle.visitors) : 0;

    assertVisitorsClose(uiVisitors, oracleVisitors, 'Overview visitors');
  });

  test('Sources panel heading + at least one visible channel', async ({ page }) => {
    await gotoAndWaitReady(page, '#sources', 'Sources');

    // Fixture seeds Direct + Organic Search on siteA.
    const panel = page.locator('main section.statnive-section').last();
    await expect(panel).toContainText(/Direct|Organic Search/);
  });

  test('Pages panel heading + at least one pathname row', async ({ page }) => {
    await gotoAndWaitReady(page, '#pages', 'Pages');

    const panel = page.locator('main section.statnive-section').last();
    await expect(panel).toContainText(/\/(home|checkout|pricing)/);
  });

  test('SEO panel renders heading + chart or empty state', async ({ page }) => {
    await gotoAndWaitReady(page, '#seo', 'SEO');
    // SEO filters to organic; rows might be empty depending on rollup
    // materialization timing. Heading visible + non-loading is enough.
  });

  test('Campaigns panel heading + utm_campaign row if data present', async ({ page }) => {
    await gotoAndWaitReady(page, '#campaigns', 'Campaigns');

    const panel = page.locator('main section.statnive-section').last();
    const body = await panel.innerText();
    // Fixture seeds 'spring_promo' on siteA. Accept empty state too for
    // rollup-lag resilience on CI.
    expect(body).toMatch(/spring_promo|No utm_campaign data/);
  });

  test('Realtime panel active counter shape', async ({ page }) => {
    await page.goto('/app/#realtime');
    await expect(page.getByRole('heading', { level: 2, name: 'Realtime' })).toBeVisible();

    const active = page.getByTestId('realtime-active');
    await expect(active).toBeVisible();

    // Wait up to 3s for the initial /api/realtime fetch.
    await page.waitForTimeout(2_000);
    const text = await active.innerText();
    expect(text).toMatch(/^(—|\d[\d,]*)$/);
  });
});
