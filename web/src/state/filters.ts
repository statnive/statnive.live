import { signal, effect } from '@preact/signals';
import { hashSignal, replaceHashParams } from './hash';

// Filters is the union of every URL-sync'd filter the panels read. Each
// non-empty field serializes to a query param on the URL hash. Keep
// keys in sync with the backend's filterFromRequest — the names here
// ARE the contract with the Go dashboard filter parser.
export interface Filters {
  device: string;     // mobile | desktop | tablet | '' (any)
  channel: string;    // Direct / Organic Search / Social Media / Email / Referral / AI / Paid / ''
  country: string;    // ISO country code or free-form; '' = any
  path: string;       // pathname "contains" filter; '' = any
  from: string;       // IRST YYYY-MM-DD
  to: string;         // IRST YYYY-MM-DD
}

export const EMPTY_FILTERS: Filters = {
  device: '',
  channel: '',
  country: '',
  path: '',
  from: '',
  to: '',
};

const KEYS: Array<keyof Filters> = [
  'device',
  'channel',
  'country',
  'path',
  'from',
  'to',
];

export function filtersToQuery(f: Filters): URLSearchParams {
  const p = new URLSearchParams();
  for (const k of KEYS) {
    const v = f[k];
    if (v) p.set(k, v);
  }
  return p;
}

export function queryToFilters(p: URLSearchParams): Filters {
  const out: Filters = { ...EMPTY_FILTERS };
  for (const k of KEYS) {
    const v = p.get(k);
    if (v) out[k] = v;
  }
  return out;
}

export const filtersSignal = signal<Filters>(
  queryToFilters(hashSignal.value.params),
);

// Keep filtersSignal and the URL hash's params in sync. Effect reads
// hashSignal.value whenever it changes; writes are pushed back via
// updateFilters below (effect guards against the round-trip loop by
// comparing serialized form).
effect(() => {
  const fromHash = queryToFilters(hashSignal.value.params);
  if (!filtersEqual(fromHash, filtersSignal.value)) {
    filtersSignal.value = fromHash;
  }
});

function filtersEqual(a: Filters, b: Filters): boolean {
  for (const k of KEYS) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}

// updateFilters merges next into the current filters signal and writes
// the URL hash. Uses replaceHashParams so chip toggles don't clutter
// the back-button history with one entry per chip click.
export function updateFilters(next: Partial<Filters>): void {
  const merged: Filters = { ...filtersSignal.value, ...next };
  filtersSignal.value = merged;
  replaceHashParams(filtersToQuery(merged));
}

// clearFilters resets all six keys to empty strings.
export function clearFilters(): void {
  filtersSignal.value = { ...EMPTY_FILTERS };
  replaceHashParams(new URLSearchParams());
}
