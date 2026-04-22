import { test, expect } from '@playwright/test';
import { primeActiveSite } from './fixtures/session';

test.describe('filter panel + date picker', () => {
  const SITE_A = process.env.STATNIVE_E2E_SITE_A!;

  test.beforeEach(async ({ page }) => {
    await primeActiveSite(page, SITE_A);
    await page.goto('/app/');
    await expect(page.getByTestId('kpi-primary')).toBeVisible({ timeout: 10_000 });
  });

  test('device chip toggles URL hash + aria-pressed', async ({ page }) => {
    await page.goto('/app/#sources');
    const chip = page.getByRole('button', { name: 'mobile', exact: true });

    await expect(chip).toHaveAttribute('aria-pressed', 'false');
    await chip.click();
    await expect(chip).toHaveAttribute('aria-pressed', 'true');
    await expect(page).toHaveURL(/device=mobile/);

    await chip.click();
    await expect(chip).toHaveAttribute('aria-pressed', 'false');
    await expect(page).not.toHaveURL(/device=mobile/);
  });

  test('deep-link with device=mobile preselects the chip', async ({ page }) => {
    await page.goto('/app/#sources?device=mobile');
    const chip = page.getByRole('button', { name: 'mobile', exact: true });
    await expect(chip).toHaveAttribute('aria-pressed', 'true');
  });

  test('reload preserves active filter', async ({ page }) => {
    await page.goto('/app/#sources?device=desktop&channel=Direct');
    await page.reload();
    await expect(page.getByRole('button', { name: 'desktop', exact: true })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await expect(page.getByRole('button', { name: 'Direct', exact: true })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  test('Clear all button resets chips and URL params', async ({ page }) => {
    await page.goto('/app/#sources?device=mobile&channel=Direct');
    await expect(page.getByRole('button', { name: 'Clear all' })).toBeVisible();
    await page.getByRole('button', { name: 'Clear all' }).click();
    await expect(page).toHaveURL(/#sources$/);
    await expect(page.getByRole('button', { name: 'mobile', exact: true })).toHaveAttribute(
      'aria-pressed',
      'false',
    );
  });

  test('preset Last 30 days updates URL', async ({ page }) => {
    await page.goto('/app/');
    await page.getByRole('button', { name: 'Last 30 days' }).click();
    await expect(page).toHaveURL(/from=\d{4}-\d{2}-\d{2}.*to=\d{4}-\d{2}-\d{2}/);
  });

  test('custom date range fires API call with from/to', async ({ page }) => {
    await page.goto('/app/#sources');

    // Capture API calls to /api/stats/sources so we can verify the
    // from/to on the follow-up request.
    const apiCalls: string[] = [];
    page.on('request', (req) => {
      const url = req.url();
      if (url.includes('/api/stats/sources')) apiCalls.push(url);
    });

    await page.getByRole('button', { name: 'Custom' }).click();
    await page.locator('#dp-from').fill('2026-01-01');
    await page.locator('#dp-to').fill('2026-01-08');
    await page.getByRole('button', { name: 'Apply' }).click();

    // Allow the refetch to fire.
    await page.waitForTimeout(500);

    const custom = apiCalls.find((u) => u.includes('from=2026-01-01') && u.includes('to=2026-01-08'));
    expect(custom, `no /api/stats/sources call with custom range; saw: ${apiCalls.join(' | ')}`).toBeDefined();
  });
});
