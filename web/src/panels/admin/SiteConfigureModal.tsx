import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import {
  updateSitePolicy,
  JURISDICTIONS,
  CONSENT_MODES,
  derivedConsentMode,
  jurisdictionLabel,
  type AdminSite,
  type SitePolicyPatch,
  type CurrencyOption,
  type TimezoneOption,
  type ConsentMode,
  type Jurisdiction,
} from '../../api/admin';
import { validators } from '../../lib/field';
import { errorMessage } from '../../lib/errorMessage';
import StatusPill from '../../components/StatusPill';

const MAX_ALLOWED_ORIGINS = 10;

export interface SiteConfigureModalProps {
  site: AdminSite;
  currencies: CurrencyOption[];
  timezones: TimezoneOption[];
  onSaved: () => void | Promise<void>;
  onClose: () => void;
}

interface Draft {
  currency: string;
  tz: string;
  respect_gpc: boolean;
  respect_dnt: boolean;
  track_bots: boolean;
  jurisdiction: Jurisdiction;
  consent_mode: ConsentMode;
  event_allowlist: string[];
}

function toDraft(site: AdminSite): Draft {
  return {
    currency: site.currency,
    tz: site.tz,
    respect_gpc: site.respect_gpc,
    respect_dnt: site.respect_dnt,
    track_bots: site.track_bots,
    jurisdiction: (site.jurisdiction ?? 'OTHER-NON-EU') as Jurisdiction,
    consent_mode: (site.consent_mode ?? 'permissive') as ConsentMode,
    event_allowlist: site.event_allowlist ?? [],
  };
}

function buildPatch(
  site: AdminSite,
  draft: Draft,
  nextOrigins: string[],
): SitePolicyPatch {
  const patch: SitePolicyPatch = {};
  if (draft.currency !== site.currency) patch.currency = draft.currency;
  if (draft.tz !== site.tz) patch.tz = draft.tz;
  if (draft.respect_gpc !== site.respect_gpc) patch.respect_gpc = draft.respect_gpc;
  if (draft.respect_dnt !== site.respect_dnt) patch.respect_dnt = draft.respect_dnt;
  if (draft.track_bots !== site.track_bots) patch.track_bots = draft.track_bots;

  const currentJ = (site.jurisdiction ?? 'OTHER-NON-EU') as Jurisdiction;
  if (draft.jurisdiction !== currentJ) patch.jurisdiction = draft.jurisdiction;

  const currentM = (site.consent_mode ?? 'permissive') as ConsentMode;
  if (draft.consent_mode !== currentM) patch.consent_mode = draft.consent_mode;

  const currentAllow = site.event_allowlist ?? [];
  if (
    draft.event_allowlist.length !== currentAllow.length ||
    draft.event_allowlist.some((v, i) => v !== currentAllow[i])
  ) {
    patch.event_allowlist = draft.event_allowlist;
  }

  const currentOrigins = site.allowed_origins ?? [];
  if (
    nextOrigins.length !== currentOrigins.length ||
    nextOrigins.some((v, i) => v !== currentOrigins[i])
  ) {
    patch.allowed_origins = nextOrigins;
  }

  return patch;
}

// Inputs render WITHOUT the `https://` prefix (the chrome carries it).
// Both the server contract and validator expect scheme present, so we
// splice it back on for validation + submit.
function withScheme(host: string): string {
  return host.startsWith('https://') ? host : `https://${host}`;
}
function withoutScheme(origin: string): string {
  return origin.replace(/^https:\/\//i, '');
}

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function SiteConfigureModal({
  site,
  currencies,
  timezones,
  onSaved,
  onClose,
}: SiteConfigureModalProps) {
  const [draft, setDraft] = useState<Draft>(() => toDraft(site));
  // originHosts holds the inputs verbatim (sans-scheme); allowed_origins
  // is derived at diff/save time. Avoids a sync useEffect that would
  // double-render every keystroke.
  const [originHosts, setOriginHosts] = useState<string[]>(() =>
    (site.allowed_origins ?? []).map(withoutScheme),
  );
  const [saving, setSaving] = useState(false);
  const [topError, setTopError] = useState<string>('');
  const containerRef = useRef<HTMLDivElement | null>(null);

  const nextOrigins = useMemo(
    () => originHosts.map((h) => withScheme(h.trim())).filter((o) => o !== 'https://'),
    [originHosts],
  );

  // Validation — every field's errorFor() runs on every render. The
  // map keys are the field IDs the JSX uses to look up its own error
  // sentence; null means the field is valid.
  const errors = useMemo(() => {
    const e: Record<string, string | null> = {};
    e.currency =
      currencies.length === 0 || currencies.some((c) => c.code === draft.currency)
        ? null
        : "We don't recognize that currency. Pick one from the list.";
    e.tz =
      timezones.length === 0 || timezones.some((t) => t.iana === draft.tz)
        ? null
        : "We don't recognize that timezone. Pick one from the list.";
    e.jurisdiction = JURISDICTIONS.includes(draft.jurisdiction)
      ? null
      : 'Pick a jurisdiction from the list.';
    e.consent_mode = CONSENT_MODES.includes(draft.consent_mode)
      ? null
      : 'Pick a consent mode from the list.';

    // Event allowlist — per-entry validation; first bad entry wins.
    if (draft.event_allowlist.length > 50) {
      e.event_allowlist = 'Maximum 50 event names.';
    } else {
      const bad = draft.event_allowlist.find(
        (name) => validators.eventName(name) !== null,
      );
      e.event_allowlist = bad === undefined
        ? null
        : `Event names use lowercase letters, numbers, and underscores only, up to 128 characters. You entered \`${bad}\`.`;
    }

    // Allowed origins — collect per-row errors so the JSX can anchor
    // each error beneath its input.
    const originErrors: (string | null)[] = originHosts.map((host) => {
      const candidate = withScheme(host.trim());
      if (host.trim() === '') return 'Enter the address or remove this row.';
      return validators.httpsOrigin(candidate);
    });
    e._origins_any =
      originErrors.some((s) => s !== null)
        ? originErrors.find((s) => s !== null) ?? null
        : null;
    if (originHosts.length > MAX_ALLOWED_ORIGINS) {
      e._origins_any = `You can register up to ${MAX_ALLOWED_ORIGINS} addresses per site.`;
    }
    // Expose per-row errors on dedicated keys for the JSX.
    originErrors.forEach((s, i) => {
      e[`origin_${i}`] = s;
    });

    return e;
  }, [draft, originHosts, currencies, timezones]);

  const isInvalid = useMemo(
    () =>
      Object.entries(errors).some(([, v]) => v !== null) ||
      originHosts.length > MAX_ALLOWED_ORIGINS,
    [errors, originHosts],
  );

  const patch = useMemo(
    () => buildPatch(site, draft, nextOrigins),
    [site, draft, nextOrigins],
  );
  const isDirty = Object.keys(patch).length > 0;

  // Focus trap: focus first focusable element on mount; Esc closes;
  // Tab/Shift+Tab cycles inside the modal.
  useEffect(() => {
    const node = containerRef.current;
    if (!node) return;
    const focusables = Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
    if (focusables[0]) focusables[0].focus();

    function onKey(ev: KeyboardEvent) {
      if (ev.key === 'Escape') {
        ev.preventDefault();
        onClose();
        return;
      }
      if (ev.key !== 'Tab') return;
      const list = Array.from(node!.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
      if (list.length === 0) return;
      const first = list[0];
      const last = list[list.length - 1];
      const active = document.activeElement as HTMLElement | null;
      if (ev.shiftKey && active === first) {
        ev.preventDefault();
        last.focus();
      } else if (!ev.shiftKey && active === last) {
        ev.preventDefault();
        first.focus();
      }
    }

    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  function onChangeJurisdiction(next: Jurisdiction) {
    // Flipping jurisdiction implies a sensible consent_mode default;
    // explicit hybrid is preserved when the operator already picked it.
    const nextMode: ConsentMode =
      draft.consent_mode === 'hybrid' ? 'hybrid' : derivedConsentMode(next);
    setDraft((d) => ({ ...d, jurisdiction: next, consent_mode: nextMode }));
  }

  function onChangeEventAllowlist(raw: string) {
    const parts = raw.split(',').map((s) => s.trim()).filter(Boolean);
    setDraft((d) => ({ ...d, event_allowlist: parts }));
  }

  function setOriginAt(idx: number, value: string) {
    setOriginHosts((prev) => prev.map((h, i) => (i === idx ? value : h)));
  }
  function addOrigin() {
    if (originHosts.length >= MAX_ALLOWED_ORIGINS) return;
    setOriginHosts((prev) => [...prev, '']);
  }
  function removeOrigin(idx: number) {
    setOriginHosts((prev) => prev.filter((_, i) => i !== idx));
  }

  async function onSave(ev: Event) {
    ev.preventDefault();
    if (isInvalid || saving || !isDirty) return;
    setSaving(true);
    setTopError('');
    try {
      await updateSitePolicy(site.site_id, patch);
      await onSaved();
      onClose();
    } catch (e) {
      setTopError(errorMessage(e, "Couldn't save your changes."));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div
      class="statnive-admin-modal-backdrop"
      role="presentation"
      onClick={(ev) => {
        if (ev.target === ev.currentTarget) onClose();
      }}
    >
      <div
        ref={containerRef}
        class="statnive-admin-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="statnive-configure-h"
      >
        <button
          type="button"
          class="statnive-admin-modal-close"
          aria-label="Close"
          onClick={onClose}
        >
          {'×'}
        </button>

        <header class="statnive-admin-modal-header">
          <span class="statnive-admin-modal-eyebrow">Configure site</span>
          <h2 id="statnive-configure-h">{site.hostname}</h2>
        </header>

        {topError ? (
          <p class="statnive-admin-alert is-error" role="alert">
            <span class="statnive-admin-alert-glyph" aria-hidden="true">{'▪'}</span>
            <span>
              <span class="statnive-admin-alert-label">Error</span>
              <span class="statnive-admin-alert-body">{topError}</span>
            </span>
          </p>
        ) : null}

        <form onSubmit={onSave} noValidate>
          {/* --- IDENTITY ---
              Read-only data renders as a definition list, not five fake
              disabled <input> rows. Collapses ~5 × 80px of editable
              affordance into ~5 × 24px of inline values and removes the
              hover-affordance that misleads on disabled inputs. The
              "what site is this" question gets a single calm panel. */}
          <h3 class="statnive-admin-modal-section">Identity</h3>

          <dl class="statnive-admin-modal-ident">
            <dt>Hostname</dt>
            <dd>{site.hostname}</dd>

            <dt>Slug</dt>
            <dd><code>{site.slug}</code></dd>

            <dt>Plan</dt>
            <dd>{site.plan}</dd>

            <dt>Site ID</dt>
            <dd><code>{String(site.site_id)}</code></dd>

            <dt>Status</dt>
            <dd>
              <StatusPill state={site.enabled ? 'live' : 'paused'} />
            </dd>
          </dl>

          <p class="statnive-admin-modal-helper">
            Hostname, slug, and site ID are fixed at creation. To change a hostname
            without losing tracking continuity, contact support. Status flips with the
            Pause / Resume button on the Sites table row.
          </p>

          {/* --- LOCALE --- */}
          <h3 class="statnive-admin-modal-section">Locale</h3>

          <div class="statnive-admin-modal-field">
            <label for="cfg-currency">Currency</label>
            <select
              id="cfg-currency"
              class={errors.currency ? 'is-invalid' : ''}
              value={draft.currency}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  currency: (e.target as HTMLSelectElement).value,
                }))
              }
            >
              {currencies.length === 0 ? (
                <option value={draft.currency}>{draft.currency}</option>
              ) : null}
              {currencies.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code} {'·'} {c.symbol} {c.name}
                </option>
              ))}
            </select>
            <p class="statnive-admin-modal-helper">
              How revenue is labeled. Does not convert stored values.
            </p>
            {errors.currency ? (
              <p class="statnive-admin-modal-helper" role="alert">{errors.currency}</p>
            ) : null}
          </div>

          <div class="statnive-admin-modal-field">
            <label for="cfg-tz">Timezone</label>
            <select
              id="cfg-tz"
              class={errors.tz ? 'is-invalid' : ''}
              value={draft.tz}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  tz: (e.target as HTMLSelectElement).value,
                }))
              }
            >
              {timezones.length === 0 ? (
                <option value={draft.tz}>{draft.tz}</option>
              ) : null}
              {timezones.map((t) => (
                <option key={t.iana} value={t.iana}>
                  {t.label} ({t.offset})
                </option>
              ))}
            </select>
            <p class="statnive-admin-modal-helper">
              Which zone the dashboard uses for &lsquo;today&rsquo; and
              &lsquo;yesterday&rsquo;.
            </p>
            {errors.tz ? (
              <p class="statnive-admin-modal-helper" role="alert">{errors.tz}</p>
            ) : null}
          </div>

          {/* --- PRIVACY --- */}
          <h3 class="statnive-admin-modal-section">Privacy</h3>

          <div class="statnive-admin-modal-field statnive-admin-modal-field-checkbox">
            <input
              id="cfg-gpc"
              type="checkbox"
              checked={draft.respect_gpc}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  respect_gpc: (e.target as HTMLInputElement).checked,
                }))
              }
            />
            <label for="cfg-gpc">
              Respect Sec-GPC: 1
              <p class="statnive-admin-modal-helper">
                When a visitor&apos;s browser sends the Global Privacy
                Control signal, hide their identity from analytics.
                <strong> EU operators must enable this.</strong>
              </p>
            </label>
          </div>

          <div class="statnive-admin-modal-field statnive-admin-modal-field-checkbox">
            <input
              id="cfg-dnt"
              type="checkbox"
              checked={draft.respect_dnt}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  respect_dnt: (e.target as HTMLInputElement).checked,
                }))
              }
            />
            <label for="cfg-dnt">
              Respect DNT: 1
              <p class="statnive-admin-modal-helper">
                Same as GPC, for the older Do-Not-Track header.
                <strong> EU operators must enable this.</strong>
              </p>
            </label>
          </div>

          <div class="statnive-admin-modal-field statnive-admin-modal-field-checkbox">
            <input
              id="cfg-bots"
              type="checkbox"
              checked={draft.track_bots}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  track_bots: (e.target as HTMLInputElement).checked,
                }))
              }
            />
            <label for="cfg-bots">
              Count bots
              <p class="statnive-admin-modal-helper">
                Bot traffic is recorded with <code>is_bot=1</code> so you
                can split it from human traffic in reports. Turn this off
                to drop bot events entirely.
              </p>
            </label>
          </div>

          {/* --- COMPLIANCE --- */}
          <h3 class="statnive-admin-modal-section">Compliance</h3>

          <div class="statnive-admin-modal-field">
            <label for="cfg-jurisdiction">Jurisdiction</label>
            <select
              id="cfg-jurisdiction"
              class={errors.jurisdiction ? 'is-invalid' : ''}
              value={draft.jurisdiction}
              onChange={(e) =>
                onChangeJurisdiction(
                  (e.target as HTMLSelectElement).value as Jurisdiction,
                )
              }
            >
              {JURISDICTIONS.map((j) => (
                <option key={j} value={j}>
                  {jurisdictionLabel(j)}
                </option>
              ))}
            </select>
            <p class="statnive-admin-modal-helper">
              Which legal regime applies to your visitors. Picking
              <code> EU-GDPR</code> switches the consent flow to
              consent-free by default.
            </p>
            {errors.jurisdiction ? (
              <p class="statnive-admin-modal-helper" role="alert">{errors.jurisdiction}</p>
            ) : null}
          </div>

          <div class="statnive-admin-modal-field">
            <label for="cfg-mode">Consent mode</label>
            <select
              id="cfg-mode"
              class={errors.consent_mode ? 'is-invalid' : ''}
              value={draft.consent_mode}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  consent_mode: (e.target as HTMLSelectElement).value as ConsentMode,
                }))
              }
            >
              {CONSENT_MODES.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
            <p class="statnive-admin-modal-helper">
              <code>consent-free</code> means no banner; identity stays
              anonymous. <code>hybrid</code> means a banner asks; we hash
              identity only after consent. <code>permissive</code> is
              legacy mode; it sets cookies and counts everyone.
              <strong> EU operators should pick consent-free or hybrid.</strong>
            </p>
            {errors.consent_mode ? (
              <p class="statnive-admin-modal-helper" role="alert">{errors.consent_mode}</p>
            ) : null}
          </div>

          {(draft.consent_mode === 'consent-free' || draft.consent_mode === 'hybrid') ? (
            <div class="statnive-admin-modal-field">
              <label for="cfg-allowlist">Event allowlist</label>
              <input
                id="cfg-allowlist"
                type="text"
                class={errors.event_allowlist ? 'is-invalid' : ''}
                placeholder="pageview, click, scroll"
                value={draft.event_allowlist.join(', ')}
                onInput={(e) =>
                  onChangeEventAllowlist((e.target as HTMLInputElement).value)
                }
              />
              <p class="statnive-admin-modal-helper">
                Only events with these names are accepted. Separate names
                with commas. Leave empty to accept every event.
              </p>
              {errors.event_allowlist ? (
                <p class="statnive-admin-modal-helper" role="alert">
                  {errors.event_allowlist}
                </p>
              ) : null}
            </div>
          ) : null}

          {/* --- ALLOWED ORIGINS --- */}
          <h3 class="statnive-admin-modal-section">Allowed origins</h3>

          <div class="statnive-admin-modal-field">
            <p class="statnive-admin-modal-helper">
              Which web addresses can send events to this site. Must start
              with <code>https://</code>. No trailing slash, no path.
              Maximum {MAX_ALLOWED_ORIGINS} entries. The same address
              cannot be on two different sites; you&apos;ll see a conflict
              error if it is.
            </p>

            {originHosts.map((host, idx) => {
              const rowError = errors[`origin_${idx}`];
              return (
                <div
                  key={`origin-${idx}`}
                  class="statnive-admin-modal-field"
                >
                  <div class={'statnive-input-group' + (rowError ? ' is-invalid' : '')}>
                    <span class="statnive-input-prefix" aria-hidden="true">https://</span>
                    <input
                      type="text"
                      aria-label={`allowed origin ${idx + 1}`}
                      placeholder="www.example.com"
                      value={host}
                      onInput={(e) =>
                        setOriginAt(idx, (e.target as HTMLInputElement).value.trim())
                      }
                    />
                    <button
                      type="button"
                      class="statnive-input-remove"
                      aria-label={`remove allowed origin ${idx + 1}`}
                      onClick={() => removeOrigin(idx)}
                    >
                      {'×'}
                    </button>
                  </div>
                  {rowError ? (
                    <p class="statnive-admin-modal-helper" role="alert">
                      {rowError}
                    </p>
                  ) : null}
                </div>
              );
            })}

            <button
              type="button"
              class="statnive-chip"
              disabled={originHosts.length >= MAX_ALLOWED_ORIGINS}
              onClick={addOrigin}
            >
              + Add another
            </button>

            {originHosts.length >= MAX_ALLOWED_ORIGINS ? (
              <p class="statnive-admin-modal-helper" role="alert">
                You&apos;ve reached the cap of {MAX_ALLOWED_ORIGINS} addresses per site.
                Remove one to add another.
              </p>
            ) : null}
          </div>

          <div class="statnive-admin-modal-footer">
            <button type="button" class="is-ghost" onClick={onClose}>
              Cancel
            </button>
            <button
              type="submit"
              class="is-primary"
              disabled={isInvalid || saving || !isDirty}
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
