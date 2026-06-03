// widerspruch-flow.spec.ts — E2E for the full opt-out + erase flow
// the Televika /widerspruch page runs in the visitor's browser
// (mirrors Step 7 of televika-PATH-A-IMPLEMENTATION.md, lines 1080-
// 1133). Three sequential calls: CSRF token → opt-out → erase.
//
// Tier-1 assertion per CLAUDE.md hierarchy: ClickHouse-Oracle
// confirms the visitor's events_raw rows drop to 0 after the erase
// goroutine lands the ALTER ... DELETE. The 3-step DOM status text
// (1/3 → 2/3 → 3/3) is intentionally NOT asserted — it's a UI nicety
// that would need a fixture HTML page; what's load-bearing for
// Televika is the API contract verified here.
//
// Plan reference: plan-to-design-real-production-eager-pine.md
// matrix entries E1 + E2 (error-path).

import { test, expect, request, type APIRequestContext } from '@playwright/test';
import { chQuery, waitForCount } from './fixtures/chOracle';
import { BASE, HOST, SITE_ID, fetchCSRF } from './fixtures/privacy';

const OPTOUT_COOKIE = `_statnive_optout_${SITE_ID}`;

// In dev-insecure mode (STATNIVE_DEV_INSECURE_CSRF=1 — set by Playwright
// globalSetup.ts so the __Host- prefix can be skipped over plain HTTP)
// the CSRF cookie is named _statnive_csrf and its value equals the
// X-CSRF-Token header (double-submit pattern per internal/middleware/
// csrf.go:VerifyCSRF). Production sets __Host-statnive_csrf instead.
const CSRF_COOKIE = '_statnive_csrf';

// buildCookieHeader joins CSRF + _statnive into a single Cookie
// header. Playwright's APIRequestContext auto-merges cookies from
// its own jar with extra headers EXCEPT when you supply a Cookie
// header — at that point it stops merging and uses the literal value.
// So callers that need both cookies must build the header themselves.
function buildCookieHeader(csrf: string, visitorCookie: string): string {
  return `${CSRF_COOKIE}=${csrf}; _statnive=${visitorCookie}`;
}

function rowCount(sql: string): number {
  const row = chQuery<{ count: string }>(sql);
  return row ? Number(row.count) : 0;
}

test.describe('/widerspruch flow — CSRF + opt-out + erase round-trip', () => {
  let ctx: APIRequestContext | null = null;

  test.beforeEach(async () => {
    ctx = await request.newContext({ baseURL: BASE });
  });

  test.afterEach(async () => {
    if (ctx) {
      await ctx.dispose();
      ctx = null;
    }
  });

  test('full sequence: CSRF → opt-out → erase, with CH row count drop', async () => {
    // Bumped above the 30s default — pollForZero alone has a 30s
    // budget for the async ALTER ... DELETE mutation to land.
    test.setTimeout(60_000);

    // Synthetic cookie for this run — handler hashes it via
    // identity.HexCookieIDHash, so the value doesn't need to be a real
    // UUID, only stable across the three calls.
    const visitorCookie = `widerspruch-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const tag = `e2e_widerspruch_${Date.now()}`;

    // Seed one event for this synthetic visitor so the erase has work
    // to do — without seeded rows, the spawnCompletionWatcher goroutine
    // emits 0 mutations and the audit-event chain is harder to assert.
    // /api/event isn't CSRF-protected; bare _statnive cookie is fine.
    const seedResp = await ctx!.post('/api/event', {
      headers: {
        'Content-Type': 'text/plain',
        'User-Agent': 'Mozilla/5.0 (WidersprcheE2E/1.0) BrowserLike',
        'Origin': `https://${HOST}`,
        // X-Statnive-Consent: given bypasses the consent gate so the
        // cookie_id hash actually lands on the row — without it, the
        // binary's default consent.required=true suppresses identity.
        'X-Statnive-Consent': 'given',
        Cookie: `_statnive=${visitorCookie}`,
      },
      data: JSON.stringify({
        hostname: HOST,
        pathname: '/widerspruch-seed',
        event_type: 'pageview',
        event_name: tag,
      }),
    });
    expect(seedResp.status(), 'seed event POST').toBe(202);

    // Wait for the consumer to drain the WAL into events_raw — Tier-1
    // assertion that we have a row to erase.
    const sqlForVisitor =
      `SELECT count() AS count FROM statnive.events_raw ` +
      `WHERE site_id = ${SITE_ID} AND event_name = '${tag}'`;
    const after = await waitForCount(sqlForVisitor, 1, 10_000);
    expect(after, 'seed event landed').toBe(1);

    // --- Step 1/3 (mirrors televika-PATH-A-IMPLEMENTATION.md line 1082-1090)
    // Fetch CSRF token from /privacy?site= landing page.
    const csrf = await fetchCSRF(ctx!);
    expect(csrf.length, 'CSRF token min length').toBeGreaterThanOrEqual(32);

    // --- Step 2/3 (lines 1092-1103): POST /api/privacy/opt-out.
    const optoutResp = await ctx!.post('/api/privacy/opt-out', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
        'Origin': `https://${HOST}`,
        Cookie: buildCookieHeader(csrf, visitorCookie),
      },
      data: '{}',
    });
    expect(optoutResp.status(), 'opt-out status').toBe(204);

    // Verify Set-Cookie matches the production attributes — the
    // tracker's ingest gate reads this cookie on every subsequent
    // event to short-circuit identity computation.
    const setCookies = optoutResp.headersArray()
      .filter(h => h.name.toLowerCase() === 'set-cookie')
      .map(h => h.value);
    const optoutCookie = setCookies.find(c => c.startsWith(`${OPTOUT_COOKIE}=`));
    expect(optoutCookie, `${OPTOUT_COOKIE} set`).toBeDefined();
    expect(optoutCookie!, 'cookie value').toMatch(new RegExp(`${OPTOUT_COOKIE}=v1`));
    expect(optoutCookie!, 'HttpOnly').toMatch(/HttpOnly/i);
    expect(optoutCookie!, 'Partitioned (CHIPS)').toMatch(/Partitioned/i);

    // --- Step 3/3 (lines 1105-1118): POST /api/privacy/erase.
    const eraseResp = await ctx!.post('/api/privacy/erase', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
        'Origin': `https://${HOST}`,
        Cookie: buildCookieHeader(csrf, visitorCookie),
      },
      data: '{}',
    });
    expect(eraseResp.status(), 'erase status').toBe(202);

    const eraseJson = await eraseResp.json();
    expect(eraseJson.status, 'erase response status field').toBe('accepted');
    expect(Array.isArray(eraseJson.tables), 'erase response tables[] array').toBe(true);

    // --- Tier-1 ClickHouse-Oracle: the visitor's row MUST disappear
    // within the spawnCompletionWatcher's 5-minute deadline. On a
    // single-row DELETE against docker CH this is sub-second; 30s
    // covers CI noise.
    const settled = await pollForZero(sqlForVisitor, 30_000);
    expect(settled, 'visitor rows erased from events_raw').toBe(0);
  });

  test('control: erase against a different cookie does NOT touch other visitors', async () => {
    test.setTimeout(60_000);

    // Two synthetic visitors on the same site. Erase A; assert B's
    // row survives. Cross-tenant-style isolation but within one site,
    // verifying the cookie_id filter is the load-bearing predicate
    // (TestDSAR_CrossTenantIsolation pins the same invariant
    // server-side; this proves it through the real browser-style
    // API path too).
    const cookieA = `widerspruch-a-${Date.now()}`;
    const cookieB = `widerspruch-b-${Date.now()}`;
    const tagA = `e2e_widerspruch_keep_a_${Date.now()}`;
    const tagB = `e2e_widerspruch_keep_b_${Date.now()}`;

    for (const [cookie, tag] of [[cookieA, tagA], [cookieB, tagB]] as const) {
      const r = await ctx!.post('/api/event', {
        headers: {
          'Content-Type': 'text/plain',
          'User-Agent': 'Mozilla/5.0 (WidersprcheE2E/1.0) BrowserLike',
          'Origin': `https://${HOST}`,
          'X-Statnive-Consent': 'given',
          Cookie: `_statnive=${cookie}`,
        },
        data: JSON.stringify({
          hostname: HOST,
          pathname: `/seed-${tag}`,
          event_type: 'pageview',
          event_name: tag,
        }),
      });
      expect(r.status()).toBe(202);
    }

    const sqlA = `SELECT count() AS count FROM statnive.events_raw WHERE site_id = ${SITE_ID} AND event_name = '${tagA}'`;
    const sqlB = `SELECT count() AS count FROM statnive.events_raw WHERE site_id = ${SITE_ID} AND event_name = '${tagB}'`;
    expect(await waitForCount(sqlA, 1, 10_000)).toBe(1);
    expect(await waitForCount(sqlB, 1, 10_000)).toBe(1);

    // Erase visitor A only.
    const csrf = await fetchCSRF(ctx!);
    const erase = await ctx!.post('/api/privacy/erase', {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        'X-Statnive-Site': HOST,
        'Origin': `https://${HOST}`,
        Cookie: buildCookieHeader(csrf, cookieA),
      },
      data: '{}',
    });
    expect(erase.status()).toBe(202);

    // Poll: A drops to 0, B stays at 1.
    expect(await pollForZero(sqlA, 30_000)).toBe(0);
    expect(rowCount(sqlB), `visitor B's row (tag=${tagB}) must survive A's erase`).toBe(1);
  });
});

// pollForZero waits up to timeoutMs for the SQL count to reach 0.
// Counterpart to waitForCount which waits for >= target; we need the
// opposite direction here (counts going DOWN as the mutation lands).
async function pollForZero(sql: string, timeoutMs: number): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = -1;
  while (Date.now() < deadline) {
    last = rowCount(sql);
    if (last === 0) return 0;
    await new Promise(r => setTimeout(r, 200));
  }
  return last;
}
