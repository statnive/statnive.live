import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/preact';
import { signal } from '@preact/signals';
import { ChartTooltip } from '../components/ChartTooltip';
import type { ChartCursorState } from '../components/Chart';
import type { MetricSpecs } from '../lib/chart';

afterEach(cleanup);

// Stroke values are placeholder var() references so the brand-grep CI
// gate (hex-only-in-tokens.css rule) stays clean. The tooltip just
// passes them through to inline style; format correctness is what we
// assert, not pixel color.
const SPECS: MetricSpecs = {
  visitors:   { label: 'Visitors',   stroke: 'var(--chart-visitors)',   value: (d) => d.visitors,                                                          format: (n) => String(n) },
  pageviews:  { label: 'Pageviews',  stroke: 'var(--chart-pageviews)',  value: (d) => d.pageviews,                                                         format: (n) => String(n) },
  conversion: { label: 'Conversion', stroke: 'var(--chart-conversion)', value: (d) => (d.visitors > 0 ? (d.goals / d.visitors) * 100 : 0),                  format: (n) => n.toFixed(2) + '%' },
  revenue:    { label: 'Revenue',    stroke: 'var(--chart-revenue)',    value: (d) => d.revenue,                                                           format: (n) => '€' + n },
  rpv:        { label: 'RPV',        stroke: 'var(--chart-rpv)',        value: (d) => (d.visitors > 0 ? d.revenue / d.visitors : 0),                       format: (n) => '€' + n.toFixed(2) },
  goals:      { label: 'Goals',      stroke: 'var(--chart-goals)',      value: (d) => d.goals,                                                             format: (n) => String(n) },
};

const data = [
  { day: '2026-05-15', visitors: 100, pageviews: 200, goals: 5,  revenue: 500 },
  { day: '2026-05-16', visitors: 120, pageviews: 240, goals: 6,  revenue: 1500 },
];

describe('ChartTooltip', () => {
  it('hides when cursor is null', () => {
    const cursor = signal<ChartCursorState | null>(null);
    const { container } = render(
      <ChartTooltip cursor={cursor} data={data} metrics={['visitors']} specs={SPECS} containerWidth={600} />,
    );
    const tip = container.querySelector('.statnive-chart-tooltip') as HTMLElement;
    expect(tip).toBeTruthy();
    expect(tip.classList.contains('is-visible')).toBe(false);
  });

  it('shows date + one row per metric when cursor is set', () => {
    const cursor = signal<ChartCursorState | null>({ idx: 1, left: 300, top: 100 });
    const { container } = render(
      <ChartTooltip cursor={cursor} data={data} metrics={['visitors', 'revenue']} specs={SPECS} containerWidth={600} />,
    );
    const tip = container.querySelector('.statnive-chart-tooltip') as HTMLElement;
    expect(tip.classList.contains('is-visible')).toBe(true);
    const rows = container.querySelectorAll('.statnive-chart-tooltip-row');
    expect(rows.length).toBe(2);

    // Row order matches the metrics array.
    expect(rows[0].querySelector('.statnive-chart-tooltip-label')?.textContent).toBe('Visitors');
    expect(rows[0].querySelector('.statnive-chart-tooltip-value')?.textContent).toBe('120');
    expect(rows[1].querySelector('.statnive-chart-tooltip-label')?.textContent).toBe('Revenue');
    expect(rows[1].querySelector('.statnive-chart-tooltip-value')?.textContent).toBe('€1500');

    // Dot border-color is bound to the series stroke so colorblind users
    // get a visual reference + a name (DESIGN.md "colour is never the
    // only carrier" rule).
    const dots = container.querySelectorAll('.statnive-chart-tooltip-dot');
    expect((dots[0] as HTMLElement).style.borderColor).toBeTruthy();
  });

  it('formats values per metric (% for conversion, € for revenue/rpv)', () => {
    const cursor = signal<ChartCursorState | null>({ idx: 1, left: 300, top: 100 });
    const { container } = render(
      <ChartTooltip cursor={cursor} data={data} metrics={['conversion', 'revenue', 'rpv']} specs={SPECS} containerWidth={600} />,
    );
    const values = Array.from(container.querySelectorAll('.statnive-chart-tooltip-value')).map((e) => e.textContent);
    // day=2026-05-16: visitors=120, goals=6 → conversion=5.00%; revenue=1500; rpv=12.50
    expect(values).toEqual(['5.00%', '€1500', '€12.50']);
  });

  it('clamps the left offset so the tooltip never overflows the right edge', () => {
    const cursor = signal<ChartCursorState | null>({ idx: 1, left: 999, top: 100 });
    const { container } = render(
      <ChartTooltip cursor={cursor} data={data} metrics={['visitors']} specs={SPECS} containerWidth={600} />,
    );
    const tip = container.querySelector('.statnive-chart-tooltip') as HTMLElement;
    // half=110, clamped to containerWidth - half = 490.
    expect(tip.style.left).toBe('490px');
  });

  it('renders empty when cursor.idx is out of bounds', () => {
    const cursor = signal<ChartCursorState | null>({ idx: 999, left: 300, top: 100 });
    const { container } = render(
      <ChartTooltip cursor={cursor} data={data} metrics={['visitors']} specs={SPECS} containerWidth={600} />,
    );
    const tip = container.querySelector('.statnive-chart-tooltip') as HTMLElement;
    expect(tip.classList.contains('is-visible')).toBe(false);
  });
});
