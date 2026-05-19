import type { Options, AlignedData } from 'uplot';
import { readBrandTokens, type BrandTokens } from '../state/tokens';
import type { DailyPoint } from '../api/types';
import type { MetricId } from '../state/filters';
import { fmtInt, fmtPct, fmtMoney, fmtRpv } from './fmt';

// toVisitorSeries converts any row type with a `day` ISO string and a
// `visitors` count into uPlot's AlignedData shape ([xs, ys]) with x in
// seconds. Used by SEO (SEORow has no goals/revenue, so toMetricSeries
// below cannot replace it).
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
// line visitors chart. Uses `--chart-visitors` (brand navy) as stroke
// with a light teal area fill, hairline y-grid, and mono tick labels.
// Call from a useMemo so getComputedStyle runs once per mount, not
// every render.
export function visitorLineChartOptions(): Omit<Options, 'width' | 'height'> {
  const tokens = readBrandTokens();
  return {
    scales: { x: { time: true }, y: { auto: true } },
    series: [
      {},
      {
        label: 'Visitors',
        stroke: tokens.chartVisitors,
        width: 2,
        fill: tokens.chartVisitorsFillWash,
        points: { show: false },
      },
    ],
    axes: [
      {
        stroke: tokens.ink2,
        grid: { show: false },
        ticks: { show: false },
        font: `11px 'JetBrains Mono', ui-monospace, monospace`,
      },
      {
        stroke: tokens.ink2,
        grid: { stroke: tokens.ruleHair, width: 1 },
        ticks: { show: false },
        font: `11px 'JetBrains Mono', ui-monospace, monospace`,
      },
    ],
    cursor: { drag: { x: true, y: false } },
  };
}

export interface MetricSpec {
  label: string;
  stroke: string;
  value: (d: DailyPoint) => number;
  format: (n: number) => string;
}

export type MetricSpecs = Record<MetricId, MetricSpec>;

export function buildMetricSpecs(tokens: BrandTokens, currency: string): MetricSpecs {
  return {
    visitors:   { label: 'Visitors',   stroke: tokens.chartVisitors,   value: (d) => d.visitors,                                                          format: fmtInt },
    pageviews:  { label: 'Pageviews',  stroke: tokens.chartPageviews,  value: (d) => d.pageviews,                                                         format: fmtInt },
    conversion: { label: 'Conversion', stroke: tokens.chartConversion, value: (d) => (d.visitors > 0 ? (d.goals / d.visitors) * 100 : 0),                  format: fmtPct },
    revenue:    { label: 'Revenue',    stroke: tokens.chartRevenue,    value: (d) => d.revenue,                                                           format: (n) => fmtMoney(n, currency) },
    rpv:        { label: 'RPV',        stroke: tokens.chartRpv,        value: (d) => (d.visitors > 0 ? d.revenue / d.visitors : 0),                       format: (n) => fmtRpv(n, currency) },
    goals:      { label: 'Goals',      stroke: tokens.chartGoals,      value: (d) => d.goals,                                                             format: fmtInt },
  };
}

// toMetricSeries projects rows into uPlot AlignedData: [xs, ys_0, ys_1, ...].
// Conversion / RPV are derived per-day so they share the divide-by-zero
// rule with the headline KPI tiles.
export function toMetricSeries(
  rows: DailyPoint[],
  metrics: readonly MetricId[],
  specs: MetricSpecs,
): AlignedData {
  const xs = rows.map((r) => Math.floor(new Date(r.day).getTime() / 1000));
  const series = metrics.map((m) => rows.map((r) => specs[m].value(r)));
  return [xs, ...series] as AlignedData;
}

// metricsLineChartOptions builds uPlot options for the multi-series
// trend chart. Single-metric mode uses a plain auto-scale and the
// visitors teal fill wash. Multi-metric mode stacks each series into
// its own vertical swimlane (top-to-bottom, 1/n-tall band per metric)
// by padding the scale outside the data range, so lines never overlap
// even when underlying shapes are identical (e.g. sparse data with a
// single spike day).
//
// Y-axis ticks stay hidden — every metric has a different scale, so
// one set of numbers would be misleading. The ChartTooltip rendered
// alongside reads per-day values on hover and formats each via
// spec.format.
export function metricsLineChartOptions(
  metrics: readonly MetricId[],
  specs: MetricSpecs,
  tokens: BrandTokens,
): Omit<Options, 'width' | 'height'> {
  const n = metrics.length;
  const isSingleVisitors = n === 1 && metrics[0] === 'visitors';
  const scales: Options['scales'] = { x: { time: true } };
  metrics.forEach((m, i) => {
    if (n === 1) {
      scales![m] = { auto: true };
      return;
    }
    // Swimlane: keep `auto: true` so uPlot computes the data range,
    // then `range` expands the scale by adding empty space below/above.
    // The data band then renders in a 1/n-tall slice of the chart
    // height; metric i sits in the i-th band from the top.
    scales![m] = {
      auto: true,
      range: (_u, dataMin, dataMax) => {
        const lo = Number.isFinite(dataMin) ? dataMin : 0;
        const hi = Number.isFinite(dataMax) ? dataMax : 1;
        const span = (hi - lo) || 1;
        const bandsAbove = i;
        const bandsBelow = n - 1 - i;
        return [lo - bandsBelow * span, hi + bandsAbove * span];
      },
    };
  });
  return {
    scales,
    series: [
      {},
      ...metrics.map((m) => ({
        label: specs[m].label,
        stroke: specs[m].stroke,
        width: 2,
        fill: isSingleVisitors ? tokens.chartVisitorsFillWash : undefined,
        scale: m,
        // Always-on data-point markers feel busy on a calm panel; uPlot
        // still renders a cursor marker per series on hover, which is
        // the affordance we actually need.
        points: { show: false },
      })),
    ],
    axes: [
      {
        stroke: tokens.ink2,
        grid: { show: false },
        ticks: { show: false },
        font: `11px 'JetBrains Mono', ui-monospace, monospace`,
      },
      { show: false, scale: metrics[0] },
    ],
    cursor: { drag: { x: true, y: false } },
    legend: { show: false },
  };
}
