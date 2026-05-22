import { useMemo } from 'preact/hooks';
import type { CampaignTreeNode } from '../lib/campaignTree';
import { LazyChart } from '../components/LazyChart';
import {
  applyReducedMotion,
  campaignsPieOption,
  readEChartsTheme,
  topCampaignsBarOption,
} from '../lib/chart';
import type { CampaignRow } from '../api/types';
import { fmtInt, fmtMoney } from '../lib/fmt';
import './CampaignCharts.css';

// Two ECharts-rendered charts above the campaigns breakdown table.
// - Pie: revenue (or visitors as fallback) share by channel
// - Bar: top-N campaigns by revenue (horizontal)
//
// Pie radius is '70%' (full pie, no inner hole) per the design contract
// in the migration plan. Eyebrow labels above each chart carry totals.

const TOP_N = 8;
const PIE_HEIGHT = 220;
const BAR_HEIGHT = 220;

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
  const pieEyebrow = showRevenue
    ? `REVENUE · ${fmtMoney(totalRevenue, currency)}`
    : `VISITORS · ${fmtInt(totalVisitors)}`;

  // Pie aggregates by channel. When the tenant has zero recorded
  // revenue, fall back to a visitors-by-channel pie so the chart still
  // tells a useful story.
  const pieOption = useMemo(() => {
    const valueOf = showRevenue ? (r: CampaignRow) => r.revenue : (r: CampaignRow) => r.visitors;
    const formatValue = showRevenue
      ? (n: number) => fmtMoney(n, currency)
      : (n: number) => fmtInt(n);
    return applyReducedMotion(campaignsPieOption(rows, theme, valueOf, formatValue));
  }, [rows, theme, currency, showRevenue]);

  const barOption = useMemo(
    () => applyReducedMotion(topCampaignsBarOption(rows, theme, currency, TOP_N)),
    [rows, theme, currency],
  );

  if (tree.length === 0) return null;

  const topCount = Math.min(rows.length, TOP_N);

  return (
    <div class="statnive-campaign-charts" data-testid="campaign-charts">
      <div class="statnive-campaign-chart" data-testid="campaign-chart-pie">
        <p class="statnive-eyebrow">{pieEyebrow}</p>
        <div aria-hidden="true">
          <LazyChart option={pieOption} height={PIE_HEIGHT} />
        </div>
      </div>

      <div class="statnive-campaign-chart" data-testid="campaign-chart-hbar">
        <p class="statnive-eyebrow">TOP {topCount} BY REVENUE</p>
        <div aria-hidden="true">
          <LazyChart option={barOption} height={BAR_HEIGHT} />
        </div>
      </div>
    </div>
  );
}
