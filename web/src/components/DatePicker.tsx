import { useEffect, useMemo, useRef } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import {
  rangeSignal,
  setRange,
  presetToRange,
  isValidIrstRange,
  addDayIRST,
  type DatePreset,
} from '../state/range';
import { useCallyReady } from './LazyCally';
import { Loader } from './Loader';
import './DatePicker.css';

const PRESETS: ReadonlyArray<{ id: DatePreset; label: string }> = [
  { id: 'today', label: 'Today' },
  { id: 'yesterday', label: 'Yesterday' },
  { id: '7d', label: 'Last 7 days' },
  { id: '30d', label: 'Last 30 days' },
  { id: '90d', label: 'Last 90 days' },
  { id: 'custom', label: 'Custom' },
];

const DURATION_PRESETS: ReadonlyArray<Exclude<DatePreset, 'custom'>> = [
  'today',
  'yesterday',
  '7d',
  '30d',
  '90d',
];

function matchPreset(from: string, to: string): DatePreset {
  for (const p of DURATION_PRESETS) {
    const r = presetToRange(p);
    if (r.from === from && r.to === to) return p;
  }
  return 'custom';
}

type Mode = 'single' | 'range';

// Shared Cally attributes — kept identical between calendar-date and
// calendar-range so theme + locale + week-start stay in sync.
// locale=en-US pins the Intl.DateTimeFormat to the Gregorian calendar
// with Latin digits; first-day-of-week=1 is Monday (ISO 8601 / GA4 /
// Plausible / Looker default).
const CALLY_ATTRS = {
  'first-day-of-week': '1',
  locale: 'en-US',
  'show-outside-days': true as const,
};

export function DatePicker() {
  const r = rangeSignal.value;
  const active = useSignal<DatePreset>(matchPreset(r.from, r.to));
  const open = useSignal<boolean>(false);
  const mode = useSignal<Mode>('range');
  const customFrom = useSignal(r.from);
  const customTo = useSignal(r.to);
  const err = useSignal<string | null>(null);

  const customChipRef = useRef<HTMLButtonElement | null>(null);
  const popoverRef = useRef<HTMLDivElement | null>(null);
  // Tracks the current Cally element + its `change` listener so the pair
  // can be torn down symmetrically on mode-switch and unmount.
  const changeBindingRef = useRef<{
    el: HTMLElement;
    fn: (e: Event) => void;
  } | null>(null);

  const callyReady = useCallyReady();

  const closePopover = (restoreFocus = false) => {
    open.value = false;
    if (restoreFocus) customChipRef.current?.focus();
  };

  const applyPreset = (p: DatePreset) => {
    active.value = p;
    err.value = null;
    if (p === 'custom') {
      customFrom.value = r.from;
      customTo.value = r.to;
      open.value = true;
      return;
    }
    open.value = false;
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
    active.value = matchPreset(customFrom.value, customTo.value);
    closePopover(true);
  };

  // Outside-click + Escape close. Attached only while open.
  useEffect(() => {
    if (!open.value) return undefined;
    const onClick = (e: MouseEvent) => {
      const t = e.target as Node | null;
      if (!t) return;
      if (popoverRef.current?.contains(t)) return;
      if (customChipRef.current?.contains(t)) return;
      closePopover(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closePopover(true);
    };
    document.addEventListener('mousedown', onClick);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onClick);
      document.removeEventListener('keydown', onKey);
    };
  }, [open.value]);

  // Detach any lingering Cally `change` listener on unmount. Mode-switch
  // detach is handled inline in attachChange below.
  useEffect(
    () => () => {
      const prior = changeBindingRef.current;
      if (prior) prior.el.removeEventListener('change', prior.fn);
      changeBindingRef.current = null;
    },
    [],
  );

  // Cally emits a non-bubbling `change` CustomEvent — Preact's JSX
  // `onChange` does not capture it. Use a callback ref and store the
  // (el, fn) pair so cleanup is symmetric across mode-switch + unmount.
  const attachChange = (el: HTMLElement | null) => {
    const prior = changeBindingRef.current;
    if (prior) prior.el.removeEventListener('change', prior.fn);
    if (!el) {
      changeBindingRef.current = null;
      return;
    }
    const fn = (e: Event) => {
      const target = e.target as HTMLElement & { value?: string };
      const val = target.value ?? '';
      if (!val) return;
      if (mode.value === 'single') {
        if (!/^\d{4}-\d{2}-\d{2}$/.test(val)) return;
        customFrom.value = val;
        customTo.value = addDayIRST(val, 1);
      } else {
        const [start, endInclusive] = val.split('/');
        if (!start || !endInclusive) return;
        customFrom.value = start;
        customTo.value = addDayIRST(endInclusive, 1);
      }
    };
    el.addEventListener('change', fn);
    changeBindingRef.current = { el, fn };
  };

  // Cally diffs `value` as a property. Memoize the range string so
  // Cally sees a stable identity when neither endpoint changed.
  const rangeValue = useMemo(
    () => `${customFrom.value}/${addDayIRST(customTo.value, -1)}`,
    [customFrom.value, customTo.value],
  );
  const echoToInclusive =
    mode.value === 'single' ? customFrom.value : addDayIRST(customTo.value, -1);

  return (
    <div class="statnive-datepicker" data-testid="datepicker">
      <div
        class="statnive-datepicker-presets"
        role="group"
        aria-label="Date range preset"
      >
        {PRESETS.map((p) => {
          const isActive = active.value === p.id;
          const isCustom = p.id === 'custom';
          return (
            <button
              key={p.id}
              ref={isCustom ? customChipRef : undefined}
              type="button"
              class={'statnive-chip' + (isActive ? ' is-active' : '')}
              aria-pressed={isActive}
              aria-expanded={isCustom ? open.value : undefined}
              aria-haspopup={isCustom ? 'dialog' : undefined}
              onClick={() => applyPreset(p.id)}
            >
              {p.label}
              {isCustom && isActive ? <span aria-hidden="true"> ⌄</span> : null}
            </button>
          );
        })}
      </div>

      {open.value ? (
        <div
          ref={popoverRef}
          class="statnive-datepicker-popover"
          role="dialog"
          aria-label="Custom date range"
        >
          <div
            class="statnive-datepicker-mode"
            role="group"
            aria-label="Date selection mode"
          >
            <button
              type="button"
              class={
                'statnive-datepicker-mode-btn' +
                (mode.value === 'range' ? ' is-active' : '')
              }
              aria-pressed={mode.value === 'range'}
              onClick={() => {
                mode.value = 'range';
              }}
            >
              Range
            </button>
            <button
              type="button"
              class={
                'statnive-datepicker-mode-btn' +
                (mode.value === 'single' ? ' is-active' : '')
              }
              aria-pressed={mode.value === 'single'}
              onClick={() => {
                mode.value = 'single';
              }}
            >
              Single
            </button>
          </div>

          {callyReady ? (
            mode.value === 'single' ? (
              <calendar-date
                value={customFrom.value}
                {...CALLY_ATTRS}
                ref={attachChange}
              >
                <calendar-month />
              </calendar-date>
            ) : (
              <calendar-range
                value={rangeValue}
                {...CALLY_ATTRS}
                ref={attachChange}
              >
                <calendar-month />
              </calendar-range>
            )
          ) : (
            <Loader />
          )}

          <dl class="statnive-datepicker-echo">
            {mode.value === 'single' ? (
              <>
                <dt>Date</dt>
                <dd>{customFrom.value}</dd>
              </>
            ) : (
              <>
                <dt>From</dt>
                <dd>{customFrom.value}</dd>
                <dt>To</dt>
                <dd>{echoToInclusive}</dd>
              </>
            )}
          </dl>

          <button
            type="button"
            class="statnive-datepicker-apply"
            onClick={applyCustom}
          >
            Apply
          </button>
          {err.value ? <span class="statnive-error">{err.value}</span> : null}
        </div>
      ) : null}
    </div>
  );
}
