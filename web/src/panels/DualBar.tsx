import { fmtInt, fmtMoney } from '../lib/fmt';

// DualBar renders the visitors + revenue side-by-side bars that
// implement the "Dual-bar visualization" product principle (top-level
// CLAUDE.md § Product Philosophy). Both values are scaled against the
// row-set max so the reader sees both magnitudes and their ratio.
// `currency` is the active site's ISO 4217 code; the bar label is
// formatted via Intl.NumberFormat with that code.
export interface DualBarProps {
  visitors: number;
  revenue: number;
  maxVisitors: number;
  maxRevenue: number;
  currency: string;
}

function pct(value: number, max: number): string {
  if (max <= 0) return '0%';
  return Math.round((value / max) * 100) + '%';
}

export function DualBar(props: DualBarProps) {
  return (
    <div class="statnive-dualbar">
      <div class="statnive-dualbar-row">
        <span class="statnive-dualbar-track">
          <span
            class="statnive-dualbar-fill is-visitors"
            style={{ width: pct(props.visitors, props.maxVisitors) }}
          />
        </span>
        <span class="statnive-dualbar-value">{fmtInt(props.visitors)}</span>
      </div>
      <div class="statnive-dualbar-row">
        <span class="statnive-dualbar-track">
          <span
            class="statnive-dualbar-fill is-revenue"
            style={{ width: pct(props.revenue, props.maxRevenue) }}
          />
        </span>
        <span class="statnive-dualbar-value">{fmtMoney(props.revenue, props.currency)}</span>
      </div>
    </div>
  );
}
