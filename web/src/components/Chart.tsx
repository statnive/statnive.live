import { useEffect, useRef } from 'preact/hooks';
import * as echarts from 'echarts/core';
import {
  LineChart,
  BarChart,
  PieChart,
} from 'echarts/charts';
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  AriaComponent,
  TitleComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { EChartsCoreOption, ECharts as EChartsInstance } from 'echarts/core';
import './Chart.css';

// Chart is a thin Preact wrapper around Apache ECharts. The chart chunk
// lives behind LazyChart so the initial SPA bundle stays free of
// ECharts until a chart actually mounts. Tree-shake discipline: only
// the chart types + components statnive-live needs are registered here
// (LineChart for Trend/SEO, BarChart for the Campaigns top-N chart,
// PieChart for Sources + Campaigns pies). One stray `import 'echarts'`
// (full package) anywhere in the tree would defeat the tree-shake;
// `web/src/__tests__/echarts-imports.test.ts` is the guardrail.

echarts.use([
  LineChart,
  BarChart,
  PieChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  AriaComponent,
  TitleComponent,
  CanvasRenderer,
]);

export interface ChartProps {
  option: EChartsCoreOption;
  height?: number;
}

export function Chart({ option, height }: ChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<EChartsInstance | null>(null);

  // Mount once, dispose on unmount. ECharts is designed to be reused
  // via setOption; recreating the instance on every option change would
  // tear down the canvas, drop animation continuity, and burn ~15-40ms
  // per metric toggle. The setOption effect below feeds new options
  // into the existing instance.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const chart = echarts.init(el, null, { renderer: 'canvas' });
    chartRef.current = chart;

    const ro = new ResizeObserver(() => {
      if (el.clientWidth > 0) {
        chart.resize();
      }
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    chart.setOption(option, { notMerge: true, lazyUpdate: true });
  }, [option]);

  const h = height ?? 220;
  return <div ref={containerRef} class="statnive-chart" style={`height:${h}px`} />;
}
