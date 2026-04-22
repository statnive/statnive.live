import { filtersSignal, updateFilters, clearFilters } from '../state/filters';
import './FilterPanel.css';

// Channel values map to daily_sources.channel (LowCardinality column)
// populated by the 17-step channel mapper in internal/enrich/channel.go.
// Keep aligned with the canonical labels emitted by the pipeline —
// "Organic Search" (not "Organic") etc.
const CHANNELS: ReadonlyArray<string> = [
  'Direct',
  'Organic Search',
  'Social Media',
  'Email',
  'Referral',
  'AI',
  'Paid',
];

// Device + Country + Browser/OS chips are deliberately omitted in v1 —
// the `hourly_visitors`, `daily_pages`, `daily_sources` rollups don't
// carry those columns, so the filter values would serialize into the
// URL but have no effect at SQL time. They ship in v1.1 alongside
// `daily_devices` + `daily_geo` rollups (PLAN.md Phase 5b Out of scope).
// The underlying filtersSignal fields stay in place so deep-link URLs
// from future panels don't 400 — only the UI chips are suppressed.
export function FilterPanel() {
  const f = filtersSignal.value;
  const any = Boolean(f.channel || f.path);

  return (
    <section class="statnive-filterpanel" aria-label="Filters">
      <div class="statnive-filter-row">
        <span class="statnive-filter-label">Channel</span>
        {CHANNELS.map((c) => (
          <button
            key={c}
            type="button"
            class={'statnive-chip' + (f.channel === c ? ' is-active' : '')}
            aria-pressed={f.channel === c}
            onClick={() => updateFilters({ channel: f.channel === c ? '' : c })}
          >
            {c}
          </button>
        ))}
      </div>

      <div class="statnive-filter-row">
        <label class="statnive-filter-label" htmlFor="flt-path">Path</label>
        <input
          id="flt-path"
          type="text"
          placeholder="/blog"
          value={f.path}
          onChange={(e) => updateFilters({ path: (e.target as HTMLInputElement).value.trim() })}
        />

        {any ? (
          <button type="button" class="statnive-chip" onClick={clearFilters}>
            Clear all
          </button>
        ) : null}
      </div>
    </section>
  );
}
