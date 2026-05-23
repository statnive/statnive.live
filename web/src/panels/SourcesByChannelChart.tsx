import { useMemo } from 'preact/hooks';
import type { SourceChannelRow } from '../api/types';
import { fmtInt, fmtMoney, fmtRpv } from '../lib/fmt';
import { LazyChart } from '../components/LazyChart';
import {
  applyReducedMotion,
  pieHueForChannel,
  readEChartsTheme,
  revenuePieOption,
  viewsPieOption,
  type EChartsTheme,
} from '../lib/chart';
import { PieSummaryList, type PieSummaryRow } from './PieSummaryList';

// SourcesByChannelChart renders two cards side-by-side. Each card
// contains:
//   - A donut chart on the left (radius ['55%','85%']; bigger than
//     the prior pie, with a generous center hole so the metric label
//     + total can live inside the donut).
//   - A summary panel on the right inside the same card: a TOP
//     CHANNELS / <METRIC> table where each row is a colored swatch +
//     channel name + horizontal bar + share% + raw value.
//
// Per-slice hue comes from the lighter --pie-* palette (not the
// --ch-* chip palette) and is consistent across both donuts and the
// Campaigns panel — operators learn the channel-color pairing once.

export interface SourcesByChannelChartProps {
  by_channel: SourceChannelRow[];
  currency: string;
}

const DONUT_HEIGHT = 240;

function summary(
  by_channel: SourceChannelRow[],
  total: number,
  pick: (r: SourceChannelRow) => number,
  theme: EChartsTheme,
): PieSummaryRow[] {
  if (total <= 0) return [];
  return by_channel
    .map((r, i) => ({
      channel: r.channel,
      value: pick(r),
      pct: (pick(r) / total) * 100,
      color: pieHueForChannel(r.channel, i, theme),
    }))
    .filter((row) => row.value > 0)
    .sort((a, b) => b.value - a.value);
}

export function SourcesByChannelChart({ by_channel, currency }: SourcesByChannelChartProps) {
  const theme = useMemo(() => readEChartsTheme(), []);

  const totals = useMemo(() => {
    let views = 0;
    let revenue = 0;
    for (const r of by_channel) {
      views += r.views;
      revenue += r.revenue;
    }
    return { views, revenue };
  }, [by_channel]);

  const viewsRows = useMemo(
    () => summary(by_channel, totals.views, (r) => r.views, theme),
    [by_channel, totals.views, theme],
  );
  const revenueRows = useMemo(
    () => summary(by_channel, totals.revenue, (r) => r.revenue, theme),
    [by_channel, totals.revenue, theme],
  );

  const viewsOption = useMemo(
    () => applyReducedMotion(viewsPieOption(by_channel, theme)),
    [by_channel, theme],
  );
  const revenueOption = useMemo(
    () => applyReducedMotion(revenuePieOption(by_channel, theme, currency)),
    [by_channel, theme, currency],
  );

  if (by_channel.length === 0) {
    return (
      <p class="statnive-empty" data-testid="sources-by-channel-empty">
        No channel data for this range.
      </p>
    );
  }

  return (
    <figure
      class="statnive-channel-pies"
      role="group"
      aria-labelledby="sources-pies-cap"
      data-testid="sources-by-channel-chart"
    >
      <figcaption id="sources-pies-cap" class="statnive-sr-only">
        Share of views and revenue by channel. Precise values in the table below.
      </figcaption>
      <div class="statnive-pies-grid">
        <PieCard
          label="VIEWS"
          summaryMetricLabel="VIEWS"
          totalDisplay={totals.views > 0 ? fmtInt(totals.views) : 'No views'}
          showChart={totals.views > 0}
          option={viewsOption}
          rows={viewsRows}
          formatValue={(v) => fmtInt(v)}
          testId="views-pie"
        />
        <PieCard
          label="REVENUE"
          summaryMetricLabel="REVENUE"
          totalDisplay={totals.revenue > 0 ? fmtMoney(totals.revenue, currency) : 'No revenue'}
          showChart={totals.revenue > 0}
          option={revenueOption}
          rows={revenueRows}
          formatValue={(v) => fmtMoney(v, currency)}
          testId="revenue-pie"
        />
      </div>
      <table class="statnive-sr-only">
        <caption>Per-channel visitors, revenue, and RPV</caption>
        <thead>
          <tr>
            <th scope="col">Channel</th>
            <th scope="col">Visitors</th>
            <th scope="col">Revenue</th>
            <th scope="col">RPV</th>
          </tr>
        </thead>
        <tbody>
          {by_channel.map((r) => (
            <tr key={r.channel}>
              <td>{r.channel}</td>
              <td>{fmtInt(r.visitors)}</td>
              <td>{fmtMoney(r.revenue, currency)}</td>
              <td>{fmtRpv(r.rpv, currency)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  );
}

interface PieCardProps {
  label: string;
  summaryMetricLabel: string;
  totalDisplay: string;
  showChart: boolean;
  option: ReturnType<typeof viewsPieOption>;
  rows: PieSummaryRow[];
  formatValue: (v: number) => string;
  testId: string;
}

function PieCard({
  label,
  summaryMetricLabel,
  totalDisplay,
  showChart,
  option,
  rows,
  formatValue,
  testId,
}: PieCardProps) {
  return (
    <article class="statnive-pie-card" data-testid={testId}>
      <div class="statnive-pie-chart-wrap">
        {showChart ? (
          <div aria-hidden="true" class="statnive-pie-chart-canvas">
            <LazyChart option={option} height={DONUT_HEIGHT} />
          </div>
        ) : (
          <div class="statnive-pie-chart-empty" aria-hidden="true" />
        )}
        <div class="statnive-pie-center" aria-hidden="true">
          <span class="statnive-pie-center-label">{label}</span>
          <span class="statnive-pie-center-value" data-fit>{totalDisplay}</span>
        </div>
      </div>
      <div class="statnive-pie-summary">
        <header class="statnive-pie-summary-head">
          <span>TOP CHANNELS</span>
          <span>{summaryMetricLabel}</span>
        </header>
        <PieSummaryList rows={rows} formatValue={formatValue} />
      </div>
    </article>
  );
}
