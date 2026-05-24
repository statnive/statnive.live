import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { SEORow } from '../api/types';
import { rangeSignal } from '../state/range';
import { dirOf, filtersSignal } from '../state/filters';
import { siteSignal, activeSiteSignal } from '../state/site';
import { LazyChart } from '../components/LazyChart';
import { SortHeader } from '../components/SortHeader';
import { fmtInt, fmtMoney } from '../lib/fmt';
import { applyReducedMotion, readEChartsTheme, visitorLineOption } from '../lib/chart';
import './panels.css';

export default function SEO() {
  const data = useSignal<SEORow[] | null>(null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<SEORow[]>(
          '/api/stats/seo',
          { from: r.from, to: r.to },
          ac.signal,
        );
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        err.value = e instanceof Error ? e.message : String(e);
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    siteSignal.value,
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.device,
    filtersSignal.value.country,
    filtersSignal.value.path,
  ]);

  const theme = useMemo(() => readEChartsTheme(), []);
  const option = useMemo(() => {
    return data.value && data.value.length > 0
      ? applyReducedMotion(visitorLineOption(data.value as SEORow[], theme))
      : null;
  }, [data.value, theme]);

  // Sort client-side so the WITH FILL trend chart above keeps one row per day.
  const sortedRows = useMemo(() => {
    const rows = data.value ?? [];
    const f = filtersSignal.value;
    if (!f.sort) return rows;
    const sign = dirOf(f) === 'asc' ? 1 : -1;
    const key = f.sort as keyof SEORow;
    return [...rows].sort((a, b) => {
      const av = a[key];
      const bv = b[key];
      if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * sign;
      return String(av).localeCompare(String(bv)) * sign;
    });
  }, [data.value, filtersSignal.value.sort, filtersSignal.value.dir]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">SEO</h2>
        <p class="statnive-error">could not load; see logs</p>
      </section>
    );
  }

  const rows = data.value;
  if (!rows) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">SEO</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  if (rows.length === 0) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">SEO</h2>
        <p class="statnive-empty">No organic-search data for this range.</p>
      </section>
    );
  }

  const currency = activeSiteSignal.value?.currency ?? 'EUR';

  return (
    <section class="statnive-section" data-testid="panel-seo">
      <h2 class="statnive-h2">SEO</h2>
      {option ? <LazyChart option={option} height={240} /> : null}
      <table class="statnive-table" style={{ marginTop: 'var(--s-3)' }}>
        <thead>
          <tr>
            <SortHeader label="Day" column="day" />
            <SortHeader label="Views" column="views" />
            <SortHeader label="Visitors" column="visitors" />
            <SortHeader label="Goals" column="goals" />
            <SortHeader label="Revenue" column="revenue" />
          </tr>
        </thead>
        <tbody>
          {sortedRows.map((r) => (
            <tr key={r.day}>
              <td>{r.day.slice(0, 10)}</td>
              <td>{fmtInt(r.views)}</td>
              <td>{fmtInt(r.visitors)}</td>
              <td>{fmtInt(r.goals)}</td>
              <td>{fmtMoney(r.revenue, currency)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
