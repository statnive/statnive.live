import type { CampaignTreeNode, PieSlice } from '../lib/campaignTree';
import { pieSlices, topCampaignAggregates } from '../lib/campaignTree';
import { rowMax, pctOfMax } from '../lib/rows';
import { fmtInt, fmtMoney, fmtPct } from '../lib/fmt';
import { DualBar } from './DualBar';
import './CampaignCharts.css';

// Three small fixed-size charts that sit above the breakdown table.
// All three render inline SVG / CSS — no chart library, no bundle hit.
// Sized so the chart strip fits one card width on a 1280-wide desktop;
// at <960 the strip wraps vertically (grid auto-fit).

const TOP_N = 8;
const CHART_HEIGHT = 220;

export interface CampaignChartsProps {
  tree: CampaignTreeNode[];
  currency: string;
}

export function CampaignCharts({ tree, currency }: CampaignChartsProps) {
  if (tree.length === 0) return null;

  const totalRevenue = tree.reduce((sum, node) => sum + node.revenue, 0);
  const totalVisitors = tree.reduce((sum, node) => sum + node.visitors, 0);

  // Show revenue share when the tenant has any revenue data, otherwise
  // fall back to visitor share. Computed lazily — the unused metric
  // never runs pieSlices().
  const showRevenue = totalRevenue > 0;
  const pie = showRevenue
    ? {
        slices: pieSlices(tree, (n) => n.revenue, 5),
        title: 'Share of revenue',
        total: fmtMoney(totalRevenue, currency),
        caption: 'revenue',
      }
    : {
        slices: pieSlices(tree, (n) => n.visitors, 5),
        title: 'Share of visitors',
        total: fmtInt(totalVisitors),
        caption: 'visitors',
      };

  const topByRevenue = topCampaignAggregates(tree, TOP_N);
  const maxRevenue = rowMax(topByRevenue, (c) => c.revenue);
  const maxVisitors = rowMax(topByRevenue, (c) => c.visitors);

  return (
    <div class="statnive-campaign-charts" data-testid="campaign-charts">
      <div class="statnive-campaign-chart" data-testid="campaign-chart-pie">
        <h3 class="statnive-campaign-chart-h">{pie.title}</h3>
        <Donut slices={pie.slices} totalLabel={pie.total} totalCaption={pie.caption} />
      </div>

      <div class="statnive-campaign-chart" data-testid="campaign-chart-hbar">
        <h3 class="statnive-campaign-chart-h">Top {topByRevenue.length} by revenue</h3>
        <ol class="statnive-hbar-list">
          {topByRevenue.map((c) => (
            <li key={c.utm_campaign} class="statnive-hbar-row">
              <span class="statnive-hbar-label" title={c.utm_campaign}>
                {c.utm_campaign}
              </span>
              <span class="statnive-hbar-track">
                <span
                  class="statnive-hbar-fill"
                  style={{ width: pctOfMax(c.revenue, maxRevenue) }}
                />
              </span>
              <span class="statnive-hbar-value">{fmtMoney(c.revenue, currency)}</span>
            </li>
          ))}
        </ol>
      </div>

      <div class="statnive-campaign-chart" data-testid="campaign-chart-dual">
        <h3 class="statnive-campaign-chart-h">Visitors vs revenue</h3>
        <ol class="statnive-dualbar-list">
          {topByRevenue.map((c) => (
            <li key={c.utm_campaign} class="statnive-dualbar-listrow">
              <span class="statnive-dualbar-listlabel" title={c.utm_campaign}>
                {c.utm_campaign}
              </span>
              <DualBar
                visitors={c.visitors}
                revenue={c.revenue}
                maxVisitors={maxVisitors}
                maxRevenue={maxRevenue}
                currency={currency}
              />
            </li>
          ))}
        </ol>
      </div>
    </div>
  );
}

interface DonutProps {
  slices: PieSlice[];
  totalLabel: string;
  totalCaption: string;
}

// Donut uses the stroke-dasharray-on-circle trick: pathLength="100" means
// the visible dash length is the slice percent and dashoffset stacks
// slices around the circle. Group is rotated -90deg so slice 0 starts at
// 12 o'clock. r = 15.915 ≈ 100 / (2π) makes the circumference exactly 100.
function Donut({ slices, totalLabel, totalCaption }: DonutProps) {
  if (slices.length === 0) {
    return (
      <p class="statnive-empty" style={{ height: CHART_HEIGHT + 'px' }}>
        no data
      </p>
    );
  }

  let cumulative = 0;
  const ring = slices.map((s) => {
    const offset = -cumulative;
    cumulative += s.percent;
    return { ...s, offset };
  });

  return (
    <div class="statnive-donut-wrap">
      <svg
        class="statnive-donut"
        viewBox="0 0 36 36"
        role="img"
        aria-label={`Donut: ${totalLabel} ${totalCaption}`}
      >
        <g transform="rotate(-90 18 18)">
          <circle
            class="statnive-donut-ground"
            cx="18"
            cy="18"
            r="15.915"
            fill="none"
            strokeWidth="3"
          />
          {ring.map((s, i) => (
            <circle
              key={i}
              cx="18"
              cy="18"
              r="15.915"
              fill="none"
              stroke={s.color}
              strokeWidth="3"
              strokeDasharray={`${s.percent} ${100 - s.percent}`}
              strokeDashoffset={String(s.offset)}
              pathLength="100"
            >
              <title>
                {s.label}: {fmtPct(s.percent)}
              </title>
            </circle>
          ))}
        </g>
        <text
          x="18"
          y="17.5"
          text-anchor="middle"
          class="statnive-donut-total"
        >
          {totalLabel}
        </text>
        <text
          x="18"
          y="21.5"
          text-anchor="middle"
          class="statnive-donut-caption"
        >
          {totalCaption}
        </text>
      </svg>
      <ul class="statnive-donut-legend">
        {slices.map((s, i) => (
          <li key={i}>
            <span class="statnive-donut-swatch" style={{ background: s.color }} />
            <span class="statnive-donut-legend-label" title={s.label}>
              {s.label}
            </span>
            <span class="statnive-donut-legend-pct">{fmtPct(s.percent)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
