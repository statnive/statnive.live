import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import {
  activeSiteSignal,
  sitesSignal,
  persistActiveSite,
  loadPersistedSiteId,
  type Site,
} from '../state/site';
import { userSignal } from '../state/auth';
import './SiteSwitcher.css';

interface SitesResponse {
  sites: Site[];
}

export function SiteSwitcher() {
  const err = useSignal<string | null>(null);
  const loading = useSignal(true);

  useEffect(() => {
    const ac = new AbortController();

    (async () => {
      try {
        const r = await apiGet<SitesResponse>('/api/sites', {}, ac.signal);
        const list = r.sites ?? [];
        sitesSignal.value = list;

        if (list.length === 0) {
          activeSiteSignal.value = null;
          return;
        }

        const persisted = loadPersistedSiteId();
        const fromPersist = persisted == null ? null : list.find((s) => s.id === persisted);
        const firstEnabled = list.find((s) => s.enabled) ?? list[0];
        activeSiteSignal.value = fromPersist ?? firstEnabled;
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        err.value = e instanceof Error ? e.message : String(e);
      } finally {
        loading.value = false;
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (err.value) {
    return <span class="statnive-site-switcher statnive-error">sites unavailable</span>;
  }

  if (loading.value) {
    return <span class="statnive-site-switcher statnive-loading">loading sites…</span>;
  }

  const list = sitesSignal.value;
  const active = activeSiteSignal.value;

  if (list.length === 0) {
    if (userSignal.value?.role === 'admin') {
      return (
        <a class="statnive-site-switcher" href="#admin">
          no sites yet; add one
        </a>
      );
    }
    return <span class="statnive-site-switcher">no sites</span>;
  }

  if (list.length === 1) {
    return (
      <span class="statnive-site-switcher" data-testid="site-single">
        {active?.hostname ?? list[0].hostname}
      </span>
    );
  }

  return (
    <label class="statnive-site-switcher">
      <span class="statnive-site-label">Site</span>
      <select
        data-testid="site-select"
        value={active?.id ?? list[0].id}
        onChange={(e) => {
          const id = Number((e.target as HTMLSelectElement).value);
          const next = list.find((s) => s.id === id);
          if (next) {
            activeSiteSignal.value = next;
            persistActiveSite(next.id);
          }
        }}
      >
        {list.map((s) => (
          <option key={s.id} value={s.id} disabled={!s.enabled}>
            {s.hostname}
            {s.enabled ? '' : ' (disabled)'}
          </option>
        ))}
      </select>
    </label>
  );
}
