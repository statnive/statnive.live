import { signal, effect } from '@preact/signals';
import { hashSignal, replaceHashParams } from './hash';

// Filters is the union of every URL-sync'd filter the panels read. Each
// non-empty field serializes to a query param on the URL hash. The first
// six (device, channel, country, path, from, to) are the contract with
// the Go dashboard filter parser (filterFromRequest). `metrics` is a
// UI-only field — the backend never reads it, but the URL-hash sync
// pattern is the same so the operator can share a link that preserves
// the selected metric series on the Overview chart.
export interface Filters {
  device: string;     // mobile | desktop | tablet | '' (any)
  channel: string;    // Direct / Organic Search / Social Media / Email / Referral / AI / Paid / ''
  country: string;    // ISO country code or free-form; '' = any
  path: string;       // pathname "contains" filter; '' = any
  from: string;       // IRST YYYY-MM-DD
  to: string;         // IRST YYYY-MM-DD
  metrics: string;    // comma-separated MetricId list — Overview chart series; '' = default (visitors only)
}

export const EMPTY_FILTERS: Filters = {
  device: '',
  channel: '',
  country: '',
  path: '',
  from: '',
  to: '',
  metrics: '',
};

const KEYS: Array<keyof Filters> = [
  'device',
  'channel',
  'country',
  'path',
  'from',
  'to',
  'metrics',
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

export function clearFilters(): void {
  filtersSignal.value = { ...EMPTY_FILTERS };
  replaceHashParams(new URLSearchParams());
}

// Keep in sync with `data-kpi` attributes on the KPI cards and with
// buildMetricSpecs() in lib/chart.ts — these three places form the
// contract; adding a metric requires touching all of them.
export const ALL_METRICS = [
  'visitors',
  'conversion',
  'revenue',
  'rpv',
  'pageviews',
  'goals',
] as const;
export type MetricId = (typeof ALL_METRICS)[number];

export const DEFAULT_METRICS: readonly MetricId[] = ['visitors'];

const METRIC_SET: ReadonlySet<string> = new Set(ALL_METRICS);

// Unknown ids are dropped silently so a stale share-link with a metric
// that's since been removed degrades to the default.
export function selectedMetrics(f: Filters): MetricId[] {
  if (!f.metrics) return [...DEFAULT_METRICS];
  const ids = f.metrics.split(',').filter((m): m is MetricId => METRIC_SET.has(m));
  return ids.length > 0 ? ids : [...DEFAULT_METRICS];
}

// Min-1 invariant: removing the last active metric is a no-op so the
// chart never disappears.
export function toggleMetric(id: MetricId): void {
  const current = selectedMetrics(filtersSignal.value);
  const next = current.includes(id)
    ? current.filter((m) => m !== id)
    : [...current, id];
  if (next.length === 0) return;
  updateFilters({ metrics: next.join(',') });
}
