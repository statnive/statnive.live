import type { ComponentType } from 'preact';
import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import type { ChartProps } from './Chart';
import { Loader } from './Loader';

// LazyChart dynamic-imports Chart.tsx (and its ECharts dep) so ECharts
// does not land in the initial bundle. Every chart-rendering panel
// (Overview TrendChart, SEO, Sources pies, Campaigns pie + bar) uses
// LazyChart so the shared chart chunk is fetched once on first mount
// and cached for every subsequent consumer.

type ChartComponent = ComponentType<ChartProps>;

let cached: ChartComponent | null = null;

export function LazyChart(props: ChartProps) {
  const comp = useSignal<ChartComponent | null>(cached);

  useEffect(() => {
    if (cached) {
      comp.value = cached;
      return;
    }
    let cancelled = false;
    void import('./Chart').then((mod) => {
      cached = mod.Chart;
      if (!cancelled) comp.value = mod.Chart;
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const C = comp.value;
  if (!C) return <Loader />;
  return <C {...props} />;
}
