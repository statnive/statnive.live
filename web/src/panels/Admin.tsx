import { useEffect, useState } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import {
  listUsers,
  createUser,
  disableUser,
  listGoals,
  createGoal,
  disableGoal,
  listSites,
  createSite,
  updateSiteEnabled,
  type AdminUser,
  type AdminGoal,
  type AdminSite,
} from '../api/admin';
import './Admin.css';

// Admin panel — single lazy chunk, tabbed between Users + Goals.
// Gated by role: App.tsx only routes here when userSignal.role === 'admin'.
//
// v1 keeps the UI deliberately simple. Inline forms, no modals, no
// pagination (admin-sized deployments have tens of rows per surface).
// Phase 11 SaaS adds cursor pagination + richer edit flows.

type Tab = 'sites' | 'users' | 'goals';

export default function Admin() {
  const tab = useSignal<Tab>('sites');

  return (
    <section class="statnive-admin">
      <div class="statnive-admin-tabs" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={tab.value === 'sites'}
          class={tab.value === 'sites' ? 'is-active' : ''}
          onClick={() => (tab.value = 'sites')}
        >
          Sites
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab.value === 'users'}
          class={tab.value === 'users' ? 'is-active' : ''}
          onClick={() => (tab.value = 'users')}
        >
          Users
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab.value === 'goals'}
          class={tab.value === 'goals' ? 'is-active' : ''}
          onClick={() => (tab.value = 'goals')}
        >
          Goals
        </button>
      </div>

      {tab.value === 'sites' ? <SitesTab /> : tab.value === 'users' ? <UsersTab /> : <GoalsTab />}
    </section>
  );
}

// ---------------- Users tab ----------------

function UsersTab() {
  const [rows, setRows] = useState<AdminUser[] | null>(null);
  const [err, setErr] = useState<string>('');

  async function refresh() {
    try {
      setRows(await listUsers());
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function onDisable(id: string) {
    try {
      await disableUser(id);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div class="statnive-admin-users">
      <NewUserForm onCreated={refresh} onError={setErr} />

      {err ? <p class="statnive-admin-error" role="alert">{err}</p> : null}

      {rows === null ? (
        <p>Loading…</p>
      ) : rows.length === 0 ? (
        <p>No users yet.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="admin-users-table">
          <thead>
            <tr>
              <th>Email</th>
              <th>Username</th>
              <th>Role</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((u) => (
              <tr key={u.user_id}>
                <td>{u.email}</td>
                <td>{u.username}</td>
                <td>{u.role}</td>
                <td>{u.disabled ? 'disabled' : 'active'}</td>
                <td>
                  {u.disabled ? null : (
                    <button type="button" onClick={() => void onDisable(u.user_id)}>
                      Disable
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function NewUserForm({
  onCreated,
  onError,
}: {
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [email, setEmail] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<AdminUser['role']>('viewer');
  const [busy, setBusy] = useState(false);

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      await createUser({ email, username, password, role });
      setEmail('');
      setUsername('');
      setPassword('');
      setRole('viewer');
      await onCreated();
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit}>
      <h3>New user</h3>
      <label>
        Email
        <input type="email" required value={email} onInput={(e) => setEmail((e.target as HTMLInputElement).value)} />
      </label>
      <label>
        Username
        <input type="text" required value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
      </label>
      <label>
        Password
        <input type="password" required value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
      </label>
      <label>
        Role
        <select
          value={role}
          onChange={(e) => setRole((e.target as HTMLSelectElement).value as AdminUser['role'])}
        >
          <option value="admin">admin</option>
          <option value="viewer">viewer</option>
          <option value="api">api</option>
        </select>
      </label>
      <button type="submit" disabled={busy}>
        {busy ? 'Creating…' : 'Create user'}
      </button>
    </form>
  );
}

// ---------------- Goals tab ----------------

function GoalsTab() {
  const [rows, setRows] = useState<AdminGoal[] | null>(null);
  const [err, setErr] = useState<string>('');

  async function refresh() {
    try {
      setRows(await listGoals());
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function onDisable(id: string) {
    try {
      await disableGoal(id);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div class="statnive-admin-goals">
      <NewGoalForm onCreated={refresh} onError={setErr} />

      {err ? <p class="statnive-admin-error" role="alert">{err}</p> : null}

      {rows === null ? (
        <p>Loading…</p>
      ) : rows.length === 0 ? (
        <p>No goals yet.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="admin-goals-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Match</th>
              <th>Pattern</th>
              <th>Value</th>
              <th>Enabled</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((g) => (
              <tr key={g.goal_id}>
                <td>{g.name}</td>
                <td>{g.match_type}</td>
                <td><code>{g.pattern}</code></td>
                <td>{g.value_rials}</td>
                <td>{g.enabled ? 'yes' : 'no'}</td>
                <td>
                  {g.enabled ? (
                    <button type="button" onClick={() => void onDisable(g.goal_id)}>
                      Disable
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function NewGoalForm({
  onCreated,
  onError,
}: {
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [name, setName] = useState('');
  const [pattern, setPattern] = useState('');
  const [valueRials, setValueRials] = useState(0);
  const [busy, setBusy] = useState(false);

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      await createGoal({
        name,
        match_type: 'event_name_equals',
        pattern,
        value_rials: valueRials,
        enabled: true,
      });
      setName('');
      setPattern('');
      setValueRials(0);
      await onCreated();
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit}>
      <h3>New goal</h3>
      <label>
        Name
        <input type="text" required value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
      </label>
      <label>
        Event name (exact match)
        <input
          type="text"
          required
          maxLength={128}
          value={pattern}
          onInput={(e) => setPattern((e.target as HTMLInputElement).value)}
        />
      </label>
      <label>
        Value (rials)
        <input
          type="number"
          min={0}
          value={valueRials}
          onInput={(e) => setValueRials(Number((e.target as HTMLInputElement).value))}
        />
      </label>
      <button type="submit" disabled={busy}>
        {busy ? 'Creating…' : 'Create goal'}
      </button>
    </form>
  );
}

// ---------------- Sites tab ----------------

function SitesTab() {
  const [rows, setRows] = useState<AdminSite[] | null>(null);
  const [err, setErr] = useState<string>('');

  async function refresh() {
    try {
      setRows(await listSites());
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function onToggleEnabled(site: AdminSite) {
    try {
      await updateSiteEnabled(site.site_id, !site.enabled);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div class="statnive-admin-sites">
      <NewSiteForm onCreated={refresh} onError={setErr} />

      {err ? <p class="statnive-admin-error" role="alert">{err}</p> : null}

      {rows === null ? (
        <p>Loading…</p>
      ) : rows.length === 0 ? (
        <p>No sites yet. Add one above to generate a tracker snippet.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="admin-sites-table">
          <thead>
            <tr>
              <th>Hostname</th>
              <th>Slug</th>
              <th>Plan</th>
              <th>Status</th>
              <th>Tracker snippet</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((s) => (
              <tr key={s.site_id}>
                <td>{s.hostname}</td>
                <td><code>{s.slug}</code></td>
                <td>{s.plan}</td>
                <td>{s.enabled ? 'active' : 'disabled'}</td>
                <td><TrackerSnippet /></td>
                <td>
                  <button type="button" onClick={() => void onToggleEnabled(s)}>
                    {s.enabled ? 'Disable' : 'Enable'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function TrackerSnippet() {
  // Per-site parametrization isn't needed — the backend resolves the
  // event's site_id from the hostname in the payload (set by the
  // tracker JS from window.location.hostname at emit time). So a single
  // origin-relative snippet works for every site on this installation.
  const origin = typeof window === 'undefined' ? '' : window.location.origin;
  const snippet = `<script src="${origin}/tracker.js" async defer></script>`;
  return <pre class="statnive-admin-snippet"><code>{snippet}</code></pre>;
}

function NewSiteForm({
  onCreated,
  onError,
}: {
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [hostname, setHostname] = useState('');
  const [slug, setSlug] = useState('');
  const [tz, setTz] = useState('Asia/Tehran');
  const [busy, setBusy] = useState(false);

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      await createSite({
        hostname,
        slug: slug || undefined,
        tz: tz || undefined,
      });
      setHostname('');
      setSlug('');
      setTz('Asia/Tehran');
      await onCreated();
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit}>
      <h3>Add site</h3>
      <label>
        Hostname
        <input
          type="text"
          required
          placeholder="example.com"
          value={hostname}
          onInput={(e) => setHostname((e.target as HTMLInputElement).value)}
        />
      </label>
      <label>
        Slug (optional)
        <input
          type="text"
          maxLength={32}
          placeholder="auto-generated"
          value={slug}
          onInput={(e) => setSlug((e.target as HTMLInputElement).value)}
        />
      </label>
      <label>
        Timezone
        <input
          type="text"
          value={tz}
          onInput={(e) => setTz((e.target as HTMLInputElement).value)}
        />
      </label>
      <button type="submit" disabled={busy}>
        {busy ? 'Adding…' : 'Add site'}
      </button>
    </form>
  );
}
