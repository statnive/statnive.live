import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { OverviewResponse } from '../api/types';
import { rangeSignal } from '../state/range';
import { siteSignal } from '../state/site';
import './Overview.css';

// Conversion% computed client-side: goals / visitors. The only client-
// derived KPI; covered by Overview.test.tsx specifically.
function conversionPct(d: OverviewResponse): number {
  return d.visitors > 0 ? (d.goals / d.visitors) * 100 : 0;
}

const fmtInt = (n: number) => n.toLocaleString('en-US');
const fmtPct = (n: number) => n.toFixed(2) + '%';
const fmtRials = (n: number) => fmtInt(n) + ' ﷼';

export function Overview() {
  const data = useSignal<OverviewResponse | null>(null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    err.value = null;

    // Cancelled flag guards against a fetch completing after unmount —
    // without it, a re-mount would see stale data written by the prior
    // instance's in-flight request.
    let cancelled = false;

    (async () => {
      try {
        const r = rangeSignal.value;
        const resp = await apiGet<OverviewResponse>('/api/stats/overview', {
          from: r.from,
          to: r.to,
        });
        if (!cancelled) data.value = resp;
      } catch (e: unknown) {
        if (!cancelled) err.value = e instanceof Error ? e.message : String(e);
      }
    })();

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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

  // Primary tier — leads with revenue-connected metrics per CLAUDE.md
  // "Reject vanity metrics". RPV is the only number that connects every
  // other metric to revenue.
  return (
    <section class="statnive-section">
      <h2 class="statnive-h2">Overview</h2>

      <div data-testid="kpi-primary" class="statnive-kpi-grid-primary">
        <div class="statnive-card" data-kpi="visitors">
          <div class="statnive-label">Visitors</div>
          <div class="statnive-num-primary">{fmtInt(d.visitors)}</div>
        </div>
        <div class="statnive-card" data-kpi="conversion">
          <div class="statnive-label">Conversion</div>
          <div class="statnive-num-primary">{fmtPct(conversionPct(d))}</div>
        </div>
        <div class="statnive-card" data-kpi="revenue">
          <div class="statnive-label">Revenue</div>
          <div class="statnive-num-primary">{fmtRials(d.revenue_rials)}</div>
        </div>
        <div class="statnive-card" data-kpi="rpv">
          <div class="statnive-label">RPV</div>
          <div class="statnive-num-primary">{fmtRials(Math.round(d.rpv_rials))}</div>
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

      <p class="statnive-meta">
        site={siteSignal.value} · {rangeSignal.value.from} → {rangeSignal.value.to} · refresh page to reload
      </p>
    </section>
  );
}
