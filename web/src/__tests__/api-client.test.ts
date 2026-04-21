import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { siteSignal } from '../state/site';
import { authSignal } from '../state/auth';
import { apiGet } from '../api/client';

describe('apiGet', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    siteSignal.value = 7;
    authSignal.value = '';
    fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ok: true }),
    });
    originalFetch = globalThis.fetch;
    globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('attaches ?site= from siteSignal', async () => {
    await apiGet('/api/stats/overview');

    expect(fetchMock).toHaveBeenCalledOnce();
    const calledUrl = String(fetchMock.mock.calls[0][0]);
    expect(calledUrl).toContain('site=7');
  });

  it('attaches Authorization: Bearer when authSignal is set', async () => {
    authSignal.value = 'tok-abc';

    await apiGet('/api/stats/overview');

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.headers).toMatchObject({ Authorization: 'Bearer tok-abc' });
  });

  it('omits Authorization header when authSignal is empty (dev mode)', async () => {
    authSignal.value = '';

    await apiGet('/api/stats/overview');

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.headers).not.toHaveProperty('Authorization');
  });

  it('throws on non-2xx', async () => {
    fetchMock.mockResolvedValueOnce({ ok: false, status: 500, json: async () => ({}) });

    await expect(apiGet('/api/stats/overview')).rejects.toThrow(/HTTP 500/);
  });

  it('forwards extra params', async () => {
    await apiGet('/api/stats/overview', { from: '2026-04-14', to: '2026-04-21' });

    const calledUrl = String(fetchMock.mock.calls[0][0]);
    expect(calledUrl).toContain('from=2026-04-14');
    expect(calledUrl).toContain('to=2026-04-21');
  });
});
