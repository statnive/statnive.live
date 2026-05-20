import { useEffect, useMemo, useState } from 'preact/hooks';
import {
  listSites,
  createSite,
  updateSiteEnabled,
  listCurrencies,
  listTimezones,
  dismissJurisdictionNotice,
  getJurisdictionNotice,
  jurisdictionLabel,
  type AdminSite,
  type CurrencyOption,
  type TimezoneOption,
} from '../../api/admin';
import { validators } from '../../lib/field';
import StatusPill from '../../components/StatusPill';
import { TrackerInstallCard } from './TrackerInstallCard';
import { SiteConfigureModal } from './SiteConfigureModal';

const FALLBACK_CURRENCY = 'EUR';
const FALLBACK_TIMEZONE = 'Europe/Berlin';

export function SitesTab() {
  const [rows, setRows] = useState<AdminSite[] | null>(null);
  const [currencies, setCurrencies] = useState<CurrencyOption[]>([]);
  const [timezones, setTimezones] = useState<TimezoneOption[]>([]);
  const [err, setErr] = useState<string>('');
  const [configuringSiteID, setConfiguringSiteID] = useState<number | null>(null);

  async function refresh() {
    try {
      setRows(await listSites());
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void refresh();
    // Currencies + timezones are server-compiled allow-lists; fetch
    // once on mount.
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

  const configuringSite = useMemo(
    () => (configuringSiteID === null ? null : rows?.find((s) => s.site_id === configuringSiteID) ?? null),
    [configuringSiteID, rows],
  );

  return (
    <div class="statnive-admin-sites">
      <JurisdictionNoticeBanner />

      <TrackerInstallCard />

      <NewSiteForm
        currencies={currencies}
        timezones={timezones}
        onCreated={refresh}
        onError={setErr}
      />

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
        <p>No sites yet. Add one above; the tracker snippet above already works for it.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="admin-sites-table">
          <thead>
            <tr>
              <th>Hostname</th>
              <th>Status</th>
              <th>Plan</th>
              <th>Jurisdiction</th>
              <th>Mode</th>
              <th aria-label="row actions" />
            </tr>
          </thead>
          <tbody>
            {rows.map((s) => (
              <tr key={s.site_id}>
                <td>{s.hostname}</td>
                <td><StatusPill state={s.enabled ? 'live' : 'paused'} /></td>
                <td class="statnive-num-cell">{s.plan}</td>
                <td>{jurisdictionSummary(s)}</td>
                <td>{modeSummary(s)}</td>
                <td>
                  <button
                    type="button"
                    onClick={() => void onToggleEnabled(s)}
                  >
                    {s.enabled ? 'Pause site' : 'Resume site'}
                  </button>
                  {' '}
                  <button
                    type="button"
                    onClick={() => setConfiguringSiteID(s.site_id)}
                  >
                    Configure {'▸'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {configuringSite ? (
        <SiteConfigureModal
          site={configuringSite}
          currencies={currencies}
          timezones={timezones}
          onSaved={refresh}
          onClose={() => setConfiguringSiteID(null)}
        />
      ) : null}
    </div>
  );
}

// jurisdictionSummary renders a one-line summary for the row's
// Jurisdiction column: jurisdiction label · currency · timezone-IANA.
// Counts/labels only — full configuration lives in the Configure modal.
function jurisdictionSummary(site: AdminSite): string {
  const j = (site.jurisdiction ?? 'OTHER-NON-EU');
  const parts: string[] = [jurisdictionLabel(j)];
  if (site.currency && site.currency !== FALLBACK_CURRENCY) parts.push(site.currency);
  if (site.tz && site.tz !== FALLBACK_TIMEZONE) parts.push(site.tz);
  return parts.join(' · ');
}

// modeSummary renders consent_mode + an origin count so the operator
// can see at a glance whether origins are configured without opening
// the modal.
function modeSummary(site: AdminSite): string {
  const mode = site.consent_mode ?? 'permissive';
  const origins = (site.allowed_origins ?? []).length;
  const allowlistEntries = (site.event_allowlist ?? []).length;
  const parts: string[] = [mode];
  if (origins > 0) parts.push(`${origins} ${origins === 1 ? 'origin' : 'origins'}`);
  if (allowlistEntries > 0) parts.push(`${allowlistEntries} allowed ${allowlistEntries === 1 ? 'event' : 'events'}`);
  return parts.join(' · ');
}

// One-time prompt nudging operators off the legacy OTHER-NON-EU +
// permissive backfill toward an explicit jurisdiction. Dismissal is
// persisted server-side; failures fall back to "don't show" so the
// banner is never a blocker.
function JurisdictionNoticeBanner() {
  const [visible, setVisible] = useState<boolean | null>(null);

  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const res = await getJurisdictionNotice();
        if (alive) setVisible(!res.dismissed);
      } catch {
        // Treat any fetch failure as "don't show the banner". The
        // operator can still configure jurisdiction from the row's
        // Configure button without the prompt.
        if (alive) setVisible(false);
      }
    })();

    return () => { alive = false; };
  }, []);

  if (!visible) return null;

  async function onDismiss() {
    setVisible(false);
    try {
      await dismissJurisdictionNotice();
    } catch {
      // No state revert; bouncing the banner back now would be jarring.
    }
  }

  return (
    <aside class="statnive-admin-alert is-notice" role="status">
      <span class="statnive-admin-alert-glyph" aria-hidden="true">{'▴'}</span>
      <span>
        <span class="statnive-admin-alert-label">Notice</span>
        <span class="statnive-admin-alert-body">
          <strong>Set your jurisdiction.</strong> This site runs in the
          legacy <code>OTHER-NON-EU</code> + <code>permissive</code>{' '}
          backfill: every visit is counted, identifier cookies are set,
          no rounding. To operate under the EU consent-free or hybrid
          flow, open the row&apos;s <strong>Configure</strong> button on
          the Sites table below. See{' '}
          <a href="/legal/lia" target="_blank" rel="noopener">/legal/lia</a>{' '}
          and{' '}
          <a href="/legal/privacy-policy/en" target="_blank" rel="noopener">/legal/privacy-policy</a>{' '}
          for the disclosure surfaces that pair with each mode.
        </span>
        <button type="button" class="statnive-chip" onClick={() => void onDismiss()}>
          Got it, don&apos;t show again
        </button>
      </span>
    </aside>
  );
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
  // Tracks whether the field has been blurred — error sentences only
  // surface after the user leaves the field, so first-keystroke errors
  // don't shout while the user is mid-typing.
  const [touched, setTouched] = useState<Record<string, boolean>>({});

  const hostnameError = validators.hostname(hostname);
  const slugError = validators.slug(slug);
  const currencyError =
    currencies.length === 0 || currencies.some((c) => c.code === currency)
      ? null
      : "We don't recognize that currency code. Pick one from the list.";
  const tzError =
    timezones.length === 0 || timezones.some((t) => t.iana === tz)
      ? null
      : "We don't recognize that timezone. Pick one from the list.";

  const isInvalid =
    hostnameError !== null ||
    slugError !== null ||
    currencyError !== null ||
    tzError !== null;

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    setTouched({ hostname: true, slug: true, currency: true, tz: true });
    if (busy || isInvalid) return;
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
      setTouched({});
      await onCreated();
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit} noValidate>
      <h3>Add site</h3>

      <label>
        Hostname
        <input
          type="text"
          required
          placeholder="example.com"
          class={touched.hostname && hostnameError ? 'is-invalid' : ''}
          value={hostname}
          onInput={(e) => setHostname((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, hostname: true }))}
          aria-invalid={touched.hostname && hostnameError ? 'true' : undefined}
        />
        {touched.hostname && hostnameError ? (
          <p class="statnive-admin-modal-helper" role="alert">{hostnameError}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            The web address that will load the tracker, for example{' '}
            <code>example.com</code>. No <code>https://</code>, no trailing slash, no path.
          </p>
        )}
      </label>

      <label>
        Slug (optional)
        <input
          type="text"
          maxLength={32}
          placeholder="auto-generated"
          class={touched.slug && slugError ? 'is-invalid' : ''}
          value={slug}
          onInput={(e) => setSlug((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, slug: true }))}
          aria-invalid={touched.slug && slugError ? 'true' : undefined}
        />
        {touched.slug && slugError ? (
          <p class="statnive-admin-modal-helper" role="alert">{slugError}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            Short identifier used in URLs and exports. Letters, numbers, and hyphens only.
            Leave empty and we&apos;ll generate one from the hostname.
          </p>
        )}
      </label>

      <label>
        Currency
        <select
          aria-label="currency"
          class={touched.currency && currencyError ? 'is-invalid' : ''}
          value={currency}
          onChange={(e) => setCurrency((e.target as HTMLSelectElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, currency: true }))}
          aria-invalid={touched.currency && currencyError ? 'true' : undefined}
        >
          {currencies.length === 0 ? (
            <option value={FALLBACK_CURRENCY}>{FALLBACK_CURRENCY}</option>
          ) : null}
          {currencies.map((c) => (
            <option key={c.code} value={c.code}>
              {c.code} {'·'} {c.symbol} {c.name}
            </option>
          ))}
        </select>
        {touched.currency && currencyError ? (
          <p class="statnive-admin-modal-helper" role="alert">{currencyError}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            How revenue numbers are labeled. A display label only; switching does not
            convert stored values.
          </p>
        )}
      </label>

      <label>
        Timezone
        <select
          aria-label="timezone"
          class={touched.tz && tzError ? 'is-invalid' : ''}
          value={tz}
          onChange={(e) => setTz((e.target as HTMLSelectElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, tz: true }))}
          aria-invalid={touched.tz && tzError ? 'true' : undefined}
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
        {touched.tz && tzError ? (
          <p class="statnive-admin-modal-helper" role="alert">{tzError}</p>
        ) : (
          <p class="statnive-admin-modal-helper">
            The zone the date-range picker uses for midnights. Defaults to <code>Europe/Berlin</code>.
          </p>
        )}
      </label>

      <button type="submit" disabled={busy || isInvalid}>
        {busy ? 'Adding…' : 'Add site'}
      </button>
    </form>
  );
}
