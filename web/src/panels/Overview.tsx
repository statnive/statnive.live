import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { OverviewResponse } from '../api/types';
import { rangeSignal } from '../state/range';
import { siteSignal, activeSiteSignal } from '../state/site';
import { filtersSignal } from '../state/filters';
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
        <p class="statnive-error">could not load — see logs</p>
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

  // Primary tier — leads with revenue-connected metrics per CLAUDE.md
  // "Reject vanity metrics". RPV is the only number that connects every
  // other metric to revenue.
  return (
    <section class="statnive-section">
      <h2 class="statnive-h2">Overview</h2>

      <div data-testid="kpi-primary" class="statnive-kpi-grid-primary">
        <div class="statnive-card" data-kpi="visitors">
          <div class="statnive-card-head">
            <div class="statnive-label">Visitors</div>
            <DeltaPill deltaPct={withDelta.visitors_delta_pct} />
          </div>
          <div class="statnive-num-primary">{fmtInt(d.visitors)}</div>
        </div>
        <div class="statnive-card" data-kpi="conversion">
          <div class="statnive-card-head">
            <div class="statnive-label">Conversion</div>
            <DeltaPill deltaPct={withDelta.conversion_delta_pct} />
          </div>
          <div class="statnive-num-primary">{fmtPct(conversionPct(d))}</div>
        </div>
        <div class="statnive-card" data-kpi="revenue">
          <div class="statnive-card-head">
            <div class="statnive-label">Revenue</div>
            <DeltaPill deltaPct={withDelta.revenue_delta_pct} />
          </div>
          <div class="statnive-num-primary">{fmtMoney(d.revenue, activeSiteSignal.value?.currency ?? 'EUR')}</div>
        </div>
        <div class="statnive-card" data-kpi="rpv">
          <div class="statnive-card-head">
            <div class="statnive-label">RPV</div>
            <DeltaPill deltaPct={withDelta.rpv_delta_pct} />
          </div>
          <div class="statnive-num-primary">{fmtRpv(d.rpv, activeSiteSignal.value?.currency ?? 'EUR')}</div>
        </div>
      </div>

      {/* Secondary tier — pageviews + goals, de-emphasized. CLAUDE.md
          explicitly bans leading with pageviews (vanity metric). */}
      <div data-testid="kpi-secondary" class="statnive-kpi-grid-secondary">
        <div class="statnive-card" data-kpi="pageviews">
          <div class="statnive-label">Pageviews</div>
          <div class="statnive-num-secondary">{fmtInt(d.pageviews)}</div>
        </div>
        <div class="statnive-card" data-kpi="goals">
          <div class="statnive-label">Goals</div>
          <div class="statnive-num-secondary">{fmtInt(d.goals)}</div>
        </div>
      </div>

      <TrendChart />

      <p class="statnive-meta">
        site={siteSignal.value} · {rangeSignal.value.from} → {rangeSignal.value.to} · refresh page to reload
      </p>
    </section>
  );
}
