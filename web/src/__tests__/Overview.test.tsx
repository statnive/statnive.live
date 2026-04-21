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
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => body,
    }) as unknown as typeof globalThis.fetch;
  }

  it('renders the four primary-tier KPIs', async () => {
    mockResponse({
      pageviews: 1000,
      visitors: 500,
      goals: 25,
      revenue_rials: 1_000_000,
      rpv_rials: 2000,
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
      revenue_rials: 50_000,
      rpv_rials: 250,
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
      revenue_rials: 0,
      rpv_rials: 0,
    });

    render(<Overview />);

    await waitFor(() => {
      expect(screen.getByTestId('kpi-primary')).toBeTruthy();
    });

    expect(screen.getByTestId('kpi-primary').querySelector('[data-kpi="conversion"]')?.textContent).toContain('0.00%');
  });

  it('shows error message on fetch failure', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('boom')) as unknown as typeof globalThis.fetch;

    render(<Overview />);

    await waitFor(() => {
      expect(screen.queryByText(/could not load/)).toBeTruthy();
    });
  });
});
