// consent-fresh-visitor.spec.ts — Stage-4 hot-fix (v0.0.35) regression.
//
// Reproduces the production bug where /api/privacy/consent returned 401
// for fresh visitors who had no _statnive cookie (the hybrid pre-consent
// posture). After the fix, the give branch mints the identifier itself.
//
// Coverage:
//  1. /privacy?site=<host> renders 200 + has the CSRF + statnive-site meta.
//  2. POST give with no _statnive returns 204 + Set-Cookie: _statnive
//     (Partitioned, HttpOnly, Secure, SameSite=None) + Set-Cookie:
//     _statnive_consent_<site_id>=v1 with the same attrs.
//  3. POST give with an existing _statnive does NOT re-Set-Cookie
//     _statnive (idempotent reuse).
//  4. POST withdraw with no _statnive returns 401 — the anchor invariant
//     for the withdraw path stays.

import { test, expect, request, type APIRequestContext } from '@playwright/test';
import { BASE, HOST, SITE_ID, fetchCSRF } from './fixtures/privacy';

const CONSENT_COOKIE = `_statnive_consent_${SITE_ID}`;

let visitorCtx: APIRequestContext | null = null;

test.describe('/api/privacy/consent — fresh-visitor give path (v0.0.35 fix)', () => {
  test.beforeEach(async () => {
    visitorCtx = await request.newContext({ baseURL: BASE });
  });

  test.afterEach(async () => {
    if (visitorCtx) {
      await visitorCtx.dispose();
      visitorCtx = null;
    }
  });

  test('/privacy?site renders with csrf + statnive-site metas', async () => {
    const page = await visitorCtx!.get(`/privacy?site=${encodeURIComponent(HOST)}`);
    expect(page.status()).toBe(200);
    const html = await page.text();
    expect(html).toMatch(/name="csrf-token" content="[^"]+"/);
    expect(html).toMatch(new RegExp(`name="statnive-site" content="${HOST}"`));
  });

  test('give with no _statnive mints the cookie + sets per-site consent', async () => {
    const csrf = await fetchCSRF(visitorCtx!);

    const resp = await visitorCtx!.post('/api/privacy/consent', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
      },
      data: { action: 'give' },
    });

    expect(resp.status(), 'consent give status').toBe(204);

    const setCookies = resp.headersArray()
      .filter(h => h.name.toLowerCase() === 'set-cookie')
      .map(h => h.value);

    const statnive = setCookies.find(c => c.startsWith('_statnive='));
    expect(statnive, '_statnive cookie minted on give').toBeDefined();
    expect(statnive!, '_statnive HttpOnly').toMatch(/HttpOnly/i);
    expect(statnive!, '_statnive SameSite=None').toMatch(/SameSite=None/i);
    expect(statnive!, '_statnive Partitioned (CHIPS)').toMatch(/Partitioned/i);
    expect(statnive!, '_statnive Path=/').toMatch(/Path=\//);

    const consent = setCookies.find(c => c.startsWith(`${CONSENT_COOKIE}=`));
    expect(consent, `${CONSENT_COOKIE} cookie set`).toBeDefined();
    expect(consent!).toMatch(new RegExp(`${CONSENT_COOKIE}=v1`));
    expect(consent!, 'consent SameSite=None').toMatch(/SameSite=None/i);
    expect(consent!, 'consent Partitioned').toMatch(/Partitioned/i);
  });

  test('give with existing _statnive does NOT re-set the identifier', async () => {
    const csrf = await fetchCSRF(visitorCtx!);
    const existingUUID = '550e8400-e29b-41d4-a716-446655440000';

    const resp = await visitorCtx!.post('/api/privacy/consent', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
        Cookie: `_statnive=${existingUUID}`,
      },
      data: { action: 'give' },
    });

    expect(resp.status()).toBe(204);

    const setCookies = resp.headersArray()
      .filter(h => h.name.toLowerCase() === 'set-cookie')
      .map(h => h.value);

    const statnive = setCookies.find(c => c.startsWith('_statnive='));
    expect(statnive, 'no fresh _statnive when one is already present').toBeUndefined();

    const consent = setCookies.find(c => c.startsWith(`${CONSENT_COOKIE}=`));
    expect(consent, `${CONSENT_COOKIE} cookie set even with existing _statnive`).toBeDefined();
  });

  test('withdraw without _statnive returns 401 — anchor invariant preserved', async () => {
    const csrf = await fetchCSRF(visitorCtx!);

    const resp = await visitorCtx!.post('/api/privacy/consent', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
      },
      data: { action: 'withdraw' },
    });

    expect(resp.status(), 'withdraw must still 401 with no identity').toBe(401);
  });
});
