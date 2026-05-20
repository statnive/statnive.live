// admin-allowed-origins.spec.ts — Stage-4-D admin UI regression.
//
// Coverage:
//  1. Operator pastes two origins → PATCH succeeds → reload restores them.
//  2. Operator pastes an HTTP (non-HTTPS) origin → client-side error
//     surfaces in the panel banner BEFORE any network call.
//  3. Operator pastes an origin already registered to another site →
//     server returns 409 → error surfaces in the panel banner.
//
// Backend regression (validator + collision) is covered by
// internal/admin/sites_handlers_origins_test.go; this spec verifies the
// UI plumbs the existing Stage-4-A surface correctly.

import { test, expect, request, type APIRequestContext } from '@playwright/test';

const ADMIN_EMAIL = process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live';
const ADMIN_PASSWORD = process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static';
const BASE = process.env.STATNIVE_E2E_BASEURL ?? 'http://127.0.0.1:18299';
const SITE_A = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
const SITE_B = Number(process.env.STATNIVE_E2E_SITE_B ?? 802);
const HOST_A = process.env.STATNIVE_E2E_HOST_A ?? 'e2e-a.example.com';
const HOST_B = process.env.STATNIVE_E2E_HOST_B ?? 'e2e-b.example.com';

// Unique per test run so reruns don't keep colliding on the same origin
// across runs (the global-uniqueness check enforced at admin write
// time would refuse a second run on the same value).
const TAG = `${Date.now().toString(36)}-${Math.floor(Math.random() * 0xffff).toString(16)}`;
const ORIGIN_A1 = `https://www.${TAG}-a.example`;
const ORIGIN_A2 = `https://${TAG}-a.example`;
const ORIGIN_B = `https://${TAG}-b.example`;

// Module-scoped admin context. One /api/login per worker keeps us
// under the per-IP rate limit (10/min default); every spec PATCH then
// reuses the cached session cookie instead of re-authenticating.
let adminCtx: APIRequestContext | null = null;
// storageState from the same login is reused by every test's page
// fixture (page.goto opens already-authenticated, no extra POST).
let adminStorageState: { cookies: Array<{ name: string; value: string; domain: string; path: string; expires: number; httpOnly: boolean; secure: boolean; sameSite: 'Strict' | 'Lax' | 'None' }>; origins: unknown[] } | null = null;

async function getAdminCtx(): Promise<APIRequestContext> {
  if (adminCtx) return adminCtx;

  adminCtx = await request.newContext({ baseURL: BASE });
  const login = await adminCtx.post('/api/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    headers: { 'Content-Type': 'application/json' },
  });
  expect(login.status(), 'admin login').toBe(200);

  adminStorageState = await adminCtx.storageState() as typeof adminStorageState;

  return adminCtx;
}

async function patchSiteOrigins(siteID: number, origins: string[]) {
  const ctx = await getAdminCtx();
  return ctx.patch(`/api/admin/sites/${siteID}`, {
    data: { allowed_origins: origins },
    headers: { 'Content-Type': 'application/json' },
  });
}

test.describe('admin UI — allowed_origins textarea (Stage 4-D)', () => {
  test.beforeAll(async () => {
    // Warm the shared admin context once for the whole suite.
    await getAdminCtx();
  });

  test.afterAll(async () => {
    if (adminCtx) {
      await adminCtx.dispose();
      adminCtx = null;
    }
  });

  test.beforeEach(async () => {
    // Clean both fixture sites' allowlists to start from a known state.
    // Assert each reset succeeds so a 500 doesn't silently land us in a
    // contaminated state where the assertions still pass vacuously.
    const resetA = await patchSiteOrigins(SITE_A, []);
    expect(resetA.status(), 'reset SITE_A').toBe(200);
    const resetB = await patchSiteOrigins(SITE_B, []);
    expect(resetB.status(), 'reset SITE_B').toBe(200);
  });

  test('paste two origins, blur, reload → persisted', async ({ browser }) => {
    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    await page.goto(`${BASE}/app/#admin`);
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    const row = page.locator(`tr:has-text("${HOST_A}")`);
    const textarea = row.getByLabel(`allowed origins for ${HOST_A}`);
    await expect(textarea).toBeVisible();

    await textarea.fill(`${ORIGIN_A1}\n${ORIGIN_A2}`);
    await textarea.blur();

    await page.waitForTimeout(500);
    await page.reload();
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    const reloadedTextarea = page
      .locator(`tr:has-text("${HOST_A}")`)
      .getByLabel(`allowed origins for ${HOST_A}`);
    await expect(reloadedTextarea).toHaveValue(new RegExp(`${ORIGIN_A1}.*${ORIGIN_A2}`, 's'));

    await ctx.close();
  });

  test('http:// origin rejected client-side (no PATCH)', async ({ browser }) => {
    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    await page.goto(`${BASE}/app/#admin`);
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    let patchCount = 0;
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes('/api/admin/sites/')) {
        patchCount += 1;
      }
    });

    const textarea = page
      .locator(`tr:has-text("${HOST_A}")`)
      .getByLabel(`allowed origins for ${HOST_A}`);

    await textarea.fill(`http://insecure.example`);
    await textarea.blur();

    await page.waitForTimeout(300);

    expect(patchCount).toBe(0);
    await expect(page.getByRole('alert')).toContainText(/invalid origin.*http:\/\/insecure\.example/i);

    await ctx.close();
  });

  test('cross-site collision returns 409 (surfaced in panel banner)', async ({ browser }) => {
    const seed = await patchSiteOrigins(SITE_A, [ORIGIN_B]);
    expect(seed.status(), 'seed SITE_A').toBe(200);

    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    await page.goto(`${BASE}/app/#admin`);
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    const textarea = page
      .locator(`tr:has-text("${HOST_B}")`)
      .getByLabel(`allowed origins for ${HOST_B}`);

    await textarea.fill(ORIGIN_B);
    await textarea.blur();

    await page.waitForTimeout(500);

    await expect(page.getByRole('alert')).toContainText(/409|already registered/i);

    await ctx.close();
  });
});
