import { signal } from '@preact/signals';

// The bearer token gating /api/stats/* until Phase 2b lands sessions +
// RBAC. The binary injects the token into index.html via a meta tag at
// request time (internal/dashboard/spa/dashboard.go rewrites
// STATNIVE_BEARER_PLACEHOLDER → cfg.Dashboard.BearerToken). Empty value =
// dev mode (BearerTokenMiddleware is no-op).
function bootstrapToken(): string {
  if (typeof document === 'undefined') return ''; // jsdom test env
  const meta = document.querySelector('meta[name="statnive-bearer"]');
  const v = meta?.getAttribute('content') ?? '';
  return v === 'STATNIVE_BEARER_PLACEHOLDER' ? '' : v;
}

export const authSignal = signal<string>(bootstrapToken());
