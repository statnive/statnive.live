import { describe, it, expect } from 'vitest';
import {
  applyReducedMotion,
  buildMetricSpecs,
  campaignsPieOption,
  metricsLineOption,
  revenuePieOption,
  topCampaignsBarOption,
  readEChartsTheme,
  viewsPieOption,
} from '../lib/chart';
import type { SourceChannelRow, DailyPoint, SEORow, CampaignRow } from '../api/types';
import { visitorLineOption } from '../lib/chart';

// Pure-function tests for every option-builder helper in lib/chart.ts.
// Asserts option shape, ECharts contract surfaces (radius, series type,
// aria.show, animation), per-channel color wiring, and reduced-motion.

const THEME = (() => {
  // Fresh theme from jsdom — channel colors fall back to var(...) strings
  // because jsdom doesn't resolve CSS custom properties.
  return readEChartsTheme();
})();

const SAMPLE_CHANNELS: SourceChannelRow[] = [
  { channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue: 0, rpv: 0 },
  { channel: 'Organic Search', views: 300, visitors: 220, goals: 30, revenue: 500, rpv: 2.27 },
  { channel: 'AI', views: 50, visitors: 40, goals: 0, revenue: 0, rpv: 0 },
];

const SAMPLE_TREND: DailyPoint[] = [
  { day: '2026-05-20', visitors: 100, pageviews: 200, goals: 5, revenue: 50 },
  { day: '2026-05-21', visitors: 120, pageviews: 240, goals: 6, revenue: 60 },
  { day: '2026-05-22', visitors: 90, pageviews: 180, goals: 4, revenue: 40 },
];

const SAMPLE_SEO: SEORow[] = [
  { day: '2026-05-20', views: 100, visitors: 80, goals: 2, revenue: 20 },
  { day: '2026-05-21', views: 120, visitors: 100, goals: 3, revenue: 30 },
];

const SAMPLE_CAMPAIGNS: CampaignRow[] = [
  { utm_campaign: 'spring-sale', utm_source: 'google', utm_medium: 'cpc', utm_content: '', utm_term: '', channel: 'Paid', views: 500, visitors: 400, goals: 30, revenue: 1500, rpv: 3.75 },
  { utm_campaign: 'summer-promo', utm_source: 'facebook', utm_medium: 'cpc', utm_content: '', utm_term: '', channel: 'Social', views: 200, visitors: 150, goals: 5, revenue: 250, rpv: 1.67 },
  { utm_campaign: 'newsletter-may', utm_source: 'newsletter', utm_medium: 'email', utm_content: '', utm_term: '', channel: 'Email', views: 100, visitors: 80, goals: 4, revenue: 200, rpv: 2.5 },
];

describe('readEChartsTheme', () => {
  it('returns a theme object with channels map covering all canonical channels', () => {
    const theme = readEChartsTheme();
    expect(theme.channels).toBeDefined();
    for (const ch of ['Direct', 'Organic Search', 'Social', 'Email', 'Referral', 'AI', 'Paid']) {
      expect(theme.channels[ch]).toBeTruthy();
    }
  });

  it('aliases UI channel labels to the same hue as the backend value (Social ≡ Organic Social ≡ Social Media)', () => {
    const theme = readEChartsTheme();
    expect(theme.channels['Organic Social']).toBe(theme.channels['Social']);
    expect(theme.channels['Social Media']).toBe(theme.channels['Social']);
  });

  it('falls back to var(--…) strings under jsdom (no live token resolution)', () => {
    const theme = readEChartsTheme();
    // Under jsdom, getComputedStyle returns empty strings for custom
    // properties, so the fallback path produces var(--...) tokens.
    expect(theme.channels['Direct']).toMatch(/var\(--ch-direct\)|#/);
  });
});

describe('applyReducedMotion', () => {
  it('returns the option unchanged when reduced-motion is not preferred', () => {
    // jsdom's matchMedia is polyfilled in setup.ts to return matches=false.
    const opt = { series: [], animation: true };
    const out = applyReducedMotion(opt);
    expect(out).toBe(opt); // same reference
  });

  it('returns option with animation:false when reduced-motion matches', () => {
    const original = window.matchMedia;
    window.matchMedia = ((q: string) =>
      ({ matches: q.includes('reduce'), media: q, onchange: null, addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false } as unknown as MediaQueryList)) as typeof window.matchMedia;
    try {
      const out = applyReducedMotion({ series: [], animation: true });
      expect(out.animation).toBe(false);
    } finally {
      window.matchMedia = original;
    }
  });
});

describe('buildMetricSpecs', () => {
  const theme = readEChartsTheme();
  const specs = buildMetricSpecs(theme, 'EUR');

  it('produces a spec entry for every MetricId', () => {
    for (const m of ['visitors', 'pageviews', 'conversion', 'revenue', 'rpv', 'goals'] as const) {
      expect(specs[m]).toBeDefined();
      expect(specs[m].label).toBeTruthy();
      expect(specs[m].color).toBeTruthy();
    }
  });

  it('formats revenue/RPV with the supplied currency', () => {
    const row: DailyPoint = { day: '2026-05-22', visitors: 100, pageviews: 0, goals: 0, revenue: 1500 };
    expect(specs.revenue.format(specs.revenue.value(row))).toContain('1,500');
    expect(specs.rpv.format(specs.rpv.value(row))).toContain('15.00');
  });

  it('guards conversion + RPV against zero visitors (no NaN)', () => {
    const row: DailyPoint = { day: '2026-05-22', visitors: 0, pageviews: 0, goals: 5, revenue: 100 };
    expect(specs.conversion.value(row)).toBe(0);
    expect(specs.rpv.value(row)).toBe(0);
  });
});

describe('visitorLineOption (SEO panel)', () => {
  it('produces a line series with rows mapped to [day, visitors] tuples', () => {
    const opt = visitorLineOption(SAMPLE_SEO, THEME) as { series: { type: string; data: unknown[][] }[] };
    expect(opt.series[0].type).toBe('line');
    expect(opt.series[0].data).toEqual([
      ['2026-05-20', 80],
      ['2026-05-21', 100],
    ]);
  });

  it('declares time-axis x and value-axis y', () => {
    const opt = visitorLineOption(SAMPLE_SEO, THEME) as { xAxis: { type: string }; yAxis: { type: string } };
    expect(opt.xAxis.type).toBe('time');
    expect(opt.yAxis.type).toBe('value');
  });

  it('exposes aria.show for AriaComponent registration', () => {
    const opt = visitorLineOption(SAMPLE_SEO, THEME) as { aria: { show: boolean } };
    expect(opt.aria.show).toBe(true);
  });
});

describe('metricsLineOption (Overview multi-metric)', () => {
  it('produces one series per metric in the input array', () => {
    const theme = readEChartsTheme();
    const specs = buildMetricSpecs(theme, 'EUR');
    const opt = metricsLineOption(SAMPLE_TREND, ['visitors', 'revenue'], specs, theme) as {
      series: { name: string; yAxisIndex: number }[];
      yAxis: unknown[];
    };
    expect(opt.series).toHaveLength(2);
    expect(opt.series[0].name).toBe('Visitors');
    expect(opt.series[1].name).toBe('Revenue');
    expect(opt.series[0].yAxisIndex).toBe(0);
    expect(opt.series[1].yAxisIndex).toBe(1);
    expect(opt.yAxis).toHaveLength(2);
  });

  it('only the first yAxis is visible (multi-metric independent scales)', () => {
    const theme = readEChartsTheme();
    const specs = buildMetricSpecs(theme, 'EUR');
    const opt = metricsLineOption(SAMPLE_TREND, ['visitors', 'revenue', 'rpv'], specs, theme) as {
      yAxis: { show: boolean }[];
    };
    expect(opt.yAxis[0].show).toBe(true);
    expect(opt.yAxis[1].show).toBe(false);
    expect(opt.yAxis[2].show).toBe(false);
  });

  it('single-visitors mode wears the area-fill wash', () => {
    const theme = readEChartsTheme();
    const specs = buildMetricSpecs(theme, 'EUR');
    const opt = metricsLineOption(SAMPLE_TREND, ['visitors'], specs, theme) as {
      series: { areaStyle?: { color: string } }[];
    };
    expect(opt.series[0].areaStyle).toBeDefined();
    expect(opt.series[0].areaStyle?.color).toBe(theme.chartVisitorsFillWash);
  });

  it('multi-metric mode drops the area-fill wash', () => {
    const theme = readEChartsTheme();
    const specs = buildMetricSpecs(theme, 'EUR');
    const opt = metricsLineOption(SAMPLE_TREND, ['visitors', 'revenue'], specs, theme) as {
      series: { areaStyle?: unknown }[];
    };
    expect(opt.series[0].areaStyle).toBeUndefined();
  });
});

describe('viewsPieOption (Sources panel views pie)', () => {
  it('uses radius "70%" for a FULL pie, not a donut', () => {
    const opt = viewsPieOption(SAMPLE_CHANNELS, THEME) as { series: { radius: string }[] };
    expect(opt.series[0].radius).toBe('70%');
  });

  it('produces type "pie"', () => {
    const opt = viewsPieOption(SAMPLE_CHANNELS, THEME) as { series: { type: string }[] };
    expect(opt.series[0].type).toBe('pie');
  });

  it('skips channels with zero views', () => {
    const opt = viewsPieOption(
      [
        { channel: 'Direct', views: 200, visitors: 150, goals: 5, revenue: 0, rpv: 0 },
        { channel: 'Email', views: 0, visitors: 0, goals: 0, revenue: 0, rpv: 0 },
      ],
      THEME,
    ) as { series: { data: { name: string }[] }[] };
    expect(opt.series[0].data).toHaveLength(1);
    expect(opt.series[0].data[0].name).toBe('Direct');
  });

  it('applies per-slice channel hue via itemStyle.color', () => {
    const opt = viewsPieOption(SAMPLE_CHANNELS, THEME) as {
      series: { data: { name: string; itemStyle: { color: string } }[] }[];
    };
    for (const entry of opt.series[0].data) {
      expect(entry.itemStyle.color).toBe(THEME.channels[entry.name]);
    }
  });

  it('hides slice labels (legend below carries names)', () => {
    const opt = viewsPieOption(SAMPLE_CHANNELS, THEME) as { series: { label: { show: boolean } }[] };
    expect(opt.series[0].label.show).toBe(false);
  });

  it('exposes aria.show for AriaComponent', () => {
    const opt = viewsPieOption(SAMPLE_CHANNELS, THEME) as { aria: { show: boolean } };
    expect(opt.aria.show).toBe(true);
  });
});

describe('revenuePieOption (Sources panel revenue pie)', () => {
  it('uses radius "70%" — same pie contract', () => {
    const opt = revenuePieOption(SAMPLE_CHANNELS, THEME, 'EUR') as { series: { radius: string }[] };
    expect(opt.series[0].radius).toBe('70%');
  });

  it('skips channels with zero revenue', () => {
    const opt = revenuePieOption(SAMPLE_CHANNELS, THEME, 'EUR') as {
      series: { data: { name: string; value: number }[] }[];
    };
    // Only Organic Search has revenue > 0 in SAMPLE_CHANNELS.
    expect(opt.series[0].data).toHaveLength(1);
    expect(opt.series[0].data[0].name).toBe('Organic Search');
    expect(opt.series[0].data[0].value).toBe(500);
  });
});

describe('campaignsPieOption (Campaigns panel pie)', () => {
  const revenueOf = (r: { revenue: number }) => r.revenue;
  const fmt = (n: number) => '€' + n;

  it('aggregates the value selector by channel', () => {
    const opt = campaignsPieOption(SAMPLE_CAMPAIGNS, THEME, revenueOf, fmt) as {
      series: { data: { name: string; value: number }[] }[];
    };
    const byName = new Map(opt.series[0].data.map((d) => [d.name, d.value]));
    expect(byName.get('Paid')).toBe(1500);
    expect(byName.get('Social')).toBe(250);
    expect(byName.get('Email')).toBe(200);
  });

  it('uses radius "70%" — pie, not donut', () => {
    const opt = campaignsPieOption(SAMPLE_CAMPAIGNS, THEME, revenueOf, fmt) as {
      series: { radius: string }[];
    };
    expect(opt.series[0].radius).toBe('70%');
  });

  it('honors a visitors fallback when called with a visitors selector', () => {
    const visitorsOf = (r: { visitors: number }) => r.visitors;
    const opt = campaignsPieOption(SAMPLE_CAMPAIGNS, THEME, visitorsOf, (n) => String(n)) as {
      series: { data: { name: string; value: number }[] }[];
    };
    const byName = new Map(opt.series[0].data.map((d) => [d.name, d.value]));
    // Visitors totals from the SAMPLE_CAMPAIGNS fixture:
    // Paid: 400, Social: 150, Email: 80
    expect(byName.get('Paid')).toBe(400);
    expect(byName.get('Social')).toBe(150);
    expect(byName.get('Email')).toBe(80);
  });
});

describe('topCampaignsBarOption (Campaigns panel top-N bar)', () => {
  it('produces a bar series with horizontal layout (category yAxis)', () => {
    const opt = topCampaignsBarOption(SAMPLE_CAMPAIGNS, THEME, 'EUR') as {
      series: { type: string }[];
      xAxis: { type: string };
      yAxis: { type: string; data: string[] };
    };
    expect(opt.series[0].type).toBe('bar');
    expect(opt.xAxis.type).toBe('value');
    expect(opt.yAxis.type).toBe('category');
  });

  it('truncates to the top N (default 8) when input exceeds it', () => {
    const many: CampaignRow[] = Array.from({ length: 12 }, (_, i) => ({
      utm_campaign: 'c-' + i,
      utm_source: 'x',
      utm_medium: 'y',
      utm_content: '',
      utm_term: '',
      channel: 'Direct',
      views: 100 - i,
      visitors: 50,
      goals: 0,
      revenue: 1000 - i * 10,
      rpv: 1,
    }));
    const opt = topCampaignsBarOption(many, THEME, 'EUR', 8) as {
      yAxis: { data: string[] };
      series: { data: unknown[] }[];
    };
    expect(opt.yAxis.data).toHaveLength(8);
    expect(opt.series[0].data).toHaveLength(8);
    // The top entry by revenue is c-0 (1000); confirm ordering.
    expect(opt.yAxis.data[0]).toBe('c-0');
  });

  it('applies per-bar channel hue via itemStyle.color', () => {
    const opt = topCampaignsBarOption(SAMPLE_CAMPAIGNS, THEME, 'EUR') as {
      series: { data: { itemStyle: { color: string } }[] }[];
      yAxis: { data: string[] };
    };
    const names = opt.yAxis.data;
    const expected = names.map((n) => {
      const row = SAMPLE_CAMPAIGNS.find((c) => c.utm_campaign === n);
      return row ? THEME.channels[row.channel] : undefined;
    });
    opt.series[0].data.forEach((d, i) => {
      expect(d.itemStyle.color).toBe(expected[i]);
    });
  });
});
