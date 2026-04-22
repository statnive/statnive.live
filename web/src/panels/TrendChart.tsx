import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { DailyPoint } from '../api/types';
import { rangeSignal } from '../state/range';
import { LazyChart } from '../components/LazyChart';
import { toVisitorSeries, visitorLineChartOptions } from '../lib/chart';

// TrendChart fetches /api/stats/trend and hands it to LazyChart. Used
// by Overview (embedded under the KPI grid) to give an at-a-glance
// visitors trend. Keeps Overview static-importable — uPlot comes in via
// LazyChart's dynamic import on mount.
export function TrendChart() {
  const data = useSignal<DailyPoint[] | null>(null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<DailyPoint[]>(
          '/api/stats/trend',
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
  }, [rangeSignal.value.from, rangeSignal.value.to]);

  const chartData = useMemo(() => (data.value ? toVisitorSeries(data.value as DailyPoint[]) : null), [data.value]);
  const chartOptions = useMemo(() => visitorLineChartOptions(), []);

  if (err.value) return null;
  if (!chartData) return null;

  return (
    <div data-testid="overview-trend" style={{ marginTop: 'var(--s-3)' }}>
      <LazyChart data={chartData} options={chartOptions} height={180} />
    </div>
  );
}
