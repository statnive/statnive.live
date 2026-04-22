import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/preact';
import { SiteSwitcher } from '../components/SiteSwitcher';
import { activeSiteSignal, sitesSignal, siteSignal, loadPersistedSiteId } from '../state/site';

describe('SiteSwitcher', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    activeSiteSignal.value = null;
    sitesSignal.value = [];
    siteSignal.value = 1;
    try {
      window.sessionStorage.removeItem('statnive.activeSiteId');
    } catch {
      /* noop */
    }
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    cleanup();
  });

  function mockSites(sites: Array<{ id: number; hostname: string; enabled?: boolean; tz?: string }>) {
    const payload = {
      sites: sites.map((s) => ({
        id: s.id,
        hostname: s.hostname,
        enabled: s.enabled ?? true,
        tz: s.tz ?? 'Asia/Tehran',
      })),
    };
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }) as unknown as typeof globalThis.fetch;
  }

  it('renders a single site as a static label (no dropdown)', async () => {
    mockSites([{ id: 42, hostname: 'single.example.com' }]);
    render(<SiteSwitcher />);
    await waitFor(() => {
      expect(screen.getByTestId('site-single')).toBeTruthy();
    });
    expect(screen.getByText('single.example.com')).toBeTruthy();
    expect(activeSiteSignal.value?.id).toBe(42);
    expect(siteSignal.value).toBe(42);
  });

  it('renders a dropdown when multiple sites and defaults to first enabled', async () => {
    mockSites([
      { id: 1, hostname: 'a.example.com' },
      { id: 2, hostname: 'b.example.com' },
    ]);
    render(<SiteSwitcher />);
    await waitFor(() => {
      expect(screen.getByTestId('site-select')).toBeTruthy();
    });
    expect(activeSiteSignal.value?.id).toBe(1);
  });

  it('persists selection to sessionStorage on change', async () => {
    mockSites([
      { id: 1, hostname: 'a.example.com' },
      { id: 2, hostname: 'b.example.com' },
    ]);
    render(<SiteSwitcher />);
    await waitFor(() => screen.getByTestId('site-select'));

    const select = screen.getByTestId('site-select') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: '2' } });

    expect(activeSiteSignal.value?.id).toBe(2);
    expect(loadPersistedSiteId()).toBe(2);
  });
});
