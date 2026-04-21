import { siteSignal } from '../state/site';
import { authSignal } from '../state/auth';

// apiGet is the single entry point for SPA → server reads. Adds the
// active site_id from siteSignal and Authorization: Bearer from
// authSignal. Plain fetch — no TanStack Query: the bundle budget can't
// absorb ~10 KB gz of TQ machinery, and signals + per-panel re-fetch
// covers the same 90% of the use case.
export async function apiGet<T>(
  path: string,
  params: Record<string, string> = {},
  signal?: AbortSignal,
): Promise<T> {
  const url = new URL(path, window.location.origin);
  url.searchParams.set('site', String(siteSignal.value));
  for (const [k, v] of Object.entries(params)) {
    url.searchParams.set(k, v);
  }

  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = authSignal.value;
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const res = await fetch(url.toString(), { headers, signal });
  if (!res.ok) {
    throw new Error(`apiGet ${path}: HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}
