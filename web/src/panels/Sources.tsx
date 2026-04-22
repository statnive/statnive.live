import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { SourceRow } from '../api/types';
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

export default function Sources() {
  const data = useSignal<SourceRow[] | null>(null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<SourceRow[]>(
          '/api/stats/sources',
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
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.channel,
    filtersSignal.value.device,
    filtersSignal.value.country,
    filtersSignal.value.path,
  ]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Sources</h2>
        <p class="statnive-error">could not load — see logs</p>
      </section>
    );
  }

  const rows = data.value;
  if (!rows) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Sources</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  if (rows.length === 0) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Sources</h2>
        <p class="statnive-empty">No source data for this range / filter.</p>
      </section>
    );
  }

  const maxVisitors = rowMax(rows, (r) => r.visitors);
  const maxRevenue = rowMax(rows, (r) => r.revenue_rials);

  return (
    <section class="statnive-section" data-testid="panel-sources">
      <h2 class="statnive-h2">Sources</h2>
      <table class="statnive-table">
        <thead>
          <tr>
            <th scope="col">Referrer</th>
            <th scope="col">Channel</th>
            <th scope="col">Views</th>
            <th scope="col">Goals</th>
            <th scope="col">RPV (﷼)</th>
            <th scope="col">Visitors / Revenue</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.referrer_name + '|' + r.channel}>
              <td>{r.referrer_name || '(direct)'}</td>
              <td><span class="statnive-channel-chip">{r.channel || '—'}</span></td>
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
    </section>
  );
}
