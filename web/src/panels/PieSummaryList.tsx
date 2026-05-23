import { fmtSharePct } from '../lib/fmt';

// PieSummaryList is the right-hand "TOP CHANNELS" panel inside a pie
// card. Pure presentational: takes a flat row array (channel, value,
// pct, color) and renders swatch + name + bar + share% (raw value).
// Shared between SourcesByChannelChart and CampaignCharts so the
// per-row visual grammar stays single-sourced.

export interface PieSummaryRow {
  channel: string;
  value: number;
  pct: number;
  color: string;
}

export interface PieSummaryListProps {
  rows: PieSummaryRow[];
  formatValue: (n: number) => string;
}

export function PieSummaryList({ rows, formatValue }: PieSummaryListProps) {
  return (
    <ul class="statnive-pie-summary-list">
      {rows.map((row) => (
        <li class="statnive-pie-summary-row" key={row.channel} data-channel={row.channel}>
          <span
            class="statnive-pie-summary-swatch"
            style={`background:${row.color}`}
            aria-hidden="true"
          />
          <span class="statnive-pie-summary-name">{row.channel || '·'}</span>
          <span class="statnive-pie-summary-bar" aria-hidden="true">
            <span
              class="statnive-pie-summary-fill"
              style={`width:${row.pct}%;background:${row.color}`}
            />
          </span>
          <span class="statnive-pie-summary-pct">
            {fmtSharePct(row.pct)}
            <span class="statnive-pie-summary-raw"> ({formatValue(row.value)})</span>
          </span>
          <span class="statnive-sr-only">{formatValue(row.value)}</span>
        </li>
      ))}
    </ul>
  );
}
