import { signal, effect } from '@preact/signals';

// Site mirrors internal/sites.Site. The JSON shape is the contract —
// the /api/sites handler wraps a []Site in {sites: [...]}.
export interface Site {
  id: number;
  hostname: string;
  enabled: boolean;
  tz: string;
}

// activeSiteSignal holds the currently-selected tenant. null = "not yet
// loaded / no site available". Populated on boot by SiteSwitcher's
// /api/sites fetch, persisted to sessionStorage so tab reopen picks up
// where the operator left off.
export const activeSiteSignal = signal<Site | null>(null);

// sitesSignal holds the full list for the switcher dropdown. Also
// populated by SiteSwitcher's boot fetch.
export const sitesSignal = signal<Site[]>([]);

// siteSignal keeps the numeric site_id that apiGet reads. Writable
// directly (tests + legacy callers) and auto-synced from
// activeSiteSignal via the effect below.
export const siteSignal = signal<number>(1);

effect(() => {
  const active = activeSiteSignal.value;
  if (active && active.id !== siteSignal.value) {
    siteSignal.value = active.id;
  }
});

const STORAGE_KEY = 'statnive.activeSiteId';

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
