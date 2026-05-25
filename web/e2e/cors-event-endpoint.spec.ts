// E2E: wire-level CORS against the live binary for /api/event —
// OPTIONS preflight (seeded, www.-toggle fallback, unseeded 403),
// POST JSON from seeded Origin (ACAO echo + CH-oracle), and POST
// text/plain from unseeded Origin (defence-in-depth: still records).
//
// We test the binary's response shape rather than spinning up a
// real cross-origin page via host-resolver-rules tricks — the
// latter would test Chromium's CORS engine instead of our code.

import { test, expect, request, type APIRequestContext } from '@playwright/test';
import { chQuery, waitForCount } from './fixtures/chOracle';

function rowCount(sql: string): number {
  const row = chQuery<{ count: string }>(sql);
  return row ? Number(row.count) : 0;
}

const ADMIN_EMAIL = process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live';
const ADMIN_PASSWORD = process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static';
const BASE = process.env.STATNIVE_E2E_BASEURL ?? 'http://127.0.0.1:18299';
const SITE_A = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
const HOST_A = process.env.STATNIVE_E2E_HOST_A ?? 'e2e-a.example.com';

// Per-run tag keeps reruns from colliding with the global uniqueness
// check on allowed_origins.
const TAG = `${Date.now().toString(36)}-${Math.floor(Math.random() * 0xffff).toString(16)}`;
const BARE_HOST = `${TAG}-cors-bare.example`;
const BARE_ORIGIN = `https://${BARE_HOST}`;
const WWW_ORIGIN = `https://www.${BARE_HOST}`;
const UNSEEDED_ORIGIN = `https://${TAG}-unseeded.attacker.example`;

let adminCtx: APIRequestContext | null = null;

async function getAdminCtx(): Promise<APIRequestContext> {
  if (adminCtx) return adminCtx;
  adminCtx = await request.newContext({ baseURL: BASE });
  const login = await adminCtx.post('/api/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    headers: { 'Content-Type': 'application/json' },
  });
  expect(login.status(), 'admin login').toBe(200);
  return adminCtx;
}

test.describe('CORS — /api/event wiring + www. equivalence', () => {
  test.beforeAll(async () => {
    // Seed SITE_A's allowed_origins with the bare variant only — the
    // www.-fallback then proves itself on the OPTIONS test below.
    const ctx = await getAdminCtx();
    const patch = await ctx.patch(`/api/admin/sites/${SITE_A}`, {
      data: { allowed_origins: [BARE_ORIGIN] },
      headers: { 'Content-Type': 'application/json' },
    });
    expect(patch.status(), 'seed allowed_origins').toBe(200);
  });

  test.afterAll(async () => {
    // Clean the allowlist so reruns don't collide on the unique
    // constraint and so other specs see SITE_A in its baseline state.
    if (adminCtx) {
      await adminCtx.patch(`/api/admin/sites/${SITE_A}`, {
        data: { allowed_origins: [] },
        headers: { 'Content-Type': 'application/json' },
      });
      await adminCtx.dispose();
      adminCtx = null;
    }
  });

  test('preflight from seeded bare origin → 204 + ACAO echo', async ({ request: req }) => {
    const resp = await req.fetch(`${BASE}/api/event`, {
      method: 'OPTIONS',
      headers: {
        Origin: BARE_ORIGIN,
        'Access-Control-Request-Method': 'POST',
        'Access-Control-Request-Headers': 'content-type',
      },
    });
    expect(resp.status(), 'OPTIONS status').toBe(204);
    expect(resp.headers()['access-control-allow-origin']).toBe(BARE_ORIGIN);
    expect(resp.headers()['vary'] ?? '').toMatch(/Origin/);
    expect(resp.headers()['access-control-allow-credentials']).toBe('true');
  });

  test('preflight from www. variant against bare-only allowlist → 204 via fallback', async ({ request: req }) => {
    const resp = await req.fetch(`${BASE}/api/event`, {
      method: 'OPTIONS',
      headers: {
        Origin: WWW_ORIGIN,
        'Access-Control-Request-Method': 'POST',
        'Access-Control-Request-Headers': 'content-type',
      },
    });
    expect(resp.status(), 'OPTIONS status via www-fallback').toBe(204);
    // ACAO must echo the REQUEST's Origin (www.), not the seeded variant
    // (bare). Browser CORS requires byte-match between request Origin
    // and response ACAO.
    expect(resp.headers()['access-control-allow-origin']).toBe(WWW_ORIGIN);
  });

  test('preflight from unseeded origin → 403, no ACAO leak', async ({ request: req }) => {
    const resp = await req.fetch(`${BASE}/api/event`, {
      method: 'OPTIONS',
      headers: {
        Origin: UNSEEDED_ORIGIN,
        'Access-Control-Request-Method': 'POST',
      },
    });
    expect(resp.status(), 'OPTIONS unseeded must 403').toBe(403);
    expect(resp.headers()['access-control-allow-origin'] ?? '').toBe('');
  });

  test('POST application/json from seeded origin → 202 + ACAO + event lands in CH', async ({ request: req }) => {
    const sql = `SELECT count() AS count FROM statnive.events_raw WHERE site_id = ${SITE_A} AND event_name = 'e2e_cors_purchase'`;
    const before = rowCount(sql);

    const resp = await req.fetch(`${BASE}/api/event`, {
      method: 'POST',
      headers: {
        Origin: BARE_ORIGIN,
        'Content-Type': 'application/json',
        'User-Agent': 'Mozilla/5.0 (CORSE2E/1.0) BrowserLike',
      },
      data: {
        hostname: HOST_A,
        pathname: '/cors-e2e/purchase-probe',
        event_type: 'custom',
        event_name: 'e2e_cors_purchase',
      },
    });
    expect(resp.status(), 'POST status').toBe(202);
    expect(resp.headers()['access-control-allow-origin']).toBe(BARE_ORIGIN);

    // ClickHouse-oracle (Tier 1): event landed on SITE_A. Poll briefly
    // — the binary batches 100ms before flushing to CH.
    const after = await waitForCount(sql, before + 1, 10_000);
    expect(after, 'CH row count after POST').toBe(before + 1);
  });

  test('POST text/plain from unseeded origin still records (sendBeacon path)', async ({ request: req }) => {
    // wp-slimstat-style traffic: empty allowed_origins, CORS-safe
    // content-type, no preflight. Server records the event; browser
    // silently drops the response (defence-in-depth, not a gatekeeper).
    const sql = `SELECT count() AS count FROM statnive.events_raw WHERE site_id = ${SITE_A} AND event_name = 'e2e_cors_pageview_unseeded'`;
    const before = rowCount(sql);

    const resp = await req.fetch(`${BASE}/api/event`, {
      method: 'POST',
      headers: {
        Origin: UNSEEDED_ORIGIN,
        'Content-Type': 'text/plain',
        'User-Agent': 'Mozilla/5.0 (CORSE2E/1.0) BrowserLike',
      },
      // Stringify so Playwright sends text/plain instead of JSON encoding.
      data: JSON.stringify({
        hostname: HOST_A,
        pathname: '/cors-e2e/pageview-probe',
        event_type: 'pageview',
        event_name: 'e2e_cors_pageview_unseeded',
      }),
    });
    expect(resp.status(), 'POST text/plain status').toBe(202);
    expect(resp.headers()['access-control-allow-origin'] ?? '').toBe('');

    const after = await waitForCount(sql, before + 1, 10_000);
    expect(after, 'CH row count after sendBeacon-style POST').toBe(before + 1);
  });
});
