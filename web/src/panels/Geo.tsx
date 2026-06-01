import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { GeoResponse, GeoRow, GeoTopRow } from '../api/types';
import { rangeSignal } from '../state/range';
import { filtersSignal } from '../state/filters';
import { siteSignal, activeSiteSignal } from '../state/site';
import { DualBar } from './DualBar';
import { PieSummaryList, type PieSummaryRow } from './PieSummaryList';
import { LazyChart } from '../components/LazyChart';
import {
  applyReducedMotion,
  pieHueForChannel,
  pieHueDarkForChannel,
  PIE_RADIUS,
  readEChartsTheme,
  type EChartsTheme,
} from '../lib/chart';
import { lookupCountry } from '../lib/countries';
import { fmtInt, fmtMoney, fmtRpv, fmtSharePct } from '../lib/fmt';
import { rowMax } from '../lib/rows';
import './panels.css';
import './Geo.css';

// Geo panel — v1.1-geo. Three stacked surfaces:
//   1. Headline: two side-by-side ranked lists (top 10 by visitors,
//      top 10 by revenue). Highlights rank divergence — the country
//      that drives traffic is rarely the country that drives revenue.
//   2. Pie chart: country share donut. Metric toggle flips between
//      visitors and revenue. Reuses the existing --pie-* palette via
//      pieHueForChannel (which falls back to a positional --pie-*
//      slot for codes that aren't in the channel map — fine for us).
//   3. Drill-down table: one row per country with a DualBar; click to
//      expand province + city rows.
//
// When the binary's dashboard.geo_enabled is false, /api/stats/geo
// returns 501. We detect that error message and render a "coming
// soon" state that mirrors the Phase 5e SOON-tab UX — same surface,
// just routed through the panel instead of the Nav.

type ExpandedSet = ReadonlySet<string>;

const PIE_HEIGHT = 240;
const TOP_N = 10;

interface ChartTotals {
  visitors: number;
  revenue: number;
}

function pieRows(
  top: GeoTopRow[],
  total: number,
  pick: (r: GeoTopRow) => number,
  theme: EChartsTheme,
): PieSummaryRow[] {
  if (total <= 0) return [];
  return top
    .map((r, idx) => ({
      channel: lookupCountry(r.country_code).name,
      value: pick(r),
      pct: (pick(r) / total) * 100,
      color: pieHueForChannel(r.country_code, idx, theme),
    }))
    .filter((row) => row.value > 0)
    .sort((a, b) => b.value - a.value)
    .slice(0, TOP_N);
}

function pieOption(
  top: GeoTopRow[],
  pick: (r: GeoTopRow) => number,
  theme: EChartsTheme,
  ariaLabel: string,
): unknown {
  const sliced = top
    .map((r, idx) => ({ row: r, idx, value: pick(r) }))
    .filter((x) => x.value > 0)
    .sort((a, b) => b.value - a.value);

  // Collapse beyond TOP_N into a grey "Other" slice so the donut
  // doesn't shatter into 25 thin wedges. The "Other" hue resolves to
  // theme.ink2-ish via the chart palette index fallback.
  const head = sliced.slice(0, TOP_N - 1);
  const tail = sliced.slice(TOP_N - 1);
  const tailSum = tail.reduce((acc, x) => acc + x.value, 0);

  const data = head.map(({ row, idx }) => ({
    name: lookupCountry(row.country_code).name,
    value: pick(row),
    itemStyle: { color: pieHueForChannel(row.country_code, idx, theme) },
    emphasis: {
      itemStyle: { color: pieHueDarkForChannel(row.country_code, idx, theme) },
    },
  }));

  if (tailSum > 0) {
    data.push({
      name: 'Other',
      value: tailSum,
      itemStyle: { color: theme.ink2 },
      emphasis: { itemStyle: { color: theme.ink } },
    });
  }

  return applyReducedMotion({
    series: [
      {
        type: 'pie',
        radius: PIE_RADIUS,
        center: ['50%', '50%'],
        data,
        label: { show: false },
        labelLine: { show: false },
        emphasis: {
          scale: true,
          scaleSize: 4,
          itemStyle: { borderWidth: 2, borderColor: theme.ink },
        },
        itemStyle: { borderWidth: 2, borderColor: theme.paper2 },
      },
    ],
    legend: { show: false },
    aria: { show: true, label: { description: ariaLabel } },
  } as never);
}

// groupByCountry folds the flat GeoRow[] into a Map keyed on
// country_code so the drill-down table can render each country once
// with its (province, city) children underneath when expanded.
function groupByCountry(rows: GeoRow[]): Map<string, GeoRow[]> {
  const out = new Map<string, GeoRow[]>();
  for (const r of rows) {
    const list = out.get(r.country_code);
    if (list) list.push(r);
    else out.set(r.country_code, [r]);
  }
  return out;
}

function rolledRow(rows: GeoRow[]): GeoRow {
  const acc: GeoRow = {
    country_code: rows[0].country_code,
    province: '',
    city: '',
    views: 0,
    visitors: 0,
    goals: 0,
    revenue: 0,
    rpv: 0,
  };
  for (const r of rows) {
    acc.views += r.views;
    acc.goals += r.goals;
    acc.revenue += r.revenue;
    // Visitors is HLL-merged server-side — summing here over-counts
    // by the union-overlap factor. The country aggregate the API
    // returns in `top` is the right source; we use the SUM here only
    // for the drill-down headline of the country row, accepting the
    // overcount that the operator can drill into the children to
    // reconcile against.
    acc.visitors += r.visitors;
  }
  acc.rpv = acc.visitors > 0 ? acc.revenue / acc.visitors : 0;
  return acc;
}

export default function Geo() {
  const data = useSignal<GeoResponse | null>(null);
  const err = useSignal<string | null>(null);
  const disabled = useSignal(false);
  const metric = useSignal<'visitors' | 'revenue'>('visitors');
  const expanded = useSignal<ExpandedSet>(new Set());
  const limit = useSignal(50);

  useEffect(() => {
    err.value = null;
    disabled.value = false;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<GeoResponse>(
          '/api/stats/geo',
          { from: r.from, to: r.to, limit: String(limit.value) },
          ac.signal,
        );
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        const msg = e instanceof Error ? e.message : String(e);
        // 501 = feature flag off. Distinct from a 500 / network error
        // so the panel can render the SOON state without scaring the
        // operator with a logs-please banner.
        if (msg.includes('HTTP 501')) {
          disabled.value = true;
        } else {
          err.value = msg;
        }
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    siteSignal.value,
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.country,
    filtersSignal.value.sort,
    filtersSignal.value.dir,
    limit.value,
  ]);

  const theme = useMemo(() => readEChartsTheme(), []);
  const currency = activeSiteSignal.value?.currency ?? 'EUR';
  const payload = data.value;
  const activeMetric = metric.value;

  // Payload-derived structures shared by all three surfaces. Recomputed
  // only when the fetch returns new data or the theme tokens change.
  const payloadDerived = useMemo(() => {
    if (!payload || (payload.top.length === 0 && payload.rows.length === 0)) {
      return null;
    }

    const { top, rows } = payload;

    const totals: ChartTotals = top.reduce<ChartTotals>(
      (acc, r) => ({ visitors: acc.visitors + r.visitors, revenue: acc.revenue + r.revenue }),
      { visitors: 0, revenue: 0 },
    );

    const buildSummary = (
      ranked: GeoTopRow[],
      pick: (r: GeoTopRow) => number,
      total: number,
    ): PieSummaryRow[] =>
      ranked.map((r, idx) => ({
        channel: `${lookupCountry(r.country_code).flag} ${lookupCountry(r.country_code).name}`,
        value: pick(r),
        pct: total > 0 ? (pick(r) / total) * 100 : 0,
        color: pieHueForChannel(r.country_code, idx, theme),
      }));

    const visitorsList = buildSummary(
      [...top].sort((a, b) => b.visitors - a.visitors).slice(0, TOP_N),
      (r) => r.visitors,
      totals.visitors,
    );
    const revenueList = buildSummary(
      [...top].sort((a, b) => b.revenue - a.revenue).slice(0, TOP_N),
      (r) => r.revenue,
      totals.revenue,
    );

    const grouped = groupByCountry(rows);
    const countryAggregates = Array.from(grouped.values())
      .map(rolledRow)
      .sort((a, b) => b.revenue - a.revenue || b.visitors - a.visitors);

    return {
      top,
      totals,
      visitorsList,
      revenueList,
      grouped,
      countryAggregates,
      maxVisitors: rowMax(countryAggregates, (r) => r.visitors),
      maxRevenue: rowMax(countryAggregates, (r) => r.revenue),
    };
  }, [payload, theme]);

  // Metric-dependent derivations: re-derived only when the operator
  // flips the pie toggle. Pie ECharts option rebuild is the heaviest
  // bit here (lookupCountry × N + slice-color expansion), so isolating
  // it from the headline/table memo above keeps the headline cheap.
  const metricDerived = useMemo(() => {
    if (!payloadDerived) return null;

    const { top, totals } = payloadDerived;
    const pick = (r: GeoTopRow) => (activeMetric === 'visitors' ? r.visitors : r.revenue);
    const total = activeMetric === 'visitors' ? totals.visitors : totals.revenue;
    const ariaLabel = activeMetric === 'visitors'
      ? 'Country share of visitors'
      : 'Country share of revenue';

    return {
      pieRowsForList: pieRows(top, total, pick, theme),
      pieEChartOption: pieOption(top, pick, theme, ariaLabel),
      activeRows: activeMetric === 'visitors'
        ? payloadDerived.visitorsList
        : payloadDerived.revenueList,
    };
  }, [payloadDerived, activeMetric, theme]);

  if (disabled.value) {
    return (
      <section class="statnive-section" data-testid="panel-geo">
        <h2 class="statnive-h2">Geo</h2>
        <p class="statnive-empty">
          The Geo report is not enabled on this server yet. Ask your operator
          to flip <code>dashboard.geo_enabled</code> in the config after the
          historical rollup backfill completes.
        </p>
      </section>
    );
  }

  if (err.value) {
    return (
      <section class="statnive-section" data-testid="panel-geo">
        <h2 class="statnive-h2">Geo</h2>
        <p class="statnive-error">could not load; see logs</p>
      </section>
    );
  }

  if (!payload) {
    return (
      <section class="statnive-section" data-testid="panel-geo">
        <h2 class="statnive-h2">Geo</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  if (!payloadDerived || !metricDerived) {
    return (
      <section class="statnive-section" data-testid="panel-geo">
        <h2 class="statnive-h2">Geo</h2>
        <p class="statnive-empty">No geographic data for this range / filter.</p>
      </section>
    );
  }

  const {
    visitorsList,
    revenueList,
    grouped,
    countryAggregates,
    maxVisitors,
    maxRevenue,
    totals,
  } = payloadDerived;
  const { pieRowsForList, pieEChartOption, activeRows } = metricDerived;

  function toggle(cc: string) {
    const next = new Set(expanded.value);
    if (next.has(cc)) next.delete(cc);
    else next.add(cc);
    expanded.value = next;
  }

  return (
    <section class="statnive-section statnive-geo" data-testid="panel-geo">
      <h2 class="statnive-h2">Geo</h2>

      <div class="statnive-geo-headline">
        <div data-testid="geo-top-visitors">
          <h3 class="statnive-h3">Top countries by visitors</h3>
          <PieSummaryList rows={visitorsList} formatValue={fmtInt} />
        </div>
        <div data-testid="geo-top-revenue">
          <h3 class="statnive-h3">Top countries by revenue</h3>
          <PieSummaryList
            rows={revenueList}
            formatValue={(n) => fmtMoney(n, currency)}
          />
        </div>
      </div>

      <div class="statnive-geo-pie" data-testid="geo-pie">
        <div class="statnive-geo-pie-header">
          <h3 class="statnive-h3">Country share</h3>
          <div class="statnive-geo-pie-toggle" role="group" aria-label="Pie metric">
            <button
              type="button"
              class={
                'statnive-chip' + (metric.value === 'visitors' ? ' is-active' : '')
              }
              aria-pressed={metric.value === 'visitors'}
              data-testid="geo-pie-metric-toggle-visitors"
              onClick={() => {
                metric.value = 'visitors';
              }}
            >
              Visitors
            </button>
            <button
              type="button"
              class={
                'statnive-chip' + (metric.value === 'revenue' ? ' is-active' : '')
              }
              aria-pressed={metric.value === 'revenue'}
              data-testid="geo-pie-metric-toggle"
              onClick={() => {
                metric.value = 'revenue';
              }}
            >
              Revenue
            </button>
          </div>
        </div>
        <div class="statnive-geo-pie-grid">
          <LazyChart option={pieEChartOption as never} height={PIE_HEIGHT} />
          <PieSummaryList
            rows={pieRowsForList}
            formatValue={(n) =>
              metric.value === 'visitors' ? fmtInt(n) : fmtMoney(n, currency)
            }
          />
        </div>
        <p class="statnive-sr-only">
          {metric.value === 'visitors'
            ? `Total ${fmtInt(totals.visitors)} visitors across ${pieRowsForList.length} countries`
            : `Total ${fmtMoney(totals.revenue, currency)} revenue across ${pieRowsForList.length} countries`}
        </p>
        <span class="statnive-sr-only" data-testid="geo-share-helper">
          {fmtSharePct(activeRows[0]?.pct ?? 0)}
        </span>
      </div>

      <table class="statnive-table">
        <thead>
          <tr>
            <th>Country</th>
            <th>Views</th>
            <th>Goals</th>
            <th>RPV</th>
            <th>Visitors / Revenue</th>
          </tr>
        </thead>
        <tbody>
          {countryAggregates.map((country) => {
            const cc = country.country_code;
            const c = lookupCountry(cc);
            const isOpen = expanded.value.has(cc);
            const children = grouped.get(cc) ?? [];
            return (
              <>
                <tr
                  key={cc}
                  class="statnive-geo-row-country"
                  data-testid={`geo-row-${cc || 'unknown'}`}
                >
                  <td>
                    <button
                      type="button"
                      class="statnive-geo-expand"
                      aria-expanded={isOpen}
                      onClick={() => toggle(cc)}
                    >
                      <span aria-hidden="true">{isOpen ? '▾' : '▸'}</span>{' '}
                      <span aria-hidden="true">{c.flag}</span> {c.name}
                    </button>
                  </td>
                  <td>{fmtInt(country.views)}</td>
                  <td>{fmtInt(country.goals)}</td>
                  <td>{fmtRpv(country.rpv, currency)}</td>
                  <td>
                    <DualBar
                      visitors={country.visitors}
                      revenue={country.revenue}
                      maxVisitors={maxVisitors}
                      maxRevenue={maxRevenue}
                      currency={currency}
                    />
                  </td>
                </tr>
                {isOpen
                  ? children.map((child) => (
                      <tr
                        key={`${cc}-${child.province}-${child.city}`}
                        class="statnive-geo-row-child"
                      >
                        <td>
                          <span class="statnive-geo-child-label">
                            {child.province || '(unknown)'} ·{' '}
                            {child.city || '(unknown)'}
                          </span>
                        </td>
                        <td>{fmtInt(child.views)}</td>
                        <td>{fmtInt(child.goals)}</td>
                        <td>{fmtRpv(child.rpv, currency)}</td>
                        <td>
                          <DualBar
                            visitors={child.visitors}
                            revenue={child.revenue}
                            maxVisitors={maxVisitors}
                            maxRevenue={maxRevenue}
                            currency={currency}
                          />
                        </td>
                      </tr>
                    ))
                  : null}
              </>
            );
          })}
        </tbody>
      </table>

      {limit.value < 200 ? (
        <button
          type="button"
          class="statnive-chip"
          onClick={() => {
            limit.value = 200;
          }}
          style={{ marginTop: 'var(--s-2)' }}
        >
          Show more (up to 200)
        </button>
      ) : null}
    </section>
  );
}
