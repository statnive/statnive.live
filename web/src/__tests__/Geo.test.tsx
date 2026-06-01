import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  render,
  screen,
  waitFor,
  cleanup,
  fireEvent,
  within,
} from '@testing-library/preact';
import type { ChartProps } from '../components/Chart';

// Mock LazyChart so the donut option is captured without mounting a
// canvas in jsdom. Tests assert the metric-toggle re-renders by
// counting how many times LazyChart received a new option.
const lazyChartCalls: ChartProps[] = [];
vi.mock('../components/LazyChart', () => ({
  LazyChart: (props: ChartProps) => {
    lazyChartCalls.push(props);
    return null;
  },
}));

import Geo from '../panels/Geo';
import type { GeoResponse, GeoRow, GeoTopRow } from '../api/types';

describe('Geo panel', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    lazyChartCalls.length = 0;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    cleanup();
  });

  function mockResponse(body: GeoResponse, status = 200, ok = true) {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok,
      status,
      json: async () => body,
    }) as unknown as typeof globalThis.fetch;
  }

  function mockError(status: number) {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status,
      json: async () => ({}),
    }) as unknown as typeof globalThis.fetch;
  }

  const TOP: GeoTopRow[] = [
    { country_code: 'IR', views: 150, visitors: 120, goals: 0, revenue: 6_000_000, rpv: 50_000 },
    { country_code: 'US', views: 25, visitors: 20, goals: 0, revenue: 25_000_000, rpv: 1_250_000 },
    { country_code: 'DE', views: 40, visitors: 35, goals: 0, revenue: 7_000_000, rpv: 200_000 },
    { country_code: 'GB', views: 35, visitors: 30, goals: 0, revenue: 4_500_000, rpv: 150_000 },
  ];

  const ROWS: GeoRow[] = [
    { country_code: 'IR', province: 'Tehran', city: 'Tehran', views: 120, visitors: 90, goals: 0, revenue: 4_500_000, rpv: 50_000 },
    { country_code: 'IR', province: 'Esfahan', city: 'Esfahan', views: 30, visitors: 30, goals: 0, revenue: 1_500_000, rpv: 50_000 },
    { country_code: 'US', province: 'California', city: 'San Francisco', views: 25, visitors: 20, goals: 0, revenue: 25_000_000, rpv: 1_250_000 },
    { country_code: 'DE', province: 'Berlin', city: 'Berlin', views: 40, visitors: 35, goals: 0, revenue: 7_000_000, rpv: 200_000 },
  ];

  it('renders a coming-soon message when the API returns 501 (feature flag off)', async () => {
    mockError(501);
    render(<Geo />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-geo')).toBeTruthy();
    });

    // Coming-soon body specifically references the operator flag name
    // so the message is grep-able when surfaced in a support ticket.
    expect(screen.getByText(/dashboard\.geo_enabled/i)).toBeTruthy();
    // Pie + headline + table should NOT render in the disabled state.
    expect(screen.queryByTestId('geo-pie')).toBeNull();
    expect(screen.queryByTestId('geo-top-visitors')).toBeNull();
  });

  it('renders the dual ranked-list headline with rank divergence', async () => {
    mockResponse({ top: TOP, rows: ROWS });
    render(<Geo />);

    const visitorsList = await waitFor(() =>
      screen.getByTestId('geo-top-visitors'),
    );
    const revenueList = screen.getByTestId('geo-top-revenue');

    // Top by visitors: IR is #1 (150 visitors)
    expect(within(visitorsList).getByText(/Iran/)).toBeTruthy();
    // Top by revenue: US is #1 (25,000,000 revenue) — rank divergence
    // is the central UX motif; this assertion is the regression guard.
    expect(within(revenueList).getByText(/United States/)).toBeTruthy();

    // Headline lists must be visually distinct — both must render.
    expect(visitorsList).not.toBe(revenueList);
  });

  it('mounts a pie chart and toggles metric between visitors and revenue', async () => {
    mockResponse({ top: TOP, rows: ROWS });
    render(<Geo />);

    await waitFor(() => screen.getByTestId('geo-pie'));

    // First call to LazyChart is the visitors pie (default metric).
    expect(lazyChartCalls.length).toBeGreaterThanOrEqual(1);

    const callsBeforeToggle = lazyChartCalls.length;
    const revenueToggle = screen.getByTestId('geo-pie-metric-toggle');
    fireEvent.click(revenueToggle);

    // Metric switch must trigger a re-render with a new option.
    await waitFor(() => {
      expect(lazyChartCalls.length).toBeGreaterThan(callsBeforeToggle);
    });

    expect(revenueToggle.getAttribute('aria-pressed')).toBe('true');
  });

  it('renders one country-aggregate row per distinct country in the drill-down', async () => {
    mockResponse({ top: TOP, rows: ROWS });
    render(<Geo />);

    // panel-geo renders in loading state too — wait for an actual
    // populated-state testid before asserting row existence.
    await waitFor(() => screen.getByTestId('geo-row-IR'));

    // 3 distinct country codes in ROWS: IR, US, DE.
    expect(screen.getByTestId('geo-row-IR')).toBeTruthy();
    expect(screen.getByTestId('geo-row-US')).toBeTruthy();
    expect(screen.getByTestId('geo-row-DE')).toBeTruthy();
  });

  it('expands a country row to reveal its province/city children on click', async () => {
    mockResponse({ top: TOP, rows: ROWS });
    render(<Geo />);

    const irRow = await waitFor(() => screen.getByTestId('geo-row-IR'));

    // IR has two children (Tehran, Esfahan); collapsed by default.
    expect(screen.queryByText(/Tehran · Tehran/)).toBeNull();

    const button = within(irRow).getByRole('button');
    fireEvent.click(button);

    expect(button.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByText(/Tehran · Tehran/)).toBeTruthy();
    expect(screen.getByText(/Esfahan · Esfahan/)).toBeTruthy();

    // Toggle closes again.
    fireEvent.click(button);
    expect(button.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByText(/Tehran · Tehran/)).toBeNull();
  });

  it('renders an empty state when the API returns zero rows', async () => {
    mockResponse({ top: [], rows: [] });
    render(<Geo />);

    await waitFor(() => {
      expect(screen.getByText(/No geographic data/i)).toBeTruthy();
    });
  });

  it('renders an unknown country label for the "--" sentinel', async () => {
    const sentinelTop: GeoTopRow[] = [
      { country_code: '--', views: 5, visitors: 5, goals: 0, revenue: 0, rpv: 0 },
    ];
    const sentinelRows: GeoRow[] = [
      { country_code: '--', province: '', city: '', views: 5, visitors: 5, goals: 0, revenue: 0, rpv: 0 },
    ];
    mockResponse({ top: sentinelTop, rows: sentinelRows });
    render(<Geo />);

    // Wait for the headline ranked list to populate; until then panel-geo
    // is still in loading state.
    await waitFor(() => screen.getByTestId('geo-top-visitors'));
    expect(screen.getAllByText(/Unknown/).length).toBeGreaterThan(0);
  });
});
