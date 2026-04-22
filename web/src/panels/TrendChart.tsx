import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import type { Options, AlignedData } from 'uplot';
import { apiGet } from '../api/client';
import type { DailyPoint } from '../api/types';
import { rangeSignal } from '../state/range';
import { LazyChart } from '../components/LazyChart';

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

function toAligned(rows: DailyPoint[]): AlignedData {
  const xs: number[] = [];
  const ys: number[] = [];
  for (const r of rows) {
    xs.push(Math.floor(new Date(r.day).getTime() / 1000));
    ys.push(r.visitors);
  }
  return [xs, ys];
}

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

  const chartData = useMemo(() => (data.value ? toAligned(data.value) : null), [data.value]);

  if (err.value) return null;
  if (!chartData) return null;

  return (
    <div data-testid="overview-trend" style={{ marginTop: 'var(--s-3)' }}>
      <LazyChart data={chartData} options={CHART_OPTIONS} height={180} />
    </div>
  );
}
