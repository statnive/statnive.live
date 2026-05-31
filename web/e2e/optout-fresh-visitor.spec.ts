// optout-fresh-visitor.spec.ts — Stage-4 hot-fix (v0.0.35) regression.
//
// Covers the OptOut → ingest-gate round trip:
//  1. POST /api/privacy/opt-out with no _statnive returns 204 + Set-Cookie:
//     _statnive_optout_<site_id>=v1 (Partitioned, HttpOnly, SameSite=None).
//  2. A subsequent /api/event POST carrying ONLY that opt-out cookie is
//     dropped at the ingest gate — ClickHouse-Oracle proves zero new rows.
//  3. Response shape stays stable: visitor MUST NOT be able to enumerate
//     whether they were suppressed (status code identical to the
//     non-opted-out path).

import { test, expect, request, type APIRequestContext } from '@playwright/test';
import { chQuery, waitForCount } from './fixtures/chOracle';
import { BASE, HOST, SITE_ID, fetchCSRF } from './fixtures/privacy';

const OPTOUT_COOKIE = `_statnive_optout_${SITE_ID}`;

let visitorCtx: APIRequestContext | null = null;

function rowCount(sql: string): number {
  const row = chQuery<{ count: string }>(sql);
  return row ? Number(row.count) : 0;
}

test.describe('/api/privacy/opt-out — fresh-visitor + ingest cookie gate (v0.0.35 fix)', () => {
  test.beforeEach(async () => {
    visitorCtx = await request.newContext({ baseURL: BASE });
  });

  test.afterEach(async () => {
    if (visitorCtx) {
      await visitorCtx.dispose();
      visitorCtx = null;
    }
  });

  test('opt-out with no _statnive sets the per-site cookie + 204', async () => {
    const csrf = await fetchCSRF(visitorCtx!);

    const resp = await visitorCtx!.post('/api/privacy/opt-out', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
      },
    });

    expect(resp.status(), 'opt-out status').toBe(204);

    const setCookies = resp.headersArray()
      .filter(h => h.name.toLowerCase() === 'set-cookie')
      .map(h => h.value);

    const optout = setCookies.find(c => c.startsWith(`${OPTOUT_COOKIE}=`));
    expect(optout, `${OPTOUT_COOKIE} set`).toBeDefined();
    expect(optout!).toMatch(new RegExp(`${OPTOUT_COOKIE}=v1`));
    expect(optout!, 'HttpOnly').toMatch(/HttpOnly/i);
    expect(optout!, 'SameSite=None').toMatch(/SameSite=None/i);
    expect(optout!, 'Partitioned (CHIPS)').toMatch(/Partitioned/i);
  });

  test('ingest gate drops events after fresh-visitor opt-out', async () => {
    // 1. Establish baseline row count for the event name we'll fire.
    const sql = `SELECT count() AS count FROM statnive.events_raw WHERE site_id = ${SITE_ID} AND event_name = 'e2e_optout_probe'`;
    const before = rowCount(sql);

    // 2. Opt out as a fresh visitor — no prior _statnive cookie.
    const csrf = await fetchCSRF(visitorCtx!);
    const optoutResp = await visitorCtx!.post('/api/privacy/opt-out', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
      },
    });
    expect(optoutResp.status()).toBe(204);

    // 3. Fire /api/event carrying only the opt-out cookie (the visitor
    // context kept the cookie automatically via the response above).
    const evResp = await visitorCtx!.post('/api/event', {
      headers: {
        'Content-Type': 'text/plain',
        'User-Agent': 'Mozilla/5.0 (OptOutE2E/1.0) BrowserLike',
        Origin: `https://${HOST}`,
      },
      data: JSON.stringify({
        hostname: HOST,
        pathname: '/optout-probe',
        event_type: 'pageview',
        event_name: 'e2e_optout_probe',
      }),
    });
    // Response shape MUST stay stable — visitor must not learn they were dropped.
    expect(evResp.status(), 'event POST status (still 202 — shape-stable drop)').toBe(202);

    // 4. ClickHouse-Oracle: give the batcher 500ms to settle, then
    // assert the row count for this event_name did NOT increase.
    // waitForCount returns the count at success; we want it to STAY at
    // `before`. If the gate failed and the event slipped through, the
    // count would tick to `before + 1` and the assertion below fires.
    await new Promise(r => setTimeout(r, 700));
    const after = rowCount(sql);
    expect(after, 'opted-out event must not land in CH').toBe(before);
  });

  test('control: non-opted-out fresh request still lands in CH (gate is selective, not blanket)', async () => {
    // Sanity check that the gate is opt-out-cookie-specific. Without
    // the opt-out cookie, the same payload lands normally — proves the
    // earlier zero-rows assertion was not just a generic broken-pipe.
    const sql = `SELECT count() AS count FROM statnive.events_raw WHERE site_id = ${SITE_ID} AND event_name = 'e2e_optout_control'`;
    const before = rowCount(sql);

    const ctx = await request.newContext({ baseURL: BASE });
    const resp = await ctx.post('/api/event', {
      headers: {
        'Content-Type': 'text/plain',
        'User-Agent': 'Mozilla/5.0 (OptOutE2E/1.0) BrowserLike',
        Origin: `https://${HOST}`,
      },
      data: JSON.stringify({
        hostname: HOST,
        pathname: '/optout-control',
        event_type: 'pageview',
        event_name: 'e2e_optout_control',
      }),
    });
    expect(resp.status()).toBe(202);

    const after = await waitForCount(sql, before + 1, 10_000);
    expect(after).toBe(before + 1);

    await ctx.dispose();
  });
});
