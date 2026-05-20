// admin-allowed-origins.spec.ts — allowed_origins UI regression.
//
// Covers the Sites → Configure modal allowed-origins editor:
//   1. Operator adds two origins → Save → modal closes; reload → values
//      persisted.
//   2. Operator types an http:// origin → client-side error surfaces in
//      the modal beneath the row; Save stays disabled; no PATCH fires.
//   3. Operator types an origin already registered to another site →
//      Save → server returns 409 → error surfaces at the top of the
//      modal banner; modal stays open.

import { test, expect, request, type APIRequestContext, type Page } from '@playwright/test';

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

const HOST_A1 = ORIGIN_A1.replace(/^https:\/\//, '');
const HOST_A2 = ORIGIN_A2.replace(/^https:\/\//, '');
const HOST_B_BARE = ORIGIN_B.replace(/^https:\/\//, '');

// Module-scoped admin context. One /api/login per worker keeps us
// under the per-IP rate limit (10/min default); every spec PATCH then
// reuses the cached session cookie instead of re-authenticating.
let adminCtx: APIRequestContext | null = null;
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

// Open the Configure modal for a given hostname. Returns the modal root.
async function openConfigureModal(page: Page, host: string) {
  await page.goto(`${BASE}/app/#admin`);
  await expect(page.getByTestId('admin-sites-table')).toBeVisible({ timeout: 5000 });
  const row = page.locator(`tr:has-text("${host}")`);
  await row.getByRole('button', { name: /Configure/ }).click();
  const modal = page.getByRole('dialog');
  await expect(modal).toBeVisible();
  return modal;
}

test.describe('admin UI — Configure modal allowed-origins editor', () => {
  test.beforeAll(async () => {
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
    const resetA = await patchSiteOrigins(SITE_A, []);
    expect(resetA.status(), 'reset SITE_A').toBe(200);
    const resetB = await patchSiteOrigins(SITE_B, []);
    expect(resetB.status(), 'reset SITE_B').toBe(200);
  });

  test('add two origins, Save, reload → persisted', async ({ browser }) => {
    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    const modal = await openConfigureModal(page, HOST_A);

    // The site starts with no origins, so the modal renders zero inputs.
    // Click "+ Add another" twice to make room for two entries.
    const addBtn = modal.getByRole('button', { name: /Add another/i });
    await addBtn.click();
    await modal.getByLabel('allowed origin 1', { exact: true }).fill(HOST_A1);
    await addBtn.click();
    await modal.getByLabel('allowed origin 2', { exact: true }).fill(HOST_A2);

    await modal.getByRole('button', { name: /^Save/ }).click();
    await expect(modal).toBeHidden({ timeout: 5000 });

    // Reload, re-open the modal, verify both inputs carry the persisted hosts.
    const reopened = await openConfigureModal(page, HOST_A);
    await expect(reopened.getByLabel('allowed origin 1', { exact: true })).toHaveValue(HOST_A1);
    await expect(reopened.getByLabel('allowed origin 2', { exact: true })).toHaveValue(HOST_A2);

    await ctx.close();
  });

  test('http:// origin rejected client-side (no PATCH)', async ({ browser }) => {
    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    let patchCount = 0;
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes('/api/admin/sites/')) {
        patchCount += 1;
      }
    });

    const modal = await openConfigureModal(page, HOST_A);

    await modal.getByRole('button', { name: /Add another/i }).click();
    // The input strips the `https://` chrome prefix; typing `http://...`
    // produces `https://http://...` after withScheme(), which the
    // validator rejects.
    await modal.getByLabel('allowed origin 1', { exact: true }).fill('http://insecure.example');

    // Save is disabled while any field is invalid; clicking it is a no-op.
    const saveBtn = modal.getByRole('button', { name: /^Save/ });
    await expect(saveBtn).toBeDisabled();

    // Per-row error sentence renders beneath the offending input.
    await expect(modal.getByRole('alert')).toContainText(/https:\/\/|invalid origin/i);
    expect(patchCount).toBe(0);

    await ctx.close();
  });

  test('cross-site collision returns 409 (surfaced in modal banner)', async ({ browser }) => {
    const seed = await patchSiteOrigins(SITE_A, [ORIGIN_B]);
    expect(seed.status(), 'seed SITE_A').toBe(200);

    expect(adminStorageState, 'storageState primed').toBeTruthy();
    const ctx = await browser.newContext({ storageState: adminStorageState! });
    const page = await ctx.newPage();

    const modal = await openConfigureModal(page, HOST_B);
    await modal.getByRole('button', { name: /Add another/i }).click();
    await modal.getByLabel('allowed origin 1', { exact: true }).fill(HOST_B_BARE);
    await modal.getByRole('button', { name: /^Save/ }).click();

    // 409 surfaces as the translated sentence at the top of the modal;
    // the modal stays open so the user can fix and retry.
    await expect(modal).toBeVisible();
    await expect(modal.getByRole('alert')).toContainText(/already registered|in use/i);

    await ctx.close();
  });
});
