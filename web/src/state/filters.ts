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
  sort: string;       // panel-specific sort key (backend whitelist per query); '' = backend default
  dir: string;        // 'asc' | 'desc' | '' (only meaningful when sort is set)
  // Segments Phase 3 — custom-dimension filters at hit / session / user scope.
  // Wire format on the URL: repeated ?hit_prop=name:value (one entry per filter).
  // Server's parseScopedProps splits on the first ':'. Stored client-side as
  // a flat name → value map; ChipStrip renders one chip per entry.
  hitProps: Record<string, string>;
  sessionProps: Record<string, string>;
  userProps: Record<string, string>;
}

export const EMPTY_FILTERS: Filters = {
  device: '',
  channel: '',
  country: '',
  path: '',
  from: '',
  to: '',
  metrics: '',
  sort: '',
  dir: '',
  hitProps: {},
  sessionProps: {},
  userProps: {},
};

// Scalar filter keys only (string-valued); prop maps round-trip via
// the dedicated PROP_PARAM_NAMES helper below.
const KEYS: Array<Exclude<keyof Filters, 'hitProps' | 'sessionProps' | 'userProps'>> = [
  'device',
  'channel',
  'country',
  'path',
  'from',
  'to',
  'metrics',
  'sort',
  'dir',
];

const PROP_PARAM_NAMES = {
  hitProps: 'hit_prop',
  sessionProps: 'session_prop',
  userProps: 'user_prop',
} as const;

export function filtersToQuery(f: Filters): URLSearchParams {
  const p = new URLSearchParams();
  for (const k of KEYS) {
    const v = f[k];
    if (v) p.set(k, v);
  }
  // Props serialize as repeated <scope>_prop=name:value pairs so the
  // Go-side parseScopedProps splits on the first ':' to recover the
  // name and value cleanly.
  for (const scope of ['hitProps', 'sessionProps', 'userProps'] as const) {
    const m = f[scope];
    const param = PROP_PARAM_NAMES[scope];
    for (const name of Object.keys(m)) {
      const value = m[name];
      if (name && value !== undefined) {
        p.append(param, `${name}:${value}`);
      }
    }
  }
  return p;
}

export function queryToFilters(p: URLSearchParams): Filters {
  const out: Filters = {
    ...EMPTY_FILTERS,
    hitProps: {},
    sessionProps: {},
    userProps: {},
  };
  for (const k of KEYS) {
    const v = p.get(k);
    if (v) out[k] = v;
  }
  // dir is meaningless without sort — drop a stray ?dir= so a shared
  // URL never carries direction without the column it applies to.
  if (!out.sort) out.dir = '';
  // Decode the three prop-scope params; each value is "name:value".
  for (const scope of ['hitProps', 'sessionProps', 'userProps'] as const) {
    const param = PROP_PARAM_NAMES[scope];
    for (const entry of p.getAll(param)) {
      const idx = entry.indexOf(':');
      if (idx <= 0) continue;
      const name = entry.slice(0, idx);
      const value = entry.slice(idx + 1);
      out[scope][name] = value;
    }
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
  for (const scope of ['hitProps', 'sessionProps', 'userProps'] as const) {
    if (!mapsEqual(a[scope], b[scope])) return false;
  }
  return true;
}

function mapsEqual(a: Record<string, string>, b: Record<string, string>): boolean {
  const ak = Object.keys(a);
  const bk = Object.keys(b);
  if (ak.length !== bk.length) return false;
  for (const k of ak) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}

// setPropFilter / removePropFilter are the chip-strip add/remove
// helpers. They merge one key into the chosen scope's map and write
// the URL hash via updateFilters. Removing the last entry leaves an
// empty map (the URL hash drops the param naturally).
export function setPropFilter(scope: 'hitProps' | 'sessionProps' | 'userProps', name: string, value: string): void {
  if (!name) return;
  const current = filtersSignal.value;
  updateFilters({ [scope]: { ...current[scope], [name]: value } } as Partial<Filters>);
}

export function removePropFilter(scope: 'hitProps' | 'sessionProps' | 'userProps', name: string): void {
  const current = filtersSignal.value;
  const next = { ...current[scope] };
  delete next[name];
  updateFilters({ [scope]: next } as Partial<Filters>);
}

// hasAnyPropFilter mirrors Filter.HasPropFilter on the server — the UI
// flips into the "raw-fallback active" advisory state when this is true.
export function hasAnyPropFilter(f: Filters): boolean {
  return (
    Object.keys(f.hitProps).length > 0 ||
    Object.keys(f.sessionProps).length > 0 ||
    Object.keys(f.userProps).length > 0
  );
}

// dirOf coerces Filters.dir to the asc|desc union used by client-side
// sort comparators. Any value other than 'asc' (including '') maps to
// 'desc' — matches the "no sort set → backend default desc" UI contract.
export function dirOf(f: Filters): 'asc' | 'desc' {
  return f.dir === 'asc' ? 'asc' : 'desc';
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
