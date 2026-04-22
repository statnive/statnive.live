import { signal } from '@preact/signals';

// Phase 2b auth state. The SPA supports two co-existing auth paths:
//
//   1. Session cookie (production) — set by POST /api/login + cleared by
//      POST /api/logout. Carried on every request automatically via
//      credentials: 'include'. The server-side SessionMiddleware attaches
//      the user to the request context.
//
//   2. Bearer token (CI / smoke / legacy) — the binary injects the token
//      into index.html at /app/ request time via the STATNIVE_BEARER_
//      PLACEHOLDER meta tag. Present only when `auth.api_tokens` or the
//      legacy `dashboard.bearer_token` config key is set. The Phase 5a-
//      smoke harness + Phase 5c Playwright `auth.spec.ts` both use this
//      path; production operators should prefer cookie auth.
//
// Both paths may be active at once: api/client.ts attaches the bearer
// header if present AND sends cookies. The server accepts whichever
// arrives first.

export interface CurrentUser {
  user_id: string;
  email: string;
  username: string;
  role: 'admin' | 'viewer' | 'api';
  site_id: number;
  demo_banner?: string;
}

function bootstrapBearer(): string {
  if (typeof document === 'undefined') return '';
  const meta = document.querySelector('meta[name="statnive-bearer"]');
  const v = meta?.getAttribute('content') ?? '';
  return v === 'STATNIVE_BEARER_PLACEHOLDER' ? '' : v;
}

// Legacy — kept so the CI bearer path still works. api/client.ts
// attaches an Authorization header whenever this is non-empty.
export const authSignal = signal<string>(bootstrapBearer());

// Signed-in user. null = unauthenticated (show Login page). Populated
// by loadCurrentUser() at SPA mount.
export const userSignal = signal<CurrentUser | null>(null);

// authChecked gates the initial render: we don't want to flash the
// Login page before /api/user resolves.
export const authCheckedSignal = signal<boolean>(false);

export async function loadCurrentUser(): Promise<CurrentUser | null> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (authSignal.value) {
    headers.Authorization = `Bearer ${authSignal.value}`;
  }

  try {
    const res = await fetch('/api/user', { headers, credentials: 'include' });
    if (res.status === 401) {
      userSignal.value = null;
      return null;
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const body = (await res.json()) as CurrentUser;
    userSignal.value = body;
    return body;
  } catch {
    userSignal.value = null;
    return null;
  } finally {
    authCheckedSignal.value = true;
  }
}

export async function logout(): Promise<void> {
  try {
    await fetch('/api/logout', { method: 'POST', credentials: 'include' });
  } catch {
    // Still clear local state even if the request failed — user intent
    // is to log out regardless.
  }
  userSignal.value = null;
  authSignal.value = '';
}
