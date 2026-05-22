import { useMemo } from 'preact/hooks';
import type { SourceChannelRow } from '../api/types';
import { fmtInt, fmtMoney, fmtRpv } from '../lib/fmt';
import { LazyChart } from '../components/LazyChart';
import {
  applyReducedMotion,
  revenuePieOption,
  readEChartsTheme,
  viewsPieOption,
} from '../lib/chart';

// SourcesByChannelChart renders two ECharts pie charts (views by
// channel, revenue by channel) above the Sources table. Each slice is
// tinted in its channel hue via theme.channels[r.channel]; a shared
// legend underneath shows the per-channel chip plus share% on each
// metric. Pie (radius: '70%'), not donut; totals live in the eyebrow
// line above each chart.
//
// The two pies answer two different questions:
//   - VIEWS:   where is traffic coming from?
//   - REVENUE: which channel actually earns?
// Mismatched proportions across the pies are the RPV story
// (CLAUDE.md Product Philosophy item 1): a channel with a fat VIEWS
// slice and a thin REVENUE slice is converting poorly.

export interface SourcesByChannelChartProps {
  by_channel: SourceChannelRow[];
  currency: string;
}

function pctLabel(value: number, total: number): string {
  if (total <= 0) return '0%';
  const p = (value / total) * 100;
  if (p === 0) return '0%';
  if (p < 1) return '<1%';
  return Math.round(p) + '%';
}

const PIE_HEIGHT = 160;

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
      <div class="statnive-channel-pies-row">
        <div class="statnive-pie" data-testid="views-pie">
          <p class="statnive-eyebrow">
            VIEWS · {totals.views > 0 ? fmtInt(totals.views) : 'No views'}
          </p>
          {totals.views > 0 ? (
            <div aria-hidden="true">
              <LazyChart option={viewsOption} height={PIE_HEIGHT} />
            </div>
          ) : null}
        </div>
        <div class="statnive-pie" data-testid="revenue-pie">
          <p class="statnive-eyebrow">
            REVENUE · {totals.revenue > 0 ? fmtMoney(totals.revenue, currency) : 'No revenue'}
          </p>
          {totals.revenue > 0 ? (
            <div aria-hidden="true">
              <LazyChart option={revenueOption} height={PIE_HEIGHT} />
            </div>
          ) : null}
        </div>
      </div>
      <ul class="statnive-channel-legend">
        {by_channel.map((r) => (
          <li class="statnive-channel-legend-row" data-channel={r.channel} key={r.channel}>
            <span class="statnive-channel-legend-dot" aria-hidden="true" />
            <span class="statnive-channel-legend-name">{r.channel || '·'}</span>
            <span class="statnive-channel-legend-metric">
              <span class="statnive-channel-legend-axis">VIEWS</span>
              <span class="statnive-channel-legend-pct">{pctLabel(r.views, totals.views)}</span>
            </span>
            <span class="statnive-channel-legend-metric">
              <span class="statnive-channel-legend-axis">REVENUE</span>
              <span class="statnive-channel-legend-pct">{pctLabel(r.revenue, totals.revenue)}</span>
            </span>
          </li>
        ))}
      </ul>
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
