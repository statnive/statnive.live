import type { Options, AlignedData } from 'uplot';
import { readBrandTokens } from '../state/tokens';

// toVisitorSeries converts any row type with a `day` ISO string and a
// `visitors` count into uPlot's AlignedData shape ([xs, ys]) with x in
// seconds. Shared by SEO (SEORow) and Overview's TrendChart (DailyPoint).
export function toVisitorSeries<T extends { day: string; visitors: number }>(
  rows: T[],
): AlignedData {
  const xs: number[] = [];
  const ys: number[] = [];
  for (const r of rows) {
    xs.push(Math.floor(new Date(r.day).getTime() / 1000));
    ys.push(r.visitors);
  }
  return [xs, ys];
}

// visitorLineChartOptions builds the uPlot options block for a single-
// line visitors chart using the live --green brand token as stroke.
// Call from a useMemo so getComputedStyle runs once per mount, not every
// render.
export function visitorLineChartOptions(): Omit<Options, 'width' | 'height'> {
  const tokens = readBrandTokens();
  return {
    scales: { x: { time: true }, y: { auto: true } },
    series: [
      {},
      { label: 'Visitors', stroke: tokens.green, width: 2 },
    ],
    axes: [{}, {}],
    cursor: { drag: { x: true, y: false } },
  };
}
