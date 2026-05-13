import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/preact';
import { Overview } from '../panels/Overview';

describe('Overview panel', () => {
  let originalFetch: typeof globalThis.fetch;

  afterEach(cleanup);

  beforeEach(() => {
    originalFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  function mockResponse(body: Record<string, number>) {
    // Overview fetches /api/stats/overview AND /api/stats/trend (via TrendChart).
    // Mock both with the same per-call handler — overview returns the KPI body,
    // trend returns an empty array (TrendChart gracefully renders nothing).
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/api/stats/trend')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => [],
        });
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => body,
      });
    }) as unknown as typeof globalThis.fetch;
  }

  it('renders the four primary-tier KPIs', async () => {
    mockResponse({
      pageviews: 1000,
      visitors: 500,
      goals: 25,
      revenue: 1_000_000,
      rpv: 2000,
    });

    render(<Overview />);

    await waitFor(() => {
      expect(screen.getByTestId('kpi-primary')).toBeTruthy();
    });

    const primary = screen.getByTestId('kpi-primary');
    expect(primary.querySelector('[data-kpi="visitors"]')?.textContent).toContain('500');
    // Conversion% = goals/visitors = 25/500 = 5.00%
    expect(primary.querySelector('[data-kpi="conversion"]')?.textContent).toContain('5.00%');
    expect(primary.querySelector('[data-kpi="revenue"]')?.textContent).toContain('1,000,000');
    expect(primary.querySelector('[data-kpi="rpv"]')?.textContent).toContain('2,000');
  });

  it('renders pageviews + goals in the secondary tier (vanity-metric demotion)', async () => {
    mockResponse({
      pageviews: 9999,
      visitors: 200,
      goals: 10,
      revenue: 50_000,
      rpv: 250,
    });

    render(<Overview />);

    await waitFor(() => {
      expect(screen.getByTestId('kpi-secondary')).toBeTruthy();
    });

    const secondary = screen.getByTestId('kpi-secondary');
    expect(secondary.querySelector('[data-kpi="pageviews"]')?.textContent).toContain('9,999');
    expect(secondary.querySelector('[data-kpi="goals"]')?.textContent).toContain('10');
  });

  it('Conversion% handles zero visitors gracefully', async () => {
    mockResponse({
      pageviews: 0,
      visitors: 0,
      goals: 0,
      revenue: 0,
      rpv: 0,
    });

    render(<Overview />);

    await waitFor(() => {
      expect(screen.getByTestId('kpi-primary')).toBeTruthy();
    });

    expect(screen.getByTestId('kpi-primary').querySelector('[data-kpi="conversion"]')?.textContent).toContain('0.00%');
  });

  it('renders fractional RPV with 2 decimal digits (sub-€1 regression case)', async () => {
    // Real production case: site_id=4 (televika.com) had 231 € revenue
    // across ~1200 unique visitors → RPV ≈ 0.19 €. Pre-fix the panel
    // wrapped this in Math.round(...) → 0 → "€0". Pin the fractional
    // shape so the regression cannot re-emerge.
    mockResponse({
      pageviews: 18607,
      visitors: 1200,
      goals: 17,
      revenue: 231,
      rpv: 0.1925,
    });

    render(<Overview />);

    await waitFor(() => {
      expect(screen.getByTestId('kpi-primary')).toBeTruthy();
    });

    const rpvCell = screen.getByTestId('kpi-primary').querySelector('[data-kpi="rpv"]');
    expect(rpvCell?.textContent).toContain('0.19');
  });

  it('shows error message on fetch failure', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('boom')) as unknown as typeof globalThis.fetch;

    render(<Overview />);

    await waitFor(() => {
      expect(screen.queryByText(/could not load/)).toBeTruthy();
    });
  });
});
