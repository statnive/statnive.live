import { useEffect, useRef, useState } from 'preact/hooks';
import { signal, useSignal } from '@preact/signals';
import uPlot from 'uplot';
import type { Options, AlignedData } from 'uplot';
import 'uplot/dist/uPlot.min.css';
import './Chart.css';
import { ChartTooltip, type ChartTooltipProps } from './ChartTooltip';

// Chart is a thin Preact wrapper around uPlot (~7 KB gz, MIT). Mounts
// into a div ref on first render, disposes on unmount, and reinstantiates
// whenever the data reference changes. Handles container-resize via a
// ResizeObserver so the chart grows/shrinks with its parent card.
//
// Optionally renders a brand-styled hover tooltip beneath the canvas
// when `tooltip` props are supplied — kept inside this file (and
// therefore inside the lazy chart chunk) so the initial SPA bundle
// doesn't carry the tooltip code until a chart actually mounts.
export interface ChartProps {
  data: AlignedData;
  options: Omit<Options, 'width' | 'height'>;
  height?: number;
  // Optional rich-tooltip wiring. Omit `tooltip` for charts that don't
  // need it (e.g. SEO panel's single-line trend).
  tooltip?: Omit<ChartTooltipProps, 'cursor' | 'containerWidth'>;
}

export interface ChartCursorState {
  // Index into the data arrays for the column under the cursor.
  idx: number;
  // Pixel offsets relative to the chart's bounding rect.
  left: number;
  top: number;
}

// Module-level no-op signal so callers that don't pass cursorSignal
// still wire up the hook without allocating a new signal per chart.
const noopCursor = signal<ChartCursorState | null>(null);

export function Chart({ data, options, height, tooltip }: ChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const plotRef = useRef<uPlot | null>(null);
  const cursor = useSignal<ChartCursorState | null>(null);
  const [containerWidth, setContainerWidth] = useState(600);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const out = tooltip ? cursor : noopCursor;
    const h = height ?? 220;
    const width = el.clientWidth || 600;
    setContainerWidth(width);
    const optsWithHooks: Options = {
      ...options,
      width,
      height: h,
      hooks: {
        ...(options.hooks ?? {}),
        setCursor: [
          (u) => {
            const { idx, left, top } = u.cursor;
            if (idx == null || left == null || left < 0 || top == null || top < 0) {
              if (out.value !== null) out.value = null;
              return;
            }
            out.value = { idx, left, top };
          },
        ],
      },
    } as Options;
    const plot = new uPlot(optsWithHooks, data, el);
    plotRef.current = plot;

    const ro = new ResizeObserver(() => {
      if (el.clientWidth > 0) {
        plot.setSize({ width: el.clientWidth, height: h });
        setContainerWidth(el.clientWidth);
      }
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      plot.destroy();
      plotRef.current = null;
      if (cursor.value !== null) cursor.value = null;
    };
    // options + data are caller-owned refs; the effect intentionally
    // re-creates the chart when either identity changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, options]);

  if (!tooltip) {
    return <div ref={containerRef} class="statnive-chart" />;
  }
  return (
    <div class="statnive-chart-wrap" style="position:relative">
      <div ref={containerRef} class="statnive-chart" />
      <ChartTooltip
        {...tooltip}
        cursor={cursor}
        containerWidth={containerWidth}
      />
    </div>
  );
}
