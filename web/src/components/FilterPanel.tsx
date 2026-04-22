import { filtersSignal, updateFilters, clearFilters } from '../state/filters';
import './FilterPanel.css';

// Hoisted outside component per `rendering-hoist-jsx`. Chip sets driven
// by arrays so adding a new option = one line added to the array.
const DEVICES: ReadonlyArray<string> = ['mobile', 'desktop', 'tablet'];

const CHANNELS: ReadonlyArray<string> = [
  'Direct',
  'Organic Search',
  'Social Media',
  'Email',
  'Referral',
  'AI',
  'Paid',
];

export function FilterPanel() {
  const f = filtersSignal.value;
  const any = Boolean(f.device || f.channel || f.country || f.path);

  return (
    <section class="statnive-filterpanel" aria-label="Filters">
      <div class="statnive-filter-row">
        <span class="statnive-filter-label">Device</span>
        {DEVICES.map((d) => (
          <button
            key={d}
            type="button"
            class={'statnive-chip' + (f.device === d ? ' is-active' : '')}
            aria-pressed={f.device === d}
            onClick={() => updateFilters({ device: f.device === d ? '' : d })}
          >
            {d}
          </button>
        ))}
      </div>

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
        <label class="statnive-filter-label" htmlFor="flt-country">Country</label>
        <input
          id="flt-country"
          type="text"
          placeholder="IR"
          value={f.country}
          onChange={(e) => updateFilters({ country: (e.target as HTMLInputElement).value.trim() })}
        />

        <label class="statnive-filter-label" htmlFor="flt-path">Path contains</label>
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
