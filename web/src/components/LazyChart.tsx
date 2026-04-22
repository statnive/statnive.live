import type { ComponentType } from 'preact';
import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import type { ChartProps } from './Chart';
import { Loader } from './Loader';

// LazyChart dynamic-imports Chart.tsx (and its uPlot dep) so uPlot
// doesn't land in the initial bundle. Both Overview (static panel) and
// SEO (lazy panel) use LazyChart — the shared dynamic Chart chunk is
// fetched once, the second consumer hits the cache.
//
// The SPA ships ~7 KB gz of uPlot only when a chart mounts.

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
