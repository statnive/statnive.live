import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { DailyPoint } from '../api/types';
import { rangeSignal } from '../state/range';
import { siteSignal, activeSiteSignal } from '../state/site';
import { filtersSignal, selectedMetrics } from '../state/filters';
import { LazyChart } from '../components/LazyChart';
import { buildMetricSpecs, toMetricSeries, metricsLineChartOptions } from '../lib/chart';
import { readBrandTokens } from '../state/tokens';

// Renders one uPlot series per metric in selectedMetrics(filtersSignal).
// LazyChart dynamic-imports uPlot so Overview's first paint doesn't
// carry the chart cost.
export function TrendChart() {
  const data = useSignal<DailyPoint[] | null>(null);
  const err = useSignal<string | null>(null);

  // Bind signals to locals in render so Preact-signals subscribes the
  // component — a read inside the useEffect deps-array literal alone
  // does not register a subscription, so the effect would never re-fire
  // on a site / filter change.
  const siteId = siteSignal.value;
  const range = rangeSignal.value;
  const filters = filtersSignal.value;

  // `metrics` is deliberately excluded — toggling a card is a
  // render-time projection over the same server response, not a refetch
  // trigger.
  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        data.value = await apiGet<DailyPoint[]>(
          '/api/stats/trend',
          { from: range.from, to: range.to },
          ac.signal,
        );
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        err.value = e instanceof Error ? e.message : String(e);
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    siteId,
    range.from,
    range.to,
    filters.device,
    filters.channel,
    filters.country,
    filters.path,
  ]);

  const metrics = selectedMetrics(filters);
  const currency = activeSiteSignal.value?.currency ?? 'EUR';
  const tokens = useMemo(() => readBrandTokens(), []);
  const specs = useMemo(() => buildMetricSpecs(tokens, currency), [tokens, currency]);
  const chartData = useMemo(
    () => (data.value ? toMetricSeries(data.value, metrics, specs) : null),
    [data.value, filters.metrics, specs],
  );
  const chartOptions = useMemo(
    () => metricsLineChartOptions(metrics, specs, tokens),
    [filters.metrics, specs, tokens],
  );

  if (err.value) return null;
  if (!chartData) return null;

  return (
    <div data-testid="overview-trend" style={{ marginTop: 'var(--s-3)' }}>
      <LazyChart
        data={chartData}
        options={chartOptions}
        height={180}
        tooltip={{ data: data.value, metrics, specs }}
      />
    </div>
  );
}
