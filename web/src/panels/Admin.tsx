import { useEffect, useState } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import {
  listUsers,
  createUser,
  updateUserSites,
  disableUser,
  listGoals,
  createGoal,
  disableGoal,
  listSites,
  createSite,
  updateSiteEnabled,
  updateSitePolicy,
  listCurrencies,
  listTimezones,
  type AdminUser,
  type AdminGoal,
  type AdminSite,
  type AdminUserSiteRef,
  type SitePolicyPatch,
  type CurrencyOption,
  type TimezoneOption,
} from '../api/admin';
import { activeSiteSignal } from '../state/site';
import './Admin.css';

// Admin panel — single lazy chunk, tabbed between Users + Goals.
// Gated by role: App.tsx only routes here when userSignal.role === 'admin'.
//
// v1 keeps the UI deliberately simple. Inline forms, no modals, no
// pagination (admin-sized deployments have tens of rows per surface).
// Phase 11 SaaS adds cursor pagination + richer edit flows.

type Tab = 'sites' | 'users' | 'goals';

const FALLBACK_CURRENCY = 'EUR';
const FALLBACK_TIMEZONE = 'Europe/Berlin';

export default function Admin() {
  const tab = useSignal<Tab>('sites');
  const activeSite = activeSiteSignal.value;

  return (
    <section class="statnive-admin">
      {activeSite ? (
        <div class="statnive-admin-context" data-testid="admin-active-site">
          <strong>Managing site:</strong> {activeSite.hostname}
          {' '}<code>(site_id={activeSite.site_id})</code>
        </div>
      ) : null}

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
  const activeSite = activeSiteSignal.value;
  const siteID = activeSite?.site_id ?? 0;

  async function refresh() {
    if (!siteID) return;
    try {
      setRows(await listUsers(siteID));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
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
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div class="statnive-admin-users">
      <NewUserForm siteID={siteID} onCreated={refresh} onError={setErr} />

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
                    <em>{u.role} @ site {u.site_id}</em>
                  )}
                </td>
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

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy) return;
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
        Role on this site
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
  const activeSite = activeSiteSignal.value;
  const siteID = activeSite?.site_id ?? 0;

  async function refresh() {
    if (!siteID) return;
    try {
      setRows(await listGoals(siteID));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void refresh();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [siteID]);

  async function onDisable(g: AdminGoal) {
    try {
      await disableGoal(g.site_id, g.goal_id);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div class="statnive-admin-goals">
      <EventApiHelpCard />
      <NewGoalForm siteID={siteID} onCreated={refresh} onError={setErr} />

      {err ? <p class="statnive-admin-error" role="alert">{err}</p> : null}

      {rows === null ? (
        <p>Loading…</p>
      ) : rows.length === 0 ? (
        <p>No goals yet for this site.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="admin-goals-table">
          <thead>
            <tr>
              <th>Site</th>
              <th>Name</th>
              <th>Pattern (event_name)</th>
              <th>Value</th>
              <th>Enabled</th>
              <th>Snippet</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((g) => (
              <tr key={g.goal_id}>
                <td>{g.hostname || '—'} <code>({g.site_id})</code></td>
                <td>{g.name}</td>
                <td><code>{g.pattern}</code></td>
                <td>{g.value}</td>
                <td>{g.enabled ? 'yes' : 'no'}</td>
                <td><GoalSnippetButton goal={g} /></td>
                <td>
                  {g.enabled ? (
                    <button type="button" onClick={() => void onDisable(g)}>
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

function EventApiHelpCard() {
  return (
    <details class="statnive-admin-help-card">
      <summary><strong>How to fire custom events</strong></summary>
      <div>
        <p>Every visit fires a pageview automatically. For a custom event (click, form submit, video play):</p>
        <pre><code>{`window.statniveLive.track(name, props, value)`}</code></pre>
        <ul>
          <li><strong>name</strong> — required string. Becomes <code>event_name</code>.</li>
          <li><strong>props</strong> — optional object. Default <code>{'{}'}</code>.</li>
          <li><strong>value</strong> — optional integer. Defaults to <code>0</code>.</li>
        </ul>
        <p><strong>When is an event also a goal?</strong> Define a goal below. When <code>event_name</code> matches a goal pattern, the server sets <code>is_goal=1</code> and overwrites <code>event_value</code> with the goal's value. The revenue card sums goal events.</p>
        <p><strong>Edge cases:</strong></p>
        <ul>
          <li>No matching goal → stored as a regular custom event (<code>is_goal=0</code>). Not in the revenue card.</li>
          <li>Disabled goal → behaves as if it doesn't exist. Re-enabling does NOT backfill past events.</li>
          <li>Tracker loaded on an unregistered hostname → server returns 204, event silently dropped.</li>
        </ul>
      </div>
    </details>
  );
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  async function onCopy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard unavailable — content is visible; user can select-copy
    }
  }

  return (
    <button type="button" class="statnive-chip" onClick={() => void onCopy()}>
      {copied ? 'Copied!' : 'Copy'}
    </button>
  );
}

function GoalSnippetButton({ goal }: { goal: AdminGoal }) {
  const [open, setOpen] = useState(false);

  const snippet = `// Direct call:\nwindow.statniveLive.track('${goal.pattern}', {\n  page: window.location.pathname,\n});\n\n// Delegated click listener (install once):\ndocument.addEventListener('click', function (e) {\n  var a = e.target.closest('[data-statnive-goal="${goal.pattern}"]');\n  if (!a || !window.statniveLive) return;\n  window.statniveLive.track('${goal.pattern}', {\n    page: window.location.pathname,\n    href: a.href || '',\n  });\n}, true);\n\n// Mark your element:\n// <a href="..." data-statnive-goal="${goal.pattern}">Click me</a>`;

  return (
    <span>
      <button type="button" class="statnive-chip" onClick={() => setOpen(!open)}>
        {open ? 'Hide' : 'Show snippet'}
      </button>
      {open ? (
        <span class="statnive-admin-snippet">
          <pre><code>{snippet}</code></pre>
          <CopyButton text={snippet} />
        </span>
      ) : null}
    </span>
  );
}

function NewGoalForm({
  siteID,
  onCreated,
  onError,
}: {
  siteID: number;
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [name, setName] = useState('');
  const [pattern, setPattern] = useState('');
  const [value, setValue] = useState(0);
  const [busy, setBusy] = useState(false);

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      await createGoal(siteID, {
        name,
        match_type: 'event_name_equals',
        pattern,
        value,
        enabled: true,
      });
      setName('');
      setPattern('');
      setValue(0);
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
        Value
        <input
          type="number"
          min={0}
          value={value}
          onInput={(e) => setValue(Number((e.target as HTMLInputElement).value))}
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
  const [currencies, setCurrencies] = useState<CurrencyOption[]>([]);
  const [timezones, setTimezones] = useState<TimezoneOption[]>([]);
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
    // Load the dropdown option lists once on mount — they're stable
    // for the life of the binary (currencies/timezones are compiled
    // into the server allow-list, not user-editable).
    void (async () => {
      try {
        const [cs, tzs] = await Promise.all([listCurrencies(), listTimezones()]);
        setCurrencies(cs);
        setTimezones(tzs);
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    })();
  }, []);

  async function onToggleEnabled(site: AdminSite) {
    try {
      await updateSiteEnabled(site.site_id, !site.enabled);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function onPatchPolicy(siteID: number, patch: SitePolicyPatch) {
    try {
      await updateSitePolicy(siteID, patch);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div class="statnive-admin-sites">
      <NewSiteForm
        currencies={currencies}
        timezones={timezones}
        onCreated={refresh}
        onError={setErr}
      />

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
              <th>Currency</th>
              <th>Timezone</th>
              <th title="Honor Sec-GPC: 1 (suppresses identity for visitors who send the header). EU operators must enable.">GPC</th>
              <th title="Honor DNT: 1 (suppresses identity for visitors who send the header). EU operators must enable.">DNT</th>
              <th title="Track bots (default on; off drops bot events at the pipeline).">Bots</th>
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
                <CurrencyCell site={s} options={currencies} onPatch={onPatchPolicy} />
                <TimezoneCell site={s} options={timezones} onPatch={onPatchPolicy} />
                <PolicyCell site={s} field="respect_gpc" label="respect Sec-GPC" onPatch={onPatchPolicy} />
                <PolicyCell site={s} field="respect_dnt" label="respect DNT"     onPatch={onPatchPolicy} />
                <PolicyCell site={s} field="track_bots"  label="track bots"      onPatch={onPatchPolicy} />
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

      <p class="statnive-admin-help">
        <strong>Currency:</strong> a display label only — the dashboard renders revenue
        integers using the selected ISO 4217 code. Switching currency does not transform
        stored values.{' '}
        <strong>Timezone:</strong> the IANA zone the dashboard's date-range picker
        interprets midnights in. Default <code>Europe/Berlin</code>.{' '}
        <strong>GPC / DNT:</strong> default off — every visit counted, identity hashed
        normally. Operators with EU visitors <strong>must</strong> enable both flags
        per site to honor the visitor's privacy signal under GDPR Art. 4(2).{' '}
        <strong>Bots:</strong> default on — bot events flow through with{' '}
        <code>is_bot=1</code> so the dashboard can split human from bot traffic.
      </p>
    </div>
  );
}

// CurrencyCell renders the currency dropdown for one site. Selecting a
// new option immediately PATCHes /api/admin/sites/{id} with {currency}.
function CurrencyCell({
  site,
  options,
  onPatch,
}: {
  site: AdminSite;
  options: CurrencyOption[];
  onPatch: (siteID: number, patch: SitePolicyPatch) => void | Promise<void>;
}) {
  return (
    <td>
      <select
        aria-label={`currency for ${site.hostname}`}
        value={site.currency}
        onChange={(e) => void onPatch(site.site_id, {
          currency: (e.target as HTMLSelectElement).value,
        })}
      >
        {options.length === 0 ? (
          <option value={site.currency}>{site.currency}</option>
        ) : null}
        {options.map((c) => (
          <option key={c.code} value={c.code}>
            {c.code} — {c.symbol} {c.name}
          </option>
        ))}
      </select>
    </td>
  );
}

// TimezoneCell renders the timezone dropdown for one site. Same PATCH
// flow as CurrencyCell.
function TimezoneCell({
  site,
  options,
  onPatch,
}: {
  site: AdminSite;
  options: TimezoneOption[];
  onPatch: (siteID: number, patch: SitePolicyPatch) => void | Promise<void>;
}) {
  return (
    <td>
      <select
        aria-label={`timezone for ${site.hostname}`}
        value={site.tz}
        onChange={(e) => void onPatch(site.site_id, {
          tz: (e.target as HTMLSelectElement).value,
        })}
      >
        {options.length === 0 ? (
          <option value={site.tz}>{site.tz}</option>
        ) : null}
        {options.map((t) => (
          <option key={t.iana} value={t.iana}>
            {t.label} ({t.offset})
          </option>
        ))}
      </select>
    </td>
  );
}

// PolicyCell renders one of the three policy checkboxes (GPC / DNT /
// track_bots) inside the Sites table. Pulled out to keep each cell
// declaration in the table to one line — the three cells differ only
// in the field name + label.
function PolicyCell({
  site,
  field,
  label,
  onPatch,
}: {
  site: AdminSite;
  field: 'respect_gpc' | 'respect_dnt' | 'track_bots';
  label: string;
  onPatch: (siteID: number, patch: SitePolicyPatch) => void | Promise<void>;
}) {
  return (
    <td>
      <input
        type="checkbox"
        aria-label={`${label} for ${site.hostname}`}
        checked={site[field]}
        onChange={(e) => void onPatch(site.site_id, {
          [field]: (e.target as HTMLInputElement).checked,
        })}
      />
    </td>
  );
}

function TrackerSnippet() {
  // Per-site parametrization isn't needed — the backend resolves the
  // event's site_id from the hostname in the payload (set by the
  // tracker JS from window.location.hostname at emit time). So a single
  // origin-relative snippet works for every site on this installation.
  // The data-statnive-endpoint attribute is explicit (not derived from
  // script.src) so customers reading the snippet can see exactly where
  // their beacons go without having to read tracker.js source.
  const origin = typeof window === 'undefined' ? '' : window.location.origin;
  const snippet = `<script src="${origin}/tracker.js" data-statnive-endpoint="${origin}/api/event" async defer></script>`;
  return <pre class="statnive-admin-snippet"><code>{snippet}</code></pre>;
}

function NewSiteForm({
  currencies,
  timezones,
  onCreated,
  onError,
}: {
  currencies: CurrencyOption[];
  timezones: TimezoneOption[];
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [hostname, setHostname] = useState('');
  const [slug, setSlug] = useState('');
  const [tz, setTz] = useState(FALLBACK_TIMEZONE);
  const [currency, setCurrency] = useState(FALLBACK_CURRENCY);
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
        currency: currency || undefined,
      });
      setHostname('');
      setSlug('');
      setTz(FALLBACK_TIMEZONE);
      setCurrency(FALLBACK_CURRENCY);
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
        Currency
        <select
          aria-label="currency"
          value={currency}
          onChange={(e) => setCurrency((e.target as HTMLSelectElement).value)}
        >
          {currencies.length === 0 ? (
            <option value={FALLBACK_CURRENCY}>{FALLBACK_CURRENCY}</option>
          ) : null}
          {currencies.map((c) => (
            <option key={c.code} value={c.code}>
              {c.code} — {c.symbol} {c.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        Timezone
        <select
          aria-label="timezone"
          value={tz}
          onChange={(e) => setTz((e.target as HTMLSelectElement).value)}
        >
          {timezones.length === 0 ? (
            <option value={FALLBACK_TIMEZONE}>{FALLBACK_TIMEZONE}</option>
          ) : null}
          {timezones.map((t) => (
            <option key={t.iana} value={t.iana}>
              {t.label} ({t.offset})
            </option>
          ))}
        </select>
      </label>
      <button type="submit" disabled={busy}>
        {busy ? 'Adding…' : 'Add site'}
      </button>
    </form>
  );
}
