// DualBar renders the visitors + revenue side-by-side bars that
// implement the "Dual-bar visualization" product principle (top-level
// CLAUDE.md § Product Philosophy). Both values are scaled against the
// row-set max so the reader sees both magnitudes and their ratio.
export interface DualBarProps {
  visitors: number;
  revenue: number;
  maxVisitors: number;
  maxRevenue: number;
}

const fmtInt = (n: number) => n.toLocaleString('en-US');

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
        <span class="statnive-dualbar-value">{fmtInt(props.revenue)} ﷼</span>
      </div>
    </div>
  );
}
