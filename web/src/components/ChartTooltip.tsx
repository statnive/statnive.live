import { useComputed, type Signal } from '@preact/signals';
import type { DailyPoint } from '../api/types';
import type { MetricId } from '../state/filters';
import type { MetricSpec, MetricSpecs } from '../lib/chart';
import type { ChartCursorState } from './Chart';
import './ChartTooltip.css';

// ChartTooltip is a brand-styled floating tooltip that reads the
// cursor state published by Chart.tsx and displays the per-day value of
// every active metric. Replaces uPlot's built-in legend so the user
// gets formatted values (€, %, integer) and a colored marker per row
// even when chart lines visually overlap.

export interface ChartTooltipProps {
  cursor: Signal<ChartCursorState | null>;
  data: DailyPoint[] | null;
  metrics: readonly MetricId[];
  specs: MetricSpecs;
  // Width of the chart container in CSS pixels; used to clamp the
  // tooltip's left offset so it never overflows the right edge.
  containerWidth: number;
}

const DATE_FMT = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

export function ChartTooltip({ cursor, data, metrics, specs, containerWidth }: ChartTooltipProps) {
  // useComputed re-renders only when one of these signals changes.
  // Returning null hides the tooltip via CSS display:none below.
  const view = useComputed(() => {
    const c = cursor.value;
    if (!c || !data || data.length === 0 || c.idx >= data.length) return null;
    return { idx: c.idx, left: c.left };
  });

  const v = view.value;
  if (!v) return <div class="statnive-chart-tooltip" aria-hidden="true" />;

  const point = data![v.idx];
  // Clamp the tooltip to stay within the container (tooltip width
  // approximated; the CSS sets a max-width). The transform centers it
  // horizontally on the cursor x, then we clamp left into a safe band.
  const half = 110;
  const left = Math.max(half, Math.min(containerWidth - half, v.left));
  const dateLabel = DATE_FMT.format(new Date(point.day));

  return (
    <div
      class="statnive-chart-tooltip is-visible"
      style={`left: ${left}px`}
      role="status"
    >
      <div class="statnive-chart-tooltip-date">{dateLabel}</div>
      <div class="statnive-chart-tooltip-rows">
        {metrics.map((m) => {
          const spec: MetricSpec = specs[m];
          return (
            <div class="statnive-chart-tooltip-row" key={m}>
              <span
                class="statnive-chart-tooltip-dot"
                style={`border-color: ${spec.stroke}`}
                aria-hidden="true"
              />
              <span class="statnive-chart-tooltip-label">{spec.label}</span>
              <span class="statnive-chart-tooltip-value">{spec.format(spec.value(point))}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
