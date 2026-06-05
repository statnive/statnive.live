import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/preact';

import { UsersTab } from '../panels/admin/UsersTab';
import { activeSiteSignal } from '../state/site';

// Route /api/admin/users by (path, method). GET lists one active user; DELETE
// returns whatever the test configures (204 success, or a guard error).
function routeFetch(opts: {
  onDelete?: (path: string) => void;
  deleteStatus?: number;
  deleteBody?: string;
}) {
  let listCalls = 0;

  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = (init?.method ?? 'GET').toUpperCase();
    const reply = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      text: async () => JSON.stringify(body),
    });

    if (path.startsWith('/api/admin/users?site_id=') && method === 'GET') {
      listCalls += 1;
      return reply(200, {
        users: [
          {
            user_id: 'u-1',
            email: 'alice@acme.co',
            username: 'alice',
            role: 'viewer',
            site_id: 1,
            disabled: false,
            sites: [{ site_id: 1, hostname: 'acme.co', role: 'viewer' }],
          },
        ],
      });
    }

    if (path === '/api/admin/users/u-1' && method === 'DELETE') {
      opts.onDelete?.(path);
      const status = opts.deleteStatus ?? 204;
      if (status === 204) {
        return { ok: true, status: 204, text: async () => '' };
      }
      return { ok: false, status, text: async () => opts.deleteBody ?? '' };
    }

    return reply(404, { error: 'unexpected ' + method + ' ' + path });
  }) as unknown as typeof globalThis.fetch;

  return { listCalls: () => listCalls };
}

describe('UsersTab — hard-delete action', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    activeSiteSignal.value = { id: 1, hostname: 'acme.co', slug: 'acme' } as never;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    activeSiteSignal.value = null;
    cleanup();
  });

  it('Delete is inline two-step: trigger reveals a confirm, Cancel restores it', async () => {
    routeFetch({});
    render(<UsersTab />);

    await waitFor(() => screen.getByText('alice@acme.co'));

    // No confirm copy until the trigger is clicked.
    expect(screen.queryByText('Delete forever')).toBeNull();

    fireEvent.click(screen.getByText('Delete'));

    expect(screen.getByText('Delete forever')).toBeTruthy();
    expect(screen.getByText(/cannot be undone/i)).toBeTruthy();

    fireEvent.click(screen.getByText('Cancel'));
    expect(screen.queryByText('Delete forever')).toBeNull();
    expect(screen.getByText('Delete')).toBeTruthy();
  });

  it('confirming issues DELETE and refreshes the list', async () => {
    let deleted = '';
    const r = routeFetch({ onDelete: (p) => { deleted = p; } });
    render(<UsersTab />);

    await waitFor(() => screen.getByText('alice@acme.co'));
    const callsBefore = r.listCalls();

    fireEvent.click(screen.getByText('Delete'));
    fireEvent.click(screen.getByText('Delete forever'));

    await waitFor(() => expect(deleted).toBe('/api/admin/users/u-1'));
    // The list re-fetches after a successful delete.
    await waitFor(() => expect(r.listCalls()).toBeGreaterThan(callsBefore));
  });

  it('maps the 403 self-delete guard to a plain sentence', async () => {
    routeFetch({ deleteStatus: 403, deleteBody: 'cannot delete your own account' });
    render(<UsersTab />);

    await waitFor(() => screen.getByText('alice@acme.co'));
    fireEvent.click(screen.getByText('Delete'));
    fireEvent.click(screen.getByText('Delete forever'));

    await waitFor(() => screen.getByText(/can't delete your own account/i));
  });

  it('maps the 409 last-admin guard to a plain sentence', async () => {
    routeFetch({ deleteStatus: 409, deleteBody: 'cannot delete the last enabled admin' });
    render(<UsersTab />);

    await waitFor(() => screen.getByText('alice@acme.co'));
    fireEvent.click(screen.getByText('Delete'));
    fireEvent.click(screen.getByText('Delete forever'));

    await waitFor(() => screen.getByText(/last enabled admin/i));
  });
});
