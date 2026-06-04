import { useEffect, useState } from 'preact/hooks';
import {
  listMCPTokens,
  createMCPToken,
  revokeMCPToken,
  getMCPConnection,
  type MCPToken,
  type MCPConnection,
} from '../api/mcp';
import { errorMessage } from '../lib/errorMessage';
import { HttpError } from '../api/admin';
import CopyButton from '../components/CopyButton';
import './ConnectAssistant.css';

// "Connect your AI assistant" — the self-serve surface over the PR-A token
// backend (gaps #5/#7/#8). A logged-in user mints a scoped, revocable bearer
// token (shown ONCE), copies the ready-to-paste `claude mcp add` command, and
// manages/revokes existing tokens. No raw secret is ever re-displayed after
// the mint response.

function fmtDate(unix: number): string {
  if (!unix) return 'never';
  return new Date(unix * 1000).toISOString().slice(0, 10);
}

export default function ConnectAssistant() {
  const [conn, setConn] = useState<MCPConnection | null>(null);
  const [rows, setRows] = useState<MCPToken[] | null>(null);
  const [err, setErr] = useState<string>('');
  // notEnabled = the feature flag is off on this server (routes 404), as
  // distinct from a transient error. Renders a friendly state, not red.
  const [notEnabled, setNotEnabled] = useState(false);
  // The just-minted token — held in memory only, shown once, never refetched.
  const [revealed, setRevealed] = useState<MCPToken | null>(null);

  async function refresh() {
    try {
      const [c, list] = await Promise.all([getMCPConnection(), listMCPTokens()]);
      setConn(c);
      setRows(list);
    } catch (e) {
      if (e instanceof HttpError && e.status === 404) {
        setNotEnabled(true); // mcp.tokens.enabled is off on this server
        return;
      }
      setErr(errorMessage(e, "Couldn't load your AI-assistant connection."));
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function onRevoke(id: string) {
    try {
      await revokeMCPToken(id);
      if (revealed?.token_id === id) setRevealed(null);
      await refresh();
    } catch (e) {
      setErr(errorMessage(e, "Couldn't revoke that token."));
    }
  }

  const command =
    revealed?.token && conn
      ? conn.add_command_template.replace('<TOKEN>', revealed.token)
      : (conn?.add_command_template ?? '');

  const intro = (
    <header class="statnive-connect-intro">
      <h2>Connect your AI assistant</h2>
      <p>
        Create a personal access token, then paste the command into Claude (or
        any MCP-compatible assistant) to ask questions about your analytics in
        plain language. Tokens are <strong>read-only</strong> and scoped to the
        sites you can already see.
      </p>
    </header>
  );

  // Feature flag off on this server (routes 404) — friendly dead-end, no form.
  if (notEnabled) {
    return (
      <section class="statnive-connect" data-testid="connect-assistant">
        {intro}
        <p class="statnive-admin-alert" role="status" data-testid="connect-not-enabled">
          Connecting an AI assistant isn{"'"}t enabled on this server yet. Ask your
          operator to turn on <code>mcp.tokens.enabled</code> to use this feature.
        </p>
      </section>
    );
  }

  return (
    <section class="statnive-connect" data-testid="connect-assistant">
      {intro}

      {err ? (
        <p class="statnive-admin-alert is-error" role="alert">
          <span class="statnive-admin-alert-glyph" aria-hidden="true">{'▪'}</span>
          <span>
            <span class="statnive-admin-alert-label">Error</span>
            <span class="statnive-admin-alert-body">{err}</span>
          </span>
        </p>
      ) : null}

      {conn && !conn.enabled ? (
        <p class="statnive-admin-alert" role="status">
          The MCP HTTP endpoint isn{"'"}t enabled on this server yet. You can still
          create a token, but ask your operator to enable it before connecting.
        </p>
      ) : null}

      {revealed?.token ? (
        <RevealBox token={revealed} command={command} onDismiss={() => setRevealed(null)} />
      ) : null}

      <MintForm conn={conn} onMinted={(t) => { setRevealed(t); void refresh(); }} onError={setErr} />

      <h3>Your tokens</h3>
      {rows === null ? (
        <p>Loading…</p>
      ) : rows.length === 0 ? (
        <p>No tokens yet. Create one above to connect an assistant.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="mcp-tokens-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Sites</th>
              <th>Role</th>
              <th>Created</th>
              <th>Expires</th>
              <th>Last used</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((t) => (
              <tr key={t.token_id}>
                <td>{t.name}</td>
                <td>
                  {t.site_ids.map((s) => (
                    <span key={s} class="statnive-site-chip">{s}</span>
                  ))}
                </td>
                <td>{t.role}</td>
                <td>{fmtDate(t.created_at)}</td>
                <td>{fmtDate(t.expires_at)}</td>
                <td>{fmtDate(t.last_used_at)}</td>
                <td>
                  <button
                    type="button"
                    class="is-destructive"
                    onClick={() => void onRevoke(t.token_id)}
                  >
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

// RevealBox shows the raw token + ready-to-paste command exactly once.
function RevealBox({
  token,
  command,
  onDismiss,
}: {
  token: MCPToken;
  command: string;
  onDismiss: () => void;
}) {
  return (
    <div class="statnive-token-reveal" role="alert" data-testid="token-reveal">
      <h3>{'✓'} Token created: copy it now</h3>
      <p>
        This is the <strong>only time</strong> the token is shown. Copy it now; if
        you lose it, revoke it and create a new one.
      </p>

      <label class="statnive-token-reveal-field">
        Token
        <div class="statnive-token-reveal-row">
          <code data-testid="revealed-token">{token.token}</code>
          <CopyButton text={token.token ?? ''} />
        </div>
      </label>

      <label class="statnive-token-reveal-field">
        Add to your assistant
        <div class="statnive-token-reveal-row">
          <code data-testid="revealed-command">{command}</code>
          <CopyButton text={command} />
        </div>
      </label>

      <button type="button" class="statnive-token-reveal-dismiss" onClick={onDismiss}>
        I{"'"}ve copied it
      </button>
    </div>
  );
}

const TOKEN_ROLES: ReadonlyArray<MCPToken['role']> = ['api', 'viewer'];

function MintForm({
  conn,
  onMinted,
  onError,
}: {
  conn: MCPConnection | null;
  onMinted: (t: MCPToken) => void;
  onError: (msg: string) => void;
}) {
  const [name, setName] = useState('');
  const [role, setRole] = useState<MCPToken['role']>('api');
  const [sites, setSites] = useState<Set<number>>(new Set());
  const [ttlDays, setTtlDays] = useState('');
  const [busy, setBusy] = useState(false);

  const available = conn?.sites ?? [];

  // Default-select all available sites once the connection loads.
  useEffect(() => {
    if (available.length > 0) setSites(new Set(available));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conn]);

  function toggle(site: number) {
    setSites((prev) => {
      const next = new Set(prev);
      if (next.has(site)) next.delete(site);
      else next.add(site);
      return next;
    });
  }

  const nameValid = name.trim().length > 0 && name.trim().length <= 80;
  const sitesValid = sites.size > 0;

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy || !nameValid || !sitesValid) return;
    setBusy(true);
    try {
      const ttl = ttlDays.trim() === '' ? undefined : Number(ttlDays);
      const minted = await createMCPToken({
        name: name.trim(),
        // Omit site_ids when every site is selected — the server defaults to
        // the caller's full readable set.
        site_ids: sites.size === available.length ? undefined : [...sites],
        role,
        ttl_days: Number.isFinite(ttl) ? ttl : undefined,
      });
      setName('');
      setTtlDays('');
      onMinted(minted);
    } catch (e) {
      onError(errorMessage(e, "Couldn't create the token."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit} noValidate>
      <h3>New token</h3>

      <label>
        Name
        <input
          type="text"
          required
          value={name}
          maxLength={80}
          placeholder="e.g. My laptop (Claude)"
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
        />
        <p class="statnive-admin-modal-helper">
          A label so you can recognise this token later (max 80 characters).
        </p>
      </label>

      {available.length > 1 ? (
        <fieldset class="statnive-connect-sites">
          <legend>Sites</legend>
          {available.map((s) => (
            <label key={s} class="statnive-connect-site-check">
              <input
                type="checkbox"
                checked={sites.has(s)}
                onChange={() => toggle(s)}
              />
              site {s}
            </label>
          ))}
          {!sitesValid ? (
            <p class="statnive-admin-modal-helper" role="alert">Select at least one site.</p>
          ) : null}
        </fieldset>
      ) : null}

      <label>
        Access
        <select
          value={role}
          onChange={(e) => setRole((e.target as HTMLSelectElement).value as MCPToken['role'])}
        >
          {TOKEN_ROLES.map((r) => (
            <option key={r} value={r}>{r}</option>
          ))}
        </select>
        <p class="statnive-admin-modal-helper">
          Both are read-only for the assistant. {'api'} is the usual choice.
        </p>
      </label>

      <label>
        Expires in (days)
        <input
          type="number"
          min={1}
          max={365}
          value={ttlDays}
          placeholder="90"
          onInput={(e) => setTtlDays((e.target as HTMLInputElement).value)}
        />
        <p class="statnive-admin-modal-helper">Leave blank for the default (90 days).</p>
      </label>

      <button type="submit" disabled={busy || !nameValid || !sitesValid}>
        {busy ? 'Creating…' : 'Create token'}
      </button>
    </form>
  );
}
