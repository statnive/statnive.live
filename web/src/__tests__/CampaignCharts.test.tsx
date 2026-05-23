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

  it('mounts the pie chart and the rank list when data is present', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    expect(screen.getByTestId('campaign-chart-pie')).toBeTruthy();
    expect(screen.getByTestId('campaign-chart-rank')).toBeTruthy();
    // Only the pie is an ECharts mount; the rank list is pure CSS.
    expect(lazyChartCalls.length).toBe(1);
  });

  it('pie uses the donut radius (["55%","85%"]) — center hole for the label overlay', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    // Only ONE LazyChart call now — the channel pie. The top-N rank
    // list is pure CSS, not an ECharts mount.
    const pie = lazyChartCalls[0].option as { series: { type: string; radius: [string, string] }[] };
    expect(pie.series[0].type).toBe('pie');
    expect(pie.series[0].radius).toEqual(['55%', '85%']);
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

  it('rank list truncates to top 8 rows with rank prefixes and bar fills', () => {
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
    const { container } = render(
      <CampaignCharts tree={manyTree} rows={many} currency="EUR" />,
    );

    const rows = container.querySelectorAll('.statnive-rank-list-row');
    expect(rows.length).toBe(8);

    const ranks = Array.from(
      container.querySelectorAll<HTMLElement>('.statnive-rank-list-rank'),
    ).map((el) => el.textContent);
    expect(ranks).toEqual(['01', '02', '03', '04', '05', '06', '07', '08']);

    const labels = Array.from(
      container.querySelectorAll<HTMLElement>('.statnive-rank-list-label'),
    ).map((el) => el.textContent);
    // Composite label: utm_campaign · utm_source · utm_medium.
    expect(labels[0]).toBe('c-0 · x · y');

    // Top row's bar fill must hit 100% (it IS the max).
    const firstFill = container.querySelector<HTMLElement>(
      '.statnive-rank-list-row:first-child .statnive-rank-list-fill',
    );
    expect(firstFill?.style.width).toBe('100%');
  });

  it('falls back to visitors when total revenue is zero', () => {
    const noRevenue: CampaignRow[] = SAMPLE_ROWS.map((r) => ({ ...r, revenue: 0 }));
    const noRevenueTree: CampaignTreeNode[] = SAMPLE_TREE.map((n) => ({ ...n, revenue: 0 }));
    render(<CampaignCharts tree={noRevenueTree} rows={noRevenue} currency="EUR" />);

    // Eyebrow text should reflect VISITORS, not REVENUE.
    const pieContainer = screen.getByTestId('campaign-chart-pie');
    expect(pieContainer.textContent).toContain('VISITORS');
  });

  it('summary header carries dynamic REVENUE / VISITORS label (not SHARE)', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    const head = screen
      .getByTestId('campaign-chart-pie')
      .querySelector('.statnive-pie-summary-head');
    expect(head?.textContent).toContain('TOP CHANNELS');
    // SAMPLE_ROWS carries non-zero revenue → header is REVENUE
    expect(head?.textContent).toContain('REVENUE');
    expect(head?.textContent).not.toContain('SHARE');
  });

  it('summary rows append the raw value in parentheses next to the percentage', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    const pct = screen
      .getByTestId('campaign-chart-pie')
      .querySelector<HTMLElement>('.statnive-pie-summary-pct');
    const text = pct?.textContent || '';
    expect(text).toMatch(/%/);
    expect(text).toMatch(/\(.+\)/);
  });

  it('rank list ships an sr-only mirror table for assistive tech', () => {
    const { container } = render(
      <CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />,
    );
    const mirror = container.querySelector(
      '[data-testid="campaign-chart-rank"] table.statnive-sr-only',
    );
    expect(mirror).toBeTruthy();
    const bodyRows = mirror!.querySelectorAll('tbody tr');
    expect(bodyRows.length).toBeGreaterThan(0);
  });

  it('every chart option exposes aria.show', () => {
    render(<CampaignCharts tree={SAMPLE_TREE} rows={SAMPLE_ROWS} currency="EUR" />);
    for (const call of lazyChartCalls) {
      const opt = call.option as { aria?: { show?: boolean } };
      expect(opt.aria?.show).toBe(true);
    }
  });
});
