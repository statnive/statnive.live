import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/preact';
import Sources from '../panels/Sources';

describe('Sources panel', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
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

  it('renders one row per source with dual-bar values', async () => {
    mockRows([
      { referrer_name: 'google', channel: 'Organic Search', views: 500, visitors: 300, goals: 20, revenue: 1000, rpv: 3 },
      { referrer_name: '(direct)', channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue: 300, rpv: 2 },
    ]);

    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    expect(screen.getByText('google')).toBeTruthy();
    expect(screen.getByText('Organic Search')).toBeTruthy();
    expect(screen.getByText('Direct')).toBeTruthy();
  });

  it('renders fractional RPV with 2 decimal digits in the RPV column', async () => {
    // Backend returns RPV as a float64 (revenue/visitors). Pre-fix the
    // panel wrapped it in Math.round, so 0.5 € RPV rendered as "€1" or
    // 0.19 € as "€0". Pin the fractional shape.
    mockRows([
      { referrer_name: 'google', channel: 'Organic Search', views: 500, visitors: 300, goals: 20, revenue: 60, rpv: 0.2 },
      { referrer_name: '(direct)', channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue: 75, rpv: 0.5 },
    ]);

    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    const panel = screen.getByTestId('panel-sources');
    expect(panel.textContent).toContain('0.20');
    expect(panel.textContent).toContain('0.50');
  });

  it('renders empty-state message when API returns empty array', async () => {
    mockRows([]);
    render(<Sources />);
    await waitFor(() => {
      expect(screen.getByText(/No source data/)).toBeTruthy();
    });
  });

  it('renders error banner on fetch failure', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('boom')) as unknown as typeof globalThis.fetch;
    render(<Sources />);
    await waitFor(() => {
      expect(screen.getByText(/could not load/)).toBeTruthy();
    });
  });
});
