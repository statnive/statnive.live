import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, cleanup } from '@testing-library/preact';
import type { ChartProps } from '../components/Chart';

const lazyChartCalls: ChartProps[] = [];
vi.mock('../components/LazyChart', () => ({
  LazyChart: (props: ChartProps) => {
    lazyChartCalls.push(props);
    return null;
  },
}));

import { CampaignCharts } from '../panels/CampaignCharts';
import type { CampaignRow } from '../api/types';
import type { CampaignTreeNode } from '../lib/campaignTree';

const SAMPLE_ROWS: CampaignRow[] = [
  { utm_campaign: 'spring-sale', utm_source: 'google', utm_medium: 'cpc', utm_content: '', utm_term: '', channel: 'Paid', views: 500, visitors: 400, goals: 30, revenue: 1500, rpv: 3.75 },
  { utm_campaign: 'summer-promo', utm_source: 'facebook', utm_medium: 'cpc', utm_content: '', utm_term: '', channel: 'Social', views: 200, visitors: 150, goals: 5, revenue: 250, rpv: 1.67 },
  { utm_campaign: 'newsletter-may', utm_source: 'newsletter', utm_medium: 'email', utm_content: '', utm_term: '', channel: 'Email', views: 100, visitors: 80, goals: 4, revenue: 200, rpv: 2.5 },
];

const SAMPLE_TREE: CampaignTreeNode[] = SAMPLE_ROWS.map((r) => ({
  level: 'campaign',
  label: r.utm_campaign,
  pathKey: r.utm_campaign,
  terms: [r.utm_campaign],
  views: r.views,
  visitors: r.visitors,
  goals: r.goals,
  revenue: r.revenue,
  rpv: r.rpv,
  children: [],
}));

describe('CampaignCharts (pie + horizontal bar)', () => {
  beforeEach(() => {
    lazyChartCalls.length = 0;
  });

  afterEach(() => {
    cleanup();
  });

  it('renders nothing when the tree is empty', () => {
    const { container } = render(
      <CampaignCharts tree={[]} rows={[]} currency="EUR" />,
    );
    expect(container.querySelector('[data-testid="campaign-charts"]')).toBeNull();
    expect(lazyChartCalls.length).toBe(0);
  });

  it('mounts both pie and bar charts when data is present', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    expect(screen.getByTestId('campaign-chart-pie')).toBeTruthy();
    expect(screen.getByTestId('campaign-chart-hbar')).toBeTruthy();
    expect(lazyChartCalls.length).toBe(2);
  });

  it('pie uses radius "70%" (no donut hole)', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    // First LazyChart call is the pie (render order: pie first, bar second)
    const pie = lazyChartCalls[0].option as { series: { type: string; radius: string }[] };
    expect(pie.series[0].type).toBe('pie');
    expect(pie.series[0].radius).toBe('70%');
  });

  it('pie aggregates revenue by channel', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    const pie = lazyChartCalls[0].option as {
      series: { data: { name: string; value: number }[] }[];
    };
    const byName = new Map(pie.series[0].data.map((d) => [d.name, d.value]));
    expect(byName.get('Paid')).toBe(1500);
    expect(byName.get('Social')).toBe(250);
    expect(byName.get('Email')).toBe(200);
  });

  it('bar chart truncates to top N=8 campaigns by revenue', () => {
    const many: CampaignRow[] = Array.from({ length: 12 }, (_, i) => ({
      utm_campaign: 'c-' + i,
      utm_source: 'x',
      utm_medium: 'y',
      utm_content: '',
      utm_term: '',
      channel: 'Direct',
      views: 100,
      visitors: 50,
      goals: 0,
      revenue: 1000 - i * 10,
      rpv: 1,
    }));
    const manyTree: CampaignTreeNode[] = many.map((r) => ({
      level: 'campaign',
      label: r.utm_campaign,
      pathKey: r.utm_campaign,
      terms: [r.utm_campaign],
      views: r.views,
      visitors: r.visitors,
      goals: r.goals,
      revenue: r.revenue,
      rpv: r.rpv,
      children: [],
    }));
    render(<CampaignCharts tree={manyTree} rows={many} currency="EUR" />);

    const bar = lazyChartCalls[1].option as {
      series: { data: unknown[] }[];
      yAxis: { data: string[] };
    };
    expect(bar.yAxis.data).toHaveLength(8);
    expect(bar.series[0].data).toHaveLength(8);
  });

  it('falls back to visitors when total revenue is zero', () => {
    const noRevenue: CampaignRow[] = SAMPLE_ROWS.map((r) => ({ ...r, revenue: 0 }));
    const noRevenueTree: CampaignTreeNode[] = SAMPLE_TREE.map((n) => ({ ...n, revenue: 0 }));
    render(<CampaignCharts tree={noRevenueTree} rows={noRevenue} currency="EUR" />);

    // Eyebrow text should reflect VISITORS, not REVENUE.
    const pieContainer = screen.getByTestId('campaign-chart-pie');
    expect(pieContainer.textContent).toContain('VISITORS');
  });

  it('every chart option exposes aria.show', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    for (const call of lazyChartCalls) {
      const opt = call.option as { aria?: { show?: boolean } };
      expect(opt.aria?.show).toBe(true);
    }
  });
});
