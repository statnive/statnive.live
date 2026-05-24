import type { ComponentType } from 'preact';
import type { ChartProps } from './Chart';
import { Loader } from './Loader';
import { makeLazyCache, useLazyImport } from '../lib/lazy';

// LazyChart dynamic-imports Chart.tsx (and its ECharts dep) so ECharts
// does not land in the initial bundle. Every chart-rendering panel
// (Overview TrendChart, SEO, Sources pies, Campaigns pie + bar) uses
// LazyChart so the shared chart chunk is fetched once on first mount
// and cached for every subsequent consumer.

type ChartComponent = ComponentType<ChartProps>;

const cache = makeLazyCache<ChartComponent>();

export function LazyChart(props: ChartProps) {
  const C = useLazyImport(cache, () => import('./Chart').then((m) => m.Chart));
  if (!C) return <Loader />;
  return <C {...props} />;
}
