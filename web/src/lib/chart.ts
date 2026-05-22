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
export interface EChartsTheme extends BrandTokens {
  channels: Record<string, string>;
}

// Channel taxonomy mirror of internal/enrich/channel.go. UI labels
// "Social Media" and "Organic Social" both resolve to the backend
// "Social" hue. Unknown channels fall back to --ink-2.
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

// readEChartsTheme reads brand tokens + channel hues from the live
// :root scope so every option-builder can hand resolved colors to
// ECharts. ECharts does not understand CSS var() strings inside its
// canvas renderer, so we resolve once at mount time. Brand-grep gate
// is satisfied because every value comes from a getPropertyValue read.
// Not a hook — named `read*` to avoid the React `use*` lint contract.
export function readEChartsTheme(): EChartsTheme {
  const tokens = readBrandTokens();
  const channels: Record<string, string> = {};

  if (typeof document !== 'undefined') {
    const root = document.getElementById('statnive-app') ?? document.documentElement;
    const cs = getComputedStyle(root);
    for (const [channel, cssVar] of Object.entries(CHANNEL_TOKEN)) {
      const v = cs.getPropertyValue(cssVar).trim();
      channels[channel] = v || `var(${cssVar})`;
    }
  } else {
    for (const [channel, cssVar] of Object.entries(CHANNEL_TOKEN)) {
      channels[channel] = `var(${cssVar})`;
    }
  }

  return { ...tokens, channels };
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

// channelHue resolves a channel name to its ECharts color, falling
// back to --ink-2 for unknown channels.
function channelHue(channel: string, theme: EChartsTheme): string {
  return theme.channels[channel] ?? theme.ink2;
}

// pieOption is the shared pie-chart shell used by viewsPieOption,
// revenuePieOption, and campaignsPieOption. Each caller produces the
// `data` array (with per-slice color) + a tooltip formatter + an aria
// description; the rest of the option (radius, center, label visibility,
// emphasis state, legend) is identical across all three.
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
        radius: '70%',
        center: ['50%', '50%'],
        data,
        label: { show: false },
        labelLine: { show: false },
        emphasis: { scale: false, itemStyle: { borderWidth: 1, borderColor: theme.ink } },
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
    .map((r) => ({
      name: r.channel,
      value: r.views,
      itemStyle: { color: channelHue(r.channel, theme) },
    }));
  return pieOption(
    theme,
    data,
    (params) => {
      const p = params as PieParams;
      return `${p.name}: ${fmtInt(p.value)} (${p.percent.toFixed(1)}%)`;
    },
    'Views by channel pie chart',
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
    .map((r) => ({
      name: r.channel,
      value: r.revenue,
      itemStyle: { color: channelHue(r.channel, theme) },
    }));
  return pieOption(
    theme,
    data,
    (params) => {
      const p = params as PieParams;
      return `${p.name}: ${fmtMoney(p.value, currency)} (${p.percent.toFixed(1)}%)`;
    },
    'Revenue by channel pie chart',
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
    .map(([channel, value]) => ({
      name: channel,
      value,
      itemStyle: { color: channelHue(channel, theme) },
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

// topCampaignsBarOption builds the horizontal bar chart for top-8
// campaigns by revenue. Per-bar color comes from the campaign's
// channel hue.
export function topCampaignsBarOption(
  rows: CampaignRow[],
  theme: EChartsTheme,
  currency: string,
  limit = 8,
): EChartsCoreOption {
  const sorted = [...rows].sort((a, b) => b.revenue - a.revenue).slice(0, limit);
  const labels = sorted.map((r) => r.utm_campaign || '(none)');
  const values = sorted.map((r) => ({
    value: r.revenue,
    itemStyle: { color: channelHue(r.channel, theme) },
  }));

  return {
    grid: { left: 140, right: 24, top: 8, bottom: 24 },
    xAxis: { type: 'value', ...axisStyle(theme), splitLine: splitLineStyle(theme) },
    yAxis: {
      type: 'category',
      data: labels,
      inverse: true,
      ...axisStyle(theme),
      axisLabel: { ...axisStyle(theme).axisLabel, width: 130, overflow: 'truncate' },
    },
    series: [
      {
        type: 'bar',
        data: values,
        barCategoryGap: '40%',
      },
    ],
    tooltip: tooltipBase(theme, (params) => {
      const p = params as { name: string; value: number };
      return `${p.name}: ${fmtMoney(p.value, currency)}`;
    }),
    aria: { show: true, label: { description: 'Top campaigns by revenue' } },
  };
}
