import { test, expect } from '@playwright/test';

// Air-gap proof: the offline API reference must render fully while making ZERO
// requests to any non-loopback host. Static grep is necessary-but-insufficient
// (the vendored Redoc bundle contains font-host strings it never fetches with
// disableGoogleFont); this is the authoritative no-outbound guard.
test('offline API reference renders with zero external requests', async ({ page }) => {
  const external: string[] = [];

  await page.route('**/*', (route) => {
    const host = new URL(route.request().url()).hostname;
    if (host === '127.0.0.1' || host === 'localhost') {
      return route.continue();
    }
    external.push(route.request().url());
    return route.abort();
  });

  await page.goto('/docs/api/index.html', { waitUntil: 'networkidle' });

  // Redoc renders the API title as a heading once the spec is parsed.
  await expect(
    page.getByRole('heading', { name: /statnive-live API/i }).first(),
  ).toBeVisible({ timeout: 15_000 });

  expect(external, `unexpected external requests:\n${external.join('\n')}`).toHaveLength(0);
});
