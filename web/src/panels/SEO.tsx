import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import type { Options, AlignedData } from 'uplot';
import { apiGet } from '../api/client';
import type { SEORow } from '../api/types';
import { rangeSignal } from '../state/range';
import { filtersSignal } from '../state/filters';
import { LazyChart } from '../components/LazyChart';
import './panels.css';

const fmtInt = (n: number) => n.toLocaleString('en-US');
const fmtRials = (n: number) => fmtInt(n) + ' ﷼';

function buildChartData(rows: SEORow[]): AlignedData {
  const xs: number[] = [];
  const ys: number[] = [];
  for (const r of rows) {
    // day is an ISO timestamp string; uPlot wants seconds.
    xs.push(Math.floor(new Date(r.day).getTime() / 1000));
    ys.push(r.visitors);
  }
  return [xs, ys];
}

const CHART_OPTIONS: Omit<Options, 'width' | 'height'> = {
  scales: { x: { time: true }, y: { auto: true } },
  series: [
    {},
    {
      label: 'Visitors',
      stroke: '#00756A',
      width: 2,
    },
  ],
  axes: [{}, {}],
  cursor: { drag: { x: true, y: false } },
};

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
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.device,
    filtersSignal.value.country,
    filtersSignal.value.path,
  ]);

  const chartData = useMemo(() => {
    return data.value ? buildChartData(data.value) : null;
  }, [data.value]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">SEO</h2>
        <p class="statnive-error">could not load — see logs</p>
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

  return (
    <section class="statnive-section" data-testid="panel-seo">
      <h2 class="statnive-h2">SEO</h2>
      {chartData ? <LazyChart data={chartData} options={CHART_OPTIONS} height={240} /> : null}
      <table class="statnive-table" style={{ marginTop: 'var(--s-3)' }}>
        <thead>
          <tr>
            <th scope="col">Day</th>
            <th scope="col">Views</th>
            <th scope="col">Visitors</th>
            <th scope="col">Goals</th>
            <th scope="col">Revenue</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.day}>
              <td>{r.day.slice(0, 10)}</td>
              <td>{fmtInt(r.views)}</td>
              <td>{fmtInt(r.visitors)}</td>
              <td>{fmtInt(r.goals)}</td>
              <td>{fmtRials(r.revenue_rials)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
