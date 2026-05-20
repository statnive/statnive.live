import { useEffect, useMemo, useState } from 'preact/hooks';
import {
  listUsers,
  createUser,
  disableUser,
  type AdminUser,
} from '../../api/admin';
import { activeSiteSignal } from '../../state/site';
import { errorMessage } from '../../lib/errorMessage';
import { validators } from '../../lib/field';
import StatusPill from '../../components/StatusPill';

export function UsersTab() {
  const [rows, setRows] = useState<AdminUser[] | null>(null);
  const [err, setErr] = useState<string>('');
  const activeSite = activeSiteSignal.value;
  const siteID = activeSite?.id ?? 0;

  async function refresh() {
    if (!siteID) return;
    try {
      setRows(await listUsers(siteID));
    } catch (e) {
      setErr(errorMessage(e, "Couldn't load users."));
    }
  }

  useEffect(() => {
    void refresh();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [siteID]);

  async function onDisable(id: string) {
    try {
      await disableUser(id);
      await refresh();
    } catch (e) {
      setErr(errorMessage(e, "Couldn't disable user."));
    }
  }

  return (
    <div class="statnive-admin-users">
      <NewUserForm siteID={siteID} onCreated={refresh} onError={setErr} />

      {err ? (
        <p class="statnive-admin-alert is-error" role="alert">
          <span class="statnive-admin-alert-glyph" aria-hidden="true">{'▪'}</span>
          <span>
            <span class="statnive-admin-alert-label">Error</span>
            <span class="statnive-admin-alert-body">{err}</span>
          </span>
        </p>
      ) : null}

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
              <th>Sites + Roles</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((u) => (
              <tr key={u.user_id}>
                <td>{u.email}</td>
                <td>{u.username}</td>
                <td>
                  {u.sites?.length > 0 ? (
                    u.sites.map((s) => (
                      <span key={s.site_id} class="statnive-site-chip">
                        {s.hostname} <em>({s.role})</em>
                      </span>
                    ))
                  ) : (
                    <em>{u.role} {'·'} site {u.site_id}</em>
                  )}
                </td>
                <td>
                  <StatusPill state={u.disabled ? 'disabled' : 'active'} />
                </td>
                <td>
                  {u.disabled ? null : (
                    <button
                      type="button"
                      class="is-destructive"
                      onClick={() => void onDisable(u.user_id)}
                    >
                      Disable user
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

const ROLES: ReadonlyArray<AdminUser['role']> = ['admin', 'viewer', 'api'];
const roleValidator = validators.oneOf(ROLES);

function NewUserForm({
  siteID,
  onCreated,
  onError,
}: {
  siteID: number;
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [email, setEmail] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<AdminUser['role']>('viewer');
  const [busy, setBusy] = useState(false);
  // Touched: only render the error sentence after the field has been
  // interacted with, so the form doesn't scream red on first paint.
  const [touched, setTouched] = useState<{
    email: boolean;
    username: boolean;
    password: boolean;
    role: boolean;
  }>({ email: false, username: false, password: false, role: false });

  const emailErr = useMemo(() => validators.email(email), [email]);
  const usernameErr = useMemo(() => validators.username(username), [username]);
  const passwordErr = useMemo(() => validators.password(password), [password]);
  const roleErr = useMemo(() => roleValidator(role), [role]);

  const anyInvalid = Boolean(emailErr || usernameErr || passwordErr || roleErr);

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy) return;
    if (anyInvalid) {
      setTouched({ email: true, username: true, password: true, role: true });
      return;
    }
    setBusy(true);
    try {
      await createUser(siteID, {
        email,
        username,
        password,
        sites: siteID > 0 ? [{ site_id: siteID, role }] : [],
      });
      setEmail('');
      setUsername('');
      setPassword('');
      setRole('viewer');
      setTouched({ email: false, username: false, password: false, role: false });
      await onCreated();
    } catch (e) {
      onError(errorMessage(e, "Couldn't create user."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit} noValidate>
      <h3>New user</h3>

      <label>
        Email
        <input
          type="email"
          required
          value={email}
          class={touched.email && emailErr ? 'is-invalid' : undefined}
          onInput={(e) => setEmail((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, email: true }))}
          aria-invalid={touched.email && emailErr ? 'true' : undefined}
        />
        {touched.email && emailErr ? (
          <p class="statnive-admin-modal-helper" role="alert">{emailErr}</p>
        ) : (
          <p class="statnive-admin-modal-helper">The email this user signs in with.</p>
        )}
      </label>

      <label>
        Username
        <input
          type="text"
          required
          value={username}
          class={touched.username && usernameErr ? 'is-invalid' : undefined}
          onInput={(e) => setUsername((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, username: true }))}
          aria-invalid={touched.username && usernameErr ? 'true' : undefined}
        />
        {touched.username && usernameErr ? (
          <p class="statnive-admin-modal-helper" role="alert">{usernameErr}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            What appears in the dashboard{"'"}s user chip. Letters, numbers, dots,
            hyphens, and underscores. Up to 32 characters.
          </p>
        )}
      </label>

      <label>
        Password
        <input
          type="password"
          required
          value={password}
          class={touched.password && passwordErr ? 'is-invalid' : undefined}
          onInput={(e) => setPassword((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, password: true }))}
          aria-invalid={touched.password && passwordErr ? 'true' : undefined}
        />
        {touched.password && passwordErr ? (
          <p class="statnive-admin-modal-helper" role="alert">{passwordErr}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            At least 12 characters. The user can change it after their first sign-in.
          </p>
        )}
      </label>

      <label>
        Role on this site
        <select
          value={role}
          class={touched.role && roleErr ? 'is-invalid' : undefined}
          onChange={(e) => {
            setRole((e.target as HTMLSelectElement).value as AdminUser['role']);
            setTouched((t) => ({ ...t, role: true }));
          }}
          aria-invalid={touched.role && roleErr ? 'true' : undefined}
        >
          <option value="admin">admin</option>
          <option value="viewer">viewer</option>
          <option value="api">api</option>
        </select>
        {touched.role && roleErr ? (
          <p class="statnive-admin-modal-helper" role="alert">{roleErr}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            admin can edit sites, users, and goals. viewer has read-only access to
            dashboards. api is token-based programmatic access only; no dashboard.
          </p>
        )}
      </label>

      <button type="submit" disabled={busy || anyInvalid}>
        {busy ? 'Creating…' : 'Create user'}
      </button>
    </form>
  );
}
