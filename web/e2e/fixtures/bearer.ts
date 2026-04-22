// Bearer fixture — reads the injected token from the SPA's <meta> tag.
// Cross-checks the Phase 5a invariant that internal/dashboard/spa/
// dashboard.go rewrites STATNIVE_BEARER_PLACEHOLDER with the configured
// token at request time (not shipped as the literal placeholder).

import type { Page } from '@playwright/test';

export async function getBearer(page: Page): Promise<string> {
  await page.goto('/app/', { waitUntil: 'domcontentloaded' });
  const token = await page.evaluate(() => {
    const m = document.querySelector<HTMLMetaElement>('meta[name="statnive-bearer"]');
    return m?.content ?? '';
  });
  if (!token || token === 'STATNIVE_BEARER_PLACEHOLDER') {
    throw new Error(
      `bearer token missing or unrewritten in <meta name="statnive-bearer"> — Phase 5a injection regressed (got: ${JSON.stringify(token)})`,
    );
  }
  return token;
}
