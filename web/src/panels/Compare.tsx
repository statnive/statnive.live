import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import { siteSignal } from '../state/site';
import { filtersSignal } from '../state/filters';
import { fmtInt } from '../lib/fmt';
import './Compare.css';

// Phase 4 of segments — A/B variant comparison panel. Pivots one scoped
// prop (e.g. session:ab_variant) against a goal event name; the backend
// returns one row per distinct value with pooled-variance z-test +
// Wilson CI + sample-size guard already applied. Frontend ships zero
// stats logic.

interface VariantRow {
  value: string;
  visitors: number;
  goal_completions: number;
  conversion_rate: number;
  delta_pp?: number;
  delta_rel?: number;
  p_value?: number;
  significant?: boolean;
  ci_low?: number;
  ci_high?: number;
}

interface CompareResponse {
  dimension: string;
  goal: string;
  control: string;
  variants: VariantRow[];
}

function pct(n: number, digits = 2): string {
  return `${(n * 100).toFixed(digits)}%`;
}

function fmtDelta(pp: number): string {
  return `${pp >= 0 ? '+' : ''}${(pp * 100).toFixed(2)} pp`;
}

function Badge({ row }: { row: VariantRow }) {
  if (row.significant === undefined) {
    if (row.visitors > 0) {
      return <span class="seg-badge seg-badge--na">n/a</span>;
    }
    return null;
  }
  if (row.significant) {
    return <span class="seg-badge seg-badge--sig">SIG</span>;
  }
  return <span class="seg-badge seg-badge--na">n/a</span>;
}

function CompareTable({ data }: { data: CompareResponse }) {
  if (data.variants.length === 0) {
    return (
      <p class="seg-empty">
        Compare needs at least 2 distinct values for the chosen dimension. Try a wider range or a different property.
      </p>
    );
  }

  return (
    <table class="seg-compare-table" role="table">
      <thead>
        <tr>
          <th scope="col">VARIANT</th>
          <th scope="col">VISITORS</th>
          <th scope="col">GOAL</th>
          <th scope="col">CR</th>
          <th scope="col">vs CONTROL</th>
          <th scope="col" title="Wilson 95% interval">95% UI</th>
          <th scope="col">SIG</th>
        </tr>
      </thead>
      <tbody>
        {data.variants.map((row) => (
          <tr class={row.value === data.control ? 'seg-row-control' : 'seg-row-variant'} key={row.value}>
            <td>
              <strong>{row.value || '(empty)'}</strong>
              {row.value === data.control && <div class="seg-control-tag">CONTROL</div>}
            </td>
            <td class="seg-num">{fmtInt(row.visitors)}</td>
            <td class="seg-num">{fmtInt(row.goal_completions)}</td>
            <td class="seg-num">{pct(row.conversion_rate, 2)}</td>
            <td class="seg-num">
              {row.delta_pp !== undefined ? (
                <>
                  <div class={row.delta_pp >= 0 ? 'seg-delta-ok' : 'seg-delta-bad'}>{fmtDelta(row.delta_pp)}</div>
                  {row.delta_rel !== undefined && (
                    <div class="seg-delta-rel">{(row.delta_rel * 100 >= 0 ? '+' : '')}{(row.delta_rel * 100).toFixed(1)}% rel</div>
                  )}
                </>
              ) : (
                <span class="seg-dash">—</span>
              )}
            </td>
            <td class="seg-num">
              {row.ci_low !== undefined && row.ci_high !== undefined ? (
                <span>{pct(row.ci_low, 2)} – {pct(row.ci_high, 2)}</span>
              ) : (
                <span class="seg-dash">—</span>
              )}
            </td>
            <td><Badge row={row} /></td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export default function Compare() {
  const dimension = useSignal<string>('session:ab_variant');
  const goal = useSignal<string>('signup');
  const data = useSignal<CompareResponse | null>(null);
  const err = useSignal<string | null>(null);
  const loading = useSignal<boolean>(false);

  useEffect(() => {
    const controller = new AbortController();
    loading.value = true;
    err.value = null;

    apiGet<CompareResponse>(
      '/api/stats/compare',
      { dimension: dimension.value, goal: goal.value },
      controller.signal,
    )
      .then((res) => {
        data.value = res;
        loading.value = false;
      })
      .catch((e: unknown) => {
        if (controller.signal.aborted) return;
        err.value = e instanceof Error ? e.message : String(e);
        loading.value = false;
      });

    return () => controller.abort();
  }, [siteSignal.value, dimension.value, goal.value, filtersSignal.value]);

  return (
    <section class="statnive-section seg-compare-panel">
      <h2>Compare</h2>

      <div class="seg-compare-pickers">
        <label>
          <span>Dimension</span>
          <input
            type="text"
            value={dimension.value}
            placeholder="session:ab_variant"
            onInput={(e) => {
              dimension.value = (e.target as HTMLInputElement).value;
            }}
          />
        </label>
        <label>
          <span>Goal</span>
          <input
            type="text"
            value={goal.value}
            placeholder="signup"
            onInput={(e) => {
              goal.value = (e.target as HTMLInputElement).value;
            }}
          />
        </label>
      </div>

      {loading.value && <p class="seg-loading">Loading…</p>}
      {err.value && <p class="statnive-error">{err.value}</p>}
      {data.value && <CompareTable data={data.value} />}

      <p class="seg-methodology">
        Pooled-variance two-proportion z-test, α = 0.05; Wilson 95% interval for the bounds.
        SIG means the conversion-rate gap is unlikely to be chance at this sample.
        Does NOT correct for multiple comparisons or sequential peeking. Treat first-look results as directional.
      </p>
    </section>
  );
}
