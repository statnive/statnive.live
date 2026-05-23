import type { EChartsCoreOption } from 'echarts/core';
import { readBrandTokens, type BrandTokens } from '../state/tokens';
import type { DailyPoint, SEORow, SourceChannelRow, CampaignRow } from '../api/types';
import type { MetricId } from '../state/filters';
import { fmtInt, fmtPct, fmtMoney, fmtRpv } from './fmt';

// Mono tick face shared by every chart axis. Hoisted so a font swap
// touches one site instead of N.
const AXIS_FONT = "11px 'JetBrains Mono', ui-monospace, monospace";

// EChartsTheme bundles every brand token a chart option might need.
// Read once per chart mount via readEChartsTheme(); resolved colors
// pass by value into the option object so the brand-grep gate stays
// satisfied (no inline hex in source — only var(--…) reads).
//
// `channels` holds the chip-style --ch-* hues for tags/badges; `pies`
// holds the lighter --pie-* hues used on canvas donuts + the campaign
// rank list. The split keeps high-contrast chip backgrounds (10% alpha
// of a saturated hex) from being forced through pie-saturation tuning.
export interface EChartsTheme extends BrandTokens {
  channels: Record<string, string>;
  pies: Record<string, string>;
  piesFallback: string[];
  pieTrack: string;
}

// CHANNEL_TOKEN drives chip color via --ch-*. Channel taxonomy mirrors
// internal/enrich/channel.go; the FilterPanel UI label "Social Media"
// and the backend "Organic Social" both resolve to the same --ch-social
// hue. Unknown channels fall back to --ink-2.
const CHANNEL_TOKEN: Record<string, string> = {
  Direct: '--ch-direct',
  'Organic Search': '--ch-search',
  Social: '--ch-social',
  'Organic Social': '--ch-social',
  'Social Media': '--ch-social',
  Email: '--ch-email',
  Referral: '--ch-referral',
  AI: '--ch-ai',
  Paid: '--ch-paid',
  Video: '--ch-search',
  'Organic Video': '--ch-search',
};

// CHANNEL_PIE_TOKEN maps the same channel taxonomy onto the lighter
// --pie-* slice palette. Deterministic and stable across charts so
// "Organic Search" reads the same hue whether it appears in the Sources
// views donut, the Sources revenue donut, the Campaigns donut, or the
// Top Campaigns rank list. The list is also the ordering used to assign
// a slot to an unrecognised channel (falls through to the next free
// --pie-* hue rather than ink-2 grey).
const CHANNEL_PIE_TOKEN: Record<string, string> = {
  'Organic Search': '--pie-teal',
  Direct: '--pie-slate',
  'Organic Social': '--pie-sky',
  Social: '--pie-sky',
  'Social Media': '--pie-sky',
  Email: '--pie-peach',
  AI: '--pie-mauve',
  Paid: '--pie-violet',
  Referral: '--pie-olive',
  Video: '--pie-crimson',
  'Organic Video': '--pie-crimson',
};

const PIE_PALETTE_FALLBACK = [
  '--pie-teal',
  '--pie-slate',
  '--pie-sky',
  '--pie-peach',
  '--pie-mauve',
  '--pie-violet',
  '--pie-olive',
  '--pie-crimson',
];

// readEChartsTheme reads brand tokens + channel hues from the live
// :root scope so every option-builder can hand resolved colors to
// ECharts. ECharts does not understand CSS var() strings inside its
// canvas renderer, so we resolve once at mount time. Brand-grep gate
// is satisfied because every value comes from a getPropertyValue read.
// Not a hook — named `read*` to avoid the React `use*` lint contract.
export function readEChartsTheme(): EChartsTheme {
  const tokens = readBrandTokens();
  const channels: Record<string, string> = {};
  const pies: Record<string, string> = {};
  const piesFallback: string[] = [];
  let pieTrack = 'var(--pie-track)';

  if (typeof document !== 'undefined') {
    const root = document.getElementById('statnive-app') ?? document.documentElement;
    const cs = getComputedStyle(root);
    const read = (cssVar: string): string =>
      cs.getPropertyValue(cssVar).trim() || `var(${cssVar})`;

    for (const [channel, cssVar] of Object.entries(CHANNEL_TOKEN)) {
      channels[channel] = read(cssVar);
    }
    for (const [channel, cssVar] of Object.entries(CHANNEL_PIE_TOKEN)) {
      pies[channel] = read(cssVar);
    }
    for (const cssVar of PIE_PALETTE_FALLBACK) {
      piesFallback.push(read(cssVar));
    }
    pieTrack = read('--pie-track');
  } else {
    for (const [channel, cssVar] of Object.entries(CHANNEL_TOKEN)) {
      channels[channel] = `var(${cssVar})`;
    }
    for (const [channel, cssVar] of Object.entries(CHANNEL_PIE_TOKEN)) {
      pies[channel] = `var(${cssVar})`;
    }
    for (const cssVar of PIE_PALETTE_FALLBACK) {
      piesFallback.push(`var(${cssVar})`);
    }
  }

  return { ...tokens, channels, pies, piesFallback, pieTrack };
}

// pieHueForIndex falls back to a positional --pie-* slot when a channel
// is missing from CHANNEL_PIE_TOKEN. Reads from the theme's cached
// fallback palette — no live getComputedStyle calls per slice.
function pieHueForIndex(idx: number, theme: EChartsTheme): string {
  return theme.piesFallback[idx % theme.piesFallback.length];
}

// applyReducedMotion disables ECharts animation when the user has
// requested reduced motion. ECharts has no native prefers-reduced-motion
// hook; every chart consumer must wrap its option through this helper.
export function applyReducedMotion<T extends EChartsCoreOption>(option: T): T {
  if (typeof window === 'undefined') return option;
  if (!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return option;
  return { ...option, animation: false };
}

// MetricSpec is the per-metric contract for the Overview multi-metric
// chart and its KPI cards. ECharts native tooltip handles hover values
// directly, so the formatter functions here are also used by the
// metricsLineOption tooltip formatter.
export interface MetricSpec {
  label: string;
  color: string;
  value: (d: DailyPoint) => number;
  format: (n: number) => string;
}

export type MetricSpecs = Record<MetricId, MetricSpec>;

export function buildMetricSpecs(theme: EChartsTheme, currency: string): MetricSpecs {
  return {
    visitors:   { label: 'Visitors',   color: theme.chartVisitors,   value: (d) => d.visitors, format: fmtInt },
    pageviews:  { label: 'Pageviews',  color: theme.chartPageviews,  value: (d) => d.pageviews, format: fmtInt },
    conversion: { label: 'Conversion', color: theme.chartConversion, value: (d) => (d.visitors > 0 ? (d.goals / d.visitors) * 100 : 0), format: fmtPct },
    revenue:    { label: 'Revenue',    color: theme.chartRevenue,    value: (d) => d.revenue, format: (n) => fmtMoney(n, currency) },
    rpv:        { label: 'RPV',        color: theme.chartRpv,        value: (d) => (d.visitors > 0 ? d.revenue / d.visitors : 0), format: (n) => fmtRpv(n, currency) },
    goals:      { label: 'Goals',      color: theme.chartGoals,      value: (d) => d.goals, format: fmtInt },
  };
}

function axisStyle(theme: EChartsTheme) {
  return {
    axisLine: { lineStyle: { color: theme.ink2 } },
    axisTick: { show: false },
    axisLabel: { color: theme.ink2, fontSize: 11, fontFamily: "'JetBrains Mono', ui-monospace, monospace" },
  };
}

function splitLineStyle(theme: EChartsTheme) {
  return { lineStyle: { color: theme.ruleHair, width: 1, type: 'solid' as const } };
}

// Shared tooltip styling for every chart. ECharts' params union is
// formatter-shape-dependent; callers narrow to the specific shape they
// expect inside the formatter body.
export type TooltipFormatter = (params: unknown) => string;

function tooltipBase(
  theme: EChartsTheme,
  formatter: TooltipFormatter,
  trigger: 'axis' | 'item' = 'item',
) {
  return {
    trigger,
    backgroundColor: theme.ink,
    borderColor: theme.ink,
    textStyle: { color: theme.paper, fontFamily: AXIS_FONT, fontSize: 11 },
    formatter,
    ...(trigger === 'axis'
      ? { axisPointer: { type: 'line' as const, lineStyle: { color: theme.ink2 } } }
      : {}),
  };
}

// pieHueForChannel resolves a channel name to a slice color from the
// --pie-* palette. Channels missing from CHANNEL_PIE_TOKEN fall back
// to a positional --pie-* slot based on their data-array index so an
// unrecognised channel never renders as ink-2 grey.
export function pieHueForChannel(
  channel: string,
  idx: number,
  theme: EChartsTheme,
): string {
  return theme.pies[channel] ?? pieHueForIndex(idx, theme);
}

// pieOption is the shared donut shell used by viewsPieOption,
// revenuePieOption, and campaignsPieOption. Donut form (radius
// ['55%', '85%']) leaves a generous center hole so an HTML overlay
// can render the metric label + total inside the chart. Each caller
// produces the `data` array (with per-slice color) + a tooltip
// formatter + an aria description.
export const PIE_RADIUS: [string, string] = ['55%', '85%'];

interface PieData {
  name: string;
  value: number;
  itemStyle: { color: string };
}

function pieOption(
  theme: EChartsTheme,
  data: PieData[],
  tooltipFormatter: TooltipFormatter,
  ariaDescription: string,
): EChartsCoreOption {
  return {
    series: [
      {
        type: 'pie',
        radius: PIE_RADIUS,
        center: ['50%', '50%'],
        data,
        label: { show: false },
        labelLine: { show: false },
        emphasis: {
          scale: true,
          scaleSize: 4,
          itemStyle: { borderWidth: 2, borderColor: theme.ink },
        },
        itemStyle: { borderWidth: 2, borderColor: theme.paper2 },
      },
    ],
    legend: { show: false },
    tooltip: tooltipBase(theme, tooltipFormatter, 'item'),
    aria: { show: true, label: { description: ariaDescription } },
  };
}

// visitorLineOption builds the ECharts option for a single-line
// visitor trend (used by the SEO panel). Brand navy stroke, light
// teal area fill, hairline y-grid, mono tick labels.
export function visitorLineOption(rows: SEORow[], theme: EChartsTheme): EChartsCoreOption {
  return {
    grid: { left: 48, right: 16, top: 16, bottom: 28 },
    xAxis: { type: 'time', ...axisStyle(theme) },
    yAxis: { type: 'value', ...axisStyle(theme), splitLine: splitLineStyle(theme) },
    series: [
      {
        type: 'line',
        name: 'Visitors',
        data: rows.map((r) => [r.day, r.visitors]),
        lineStyle: { color: theme.chartVisitors, width: 2 },
        itemStyle: { color: theme.chartVisitors },
        symbol: 'none',
        areaStyle: { color: theme.chartVisitorsFillWash },
      },
    ],
    tooltip: tooltipBase(
      theme,
      (params) => {
        const arr = params as Array<{ value: [string, number] }>;
        const v = arr[0]?.value;
        if (!v) return '';
        return `${new Date(v[0]).toLocaleDateString()}<br/>${fmtInt(v[1])} visitors`;
      },
      'axis',
    ),
    aria: { show: true, label: { description: 'Daily visitors trend' } },
  };
}

// metricsLineOption builds the option for the Overview multi-metric
// trend chart. Each metric gets its own yAxis (independent scales);
// the chart container shows axes hidden so the canvas reads cleanly.
// Single-visitors mode reuses the area-fill wash from visitorLineOption.
export function metricsLineOption(
  rows: DailyPoint[],
  metrics: readonly MetricId[],
  specs: MetricSpecs,
  theme: EChartsTheme,
): EChartsCoreOption {
  const isSingleVisitors = metrics.length === 1 && metrics[0] === 'visitors';

  return {
    grid: { left: 48, right: 16, top: 16, bottom: 28 },
    xAxis: { type: 'time', ...axisStyle(theme) },
    yAxis: metrics.map((_m, i) => ({
      type: 'value' as const,
      show: i === 0,
      ...axisStyle(theme),
      splitLine: i === 0 ? splitLineStyle(theme) : { show: false },
      scale: true,
    })),
    series: metrics.map((m, i) => {
      const spec = specs[m];
      return {
        type: 'line',
        name: spec.label,
        yAxisIndex: i,
        data: rows.map((r) => [r.day, spec.value(r)]),
        lineStyle: { color: spec.color, width: 2 },
        itemStyle: { color: spec.color },
        symbol: 'none',
        areaStyle: isSingleVisitors ? { color: theme.chartVisitorsFillWash } : undefined,
      };
    }),
    legend: { show: false },
    tooltip: tooltipBase(
      theme,
      (params) => {
        const arr = params as Array<{ value: [string, number]; seriesName: string; color: string }>;
        if (!arr.length) return '';
        const date = new Date(arr[0].value[0]).toLocaleDateString();
        const lines = arr.map((p, i) => {
          const spec = specs[metrics[i]];
          const dot = `<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:${p.color};margin-right:6px;"></span>`;
          return `${dot}${p.seriesName}: ${spec.format(p.value[1])}`;
        });
        return `${date}<br/>${lines.join('<br/>')}`;
      },
      'axis',
    ),
    aria: { show: true, label: { description: 'Multi-metric trend chart' } },
  };
}

type PieParams = { name: string; value: number; percent: number };

// viewsPieOption builds the pie chart for views by channel (Sources
// panel). radius is fixed at '70%' (full pie, no inner hole) by the
// shared pieOption helper; totals appear in an eyebrow above the
// chart, not in a donut center.
export function viewsPieOption(by_channel: SourceChannelRow[], theme: EChartsTheme): EChartsCoreOption {
  const data: PieData[] = by_channel
    .filter((r) => r.views > 0)
    .map((r, i) => ({
      name: r.channel,
      value: r.views,
      itemStyle: { color: pieHueForChannel(r.channel, i, theme) },
    }));
  return pieOption(
    theme,
    data,
    (params) => {
      const p = params as PieParams;
      return `${p.name}: ${fmtInt(p.value)} (${p.percent.toFixed(1)}%)`;
    },
    'Views by channel donut chart',
  );
}

// revenuePieOption mirrors viewsPieOption but plots revenue values.
export function revenuePieOption(
  by_channel: SourceChannelRow[],
  theme: EChartsTheme,
  currency: string,
): EChartsCoreOption {
  const data: PieData[] = by_channel
    .filter((r) => r.revenue > 0)
    .map((r, i) => ({
      name: r.channel,
      value: r.revenue,
      itemStyle: { color: pieHueForChannel(r.channel, i, theme) },
    }));
  return pieOption(
    theme,
    data,
    (params) => {
      const p = params as PieParams;
      return `${p.name}: ${fmtMoney(p.value, currency)} (${p.percent.toFixed(1)}%)`;
    },
    'Revenue by channel donut chart',
  );
}

// campaignsPieOption aggregates campaign rows by channel and returns
// the pie option for the Campaigns panel. `valueOf` lets the caller
// choose revenue (the normal case) or visitors (the fallback when the
// tenant has zero recorded revenue across all campaigns).
export function campaignsPieOption(
  rows: CampaignRow[],
  theme: EChartsTheme,
  valueOf: (r: CampaignRow) => number,
  formatValue: (n: number) => string,
): EChartsCoreOption {
  const byChannel = new Map<string, number>();
  for (const r of rows) {
    byChannel.set(r.channel, (byChannel.get(r.channel) ?? 0) + valueOf(r));
  }
  const data: PieData[] = Array.from(byChannel.entries())
    .filter(([, v]) => v > 0)
    .map(([channel, value], i) => ({
      name: channel,
      value,
      itemStyle: { color: pieHueForChannel(channel, i, theme) },
    }));
  return pieOption(
    theme,
    data,
    (params) => {
      const p = params as PieParams;
      return `${p.name}: ${formatValue(p.value)} (${p.percent.toFixed(1)}%)`;
    },
    'Revenue or visitors by channel for campaigns',
  );
}

// topCampaignsRanked sorts campaigns by revenue and projects them to a
// shape the CampaignCharts rank list can render directly. Each row
// carries its rank, label, value, percent-of-max, and the resolved hue
// from the --pie-* palette so the bar fill matches the channel pie above.
export interface RankedCampaign {
  rank: number;
  label: string;
  value: number;
  pctOfMax: number;
  color: string;
}

export function topCampaignsRanked(
  rows: CampaignRow[],
  theme: EChartsTheme,
  limit = 8,
): RankedCampaign[] {
  const sorted = [...rows].sort((a, b) => b.revenue - a.revenue).slice(0, limit);
  const max = sorted.length > 0 ? sorted[0].revenue : 0;
  return sorted.map((r, i) => ({
    rank: i + 1,
    label: campaignLabel(r),
    value: r.revenue,
    pctOfMax: max > 0 ? (r.revenue / max) * 100 : 0,
    color: pieHueForChannel(r.channel, i, theme),
  }));
}

// campaignLabel composes the rank-list label from utm_campaign /
// utm_source / utm_medium so operators can disambiguate two campaigns
// that share a name. Missing parts are skipped — never rendered as '·'
// or empty parens.
function campaignLabel(r: CampaignRow): string {
  const parts = [r.utm_campaign, r.utm_source, r.utm_medium]
    .map((p) => (p ?? '').trim())
    .filter((p) => p.length > 0);
  if (parts.length === 0) return '(none)';
  return parts.join(' · ');
}
