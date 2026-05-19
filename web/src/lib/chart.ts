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
// trend chart. Each metric gets its own auto-scale so a 3,479-visitors
// line and a 1.15% conversion line are both visible on their own y-band.
// Y-axis ticks stay hidden — the ChartTooltip rendered alongside reads
// per-day values on hover and formats each via spec.format.
//
// Per-series cursor points (size 6 ring in the series stroke) appear at
// the cursor's x so the user can still see exactly where each metric is
// in the rare cases that two lines happen to overlap visually.
export function metricsLineChartOptions(
  metrics: readonly MetricId[],
  specs: MetricSpecs,
  tokens: BrandTokens,
): Omit<Options, 'width' | 'height'> {
  const isSingleVisitors = metrics.length === 1 && metrics[0] === 'visitors';
  const scales: Options['scales'] = { x: { time: true } };
  for (const m of metrics) scales![m] = { auto: true };
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
        points: {
          show: true,
          size: 6,
          stroke: specs[m].stroke,
          fill: tokens.paper2,
          width: 2,
        },
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
