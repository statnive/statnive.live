import { useSignal } from '@preact/signals';
import {
  rangeSignal,
  setRange,
  presetToRange,
  isValidIrstRange,
  type DatePreset,
} from '../state/range';
import './DatePicker.css';

// Hoisted outside component per `rendering-hoist-jsx`.
const PRESETS: ReadonlyArray<{ id: DatePreset; label: string }> = [
  { id: '7d', label: 'Last 7 days' },
  { id: '30d', label: 'Last 30 days' },
  { id: '90d', label: 'Last 90 days' },
  { id: 'custom', label: 'Custom' },
];

function matchPreset(from: string, to: string): DatePreset {
  for (const p of ['7d', '30d', '90d'] as DatePreset[]) {
    const r = presetToRange(p);
    if (r.from === from && r.to === to) return p;
  }
  return 'custom';
}

export function DatePicker() {
  const r = rangeSignal.value;
  const active = useSignal<DatePreset>(matchPreset(r.from, r.to));
  const customFrom = useSignal(r.from);
  const customTo = useSignal(r.to);
  const err = useSignal<string | null>(null);

  const applyPreset = (p: DatePreset) => {
    active.value = p;
    err.value = null;
    if (p === 'custom') {
      customFrom.value = r.from;
      customTo.value = r.to;
      return;
    }
    const next = presetToRange(p);
    setRange(next.from, next.to);
  };

  const applyCustom = () => {
    if (!isValidIrstRange(customFrom.value, customTo.value)) {
      err.value = 'dates must be YYYY-MM-DD and from ≤ to';
      return;
    }
    err.value = null;
    setRange(customFrom.value, customTo.value);
  };

  return (
    <div class="statnive-datepicker" data-testid="datepicker">
      <div class="statnive-datepicker-presets" role="group" aria-label="Date range preset">
        {PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            class={'statnive-chip' + (active.value === p.id ? ' is-active' : '')}
            aria-pressed={active.value === p.id}
            onClick={() => applyPreset(p.id)}
          >
            {p.label}
          </button>
        ))}
      </div>

      {active.value === 'custom' ? (
        <div class="statnive-datepicker-custom">
          <label class="statnive-label" htmlFor="dp-from">From</label>
          <input
            id="dp-from"
            type="date"
            value={customFrom.value}
            onInput={(e) => {
              customFrom.value = (e.target as HTMLInputElement).value;
            }}
          />
          <label class="statnive-label" htmlFor="dp-to">To</label>
          <input
            id="dp-to"
            type="date"
            value={customTo.value}
            onInput={(e) => {
              customTo.value = (e.target as HTMLInputElement).value;
            }}
          />
          <button type="button" class="statnive-chip" onClick={applyCustom}>
            Apply
          </button>
          {err.value ? <span class="statnive-error">{err.value}</span> : null}
        </div>
      ) : null}
    </div>
  );
}
