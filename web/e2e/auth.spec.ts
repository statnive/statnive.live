import { test, expect } from '@playwright/test';
import { getBearer } from './fixtures/bearer';

test.describe('auth + CSP', () => {
  test('bearer is injected into /app/ meta tag', async ({ page }) => {
    const token = await getBearer(page);
    expect(token).toBe(process.env.STATNIVE_E2E_TOKEN);
  });

  test('/api/stats/overview rejects requests without bearer', async ({ request }) => {
    const site = process.env.STATNIVE_E2E_SITE_A!;
    const res = await request.get(`/api/stats/overview?site=${site}`);
    expect(res.status()).toBe(401);
  });

  test('no CSP violations on Overview or Sources panel load', async ({ page }) => {
    const cspErrors: string[] = [];
    page.on('console', (msg) => {
      const text = msg.text();
      if (/content.*security.*policy|refused to execute|refused to load/i.test(text)) {
        cspErrors.push(text);
      }
    });
    page.on('pageerror', (err) => cspErrors.push(err.message));

    await page.goto('/app/');
    await expect(page.getByTestId('kpi-primary')).toBeVisible();

    // Navigate to Sources panel via hash; LazyPanel chunk must also
    // load without CSP violations.
    await page.goto('/app/#sources');
    await expect(page.getByTestId('panel-sources')).toBeVisible();

    expect(cspErrors, cspErrors.join('\n')).toHaveLength(0);
  });
});
