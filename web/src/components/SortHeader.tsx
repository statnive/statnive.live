import { filtersSignal, updateFilters } from '../state/filters';

// SortHeader renders one <th scope="col"> in a .statnive-table thead.
// Click cycles inactive → desc → asc → cleared (back to backend default).
// Sort + dir live on the shared Filters URL hash so a shared link
// preserves the operator's chosen ordering.
//
// `column` must match the backend whitelist key for the panel's query
// (see internal/storage/queries.go {pages,sources,campaigns}Sortable).
// `column=""` renders a non-interactive header — the only place that's
// used in v1 is the SEO panel's non-day columns.
//
// The dual-bar header (Visitors / Revenue) uses <DualSortHeader> below
// — it shares the click handler but renders two stacked labels and pins
// the active-arrow glyph to the REVENUE row (the primary sort term in
// the compound revenue-then-visitors ORDER BY).

interface SortHeaderProps {
  label: string;
  column: string;
}

function ariaSortFor(active: boolean, dir: string): 'ascending' | 'descending' | 'none' {
  if (!active) return 'none';
  return dir === 'asc' ? 'ascending' : 'descending';
}

function cycleClick(column: string, active: boolean, dir: string): void {
  if (!active) {
    updateFilters({ sort: column, dir: 'desc' });
    return;
  }
  if (dir === 'desc') {
    updateFilters({ sort: column, dir: 'asc' });
    return;
  }
  updateFilters({ sort: '', dir: '' });
}

function arrowGlyph(active: boolean, dir: string): string {
  if (!active) return '↕';
  return dir === 'asc' ? '↑' : '↓';
}

function SortGlyph({ active, dir }: { active: boolean; dir: string }) {
  return (
    <span class={`statnive-sort-glyph${active ? ' is-active' : ''}`} aria-hidden="true">
      {arrowGlyph(active, dir)}
    </span>
  );
}

function srLabel(label: string, active: boolean, dir: string): string {
  if (!active) return `sort by ${label}`;
  return `sort by ${label}, currently ${dir === 'asc' ? 'ascending' : 'descending'}`;
}

export function SortHeader({ label, column }: SortHeaderProps) {
  if (!column) {
    return <th scope="col">{label}</th>;
  }

  const f = filtersSignal.value;
  const active = f.sort === column;
  const dir = active ? f.dir : '';

  return (
    <th
      scope="col"
      aria-sort={ariaSortFor(active, dir)}
      data-sortable="true"
    >
      <button
        type="button"
        class="statnive-th-btn"
        onClick={() => cycleClick(column, active, dir)}
      >
        {label}
        <SortGlyph active={active} dir={dir} />
        <span class="statnive-sr-only">{srLabel(label, active, dir)}</span>
      </button>
    </th>
  );
}

// DualSortHeader renders the Visitors / Revenue dual-bar column header.
// Two stacked mono labels (VISITORS navy, REVENUE teal) inside one
// <button> so the whole cell is a single hit target with a single focus
// ring. Sort key is fixed to `revenue` — the backend whitelist resolves
// that to compound `ORDER BY revenue ${dir}, visitors ${dir}`.

export function DualSortHeader() {
  const column = 'revenue';
  const f = filtersSignal.value;
  const active = f.sort === column;
  const dir = active ? f.dir : '';

  return (
    <th
      scope="col"
      aria-sort={ariaSortFor(active, dir)}
      data-sortable="true"
    >
      <button
        type="button"
        class="statnive-th-btn statnive-th-dual"
        onClick={() => cycleClick(column, active, dir)}
      >
        <span class="visitors-row">Visitors</span>
        <span class="revenue-row">
          Revenue
          <SortGlyph active={active} dir={dir} />
        </span>
        <span class="statnive-sr-only">{srLabel('revenue, then visitors', active, dir)}</span>
      </button>
    </th>
  );
}
