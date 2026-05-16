import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { FilterPanel } from '../components/FilterPanel';
import { filtersSignal, EMPTY_FILTERS } from '../state/filters';
import { hashSignal } from '../state/hash';
import type { PanelName } from '../state/hash';

// FilterPanel reads the active panel from hashSignal so it can hide the
// Channel chip row on SEO (where the SQL hardcodes channel='Organic
// Search'). These tests pin that contract — without it the dashboard
// shows chips that silently do nothing.
beforeEach(() => {
  filtersSignal.value = { ...EMPTY_FILTERS };
  hashSignal.value = { panel: 'sources' as PanelName, params: new URLSearchParams() };
});

afterEach(() => {
  cleanup();
});

describe('FilterPanel — channel chips', () => {
  it('renders all 7 canonical chips on Sources', () => {
    const { getByText } = render(<FilterPanel />);

    for (const label of ['Direct', 'Organic Search', 'Social Media', 'Email', 'Referral', 'AI', 'Paid']) {
      expect(getByText(label)).toBeTruthy();
    }
  });

  it('renders the chip row on every non-SEO panel', () => {
    for (const panel of ['overview', 'sources', 'pages', 'campaigns', 'realtime'] as const) {
      cleanup();
      hashSignal.value = { panel, params: new URLSearchParams() };
      const { getByText, queryByTestId } = render(<FilterPanel />);
      expect(getByText('Direct')).toBeTruthy();
      expect(queryByTestId('filter-seo-note')).toBeNull();
    }
  });

  it('hides the chip row on SEO and shows the inline note instead', () => {
    hashSignal.value = { panel: 'seo' as PanelName, params: new URLSearchParams() };
    const { queryByText, getByTestId } = render(<FilterPanel />);

    expect(queryByText('Direct')).toBeNull();
    expect(queryByText('Organic Search')).toBeNull();
    expect(getByTestId('filter-seo-note').textContent).toMatch(/Organic Search/);
  });

  it('toggles a chip writes filtersSignal.channel', () => {
    const { getByText } = render(<FilterPanel />);

    fireEvent.click(getByText('Direct'));
    expect(filtersSignal.value.channel).toBe('Direct');

    fireEvent.click(getByText('Direct'));
    expect(filtersSignal.value.channel).toBe('');
  });

  it('keeps Clear all visible when channel is set even on a panel without chips', () => {
    // hashSignal carries the filters in its params — filters.ts has an
    // effect that resyncs filtersSignal from the URL hash whenever it
    // changes, so the test seeds the channel through the URL to avoid
    // racing that effect.
    const params = new URLSearchParams();
    params.set('channel', 'Direct');
    hashSignal.value = { panel: 'seo' as PanelName, params };

    const { getByText } = render(<FilterPanel />);

    // Clear all is rendered when any filter is set, regardless of panel.
    expect(getByText('Clear all')).toBeTruthy();
  });
});
