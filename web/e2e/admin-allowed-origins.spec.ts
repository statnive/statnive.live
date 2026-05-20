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

import { test, expect, request } from '@playwright/test';

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

async function patchSiteOrigins(siteID: number, origins: string[]) {
  const ctx = await request.newContext({ baseURL: BASE });
  const login = await ctx.post('/api/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    headers: { 'Content-Type': 'application/json' },
  });
  expect(login.status(), 'admin login').toBe(200);

  const patch = await ctx.patch(`/api/admin/sites/${siteID}`, {
    data: { allowed_origins: origins },
    headers: { 'Content-Type': 'application/json' },
  });
  await ctx.dispose();

  return patch;
}

test.describe('admin UI — allowed_origins textarea (Stage 4-D)', () => {
  test.beforeEach(async () => {
    // Clean both fixture sites' allowlists to start from a known state.
    // Assert each reset succeeds so a 500 doesn't silently land us in a
    // contaminated state where the assertions still pass vacuously.
    const resetA = await patchSiteOrigins(SITE_A, []);
    expect(resetA.status(), 'reset SITE_A').toBe(200);
    const resetB = await patchSiteOrigins(SITE_B, []);
    expect(resetB.status(), 'reset SITE_B').toBe(200);
  });

  test('paste two origins, blur, reload → persisted', async ({ page }) => {
    // Pattern from dashboard-authz.spec.ts: drive /api/login via the
    // BrowserContext so the session cookie lands on `page`, then navigate
    // to /app/ already authenticated. Avoids inventing UI form selectors.
    const login = await page.request.post('/api/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(login.status(), 'admin login').toBe(200);

    await page.goto(`${BASE}/app/#admin`);
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    // Find the row for SITE_A and the new allowed_origins textarea.
    const row = page.locator(`tr:has-text("${HOST_A}")`);
    const textarea = row.getByLabel(`allowed origins for ${HOST_A}`);
    await expect(textarea).toBeVisible();

    // Paste two origins (newline-separated) and blur to trigger save.
    await textarea.fill(`${ORIGIN_A1}\n${ORIGIN_A2}`);
    await textarea.blur();

    // Wait for PATCH to settle, then reload and re-read the textarea.
    await page.waitForTimeout(500);
    await page.reload();
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    const reloadedTextarea = page
      .locator(`tr:has-text("${HOST_A}")`)
      .getByLabel(`allowed origins for ${HOST_A}`);
    await expect(reloadedTextarea).toHaveValue(new RegExp(`${ORIGIN_A1}.*${ORIGIN_A2}`, 's'));
  });

  test('http:// origin rejected client-side (no PATCH)', async ({ page }) => {
    const login = await page.request.post('/api/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(login.status(), 'admin login').toBe(200);

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

    // Client-side validator caught it — no network call.
    expect(patchCount).toBe(0);

    // The error banner surfaces the rejection so the operator sees why
    // their save was suppressed.
    await expect(page.getByRole('alert')).toContainText(/invalid origin.*http:\/\/insecure\.example/i);
  });

  test('cross-site collision returns 409 (surfaced in panel banner)', async ({ page }) => {
    // Seed: SITE_A holds the collision origin.
    const seed = await patchSiteOrigins(SITE_A, [ORIGIN_B]);
    expect(seed.status(), 'seed SITE_A').toBe(200);

    const login = await page.request.post('/api/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(login.status(), 'admin login').toBe(200);

    await page.goto(`${BASE}/app/#admin`);
    await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });

    const textarea = page
      .locator(`tr:has-text("${HOST_B}")`)
      .getByLabel(`allowed origins for ${HOST_B}`);

    await textarea.fill(ORIGIN_B);
    await textarea.blur();

    await page.waitForTimeout(500);

    await expect(page.getByRole('alert')).toContainText(/409|already registered/i);
  });
});
