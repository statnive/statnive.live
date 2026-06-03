// admin-tz-chip.spec.ts — .statnive-tz-chip dynamic-content regression.
//
// Verifies the timezone chip in the dashboard's date bar reflects the
// active site's actual tz at all times (not the v0.0.38 static
// "Tehran" label). Covered behaviours:
//   1. Initial render uses the active site's tz when one is selected.
//   2. Switching active site via the SiteSwitcher reactively updates
//      the chip text + title + data-tz attributes.
//   3. Editing a site's tz via the admin PATCH path propagates to the
//      chip after a re-fetch.
//
// Pre-seed (web/e2e/globalSetup.ts): SITE_A on Asia/Tehran, SITE_B on UTC.

import { test, expect, request, type APIRequestContext } from '@playwright/test';

const ADMIN_EMAIL = process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live';
const ADMIN_PASSWORD = process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static';
const BASE = process.env.STATNIVE_E2E_BASEURL ?? 'http://127.0.0.1:18299';
const SITE_A = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
const SITE_B = Number(process.env.STATNIVE_E2E_SITE_B ?? 802);

// Module-scoped admin context — one /api/login per worker.
let adminCtx: APIRequestContext | null = null;
let adminStorageState: Awaited<ReturnType<APIRequestContext['storageState']>> | null = null;

async function getAdminCtx(): Promise<APIRequestContext> {
  if (adminCtx) return adminCtx;

  adminCtx = await request.newContext({ baseURL: BASE });
  const login = await adminCtx.post('/api/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    headers: { 'Content-Type': 'application/json' },
  });
  expect(login.status(), 'admin login').toBe(200);
  adminStorageState = await adminCtx.storageState();

  return adminCtx;
}

async function patchSiteTZ(siteID: number, tz: string) {
  const ctx = await getAdminCtx();
  return ctx.patch(`/api/admin/sites/${siteID}`, {
    data: { tz },
    headers: { 'Content-Type': 'application/json' },
  });
}

// Matchers tolerant of the Node-vs-browser Intl divergence:
// browsers return CEST/CET/IRST, Node returns GMT+N. Both are valid.
const BERLIN_LABEL = /^(CEST|CET|GMT\+2|GMT\+1)$/;
const TEHRAN_LABEL = /^(IRST|GMT\+3:30)$/;
const UTC_LABEL = /^UTC$/;

test.describe('AppShell .statnive-tz-chip — dynamic per-site timezone label', () => {
  test.beforeAll(async () => {
    await getAdminCtx();
    // Reset SITE_A to its seed value so the spec is rerun-safe.
    const reset = await patchSiteTZ(SITE_A, 'Asia/Tehran');
    expect(reset.status(), 'reset SITE_A tz').toBe(200);
  });

  test.afterAll(async () => {
    // Restore SITE_A to its seed value after the test mutates it.
    if (adminCtx) {
      await patchSiteTZ(SITE_A, 'Asia/Tehran').catch(() => undefined);
      await adminCtx.dispose();
      adminCtx = null;
    }
  });

  test('chip reflects SITE_A (Asia/Tehran) then updates when switching to SITE_B (UTC)', async ({ browser }) => {
    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    // Pre-set the persisted active-site to SITE_A so the dashboard
    // boots with that site already selected. This mirrors how a
    // returning operator would land — STORAGE_KEY is exported from
    // web/src/state/site.ts for exactly this purpose.
    await page.addInitScript((siteID) => {
      sessionStorage.setItem('statnive.activeSiteId', String(siteID));
    }, SITE_A);

    await page.goto(`${BASE}/app/`);
    const chip = page.locator('.statnive-tz-chip');
    await expect(chip).toBeVisible({ timeout: 5000 });

    // Phase 1: SITE_A (Asia/Tehran).
    await expect(chip).toHaveText(TEHRAN_LABEL);
    await expect(chip).toHaveAttribute('title', 'Asia/Tehran');
    await expect(chip).toHaveAttribute('data-tz', 'Asia/Tehran');

    // Phase 2: switch to SITE_B via SiteSwitcher. The component
    // renders a plain <select data-testid="site-select"> when more
    // than one site is available — see web/src/components/SiteSwitcher.tsx.
    const switcher = page.getByTestId('site-select');
    await switcher.selectOption(String(SITE_B));

    // After switching, the chip text + attrs reflect SITE_B's tz=UTC.
    await expect(chip).toHaveText(UTC_LABEL);
    await expect(chip).toHaveAttribute('title', 'UTC');
    await expect(chip).toHaveAttribute('data-tz', 'UTC');

    await ctx.close();
  });

  test('editing SITE_A.tz via admin API propagates to chip after refresh', async ({ browser }) => {
    expect(adminStorageState, 'storageState primed').toBeTruthy();

    // Mutate SITE_A.tz to Europe/Berlin via the admin PATCH.
    const patch = await patchSiteTZ(SITE_A, 'Europe/Berlin');
    expect(patch.status(), 'patch SITE_A tz=Europe/Berlin').toBe(200);

    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();
    await page.addInitScript((siteID) => {
      sessionStorage.setItem('statnive.activeSiteId', String(siteID));
    }, SITE_A);

    await page.goto(`${BASE}/app/`);
    const chip = page.locator('.statnive-tz-chip');
    await expect(chip).toBeVisible({ timeout: 5000 });

    await expect(chip).toHaveText(BERLIN_LABEL);
    await expect(chip).toHaveAttribute('title', 'Europe/Berlin');
    await expect(chip).toHaveAttribute('data-tz', 'Europe/Berlin');

    await ctx.close();
  });
});

