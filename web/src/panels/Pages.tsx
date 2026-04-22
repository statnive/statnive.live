import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { PageRow } from '../api/types';
import { rangeSignal } from '../state/range';
import { filtersSignal } from '../state/filters';
import { DualBar } from './DualBar';
import './panels.css';

const fmtInt = (n: number) => n.toLocaleString('en-US');

function rowMax<T>(rows: T[], pick: (r: T) => number): number {
  let m = 0;
  for (const r of rows) {
    const v = pick(r);
    if (v > m) m = v;
  }
  return m;
}

export default function Pages() {
  const data = useSignal<PageRow[] | null>(null);
  const err = useSignal<string | null>(null);
  const limit = useSignal(20);

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<PageRow[]>(
          '/api/stats/pages',
          { from: r.from, to: r.to, limit: String(limit.value) },
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
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.path,
    filtersSignal.value.channel,
    filtersSignal.value.device,
    filtersSignal.value.country,
    limit.value,
  ]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Pages</h2>
        <p class="statnive-error">could not load — see logs</p>
      </section>
    );
  }

  const rows = data.value;
  if (!rows) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Pages</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  if (rows.length === 0) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Pages</h2>
        <p class="statnive-empty">No page data for this range / filter.</p>
      </section>
    );
  }

  const maxVisitors = rowMax(rows, (r) => r.visitors);
  const maxRevenue = rowMax(rows, (r) => r.revenue_rials);

  return (
    <section class="statnive-section" data-testid="panel-pages">
      <h2 class="statnive-h2">Pages</h2>
      <table class="statnive-table">
        <thead>
          <tr>
            <th scope="col">Pathname</th>
            <th scope="col">Views</th>
            <th scope="col">Goals</th>
            <th scope="col">RPV (﷼)</th>
            <th scope="col">Visitors / Revenue</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.pathname}>
              <td>{r.pathname}</td>
              <td>{fmtInt(r.views)}</td>
              <td>{fmtInt(r.goals)}</td>
              <td>{fmtInt(Math.round(r.rpv_rials))}</td>
              <td>
                <DualBar
                  visitors={r.visitors}
                  revenue={r.revenue_rials}
                  maxVisitors={maxVisitors}
                  maxRevenue={maxRevenue}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {limit.value < 100 ? (
        <button
          type="button"
          class="statnive-chip"
          onClick={() => { limit.value = 100; }}
          style={{ marginTop: 'var(--s-2)' }}
        >
          Show all (up to 100)
        </button>
      ) : null}
    </section>
  );
}
