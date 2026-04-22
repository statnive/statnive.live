import { test, expect } from '@playwright/test';
import { getBearer } from './fixtures/bearer';

const ADMIN_EMAIL = process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live';
const ADMIN_PASSWORD = process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static';

test.describe('auth + CSP', () => {
  test('bearer is injected into /app/ meta tag', async ({ page }) => {
    const token = await getBearer(page);
    expect(token).toBe(process.env.STATNIVE_E2E_TOKEN);
  });

  test('/api/stats/overview rejects requests without bearer or cookie', async ({ request }) => {
    const site = process.env.STATNIVE_E2E_SITE_A!;
    // A freshly-constructed request context has no cookie jar + we
    // strip the Authorization header, so this hits the unauthenticated
    // middleware path.
    const res = await request.get(`/api/stats/overview?site=${site}`, {
      headers: { Authorization: '' },
    });
    expect(res.status()).toBe(401);
  });

  test('no CSP violations on Overview or Sources panel load', async ({ page }) => {
    const cspErrors: string[] = [];
    page.on('console', (msg) => {
      const text = msg.text();
      if (/content.*security.*policy|refused to execute|refused to load/i.test(text)) {
        cspErrors.push(text);
      }
    });
    page.on('pageerror', (err) => cspErrors.push(err.message));

    await page.goto('/app/');
    await expect(page.getByTestId('kpi-primary')).toBeVisible();

    // Navigate to Sources panel via hash; LazyPanel chunk must also
    // load without CSP violations.
    await page.goto('/app/#sources');
    await expect(page.getByTestId('panel-sources')).toBeVisible();

    expect(cspErrors, cspErrors.join('\n')).toHaveLength(0);
  });
});

test.describe('Phase 2b session cookie flow', () => {
  // Each test in this block gets a clean request context (no bearer,
  // no cookies). `request` is configured in playwright.config.ts to
  // inherit STATNIVE_E2E_BASEURL.

  test('POST /api/login issues a session cookie + /api/user returns 200', async ({
    request,
  }) => {
    const loginRes = await request.post('/api/login', {
      headers: { 'Content-Type': 'application/json' },
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    expect(loginRes.status()).toBe(200);

    const setCookie = loginRes.headers()['set-cookie'] ?? '';
    expect(setCookie).toMatch(/statnive_session=/);
    expect(setCookie).toMatch(/HttpOnly/i);
    expect(setCookie).toMatch(/SameSite=Lax/i);

    // Playwright request context auto-follows the cookie jar.
    const meRes = await request.get('/api/user');
    expect(meRes.status()).toBe(200);
    const me = await meRes.json();
    expect(me.email).toBe(ADMIN_EMAIL);
    expect(me.role).toBe('admin');

    // Stats must also work on the session cookie path.
    const site = process.env.STATNIVE_E2E_SITE_A!;
    const statsRes = await request.get(`/api/stats/overview?site=${site}`);
    expect(statsRes.status()).toBe(200);
  });

  test('POST /api/login rejects wrong password with uniform body', async ({
    request,
  }) => {
    const res = await request.post('/api/login', {
      headers: { 'Content-Type': 'application/json' },
      data: { email: ADMIN_EMAIL, password: 'obviously-wrong' },
    });
    expect(res.status()).toBe(401);
    const body = await res.json();
    expect(body.error).toBe('invalid credentials');
  });

  test('POST /api/login rejects unknown-field body (F4 mass-assignment guard)', async ({
    request,
  }) => {
    const res = await request.post('/api/login', {
      headers: { 'Content-Type': 'application/json' },
      data: {
        email: ADMIN_EMAIL,
        password: ADMIN_PASSWORD,
        role: 'admin',
        site_id: 99,
        is_admin: true,
      },
    });
    expect(res.status()).toBe(401);
  });

  test('POST /api/logout clears the cookie + stats returns 401 afterwards', async ({
    request,
  }) => {
    // Start fresh: log in, confirm, log out, confirm.
    const login = await request.post('/api/login', {
      headers: { 'Content-Type': 'application/json' },
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    expect(login.status()).toBe(200);

    const logout = await request.post('/api/logout');
    expect(logout.status()).toBe(204);

    const site = process.env.STATNIVE_E2E_SITE_A!;
    const stats = await request.get(`/api/stats/overview?site=${site}`, {
      headers: { Authorization: '' },
    });
    expect(stats.status()).toBe(401);
  });
});
