// dashboard-authz.spec.ts — per-site authorization regression suite
// (Lesson 35 / OWASP A01:2021 IDOR).
//
// Strategy: log in as the bootstrap admin (who has admin grants on
// SITE_A + SITE_B per globalSetup), provision a fresh viewer user via
// /api/admin/users granted only on SITE_A, log in as that viewer, then
// attempt to fetch SITE_B's analytics. Every request MUST 403.
//
// CH-oracle Tier 1: the spec asserts SITE_B has > 0 visitors in
// hourly_visitors before the forbidden fetch, so the 403 is a real-authz
// block (not "no rows to leak"). Without the oracle a vacuously-passing
// rejected-empty response would mask a regression.

import { test, expect, request } from '@playwright/test';
import { chQuery } from './fixtures/chOracle';
import { STORAGE_KEY } from '../src/state/site';

const ADMIN_EMAIL = process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live';
const ADMIN_PASSWORD = process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static';
const BASE = process.env.STATNIVE_E2E_BASEURL ?? 'http://127.0.0.1:18299';
const SITE_A = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
const SITE_B = Number(process.env.STATNIVE_E2E_SITE_B ?? 802);
const HOST_A = process.env.STATNIVE_E2E_HOST_A ?? 'e2e-a.example.com';
const HOST_B = process.env.STATNIVE_E2E_HOST_B ?? 'e2e-b.example.com';

// Unique per test run so reruns don't conflict on the email uniqueness
// invariant. The 409 path is also handled (idempotent — we keep going).
const TEST_RUN_TAG = `${Date.now().toString(36)}-${Math.floor(Math.random() * 0xffff).toString(16)}`;
const VIEWER_EMAIL = `e2e-authz-viewer-${TEST_RUN_TAG}@statnive.live`;
const VIEWER_PASSWORD = `Authz-Viewer-Pass-${TEST_RUN_TAG}-12345`;

test.describe('dashboard authz — per-site enforcement (Lesson 35)', () => {
  test('CH oracle: SITE_B has events so a 403 is a real-authz block', async () => {
    const row = chQuery<{ visitors: string }>(
      `SELECT toUInt64(uniqCombined64Merge(visitors_state)) AS visitors
       FROM statnive.hourly_visitors
       WHERE site_id = ${SITE_B} AND hour >= now() - INTERVAL 7 DAY`,
    );
    const oracleB = row ? Number(row.visitors) : 0;
    expect(oracleB, `SITE_B must have > 0 visitors in CH for the 403 test to be meaningful`).toBeGreaterThan(0);
  });

  test('SITE_A-only viewer is 403 on every /api/stats/* for SITE_B', async ({ browser }) => {
    // 1) Admin context — provisions the viewer and grants on SITE_A only.
    const adminCtx = await request.newContext({ baseURL: BASE });
    const loginResp = await adminCtx.post('/api/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(loginResp.status(), 'admin login').toBe(200);

    const createResp = await adminCtx.post(`/api/admin/users?site_id=${SITE_A}`, {
      data: {
        email: VIEWER_EMAIL,
        username: `e2e-authz-viewer-${TEST_RUN_TAG}`,
        password: VIEWER_PASSWORD,
        sites: [{ site_id: SITE_A, role: 'viewer' }],
      },
      headers: { 'Content-Type': 'application/json' },
    });
    // 201 (new) or 409 (idempotent re-run) — both fine.
    expect([201, 409], 'viewer create').toContain(createResp.status());

    await adminCtx.dispose();

    // 2) Viewer browser context — login, attempt cross-tenant fetch.
    const ctx = await browser.newContext();
    const page = await ctx.newPage();

    // Hit /api/login from the page so the cookie is set in the browser
    // context that subsequent fetches read.
    const viewerLogin = await page.request.post('/api/login', {
      data: { email: VIEWER_EMAIL, password: VIEWER_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(viewerLogin.status(), 'viewer login').toBe(200);

    // /api/sites must surface exactly ONE entry — SITE_A.
    const sitesResp = await page.request.get('/api/sites');
    expect(sitesResp.status()).toBe(200);
    const sitesBody = (await sitesResp.json()) as { sites: Array<{ id: number; hostname: string }> };
    expect(sitesBody.sites, 'viewer /api/sites is grant-filtered').toHaveLength(1);
    expect(sitesBody.sites[0].id).toBe(SITE_A);
    expect(sitesBody.sites[0].hostname).toBe(HOST_A);

    // /api/stats/* on the viewer's own site (SITE_A) must work.
    const ownResp = await page.request.get(`/api/stats/overview?site=${SITE_A}`);
    expect(ownResp.status(), `viewer own-site overview`).toBe(200);

    // /api/stats/* on SITE_B (which the viewer has no grant on) MUST 403.
    const crossRoutes = ['overview', 'sources', 'pages', 'seo', 'trend', 'campaigns'];
    for (const route of crossRoutes) {
      const resp = await page.request.get(`/api/stats/${route}?site=${SITE_B}`);
      expect(resp.status(), `cross-tenant /api/stats/${route} → ${resp.status()} (want 403)`).toBe(403);
    }

    // /api/realtime/visitors on SITE_B — same matrix.
    const realtimeResp = await page.request.get(`/api/realtime/visitors?site=${SITE_B}`);
    expect(realtimeResp.status(), 'cross-tenant /api/realtime/visitors').toBe(403);

    await ctx.close();
  });

  test('SPA bypass via DevTools fetch — page.evaluate rejected with 403', async ({ browser }) => {
    // Same viewer (created in previous test, or by the create path here
    // since the email is unique per run — that test ordering across files
    // isn't guaranteed by Playwright when sharding).
    const adminCtx = await request.newContext({ baseURL: BASE });
    const loginResp = await adminCtx.post('/api/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(loginResp.status()).toBe(200);

    const createResp = await adminCtx.post(`/api/admin/users?site_id=${SITE_A}`, {
      data: {
        email: VIEWER_EMAIL,
        username: `e2e-authz-viewer-${TEST_RUN_TAG}`,
        password: VIEWER_PASSWORD,
        sites: [{ site_id: SITE_A, role: 'viewer' }],
      },
      headers: { 'Content-Type': 'application/json' },
    });
    expect([201, 409]).toContain(createResp.status());

    await adminCtx.dispose();

    const ctx = await browser.newContext();
    const page = await ctx.newPage();

    // Prime the active site so SiteSwitcher boots happily on SITE_A.
    await page.addInitScript(
      ([key, id]: [string, string]) => {
        window.sessionStorage.setItem(key, id);
      },
      [STORAGE_KEY, String(SITE_A)],
    );

    // Login as viewer + open the SPA.
    await page.request.post('/api/login', {
      data: { email: VIEWER_EMAIL, password: VIEWER_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    await page.goto('/app/');

    // Use the browser's fetch to attempt the cross-tenant request as
    // if the user opened DevTools and ran fetch by hand.
    const status = await page.evaluate(async (siteB) => {
      const r = await fetch(`/api/stats/overview?site=${siteB}`, { credentials: 'include' });
      return r.status;
    }, SITE_B);

    expect(status, 'DevTools-bypass attempt MUST 403').toBe(403);

    await ctx.close();
  });

  test('site switcher options omit ungranted tenants', async ({ browser }) => {
    // Provision (idempotent) and log in as viewer.
    const adminCtx = await request.newContext({ baseURL: BASE });
    await adminCtx.post('/api/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    await adminCtx.post(`/api/admin/users?site_id=${SITE_A}`, {
      data: {
        email: VIEWER_EMAIL,
        username: `e2e-authz-viewer-${TEST_RUN_TAG}`,
        password: VIEWER_PASSWORD,
        sites: [{ site_id: SITE_A, role: 'viewer' }],
      },
      headers: { 'Content-Type': 'application/json' },
    });
    await adminCtx.dispose();

    const ctx = await browser.newContext();
    const page = await ctx.newPage();

    await page.addInitScript(
      ([key, id]: [string, string]) => {
        window.sessionStorage.setItem(key, id);
      },
      [STORAGE_KEY, String(SITE_A)],
    );

    await page.request.post('/api/login', {
      data: { email: VIEWER_EMAIL, password: VIEWER_PASSWORD },
      headers: { 'Content-Type': 'application/json' },
    });
    await page.goto('/app/');

    const select = page.getByTestId('site-select');
    await expect(select).toBeVisible({ timeout: 10_000 });

    const optionTexts = await select.locator('option').allInnerTexts();
    const joined = optionTexts.join(' | ');
    expect(joined, `should contain HOST_A`).toContain(HOST_A);
    expect(joined, `must NOT contain HOST_B`).not.toContain(HOST_B);

    await ctx.close();
  });
});
