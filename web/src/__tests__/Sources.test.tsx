import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/preact';
import type { ChartProps } from '../components/Chart';

// Mock LazyChart BEFORE importing Sources. The mock captures the
// ECharts option payload (no canvas mount in jsdom) so we can assert
// pie radius, per-slice color, and aria wiring without rendering.
const lazyChartCalls: ChartProps[] = [];
vi.mock('../components/LazyChart', () => ({
  LazyChart: (props: ChartProps) => {
    lazyChartCalls.push(props);
    return null;
  },
}));

import Sources from '../panels/Sources';
import type { SourceRow, SourceChannelRow } from '../api/types';

describe('Sources panel', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    lazyChartCalls.length = 0;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    cleanup();
  });

  function mockResponse(rows: SourceRow[], byChannel: SourceChannelRow[]) {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ rows, by_channel: byChannel }),
    }) as unknown as typeof globalThis.fetch;
  }

  const SAMPLE_ROWS: SourceRow[] = [
    { referrer_name: 'google', channel: 'Organic Search', views: 500, visitors: 300, goals: 20, revenue: 1000, rpv: 3 },
    { referrer_name: 'bing', channel: 'Organic Search', views: 100, visitors: 80, goals: 2, revenue: 200, rpv: 2.5 },
    { referrer_name: '(direct)', channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue: 300, rpv: 2 },
  ];

  const SAMPLE_BY_CHANNEL: SourceChannelRow[] = [
    { channel: 'Organic Search', views: 600, visitors: 350, goals: 22, revenue: 1200, rpv: 3.43 },
    { channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue: 300, rpv: 2 },
  ];

  it('renders the channel chart container and one row per channel header', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    expect(screen.getByTestId('sources-by-channel-chart')).toBeTruthy();

    // data-channel is the regression target — fixes the chip-color bug
    // where panels.css per-channel rules never fired in production.
    const headers = document.querySelectorAll('tr[data-channel]');
    expect(headers.length).toBe(SAMPLE_BY_CHANNEL.length);
    const channelsRendered = Array.from(headers).map((row) =>
      row.getAttribute('data-channel'),
    );
    expect(channelsRendered).toContain('Organic Search');
    expect(channelsRendered).toContain('Direct');
  });

  it('starts with every channel collapsed (referrer rows hidden)', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    const headers = document.querySelectorAll<HTMLTableRowElement>('tr[data-channel]');
    expect(headers.length).toBe(2);
    headers.forEach((row) => {
      expect(row.getAttribute('aria-expanded')).toBe('false');
    });

    document.querySelectorAll<HTMLTableRowElement>('.statnive-channel-detail').forEach((row) => {
      expect(row.hasAttribute('hidden')).toBe(true);
    });
  });

  it('toggles aria-expanded and reveals referrer rows on header click', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    const header = document.querySelector<HTMLTableRowElement>(
      "tr[data-channel='Organic Search']",
    );
    expect(header).toBeTruthy();
    fireEvent.click(header!);

    expect(header!.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByText('google')).toBeTruthy();
    expect(screen.getByText('bing')).toBeTruthy();
  });

  it('every channel chip carries the data-channel attribute (color tokens fire)', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    const chips = document.querySelectorAll<HTMLSpanElement>('.statnive-channel-chip');
    expect(chips.length).toBeGreaterThan(0);
    chips.forEach((chip) => {
      expect(chip.getAttribute('data-channel')).toBeTruthy();
    });
  });

  it('renders empty-state message when API returns empty arrays', async () => {
    mockResponse([], []);
    render(<Sources />);
    await waitFor(() => {
      expect(screen.getByText(/No source data/)).toBeTruthy();
    });
  });

  it('renders chart empty state when by_channel is empty but rows exist', async () => {
    mockResponse(SAMPLE_ROWS, []);
    render(<Sources />);
    await waitFor(() => {
      expect(screen.getByTestId('sources-by-channel-empty')).toBeTruthy();
    });
  });

  it('renders error banner on fetch failure', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('boom')) as unknown as typeof globalThis.fetch;
    render(<Sources />);
    await waitFor(() => {
      expect(screen.getByText(/could not load/)).toBeTruthy();
    });
  });

  it('both donuts use the shared PIE_RADIUS (donut, generous center hole)', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThanOrEqual(2);
    });

    // First two LazyChart calls are the views + revenue pies.
    for (const call of lazyChartCalls.slice(0, 2)) {
      const opt = call.option as { series: { radius: [string, string]; type: string }[] };
      expect(opt.series[0].type).toBe('pie');
      expect(opt.series[0].radius).toEqual(['55%', '85%']);
    }
  });

  it('renders eyebrow totals above each pie', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    const viewsPie = screen.getByTestId('views-pie');
    const revenuePie = screen.getByTestId('revenue-pie');
    // Total views = 600 + 200 = 800
    expect(viewsPie.textContent).toContain('VIEWS');
    expect(viewsPie.textContent).toContain('800');
    // Total revenue = 1200 + 300 = 1500, EUR formatting
    expect(revenuePie.textContent).toContain('REVENUE');
    expect(revenuePie.textContent).toContain('1,500');
  });

  it('every pie slice carries the channel hue via itemStyle.color', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThanOrEqual(2);
    });

    for (const call of lazyChartCalls.slice(0, 2)) {
      const opt = call.option as {
        series: { data: { name: string; itemStyle: { color: string } }[] }[];
      };
      for (const slice of opt.series[0].data) {
        expect(slice.itemStyle.color).toBeTruthy();
      }
    }
  });

  it('every pie slice carries a darkened emphasis.itemStyle.color so hover never fades to white', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThanOrEqual(2);
    });

    for (const call of lazyChartCalls.slice(0, 2)) {
      const opt = call.option as {
        series: {
          data: {
            itemStyle: { color: string };
            emphasis: { itemStyle: { color: string } };
          }[];
        }[];
      };
      for (const slice of opt.series[0].data) {
        expect(slice.emphasis.itemStyle.color).toBeTruthy();
        expect(slice.emphasis.itemStyle.color).not.toBe(slice.itemStyle.color);
      }
    }
  });

  it('summary header carries the dynamic metric label (VIEWS / REVENUE), not the static SHARE', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    const viewsHead = screen.getByTestId('views-pie').querySelector('.statnive-pie-summary-head');
    const revenueHead = screen.getByTestId('revenue-pie').querySelector('.statnive-pie-summary-head');
    expect(viewsHead?.textContent).toContain('TOP CHANNELS');
    expect(viewsHead?.textContent).toContain('VIEWS');
    expect(viewsHead?.textContent).not.toContain('SHARE');
    expect(revenueHead?.textContent).toContain('REVENUE');
    expect(revenueHead?.textContent).not.toContain('SHARE');
  });

  it('summary rows render percentage followed by the raw value in parentheses', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    // Total views = 800; Organic Search = 600 → 75% (600). Direct = 200 → 25% (200).
    const viewsRows = screen
      .getByTestId('views-pie')
      .querySelectorAll<HTMLElement>('.statnive-pie-summary-pct');
    expect(viewsRows.length).toBeGreaterThan(0);
    const firstViewsRow = viewsRows[0].textContent || '';
    expect(firstViewsRow).toMatch(/%/);
    expect(firstViewsRow).toMatch(/\(.+\)/);
  });

  it('DualBar is untouched (regression guard against accidental migration)', async () => {
    mockResponse(SAMPLE_ROWS, SAMPLE_BY_CHANNEL);
    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    // Expand the Organic Search channel to reveal detail rows with DualBar
    const header = document.querySelector<HTMLTableRowElement>(
      "tr[data-channel='Organic Search']",
    );
    expect(header).toBeTruthy();
    fireEvent.click(header!);

    // DualBar CSS classes must still exist in the DOM
    const dualbars = document.querySelectorAll('.statnive-dualbar');
    expect(dualbars.length).toBeGreaterThan(0);
    const fills = document.querySelectorAll('.statnive-dualbar-fill');
    expect(fills.length).toBeGreaterThan(0);
  });
});
