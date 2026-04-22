import { useEffect, useRef } from 'preact/hooks';
import uPlot from 'uplot';
import type { Options, AlignedData } from 'uplot';
import 'uplot/dist/uPlot.min.css';

// Chart is a thin Preact wrapper around uPlot (~7 KB gz, MIT). Mounts
// into a div ref on first render, disposes on unmount, and reinstantiates
// whenever the data reference changes. Handles container-resize via a
// ResizeObserver so the chart grows/shrinks with its parent card.
//
// Kept deliberately simple: one data tuple in, one chart out. Callers
// build uPlot Options (series, scales, axes, cursor) themselves — Chart
// doesn't synthesize any defaults.
export interface ChartProps {
  data: AlignedData;
  options: Omit<Options, 'width' | 'height'>;
  height?: number;
}

export function Chart({ data, options, height }: ChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const plotRef = useRef<uPlot | null>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const h = height ?? 220;
    const width = el.clientWidth || 600;
    const plot = new uPlot({ ...options, width, height: h } as Options, data, el);
    plotRef.current = plot;

    const ro = new ResizeObserver(() => {
      if (el.clientWidth > 0) {
        plot.setSize({ width: el.clientWidth, height: h });
      }
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      plot.destroy();
      plotRef.current = null;
    };
    // options + data are caller-owned refs; the effect intentionally
    // re-creates the chart when either identity changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, options]);

  return <div ref={containerRef} class="statnive-chart" />;
}
