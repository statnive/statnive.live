import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/preact';

// Stub ECharts module before Chart.tsx imports it. The stub exposes a
// per-test-resettable spy harness so we can assert init/setOption/
// resize/dispose are called with the right arguments and counts.

interface ChartStub {
  setOption: ReturnType<typeof vi.fn>;
  resize: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
}

const initSpy = vi.fn();
const lastChart: { instance: ChartStub | null } = { instance: null };

function makeStub(): ChartStub {
  return {
    setOption: vi.fn(),
    resize: vi.fn(),
    dispose: vi.fn(),
  };
}

vi.mock('echarts/core', () => ({
  use: vi.fn(),
  init: (...args: unknown[]) => {
    initSpy(...args);
    const inst = makeStub();
    lastChart.instance = inst;
    return inst;
  },
}));

vi.mock('echarts/charts', () => ({
  LineChart: {},
  BarChart: {},
  PieChart: {},
}));

vi.mock('echarts/components', () => ({
  GridComponent: {},
  TooltipComponent: {},
  LegendComponent: {},
  AriaComponent: {},
  TitleComponent: {},
}));

vi.mock('echarts/renderers', () => ({
  CanvasRenderer: {},
}));

import { Chart } from '../components/Chart';

describe('Chart wrapper lifecycle', () => {
  beforeEach(() => {
    initSpy.mockClear();
    lastChart.instance = null;
  });

  afterEach(() => {
    cleanup();
  });

  it('calls echarts.init exactly once on mount with the canvas renderer', () => {
    render(<Chart option={{ series: [] }} />);
    expect(initSpy).toHaveBeenCalledTimes(1);
    const [, theme, opts] = initSpy.mock.calls[0] as [HTMLDivElement, null, { renderer: string }];
    expect(theme).toBeNull();
    expect(opts.renderer).toBe('canvas');
  });

  it('calls setOption with the supplied option (notMerge + lazyUpdate)', () => {
    const option = { series: [{ type: 'line' as const, data: [] }] };
    render(<Chart option={option} />);
    const inst = lastChart.instance!;
    expect(inst.setOption).toHaveBeenCalledTimes(1);
    expect(inst.setOption).toHaveBeenCalledWith(option, { notMerge: true, lazyUpdate: true });
  });

  it('sets the container height inline based on the height prop (default 220)', () => {
    const { container } = render(<Chart option={{ series: [] }} />);
    const div = container.querySelector<HTMLDivElement>('.statnive-chart');
    expect(div?.style.height).toBe('220px');
  });

  it('honors a custom height prop', () => {
    const { container } = render(<Chart option={{ series: [] }} height={280} />);
    const div = container.querySelector<HTMLDivElement>('.statnive-chart');
    expect(div?.style.height).toBe('280px');
  });

  it('calls dispose on unmount', () => {
    const { unmount } = render(<Chart option={{ series: [] }} />);
    const inst = lastChart.instance!;
    expect(inst.dispose).not.toHaveBeenCalled();
    unmount();
    expect(inst.dispose).toHaveBeenCalledTimes(1);
  });

  it('reuses the same ECharts instance via setOption on option-prop change (no dispose+init thrash)', () => {
    const { rerender } = render(<Chart option={{ series: [] }} />);
    expect(initSpy).toHaveBeenCalledTimes(1);
    const inst = lastChart.instance!;
    expect(inst.setOption).toHaveBeenCalledTimes(1);

    rerender(<Chart option={{ series: [{ type: 'pie' as const, data: [] }] }} />);

    // Same instance — dispose NOT called; init NOT called again;
    // setOption called a second time with the new option.
    expect(inst.dispose).not.toHaveBeenCalled();
    expect(initSpy).toHaveBeenCalledTimes(1);
    expect(inst.setOption).toHaveBeenCalledTimes(2);
    expect(lastChart.instance).toBe(inst);
  });
});
