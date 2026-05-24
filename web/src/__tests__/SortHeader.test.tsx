import { describe, it, expect, beforeEach } from 'vitest';
import { fireEvent, render } from '@testing-library/preact';
import { DualSortHeader, SortHeader } from '../components/SortHeader';
import { EMPTY_FILTERS, filtersSignal, updateFilters } from '../state/filters';
import { hashSignal } from '../state/hash';

function reset() {
  window.history.replaceState(null, '', '/#pages');
  hashSignal.value = { panel: 'pages', params: new URLSearchParams() };
  filtersSignal.value = { ...EMPTY_FILTERS };
}

function wrap(ui: preact.ComponentChildren) {
  return (
    <table>
      <thead>
        <tr>{ui}</tr>
      </thead>
    </table>
  );
}

describe('SortHeader', () => {
  beforeEach(reset);

  it('renders a non-interactive <th> when column is empty', () => {
    const { container } = render(wrap(<SortHeader label="Day" column="" />));
    const th = container.querySelector('th');
    expect(th?.getAttribute('aria-sort')).toBeNull();
    expect(th?.getAttribute('data-sortable')).toBeNull();
    expect(th?.querySelector('button')).toBeNull();
  });

  it('starts with aria-sort=none and shows dim ↕ glyph when inactive', () => {
    const { container } = render(wrap(<SortHeader label="Views" column="views" />));
    const th = container.querySelector('th');
    expect(th?.getAttribute('aria-sort')).toBe('none');
    expect(th?.textContent).toMatch(/^Views/);
    expect(th?.textContent).toMatch(/↕/);
    expect(th?.textContent).not.toMatch(/↑|↓/);
    const glyph = th?.querySelector('.statnive-sort-glyph');
    expect(glyph?.className).not.toMatch(/is-active/);
  });

  it('cycles inactive → desc → asc → cleared on click', () => {
    const { container } = render(wrap(<SortHeader label="Views" column="views" />));
    const button = container.querySelector('button')!;

    fireEvent.click(button);
    expect(filtersSignal.value.sort).toBe('views');
    expect(filtersSignal.value.dir).toBe('desc');

    fireEvent.click(button);
    expect(filtersSignal.value.sort).toBe('views');
    expect(filtersSignal.value.dir).toBe('asc');

    fireEvent.click(button);
    expect(filtersSignal.value.sort).toBe('');
    expect(filtersSignal.value.dir).toBe('');
  });

  it('reflects active state when filters signal carries this column', () => {
    updateFilters({ sort: 'goals', dir: 'desc' });
    const { container } = render(wrap(<SortHeader label="Goals" column="goals" />));
    const th = container.querySelector('th');
    expect(th?.getAttribute('aria-sort')).toBe('descending');
    expect(th?.textContent).toMatch(/↓/);
    const glyph = th?.querySelector('.statnive-sort-glyph');
    expect(glyph?.className).toMatch(/is-active/);
  });

  it('keeps aria-sort=none + dim ↕ on inactive headers when another column is active', () => {
    updateFilters({ sort: 'goals', dir: 'desc' });
    const { container } = render(wrap(<SortHeader label="Views" column="views" />));
    const th = container.querySelector('th');
    expect(th?.getAttribute('aria-sort')).toBe('none');
    expect(th?.textContent).toMatch(/↕/);
    expect(th?.textContent).not.toMatch(/↑|↓/);
    const glyph = th?.querySelector('.statnive-sort-glyph');
    expect(glyph?.className).not.toMatch(/is-active/);
  });
});

describe('DualSortHeader', () => {
  beforeEach(reset);

  it('renders both Visitors and Revenue rows inside one button', () => {
    const { container } = render(wrap(<DualSortHeader />));
    const button = container.querySelector('button.statnive-th-dual');
    expect(button).not.toBeNull();
    expect(button?.querySelector('.visitors-row')?.textContent).toMatch(/Visitors/);
    expect(button?.querySelector('.revenue-row')?.textContent).toMatch(/Revenue/);
  });

  it('clicking emits sort=revenue dir=desc and pins the active arrow to the revenue row', () => {
    const { container } = render(wrap(<DualSortHeader />));
    const button = container.querySelector('button')!;

    fireEvent.click(button);

    expect(filtersSignal.value.sort).toBe('revenue');
    expect(filtersSignal.value.dir).toBe('desc');

    const refreshed = render(wrap(<DualSortHeader />));
    const revenueRow = refreshed.container.querySelector('.revenue-row');
    const visitorsRow = refreshed.container.querySelector('.visitors-row');
    expect(revenueRow?.textContent).toMatch(/↓/);
    expect(visitorsRow?.textContent).not.toMatch(/↑|↓|↕/);
    const glyph = revenueRow?.querySelector('.statnive-sort-glyph');
    expect(glyph?.className).toMatch(/is-active/);
  });

  it('inactive dual-bar header shows dim ↕ next to the REVENUE row only', () => {
    const { container } = render(wrap(<DualSortHeader />));
    const revenueRow = container.querySelector('.revenue-row');
    const visitorsRow = container.querySelector('.visitors-row');
    expect(revenueRow?.textContent).toMatch(/↕/);
    expect(visitorsRow?.textContent).not.toMatch(/↕|↑|↓/);
    const glyph = revenueRow?.querySelector('.statnive-sort-glyph');
    expect(glyph?.className).not.toMatch(/is-active/);
  });
});
