import { siteSignal, activeSiteSignal, sitesSignal, STORAGE_KEY } from '../state/site';
import { authSignal } from '../state/auth';
import { filtersSignal } from '../state/filters';

// apiGet is the single entry point for SPA → server reads. Adds the
// active site_id from siteSignal, Authorization: Bearer from authSignal,
// and — for /api/stats/* routes — the non-empty filters from filtersSignal.
// Plain fetch — no TanStack Query: the bundle budget can't absorb ~10 KB gz
// of TQ machinery, and signals + per-panel re-fetch covers the same 90%
// of the use case.
//
// On HTTP 403 for a site-scoped path (per-site grant revoked or stale
// active site after an admin yanked the grant), clears the persisted
// active site and drops the cached sites list so SiteSwitcher refetches
// /api/sites and renders the correct dropdown on the next render tick.
// Signal writes are change-guarded so panel 403 storms don't cascade
// into N re-renders.
export async function apiGet<T>(
  path: string,
  params: Record<string, string> = {},
  signal?: AbortSignal,
): Promise<T> {
  const url = new URL(path, window.location.origin);
  url.searchParams.set('site', String(siteSignal.value));

  // Merge non-empty filters from filtersSignal. Explicit params win so
  // a panel can override (e.g. a panel-specific `from`/`to`).
  const f = filtersSignal.value;
  for (const [k, v] of Object.entries(f)) {
    if (v) url.searchParams.set(k, v);
  }
  for (const [k, v] of Object.entries(params)) {
    url.searchParams.set(k, v);
  }

  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = authSignal.value;
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const res = await fetch(url.toString(), { headers, signal, credentials: 'include' });
  if (res.status === 403 && isSiteScopedPath(path)) {
    clearStaleActiveSite();
  }
  if (!res.ok) {
    throw new Error(`apiGet ${path}: HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

// isSiteScopedPath returns true for dashboard routes that carry a ?site
// query parameter — the only 403 class where the stale-active-site
// recovery is appropriate. Login rate-limits, admin endpoints the
// viewer isn't allowed to see, etc. also return 403 but must NOT clear
// the active-site selection.
function isSiteScopedPath(path: string): boolean {
  return path.startsWith('/api/stats/') || path === '/api/realtime/visitors';
}

// clearStaleActiveSite drops the persisted active-site selection +
// in-memory signals so SiteSwitcher refetches /api/sites and re-renders
// with whatever grants the actor actually holds now. Change-guarded so
// a 403 storm from N concurrently-mounted panels collapses to at most
// one signal write.
function clearStaleActiveSite(): void {
  try {
    window.sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    // sessionStorage unavailable — no-op.
  }
  if (activeSiteSignal.value !== null) {
    activeSiteSignal.value = null;
  }
  if (sitesSignal.value.length !== 0) {
    sitesSignal.value = [];
  }
}
