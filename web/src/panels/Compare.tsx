import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import { siteSignal } from '../state/site';
import { filtersSignal } from '../state/filters';
import { fmtInt } from '../lib/fmt';
import { SegmentCaption } from '../components/SegmentCaption';
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

interface PropNameRow {
  name: string;
  sample_values: string[];
  last_seen: string;
}

interface GoalSummary {
  name: string;
  pattern: string;
}

const API_SCOPES = ['hit', 'session', 'user'] as const;
type ApiScope = (typeof API_SCOPES)[number];

// Module-level catalogue caches survive panel remount within a session.
// Keyed by site_id; SiteSwitcher swaps the key, no manual invalidation.
const dimensionCatalog = new Map<number, ComboOption[]>();
const goalCatalog = new Map<number, ComboOption[]>();

// Debounce window for keystroke-driven /api/stats/compare refire. Below
// 200 ms still spams CH; above 400 ms feels laggy on a paste.
const COMPARE_DEBOUNCE_MS = 250;

function pct(n: number, digits = 2): string {
  return `${(n * 100).toFixed(digits)}%`;
}

function fmtDelta(pp: number): string {
  return `${pp >= 0 ? '+' : ''}${(pp * 100).toFixed(2)} pp`;
}

function Badge({ row }: { row: VariantRow }) {
  if (row.significant === undefined) {
    if (row.visitors > 0) {
      return <span class="seg-badge seg-badge--na" title="Not enough sample yet (need at least 100 visitors and 25 conversions per variant)">n/a</span>;
    }
    return null;
  }
  if (row.significant) {
    return <span class="seg-badge seg-badge--sig" title="The gap is unlikely to be chance at this sample size">SIG</span>;
  }
  return <span class="seg-badge seg-badge--na" title="The gap could still be chance at this sample size">n/a</span>;
}

function CompareTable({ data }: { data: CompareResponse }) {
  if (data.variants.length === 0) {
    return (
      <p class="seg-empty">
        No data for this property yet. Try a longer date range, a different property,
        or check that visitors are firing the goal.
      </p>
    );
  }

  return (
    <table class="seg-compare-table" role="table" aria-label={`Variants of ${data.dimension} measured against ${data.goal}`}>
      <thead>
        <tr>
          <th scope="col">VARIANT</th>
          <th scope="col">VISITORS</th>
          <th scope="col">CONVERSIONS</th>
          <th scope="col">RATE</th>
          <th scope="col">VS CONTROL</th>
          <th scope="col" title="Wilson 95% interval: where the true rate is likely to sit">UNCERTAINTY (95%)</th>
          <th scope="col" title="SIG = unlikely to be chance · n/a = not enough sample">SIGNIFICANCE</th>
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
                <span class="seg-dash" title="Control row (nothing to compare against)">—</span>
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

interface ComboOption {
  id: string;
  primary: string;
  secondary?: string;
}

interface ComboboxProps {
  inputId: string;
  value: string;
  options: ComboOption[];
  placeholder: string;
  ariaLabel: string;
  describedBy?: string;
  onChange(value: string): void;
}

// Open-on-focus combobox. mousedown.preventDefault on each <li> blocks
// the focus shift, so blur never fires from a click; no setTimeout dance
// needed (Compare-rewrite simplify pass).
function Combobox({
  inputId,
  value,
  options,
  placeholder,
  ariaLabel,
  describedBy,
  onChange,
}: ComboboxProps) {
  const open = useSignal(false);
  const highlight = useSignal(0);
  const listId = `${inputId}-list`;

  const q = value.trim().toLowerCase();
  const filtered = useMemo(() => {
    const pool = q === '' ? options : options.filter((o) =>
      o.primary.toLowerCase().includes(q) ||
      (o.secondary?.toLowerCase().includes(q) ?? false),
    );
    return pool.slice(0, 8);
  }, [options, q]);

  useEffect(() => {
    highlight.value = 0;
  }, [filtered.length, value]);

  const commit = (opt: ComboOption | undefined) => {
    if (opt) onChange(opt.id);
    open.value = false;
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      open.value = true;
      highlight.value = Math.min(highlight.value + 1, Math.max(filtered.length - 1, 0));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      highlight.value = Math.max(highlight.value - 1, 0);
      return;
    }
    if (e.key === 'Enter' && open.value && filtered[highlight.value]) {
      e.preventDefault();
      commit(filtered[highlight.value]);
      return;
    }
    if (e.key === 'Escape') {
      open.value = false;
    }
  };

  const showList = open.value && filtered.length > 0;

  return (
    <div class="seg-combo">
      <input
        id={inputId}
        type="text"
        value={value}
        placeholder={placeholder}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={showList}
        aria-controls={listId}
        aria-activedescendant={showList ? `${listId}-opt-${highlight.value}` : undefined}
        aria-label={ariaLabel}
        aria-describedby={describedBy}
        onFocus={() => { open.value = true; }}
        onBlur={() => { open.value = false; }}
        onInput={(e) => {
          onChange((e.target as HTMLInputElement).value);
          open.value = true;
        }}
        onKeyDown={onKey}
      />
      {showList && (
        <ul id={listId} role="listbox" class="seg-combo-list">
          {filtered.map((opt, i) => (
            <li
              key={opt.id}
              id={`${listId}-opt-${i}`}
              role="option"
              aria-selected={i === highlight.value}
              class={'seg-combo-row' + (i === highlight.value ? ' is-active' : '')}
              onMouseDown={(e) => {
                e.preventDefault();
                commit(opt);
              }}
            >
              <span class="seg-combo-primary">{opt.primary}</span>
              {opt.secondary ? <span class="seg-combo-secondary">{opt.secondary}</span> : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export default function Compare() {
  const dimension = useSignal<string>('session:ab_variant');
  const goal = useSignal<string>('signup');
  const data = useSignal<CompareResponse | null>(null);
  const err = useSignal<string | null>(null);
  const loading = useSignal<boolean>(false);

  // Catalogues projected to ComboOption at fetch time (no signal-then-
  // useMemo re-derivation). Module caches above survive remount.
  const dimensionOptions = useSignal<ComboOption[]>(dimensionCatalog.get(siteSignal.value) ?? []);
  const goalOptions = useSignal<ComboOption[]>(goalCatalog.get(siteSignal.value) ?? []);

  useEffect(() => {
    const siteID = siteSignal.value;
    const cachedDims = dimensionCatalog.get(siteID);
    const cachedGoals = goalCatalog.get(siteID);

    if (cachedDims) dimensionOptions.value = cachedDims;
    if (cachedGoals) goalOptions.value = cachedGoals;

    if (cachedDims && cachedGoals) return;

    const ctrl = new AbortController();

    if (!cachedDims) {
      Promise.all(
        API_SCOPES.map((s: ApiScope) =>
          apiGet<PropNameRow[]>('/api/props/list', { scope: s }, ctrl.signal)
            .then((rows): ComboOption[] => rows.map((r) => ({
              id: `${s}:${r.name}`,
              primary: `${s}:${r.name}`,
              secondary: `${r.sample_values.length}v`,
            })))
            .catch(() => [] as ComboOption[]),
        ),
      ).then((batches) => {
        if (ctrl.signal.aborted) return;
        const flat = batches.flat();
        dimensionCatalog.set(siteID, flat);
        dimensionOptions.value = flat;
      });
    }

    if (!cachedGoals) {
      apiGet<GoalSummary[]>('/api/goals/list', {}, ctrl.signal)
        .then((rows): ComboOption[] => rows.map((g) => ({
          id: g.pattern,
          primary: g.pattern,
          secondary: g.name && g.name !== g.pattern ? g.name : undefined,
        })))
        .then((opts) => {
          if (ctrl.signal.aborted) return;
          goalCatalog.set(siteID, opts);
          goalOptions.value = opts;
        })
        .catch(() => { /* SPA falls back to free-text */ });
    }

    return () => ctrl.abort();
  }, [siteSignal.value]);

  // Debounced query trigger — keystrokes on dimension/goal pickers
  // would otherwise spam /api/stats/compare; AbortController cancels
  // in-flight but the server still pays the early-CH cost.
  useEffect(() => {
    const ctrl = new AbortController();

    const timer = window.setTimeout(() => {
      if (ctrl.signal.aborted) return;
      loading.value = true;
      err.value = null;

      apiGet<CompareResponse>(
        '/api/stats/compare',
        { dimension: dimension.value, goal: goal.value },
        ctrl.signal,
      )
        .then((res) => {
          data.value = res;
          loading.value = false;
        })
        .catch((e: unknown) => {
          if (ctrl.signal.aborted) return;
          err.value = e instanceof Error ? e.message : String(e);
          loading.value = false;
        });
    }, COMPARE_DEBOUNCE_MS);

    return () => {
      window.clearTimeout(timer);
      ctrl.abort();
    };
  }, [siteSignal.value, dimension.value, goal.value, filtersSignal.value]);

  return (
    <section class="statnive-section seg-compare-panel">
      <h2 class="statnive-h2">Compare variants</h2>
      <p class="seg-compare-lead">
        Split visitors by a property, then measure how each value converts on a goal.
        Use it to read A/B tests, plan tiers, or any segment you tag at the tracker.
      </p>
      <SegmentCaption lead="Within" />

      <div class="seg-compare-pickers" role="group" aria-label="Comparison setup">
        <label for="seg-dim-input">
          <span>Split visitors by</span>
          <Combobox
            inputId="seg-dim-input"
            value={dimension.value}
            options={dimensionOptions.value}
            placeholder="session:ab_variant"
            ariaLabel="Property to split visitors by"
            describedBy="seg-dim-hint"
            onChange={(v) => { dimension.value = v; }}
          />
          <small id="seg-dim-hint" class="seg-input-hint">
            Format: <code>scope:name</code>. Scope is <code>hit</code>, <code>session</code>, or <code>user</code>.
          </small>
        </label>
        <label for="seg-goal-input">
          <span>Measure goal</span>
          <Combobox
            inputId="seg-goal-input"
            value={goal.value}
            options={goalOptions.value}
            placeholder="signup"
            ariaLabel="Goal event name to measure"
            describedBy="seg-goal-hint"
            onChange={(v) => { goal.value = v; }}
          />
          <small id="seg-goal-hint" class="seg-input-hint">
            The event name passed to <code>track()</code>, e.g. <code>signup</code> or <code>purchase</code>.
          </small>
        </label>
      </div>

      {loading.value && <p class="seg-loading">Computing variant stats…</p>}
      {err.value && (
        <p class="statnive-error">
          Could not load this comparison. Check the property scope and goal name, then try again.
        </p>
      )}
      {data.value && <CompareTable data={data.value} />}

      <details class="seg-methodology-details">
        <summary>How significance is calculated</summary>
        <p class="seg-methodology">
          Pooled-variance two-proportion z-test (α = 0.05), Wilson 95% interval for the bounds.
          <strong> SIG</strong> appears when the conversion-rate gap is unlikely to be chance at the current sample;
          <strong> n/a</strong> means you need at least 100 visitors and 25 conversions per variant before a verdict is honest.
          Does not correct for multiple comparisons or sequential peeking. Treat first-look results as directional.
        </p>
      </details>
    </section>
  );
}
