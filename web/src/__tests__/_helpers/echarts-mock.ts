import { vi } from 'vitest';
import type { ChartProps } from '../../components/Chart';

// Shared LazyChart mock for every chart-rendering panel test.
// Captures the ChartProps payload (mainly `option`) that would have
// been passed to the real ECharts instance so tests can assert against
// the option shape without mounting a real canvas.
//
// Pattern matches the LazyChart mock that Sources.test.tsx and
// TrendChart.test.tsx have used historically; the helper centralizes
// it so adding a new chart test is one import line.

export const chartCalls: ChartProps[] = [];

export function resetChartMock(): void {
  chartCalls.length = 0;
}

export function installChartMock(): void {
  vi.mock('../../components/LazyChart', () => ({
    LazyChart: (props: ChartProps) => {
      chartCalls.push(props);
      return null;
    },
  }));
}
