import { signal } from '@preact/signals';

// Phase 5a: hardcoded to the dogfood site. Phase 5b adds a switcher
// driven by GET /api/sites (Phase 3c — pending) or by a config-injected
// list. Cross-tenant isolation is enforced server-side by site_id; the
// SPA only chooses which site_id to query.
export const siteSignal = signal<number>(1);
