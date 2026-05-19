import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { OverviewResponse } from '../api/types';
import { rangeSignal } from '../state/range';
import { siteSignal, activeSiteSignal } from '../state/site';
import {
  filtersSignal,
  selectedMetrics,
  toggleMetric,
  type MetricId,
} from '../state/filters';
import { fmtInt, fmtPct, fmtMoney, fmtRpv } from '../lib/fmt';
import { DeltaPill } from '../components/DeltaPill';
import { TrendChart } from './TrendChart';
import './Overview.css';

// Conversion% computed client-side: goals / visitors. The only client-
// derived KPI; covered by Overview.test.tsx specifically.
function conversionPct(d: OverviewResponse): number {
  return d.visitors > 0 ? (d.goals / d.visitors) * 100 : 0;
}

// Shape of delta fields the backend may return in the future (Phase 5f).
// Today /api/stats/overview doesn't ship these — DeltaPill hides itself
// when undefined, so the tile still renders cleanly on v1 backends.
interface WithDelta {
  visitors_delta_pct?: number;
  conversion_delta_pct?: number;
  revenue_delta_pct?: number;
  rpv_delta_pct?: number;
}

// Toggle-button KPI tile. Active state = 2px bottom underline in the
// metric's chart color (Nav Tab idiom, DESIGN.md §5). The
// --card-active-color CSS var is bound by Overview.css via the
// [data-kpi="..."] attribute selector so this component stays free of
// inline style allocations.
interface KpiCardProps {
  id: MetricId;
  label: string;
  value: string;
  tier: 'primary' | 'secondary';
  deltaPct?: number;
  selected: readonly MetricId[];
}

function KpiCard({ id, label, value, tier, deltaPct, selected }: KpiCardProps) {
  const isActive = selected.includes(id);
  const numClass = tier === 'primary' ? 'statnive-num-primary' : 'statnive-num-secondary';
  return (
    <button
      type="button"
      class={'statnive-card' + (isActive ? ' is-active' : '')}
      data-kpi={id}
      aria-pressed={isActive}
      aria-label={`Toggle ${label} on chart`}
      onClick={() => toggleMetric(id)}
    >
      {tier === 'primary' ? (
        <div class="statnive-card-head">
          <div class="statnive-label">
            <span class="statnive-card-dot" aria-hidden="true" />
            {label}
          </div>
          <DeltaPill deltaPct={deltaPct} />
        </div>
      ) : (
        <div class="statnive-label">
          <span class="statnive-card-dot" aria-hidden="true" />
          {label}
        </div>
      )}
      <div class={numClass}>{value}</div>
    </button>
  );
}

export function Overview() {
  const data = useSignal<OverviewResponse | null>(null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    err.value = null;

    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<OverviewResponse>('/api/stats/overview', {
          from: r.from,
          to: r.to,
        }, ac.signal);
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        err.value = e instanceof Error ? e.message : String(e);
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    siteSignal.value,
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.device,
    filtersSignal.value.channel,
    filtersSignal.value.country,
    filtersSignal.value.path,
  ]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Overview</h2>
        <p class="statnive-error">could not load; see logs</p>
      </section>
    );
  }

  const d = data.value;
  if (!d) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Overview</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  const withDelta = d as OverviewResponse & WithDelta;
  const selected = selectedMetrics(filtersSignal.value);
  const currency = activeSiteSignal.value?.currency ?? 'EUR';

  // Primary tier — leads with revenue-connected metrics per CLAUDE.md
  // "Reject vanity metrics". RPV is the only number that connects every
  // other metric to revenue. Each card is a toggle button that adds or
  // removes its metric series on the TrendChart below.
  return (
    <section class="statnive-section">
      <h2 class="statnive-h2">Overview</h2>

      <div data-testid="kpi-primary" class="statnive-kpi-grid-primary">
        <KpiCard id="visitors"   label="Visitors"   value={fmtInt(d.visitors)}             tier="primary" deltaPct={withDelta.visitors_delta_pct}   selected={selected} />
        <KpiCard id="conversion" label="Conversion" value={fmtPct(conversionPct(d))}        tier="primary" deltaPct={withDelta.conversion_delta_pct} selected={selected} />
        <KpiCard id="revenue"    label="Revenue"    value={fmtMoney(d.revenue, currency)}   tier="primary" deltaPct={withDelta.revenue_delta_pct}    selected={selected} />
        <KpiCard id="rpv"        label="RPV"        value={fmtRpv(d.rpv, currency)}         tier="primary" deltaPct={withDelta.rpv_delta_pct}        selected={selected} />
      </div>

      {/* Secondary tier — pageviews + goals, de-emphasized. CLAUDE.md
          explicitly bans leading with pageviews (vanity metric). */}
      <div data-testid="kpi-secondary" class="statnive-kpi-grid-secondary">
        <KpiCard id="pageviews" label="Pageviews" value={fmtInt(d.pageviews)} tier="secondary" selected={selected} />
        <KpiCard id="goals"     label="Goals"     value={fmtInt(d.goals)}     tier="secondary" selected={selected} />
      </div>

      <TrendChart />

      <p class="statnive-meta">
        site={siteSignal.value} · {rangeSignal.value.from} → {rangeSignal.value.to} · refresh page to reload
      </p>
    </section>
  );
}
