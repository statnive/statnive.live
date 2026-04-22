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
      { referrer_name: 'google', channel: 'Organic Search', views: 500, visitors: 300, goals: 20, revenue_rials: 1000, rpv_rials: 3 },
      { referrer_name: '(direct)', channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue_rials: 300, rpv_rials: 2 },
    ]);

    render(<Sources />);

    await waitFor(() => {
      expect(screen.getByTestId('panel-sources')).toBeTruthy();
    });

    expect(screen.getByText('google')).toBeTruthy();
    expect(screen.getByText('Organic Search')).toBeTruthy();
    expect(screen.getByText('Direct')).toBeTruthy();
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
