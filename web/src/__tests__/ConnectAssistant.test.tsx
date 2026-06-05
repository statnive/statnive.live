import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/preact';

import ConnectAssistant from '../panels/ConnectAssistant';
import type { MCPToken } from '../api/mcp';

// Route fetch by (path, method) so the panel's mount (GET connection + GET
// tokens), mint (POST), and revoke (DELETE) each get the right reply. The
// shared request() helper reads res.text() then JSON.parses, so every reply
// provides text().
function routeFetch(opts: {
  connectionSites?: number[];
  enabled?: boolean;
  tokens?: MCPToken[];
  minted?: MCPToken;
  onPost?: (body: unknown) => void;
  onDelete?: (path: string) => void;
}) {
  const tokens = opts.tokens ?? [];

  globalThis.fetch = vi.fn(async (path: string, init?: RequestInit) => {
    const method = (init?.method ?? 'GET').toUpperCase();
    const reply = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      text: async () => JSON.stringify(body),
    });

    if (path === '/api/mcp/connection') {
      return reply(200, {
        enabled: opts.enabled ?? true,
        transport: 'http',
        url: 'https://app.statnive.live/mcp',
        role: 'admin',
        sites: opts.connectionSites ?? [1],
        add_command_template:
          'claude mcp add --transport http https://app.statnive.live/mcp --header "Authorization: Bearer <TOKEN>"',
      });
    }

    if (path === '/api/mcp/tokens' && method === 'GET') {
      return reply(200, { tokens });
    }

    if (path === '/api/mcp/tokens' && method === 'POST') {
      opts.onPost?.(JSON.parse(String(init?.body ?? '{}')));
      return reply(201, opts.minted);
    }

    if (path.startsWith('/api/mcp/tokens/') && method === 'DELETE') {
      opts.onDelete?.(path);
      return { ok: true, status: 204, text: async () => '' };
    }

    return reply(404, { error: 'unexpected ' + method + ' ' + path });
  }) as unknown as typeof globalThis.fetch;
}

describe('ConnectAssistant panel', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    // jsdom lacks navigator.clipboard — stub it so copy buttons resolve.
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    cleanup();
  });

  it('lists existing tokens (no raw secret column)', async () => {
    routeFetch({
      tokens: [
        { token_id: 'aaaa', name: 'laptop', site_ids: [1], role: 'api', created_at: 1000, expires_at: 0, last_used_at: 0 },
      ],
    });
    render(<ConnectAssistant />);

    await waitFor(() => screen.getByTestId('mcp-tokens-table'));
    expect(screen.getByText('laptop')).toBeTruthy();
    // The list must never carry a raw token value.
    expect(screen.queryByTestId('revealed-token')).toBeNull();
  });

  it('mints a token and shows it once with a ready-to-paste command', async () => {
    let posted: unknown = null;
    routeFetch({
      connectionSites: [1],
      minted: {
        token_id: 'newid', name: 'My laptop', site_ids: [1], role: 'api',
        created_at: 2000, expires_at: 0, last_used_at: 0, token: 'stnv_rawsecret123',
      },
      onPost: (b) => { posted = b; },
    });
    render(<ConnectAssistant />);

    // Wait for the mount fetch (connection + tokens) to resolve so the mint
    // form's default site selection is populated before we submit.
    await waitFor(() => screen.getByText(/No tokens yet/i));

    fireEvent.input(screen.getByPlaceholderText(/My laptop/i), { target: { value: 'My laptop' } });
    fireEvent.click(screen.getByText('Create token'));

    // Show-once reveal carries the raw token + the command with the token spliced in.
    await waitFor(() => screen.getByTestId('token-reveal'));
    expect(screen.getByTestId('revealed-token').textContent).toBe('stnv_rawsecret123');
    expect(screen.getByTestId('revealed-command').textContent).toContain('Bearer stnv_rawsecret123');

    // Single site ⇒ site_ids omitted (server defaults to the caller's full set).
    expect((posted as { name: string; site_ids?: number[] }).name).toBe('My laptop');
    expect((posted as { site_ids?: number[] }).site_ids).toBeUndefined();
  });

  it('revokes a token', async () => {
    let deleted = '';
    routeFetch({
      tokens: [
        { token_id: 'tok-1', name: 'old', site_ids: [1], role: 'api', created_at: 1000, expires_at: 0, last_used_at: 0 },
      ],
      onDelete: (p) => { deleted = p; },
    });
    render(<ConnectAssistant />);

    await waitFor(() => screen.getByText('Revoke'));
    fireEvent.click(screen.getByText('Revoke'));

    await waitFor(() => expect(deleted).toBe('/api/mcp/tokens/tok-1'));
  });

  it('warns when the MCP HTTP endpoint is disabled', async () => {
    routeFetch({ enabled: false });
    render(<ConnectAssistant />);

    await waitFor(() => screen.getByText(/isn.t enabled on this server/i));
  });

  it('shows a friendly not-enabled state when the feature flag is off (routes 404)', async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: false,
      status: 404,
      text: async () => JSON.stringify({ error: 'not found' }),
    })) as unknown as typeof globalThis.fetch;

    render(<ConnectAssistant />);

    await waitFor(() => screen.getByTestId('connect-not-enabled'));
    // The mint form must not render when the feature is off.
    expect(screen.queryByText('Create token')).toBeNull();
  });
});
