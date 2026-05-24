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
  // Darkened (22% toward --ink) variants of the pie palette, resolved
  // through a probe so ECharts canvas gets a baked rgb()/oklch() string
  // rather than relying on Canvas2D's color-mix() parsing.
  piesDark: Record<string, string>;
  piesDarkFallback: string[];
  pieTrack: string;
  // Darkened multi-metric line colors. Same 22%-toward-ink rule; used
  // by series-level emphasis.lineStyle.color so hover never fades to
  // white. One entry per MetricId.
  chartDark: {
    visitors: string;
    pageviews: string;
    conversion: string;
    revenue: string;
    rpv: string;
    goals: string;
  };
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
// DARKEN_TOWARD_INK_PCT controls hover-state contrast against the
// resting hue. 22% pulls each color toward --ink without flipping the
// hue. Matches WCAG 1.4.11 non-text-contrast (≥3:1) against every
// resting --pie-* token at the OKLCH lightness it ships at.
const DARKEN_TOWARD_INK_PCT = 22;

const CHART_METRIC_TOKEN: Record<keyof EChartsTheme['chartDark'], string> = {
  visitors: '--chart-visitors',
  pageviews: '--chart-pageviews',
  conversion: '--chart-conversion',
  revenue: '--chart-revenue',
  rpv: '--chart-rpv',
  goals: '--chart-goals',
};

export function readEChartsTheme(): EChartsTheme {
  const tokens = readBrandTokens();
  const channels: Record<string, string> = {};
  const pies: Record<string, string> = {};
  const piesFallback: string[] = [];
  const piesDark: Record<string, string> = {};
  const piesDarkFallback: string[] = [];
  let pieTrack = 'var(--pie-track)';
  const chartDark: EChartsTheme['chartDark'] = {
    visitors: '',
    pageviews: '',
    conversion: '',
    revenue: '',
    rpv: '',
    goals: '',
  };

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

    // Resolve color-mix() through a probe so ECharts canvas always sees
    // a real rgb()/oklch() string. Canvas2D fillStyle's own color-mix
    // support is patchy (broken on Safari ≤16.3). One probe per theme
    // read amortises across every chart in this mount.
    const probe = document.createElement('span');
    probe.style.cssText = 'position:absolute;visibility:hidden;pointer-events:none;';
    root.appendChild(probe);
    try {
      const resolveDark = (cssVar: string): string => {
        probe.style.color = `color-mix(in oklab, var(${cssVar}) ${100 - DARKEN_TOWARD_INK_PCT}%, var(--ink))`;
        const v = getComputedStyle(probe).color;
        return v && v !== 'rgba(0, 0, 0, 0)' ? v : read(cssVar);
      };
      for (const [channel, cssVar] of Object.entries(CHANNEL_PIE_TOKEN)) {
        piesDark[channel] = resolveDark(cssVar);
      }
      for (const cssVar of PIE_PALETTE_FALLBACK) {
        piesDarkFallback.push(resolveDark(cssVar));
      }
      for (const key of Object.keys(CHART_METRIC_TOKEN) as Array<keyof typeof CHART_METRIC_TOKEN>) {
        chartDark[key] = resolveDark(CHART_METRIC_TOKEN[key]);
      }
    } finally {
      root.removeChild(probe);
    }
  } else {
    const darkMix = (cssVar: string): string =>
      `color-mix(in oklab, var(${cssVar}) ${100 - DARKEN_TOWARD_INK_PCT}%, var(--ink))`;
    for (const [channel, cssVar] of Object.entries(CHANNEL_TOKEN)) {
      channels[channel] = `var(${cssVar})`;
    }
    for (const [channel, cssVar] of Object.entries(CHANNEL_PIE_TOKEN)) {
      pies[channel] = `var(${cssVar})`;
      // jsdom path — distinguishable from the resting hue so the
      // emphasis-color test can assert dark !== base without paint.
      piesDark[channel] = darkMix(cssVar);
    }
    for (const cssVar of PIE_PALETTE_FALLBACK) {
      piesFallback.push(`var(${cssVar})`);
      piesDarkFallback.push(darkMix(cssVar));
    }
    for (const key of Object.keys(CHART_METRIC_TOKEN) as Array<keyof typeof CHART_METRIC_TOKEN>) {
      chartDark[key] = darkMix(CHART_METRIC_TOKEN[key]);
    }
  }

  return {
    ...tokens,
    channels,
    pies,
    piesFallback,
    piesDark,
    piesDarkFallback,
    pieTrack,
    chartDark,
  };
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
  // darkColor is the hover-state color (22% toward --ink). Suppresses
  // ECharts' default brightening so the hovered line never fades to
  // white against the off-white paper background.
  darkColor: string;
  value: (d: DailyPoint) => number;
  format: (n: number) => string;
}

export type MetricSpecs = Record<MetricId, MetricSpec>;

export function buildMetricSpecs(theme: EChartsTheme, currency: string): MetricSpecs {
  return {
    visitors:   { label: 'Visitors',   color: theme.chartVisitors,   darkColor: theme.chartDark.visitors,   value: (d) => d.visitors, format: fmtInt },
    pageviews:  { label: 'Pageviews',  color: theme.chartPageviews,  darkColor: theme.chartDark.pageviews,  value: (d) => d.pageviews, format: fmtInt },
    conversion: { label: 'Conversion', color: theme.chartConversion, darkColor: theme.chartDark.conversion, value: (d) => (d.visitors > 0 ? (d.goals / d.visitors) * 100 : 0), format: fmtPct },
    revenue:    { label: 'Revenue',    color: theme.chartRevenue,    darkColor: theme.chartDark.revenue,    value: (d) => d.revenue, format: (n) => fmtMoney(n, currency) },
    rpv:        { label: 'RPV',        color: theme.chartRpv,        darkColor: theme.chartDark.rpv,        value: (d) => (d.visitors > 0 ? d.revenue / d.visitors : 0), format: (n) => fmtRpv(n, currency) },
    goals:      { label: 'Goals',      color: theme.chartGoals,      darkColor: theme.chartDark.goals,      value: (d) => d.goals, format: fmtInt },
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

// pieHueDarkForChannel returns the same hue, darkened 22% toward --ink
// (resolved at theme-read time). Wired into per-slice
// emphasis.itemStyle.color to override ECharts' default brightening
// (which fades light slices toward white).
export function pieHueDarkForChannel(
  channel: string,
  idx: number,
  theme: EChartsTheme,
): string {
  return theme.piesDark[channel] ?? theme.piesDarkFallback[idx % theme.piesDarkFallback.length];
}

// pieOption is the shared donut shell used by viewsPieOption,
// revenuePieOption, and campaignsPieOption. Donut form (radius
// ['55%', '85%']) leaves a generous center hole so an HTML overlay
// can render the metric label + total inside the chart. Each caller
// produces the `data` array (with per-slice color + per-slice dark
// emphasis color) + a tooltip formatter + an aria description.
export const PIE_RADIUS: [string, string] = ['55%', '85%'];

interface PieData {
  name: string;
  value: number;
  itemStyle: { color: string };
  emphasis: { itemStyle: { color: string } };
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
        // Per-slice emphasis.itemStyle.color (set inside each `data`
        // entry) overrides ECharts' default brightening — the hovered
        // slice goes darker, never toward white. Series-level emphasis
        // still owns the scale-up + bolder ink border.
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

// buildPieData centralises the per-slice color + dark-color projection
// so the three pie builders stay one-liners. `name`/`value` are
// caller-specific; everything else is theme-driven.
function buildPieData(channel: string, value: number, idx: number, theme: EChartsTheme): PieData {
  return {
    name: channel,
    value,
    itemStyle: { color: pieHueForChannel(channel, idx, theme) },
    emphasis: { itemStyle: { color: pieHueDarkForChannel(channel, idx, theme) } },
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
        // Single-line chart — focus 'self' suppresses ECharts' default
        // brightening on hover; explicit darkColor takes the stroke
        // bolder toward --ink instead of fading toward white.
        emphasis: {
          focus: 'self',
          lineStyle: { color: theme.chartDark.visitors, width: 3 },
          itemStyle: { color: theme.chartDark.visitors },
        },
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
        // focus: 'series' fades the other metrics so the hovered line
        // pops; per-series darkColor swaps the brightening default for
        // an explicit darken toward --ink.
        emphasis: {
          focus: 'series',
          lineStyle: { color: spec.darkColor, width: 3 },
          itemStyle: { color: spec.darkColor },
        },
        blur: {
          lineStyle: { opacity: 0.25 },
          areaStyle: { opacity: 0.1 },
        },
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
    .map((r, i) => buildPieData(r.channel, r.views, i, theme));
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
    .map((r, i) => buildPieData(r.channel, r.revenue, i, theme));
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
    .map(([channel, value], i) => buildPieData(channel, value, i, theme));
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
