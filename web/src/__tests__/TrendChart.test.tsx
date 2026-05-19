import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, waitFor, cleanup } from '@testing-library/preact';
import type { ChartProps } from '../components/Chart';

// Mock LazyChart BEFORE importing TrendChart so the test asserts the
// props uPlot would receive, not uPlot's render (which needs canvas +
// matchMedia, neither in jsdom). The mock collects every props payload
// each render so the test can inspect series shape + refetch count.
const lazyChartCalls: ChartProps[] = [];
vi.mock('../components/LazyChart', () => ({
  LazyChart: (props: ChartProps) => {
    lazyChartCalls.push(props);
    return null;
  },
}));

import { TrendChart } from '../panels/TrendChart';
import { updateFilters } from '../state/filters';
import { siteSignal } from '../state/site';
import { replaceHashParams } from '../state/hash';

describe('TrendChart', () => {
  let originalFetch: typeof globalThis.fetch;
  let fetchCalls: string[];

  beforeEach(() => {
    lazyChartCalls.length = 0;
    fetchCalls = [];
    originalFetch = globalThis.fetch;
    replaceHashParams(new URLSearchParams());
    siteSignal.value = 1;

    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      fetchCalls.push(url);
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => [
          { day: '2026-05-10', visitors: 100, pageviews: 200, goals: 5, revenue: 1000 },
          { day: '2026-05-11', visitors: 120, pageviews: 240, goals: 6, revenue: 1500 },
        ],
      });
    }) as unknown as typeof globalThis.fetch;
  });

  afterEach(() => {
    cleanup();
    globalThis.fetch = originalFetch;
  });

  it('renders a single-series chart when only visitors is selected (default)', async () => {
    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const last = lazyChartCalls[lazyChartCalls.length - 1];
    // AlignedData layout: [xs, ys_for_visitors] = 2 entries.
    expect(last?.data.length).toBe(2);
    // uPlot series: index 0 is the x-axis placeholder, then one per metric.
    expect(last?.options.series?.length).toBe(2);
    expect(last?.options.series?.[1]?.label).toBe('Visitors');
  });

  it('renders multi-series when metrics filter has visitors + revenue', async () => {
    updateFilters({ metrics: 'visitors,revenue' });

    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const last = lazyChartCalls[lazyChartCalls.length - 1];
    // [xs, ys_visitors, ys_revenue] = 3 entries.
    expect(last?.data.length).toBe(3);
    // x-axis + 2 metric series = 3 series.
    expect(last?.options.series?.length).toBe(3);
    expect(last?.options.series?.[1]?.label).toBe('Visitors');
    expect(last?.options.series?.[2]?.label).toBe('Revenue');
    // Visitors fill wash is dropped when a second metric is on (DESIGN.md
    // ≤10% Persian Teal budget).
    expect(last?.options.series?.[1]?.fill).toBeFalsy();
  });

  it('derives conversion = goals/visitors per day client-side', async () => {
    updateFilters({ metrics: 'conversion' });

    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const last = lazyChartCalls[lazyChartCalls.length - 1];
    // Fixture: day 0 = 5/100*100 = 5.0; day 1 = 6/120*100 = 5.0
    const ys = last?.data[1] as readonly number[] | undefined;
    expect(ys?.[0]).toBeCloseTo(5, 5);
    expect(ys?.[1]).toBeCloseTo(5, 5);
  });

  it('refetches /api/stats/trend when siteSignal changes (regression guard)', async () => {
    render(<TrendChart />);

    await waitFor(() => {
      expect(fetchCalls.length).toBeGreaterThanOrEqual(1);
    });

    const initial = fetchCalls.length;
    siteSignal.value = 2;

    await waitFor(() => {
      expect(fetchCalls.length).toBeGreaterThan(initial);
    });
  });

  it('refetches when a filter (channel) changes', async () => {
    render(<TrendChart />);

    await waitFor(() => {
      expect(fetchCalls.length).toBeGreaterThanOrEqual(1);
    });

    const initial = fetchCalls.length;
    updateFilters({ channel: 'Organic Search' });

    await waitFor(() => {
      expect(fetchCalls.length).toBeGreaterThan(initial);
    });
  });

  it('does NOT refetch when only the metrics selection changes', async () => {
    render(<TrendChart />);

    await waitFor(() => {
      expect(fetchCalls.length).toBeGreaterThanOrEqual(1);
    });

    const initial = fetchCalls.length;
    updateFilters({ metrics: 'visitors,revenue' });

    // Give the re-render a tick to settle; assert no additional fetch.
    await new Promise((r) => setTimeout(r, 20));
    expect(fetchCalls.length).toBe(initial);

    // But the chart did re-render with multi-series props.
    await waitFor(() => {
      const last = lazyChartCalls[lazyChartCalls.length - 1];
      expect(last?.options.series?.length).toBe(3);
    });
  });
});
