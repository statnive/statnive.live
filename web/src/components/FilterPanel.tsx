import { filtersSignal, updateFilters, clearFilters, hasAnyPropFilter } from '../state/filters';
import { hashSignal } from '../state/hash';
import { PropFilterChip } from './PropFilterChip';
import { PropFilterAdd } from './PropFilterAdd';
import './FilterPanel.css';

// Channel values map to the channel column on the v1 rollups (daily_sources
// in v1; hourly_visitors + daily_pages joined the set in migration 015).
// They are populated by the 17-step channel mapper in
// internal/enrich/channel.go. Keep aligned with the canonical labels
// emitted by the pipeline — "Organic Search" (not "Organic") etc.
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
//
// SEO is channel-locked to "Organic Search" by definition (queries.go
// hardcodes the predicate). On that panel the chip row is replaced by
// an inline note so operators don't try to toggle a filter that has no
// effect.
export function FilterPanel() {
  const f = filtersSignal.value;
  const panel = hashSignal.value.panel;
  const hasProps = hasAnyPropFilter(f);
  const any = Boolean(f.channel || f.path || hasProps);
  const isSEO = panel === 'seo';

  return (
    <section class="statnive-filterpanel" aria-label="Filters">
      <div class="statnive-filter-row">
        <span class="statnive-filter-label">Channel</span>
        {isSEO ? (
          <span class="statnive-filter-note" data-testid="filter-seo-note">
            Showing Organic Search only
          </span>
        ) : (
          CHANNELS.map((c) => (
            <button
              key={c}
              type="button"
              class={'statnive-chip' + (f.channel === c ? ' is-active' : '')}
              aria-pressed={f.channel === c}
              onClick={() => updateFilters({ channel: f.channel === c ? '' : c })}
            >
              {c}
            </button>
          ))
        )}
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

      {/* Segments Phase 5 — property chip strip. Renders only when at
          least one prop filter is active OR for surfaces where the
          add affordance is always available. The strip lives in a
          dedicated row below the dimension chips so the two filter
          vocabularies don't compete for the same horizontal scan. */}
      <div class="statnive-filter-row seg-chip-row" aria-label="Property filters">
        {(['hitProps', 'sessionProps', 'userProps'] as const).flatMap((scope) =>
          Object.entries(f[scope]).map(([name, value]) => (
            <PropFilterChip key={`${scope}:${name}`} scope={scope} name={name} value={value} />
          ))
        )}
        <PropFilterAdd />
      </div>
    </section>
  );
}
