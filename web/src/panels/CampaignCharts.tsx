import { useMemo } from 'preact/hooks';
import type { CampaignTreeNode } from '../lib/campaignTree';
import { LazyChart } from '../components/LazyChart';
import {
  applyReducedMotion,
  campaignsPieOption,
  pieHueForChannel,
  readEChartsTheme,
  topCampaignsRanked,
  type EChartsTheme,
} from '../lib/chart';
import type { CampaignRow } from '../api/types';
import { fmtInt, fmtMoney } from '../lib/fmt';
import { PieSummaryList } from './PieSummaryList';
import './CampaignCharts.css';

// CampaignCharts renders two surfaces above the breakdown table:
//   1. A donut pie of revenue (or visitors, when totalRevenue is zero)
//      aggregated by channel, with center label and right-side summary
//      (same component shape as Sources).
//   2. A "Best campaigns" rank list — pure HTML+CSS rows with rank
//      number, color swatch, label, horizontal bar fill, and value.
//      Editorial-typographic layout per the design mockup.

const TOP_N = 8;
const PIE_HEIGHT = 240;

export interface CampaignChartsProps {
  tree: CampaignTreeNode[];
  rows: CampaignRow[];
  currency: string;
}

export function CampaignCharts({ tree, rows, currency }: CampaignChartsProps) {
  const theme = useMemo(() => readEChartsTheme(), []);

  const { totalRevenue, totalVisitors } = useMemo(() => {
    let rev = 0;
    let vis = 0;
    for (const node of tree) {
      rev += node.revenue;
      vis += node.visitors;
    }
    return { totalRevenue: rev, totalVisitors: vis };
  }, [tree]);

  const showRevenue = totalRevenue > 0;
  const pieCenterLabel = showRevenue ? 'REVENUE' : 'VISITORS';
  const pieCenterValue = showRevenue
    ? fmtMoney(totalRevenue, currency)
    : fmtInt(totalVisitors);
  const summaryMetricLabel = showRevenue ? 'REVENUE' : 'VISITORS';

  const pieOption = useMemo(() => {
    const valueOf = showRevenue ? (r: CampaignRow) => r.revenue : (r: CampaignRow) => r.visitors;
    const formatValue = showRevenue
      ? (n: number) => fmtMoney(n, currency)
      : (n: number) => fmtInt(n);
    return applyReducedMotion(campaignsPieOption(rows, theme, valueOf, formatValue));
  }, [rows, theme, currency, showRevenue]);

  const ranked = useMemo(() => topCampaignsRanked(rows, theme, TOP_N), [rows, theme]);
  const rankedTotal = useMemo(
    () => ranked.reduce((sum, r) => sum + r.value, 0),
    [ranked],
  );

  if (tree.length === 0) return null;

  return (
    <div class="statnive-campaign-charts" data-testid="campaign-charts">
      <article class="statnive-pie-card" data-testid="campaign-chart-pie">
        <div class="statnive-pie-chart-wrap">
          <div aria-hidden="true" class="statnive-pie-chart-canvas">
            <LazyChart option={pieOption} height={PIE_HEIGHT} />
          </div>
          <div class="statnive-pie-center" aria-hidden="true">
            <span class="statnive-pie-center-label">{pieCenterLabel}</span>
            <span class="statnive-pie-center-value" data-fit>{pieCenterValue}</span>
          </div>
        </div>
        <div class="statnive-pie-summary">
          <header class="statnive-pie-summary-head">
            <span>TOP CHANNELS</span>
            <span>{summaryMetricLabel}</span>
          </header>
          <ChannelSummary
            rows={rows}
            showRevenue={showRevenue}
            currency={currency}
            theme={theme}
          />
        </div>
      </article>

      <section class="statnive-rank-list" data-testid="campaign-chart-rank">
        <header class="statnive-rank-list-head">
          <p class="statnive-eyebrow">TOP {Math.min(ranked.length, TOP_N)} · BY REVENUE</p>
          <h3 class="statnive-rank-list-title">Best campaigns</h3>
          <p class="statnive-rank-list-total">
            <span class="statnive-rank-list-total-label">TOTAL</span>
            <span class="statnive-rank-list-total-value">{fmtMoney(rankedTotal, currency)}</span>
          </p>
        </header>
        <ol class="statnive-rank-list-rows" aria-hidden="true">
          {ranked.map((r) => (
            <li class="statnive-rank-list-row" key={r.label + '|' + r.rank}>
              <span class="statnive-rank-list-rank">{String(r.rank).padStart(2, '0')}</span>
              <span
                class="statnive-rank-list-swatch"
                style={`background:${r.color}`}
                aria-hidden="true"
              />
              <span class="statnive-rank-list-label" title={r.label}>{r.label}</span>
              <span class="statnive-rank-list-bar" aria-hidden="true">
                <span
                  class="statnive-rank-list-fill"
                  style={`width:${r.pctOfMax}%;background:${r.color}`}
                />
              </span>
              <span class="statnive-rank-list-value">{fmtMoney(r.value, currency)}</span>
            </li>
          ))}
        </ol>
        <table class="statnive-sr-only">
          <caption>Top campaigns by revenue</caption>
          <thead>
            <tr>
              <th scope="col">Rank</th>
              <th scope="col">Campaign</th>
              <th scope="col">Revenue</th>
            </tr>
          </thead>
          <tbody>
            {ranked.map((r) => (
              <tr key={r.label + '|sr|' + r.rank}>
                <td>{r.rank}</td>
                <td>{r.label}</td>
                <td>{fmtMoney(r.value, currency)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}

interface ChannelSummaryProps {
  rows: CampaignRow[];
  showRevenue: boolean;
  currency: string;
  theme: EChartsTheme;
}

function ChannelSummary({ rows, showRevenue, currency, theme }: ChannelSummaryProps) {
  const summary = useMemo(() => {
    const totals = new Map<string, number>();
    for (const r of rows) {
      const v = showRevenue ? r.revenue : r.visitors;
      totals.set(r.channel, (totals.get(r.channel) ?? 0) + v);
    }
    const entries = Array.from(totals.entries()).filter(([, v]) => v > 0);
    const sum = entries.reduce((s, [, v]) => s + v, 0);
    if (sum === 0) return [];
    // pieHueForChannel keeps fallback hues consistent with the donut
    // above when an unrecognised channel lands here for the first time.
    return entries
      .map(([channel, value], i) => ({
        channel,
        value,
        pct: (value / sum) * 100,
        color: pieHueForChannel(channel, i, theme),
      }))
      .sort((a, b) => b.value - a.value);
  }, [rows, showRevenue, theme]);

  const formatValue = showRevenue
    ? (n: number) => fmtMoney(n, currency)
    : (n: number) => fmtInt(n);

  return <PieSummaryList rows={summary} formatValue={formatValue} />;
}
