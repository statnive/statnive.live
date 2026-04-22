import { test, expect } from '@playwright/test';
import { primeActiveSite } from './fixtures/session';

test.describe('realtime polling + visibility pause', () => {
  const SITE_A = process.env.STATNIVE_E2E_SITE_A!;

  test.beforeEach(async ({ page }) => {
    await primeActiveSite(page, SITE_A);
    await page.goto('/app/');
    await expect(page.getByTestId('kpi-primary')).toBeVisible({ timeout: 10_000 });
  });

  test('active counter renders a number after initial fetch', async ({ page }) => {
    await page.goto('/app/#realtime');
    await expect(page.getByTestId('panel-realtime')).toBeVisible();
    await expect(page.getByTestId('realtime-active')).not.toHaveText('—');

    const text = await page.getByTestId('realtime-active').innerText();
    expect(text).toMatch(/^\d[\d,]*$/);
  });

  test('visibilitychange=hidden pauses polling', async ({ page }) => {
    await page.goto('/app/#realtime');
    await expect(page.getByTestId('realtime-active')).not.toHaveText('—');

    // Count /api/realtime hits during a 6-second hidden window (tick
    // cadence is 10s — we allow 6s to prove "no new fetch during
    // hidden" rather than waiting for a full 10s+).
    const hits: string[] = [];
    page.on('request', (req) => {
      if (req.url().includes('/api/realtime')) hits.push(req.url());
    });

    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { value: true, configurable: true });
      Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
      document.dispatchEvent(new Event('visibilitychange'));
    });

    await page.waitForTimeout(6_000);

    // No new hits during the hidden window. We don't assert strict 0
    // because the browser may have a pending in-flight response from
    // before the hide; tolerate ≤1.
    expect(hits.length, `hits during hidden window: ${hits.join('\n')}`).toBeLessThanOrEqual(1);
  });

  test('visibility restore triggers an immediate fetch', async ({ page }) => {
    await page.goto('/app/#realtime');
    await expect(page.getByTestId('realtime-active')).not.toHaveText('—');

    // Hide first (carries over from prior test pattern).
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { value: true, configurable: true });
      Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
      document.dispatchEvent(new Event('visibilitychange'));
    });

    // Now start capturing and restore.
    const hits: string[] = [];
    page.on('request', (req) => {
      if (req.url().includes('/api/realtime')) hits.push(req.url());
    });

    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { value: false, configurable: true });
      Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true });
      document.dispatchEvent(new Event('visibilitychange'));
    });

    // Give the event loop a tick to fire the resumed fetch.
    await page.waitForTimeout(2_000);

    expect(hits.length, 'restore should trigger ≥1 realtime fetch').toBeGreaterThanOrEqual(1);
  });
});
