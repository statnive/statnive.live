import './DeltaPill.css';

// DeltaPill renders the up/down/flat change-vs-previous-period indicator
// used on KPI tiles. v1 falls back to hiding itself when `deltaPct` is
// undefined — the backend at /api/stats/overview doesn't ship previous-
// period comparison today (follow-up slice Phase 5f wires that up).
// Component degrades cleanly so the tile layout still reads right.
export interface DeltaPillProps {
  /** Percent delta vs previous period. `undefined` hides the pill. */
  deltaPct?: number;
  /** Optional caption, e.g. "vs previous 7 days". Mono, ink-60%. */
  vsLabel?: string;
}

function direction(p: number): 'up' | 'down' | 'flat' {
  // ±1% deadband so small noise doesn't flip direction every refresh.
  if (p > 1) return 'up';
  if (p < -1) return 'down';
  return 'flat';
}

const ARROW: Record<'up' | 'down' | 'flat', string> = {
  up: '↑',
  down: '↓',
  flat: '•',
};

export function DeltaPill({ deltaPct, vsLabel }: DeltaPillProps) {
  if (deltaPct == null || Number.isNaN(deltaPct)) return null;

  const dir = direction(deltaPct);
  const classes = ['statnive-delta-pill', `is-${dir}`].join(' ');
  const sign = deltaPct > 0 ? '+' : '';
  const pct = `${sign}${deltaPct.toFixed(1)}%`;

  return (
    <span class="statnive-delta" aria-label={`${dir} ${pct} ${vsLabel ?? ''}`.trim()}>
      <span class={classes}>
        <span class="delta-arrow" aria-hidden="true">{ARROW[dir]}</span>
        <span class="delta-pct">{pct}</span>
      </span>
      {vsLabel ? <span class="delta-vs">{vsLabel}</span> : null}
    </span>
  );
}
