import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, waitFor, cleanup } from '@testing-library/preact';
import type { ChartProps } from '../components/Chart';

// Mock LazyChart BEFORE importing TrendChart so the test asserts the
// ECharts option payload, not the canvas render (which needs canvas +
// matchMedia + ResizeObserver, none of which jsdom provides). The mock
// collects every props payload each render so the test can inspect
// the series shape + refetch count.
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

interface CapturedSeries {
  name?: string;
  data?: unknown[];
  areaStyle?: unknown;
  yAxisIndex?: number;
}

function capturedSeries(call: ChartProps | undefined): CapturedSeries[] {
  if (!call) return [];
  const opt = call.option as { series?: CapturedSeries[] };
  return opt.series ?? [];
}

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

  it('renders a single-series chart with the visitors area-fill wash when only visitors is selected', async () => {
    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const series = capturedSeries(lazyChartCalls[lazyChartCalls.length - 1]);
    expect(series).toHaveLength(1);
    expect(series[0].name).toBe('Visitors');
    expect(series[0].areaStyle).toBeDefined();
  });

  it('renders multi-series when metrics filter has visitors + revenue', async () => {
    updateFilters({ metrics: 'visitors,revenue' });

    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const series = capturedSeries(lazyChartCalls[lazyChartCalls.length - 1]);
    expect(series).toHaveLength(2);
    expect(series[0].name).toBe('Visitors');
    expect(series[1].name).toBe('Revenue');
    // Visitors fill wash is dropped when a second metric is on (DESIGN.md
    // ≤10% Persian Teal budget).
    expect(series[0].areaStyle).toBeUndefined();
  });

  it('each metric series carries its own yAxisIndex (independent scales)', async () => {
    updateFilters({ metrics: 'visitors,revenue,rpv' });
    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const series = capturedSeries(lazyChartCalls[lazyChartCalls.length - 1]);
    expect(series).toHaveLength(3);
    expect(series[0].yAxisIndex).toBe(0);
    expect(series[1].yAxisIndex).toBe(1);
    expect(series[2].yAxisIndex).toBe(2);
  });

  it('derives conversion = goals/visitors per day client-side', async () => {
    updateFilters({ metrics: 'conversion' });

    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const series = capturedSeries(lazyChartCalls[lazyChartCalls.length - 1]);
    const data = series[0].data as Array<[string, number]>;
    // Fixture: day 0 = 5/100*100 = 5.0; day 1 = 6/120*100 = 5.0
    expect(data[0][1]).toBeCloseTo(5, 5);
    expect(data[1][1]).toBeCloseTo(5, 5);
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
      const series = capturedSeries(lazyChartCalls[lazyChartCalls.length - 1]);
      expect(series).toHaveLength(2);
    });
  });

  it('exposes aria.show for AriaComponent', async () => {
    render(<TrendChart />);

    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(0);
    });

    const last = lazyChartCalls[lazyChartCalls.length - 1];
    const opt = last.option as { aria?: { show?: boolean } };
    expect(opt.aria?.show).toBe(true);
  });
});
