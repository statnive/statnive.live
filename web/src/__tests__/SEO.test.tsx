import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/preact';
import type { ChartProps } from '../components/Chart';

const lazyChartCalls: ChartProps[] = [];
vi.mock('../components/LazyChart', () => ({
  LazyChart: (props: ChartProps) => {
    lazyChartCalls.push(props);
    return null;
  },
}));

import SEO from '../panels/SEO';

describe('SEO panel chart', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    lazyChartCalls.length = 0;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    cleanup();
  });

  function mockRows(rows: unknown[]) {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => rows,
    }) as unknown as typeof globalThis.fetch;
  }

  it('renders an ECharts line chart with one series', async () => {
    mockRows([
      { day: '2026-05-20', views: 200, visitors: 150, goals: 5, revenue: 100 },
      { day: '2026-05-21', views: 220, visitors: 170, goals: 6, revenue: 120 },
    ]);
    render(<SEO />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const last = lazyChartCalls[lazyChartCalls.length - 1];
    const opt = last.option as { series: { type: string; data: unknown[] }[] };
    expect(opt.series).toHaveLength(1);
    expect(opt.series[0].type).toBe('line');
    expect(opt.series[0].data).toHaveLength(2);
  });

  it('renders empty-state message when API returns []', async () => {
    mockRows([]);
    render(<SEO />);
    await waitFor(() => {
      expect(screen.getByText(/No organic-search data/)).toBeTruthy();
    });
    // No chart mount with zero rows.
    expect(lazyChartCalls.length).toBe(0);
  });

  it('renders error banner on fetch failure (no chart mount)', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('boom')) as unknown as typeof globalThis.fetch;
    render(<SEO />);
    await waitFor(() => {
      expect(screen.getByText(/could not load/)).toBeTruthy();
    });
    expect(lazyChartCalls.length).toBe(0);
  });

  it('exposes aria.show for AriaComponent', async () => {
    mockRows([
      { day: '2026-05-20', views: 200, visitors: 150, goals: 5, revenue: 100 },
    ]);
    render(<SEO />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const last = lazyChartCalls[lazyChartCalls.length - 1];
    const opt = last.option as { aria: { show: boolean } };
    expect(opt.aria.show).toBe(true);
  });
});
