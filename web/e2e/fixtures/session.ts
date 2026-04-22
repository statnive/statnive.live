// Session fixture — one-liner to prime sessionStorage with an active
// site_id before the SPA boots. Centralizes the literal storage key so
// a rename in web/src/state/site.ts won't silently break every spec.

import type { Page } from '@playwright/test';
import { STORAGE_KEY } from '../../src/state/site';

export async function primeActiveSite(page: Page, siteID: string | number): Promise<void> {
  await page.addInitScript(
    ([key, id]: [string, string]) => {
      window.sessionStorage.setItem(key, id);
    },
    [STORAGE_KEY, String(siteID)],
  );
}
