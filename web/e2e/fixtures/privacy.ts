// Shared helpers for /api/privacy/* e2e specs. The CSRF token comes
// from a meta tag on /privacy; tokens are stable for the request
// context's lifetime, so the helper memoises per APIRequestContext.

import { expect, type APIRequestContext } from '@playwright/test';

export const BASE = process.env.STATNIVE_E2E_BASEURL ?? 'http://127.0.0.1:18299';
export const SITE_ID = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
export const HOST = process.env.STATNIVE_E2E_HOST_A ?? 'e2e-a.example.com';

const csrfCache = new WeakMap<APIRequestContext, string>();

export async function fetchCSRF(ctx: APIRequestContext, site: string = HOST): Promise<string> {
  const cached = csrfCache.get(ctx);
  if (cached) return cached;

  const page = await ctx.get(`/privacy?site=${encodeURIComponent(site)}`);
  expect(page.status(), '/privacy GET status').toBe(200);
  const html = await page.text();
  const meta = html.match(/name="csrf-token" content="([^"]+)"/);
  expect(meta, 'csrf-token meta present').not.toBeNull();

  const token = meta![1];
  csrfCache.set(ctx, token);
  return token;
}
