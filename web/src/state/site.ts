import { signal, effect } from '@preact/signals';

// Site mirrors internal/sites.Site. The JSON shape is the contract —
// the /api/sites handler wraps a []Site in {sites: [...]}. Currency
// is the ISO 4217 alpha-3 label fmtMoney uses; tz is the IANA zone
// the dashboard's date-range picker interprets.
export interface Site {
  id: number;
  hostname: string;
  enabled: boolean;
  tz: string;
  currency: string;
}

// activeSiteSignal holds the currently-selected tenant. null = "not yet
// loaded / no site available". Populated on boot by SiteSwitcher's
// /api/sites fetch, persisted to sessionStorage so tab reopen picks up
// where the operator left off.
export const activeSiteSignal = signal<Site | null>(null);

// sitesSignal holds the full list for the switcher dropdown. Also
// populated by SiteSwitcher's boot fetch.
export const sitesSignal = signal<Site[]>([]);

// Exported so Playwright e2e specs can set the value via addInitScript
// without hard-coding the literal across every beforeEach.
export const STORAGE_KEY = 'statnive.activeSiteId';

function bootSiteId(): number {
  // Read persisted site_id at module load so panels' initial apiGet
  // calls (which read siteSignal.value at mount, before SiteSwitcher's
  // /api/sites fetch resolves) hit the right tenant. Falls back to 1
  // when sessionStorage is empty or unavailable — SiteSwitcher's boot
  // effect overwrites this with the first enabled site from /api/sites.
  try {
    const raw = typeof window !== 'undefined'
      ? window.sessionStorage.getItem(STORAGE_KEY)
      : null;
    if (!raw) return 1;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : 1;
  } catch {
    return 1;
  }
}

// siteSignal keeps the numeric site_id that apiGet reads. Writable
// directly (tests + legacy callers) and auto-synced from
// activeSiteSignal via the effect below.
export const siteSignal = signal<number>(bootSiteId());

effect(() => {
  const active = activeSiteSignal.value;
  if (active && active.id !== siteSignal.value) {
    siteSignal.value = active.id;
  }
});

export function persistActiveSite(id: number): void {
  try {
    window.sessionStorage.setItem(STORAGE_KEY, String(id));
  } catch {
    // sessionStorage unavailable (iframe sandbox, private mode in some
    // browsers) — silently skip; next boot picks the first enabled site.
  }
}

export function loadPersistedSiteId(): number | null {
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : null;
  } catch {
    return null;
  }
}
