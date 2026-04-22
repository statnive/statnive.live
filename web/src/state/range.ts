import { signal, effect } from '@preact/signals';
import { hashSignal, navigate } from './hash';
import { filtersSignal, updateFilters } from './filters';

// IRST midnight YYYY-MM-DD strings — same shape as the Go API's Filter.
// IRST = UTC+3:30, no DST (since Sep 2022 per CLAUDE.md § Privacy Rules).
// Hoisted outside effects per `js-hoist-regexp`.
export const IRST_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

export function daysAgoIRST(days: number): string {
  const nowMs = Date.now();
  const irstMs = nowMs + (3 * 60 + 30) * 60 * 1000;
  const irstDate = new Date(irstMs - days * 24 * 60 * 60 * 1000);
  return irstDate.toISOString().slice(0, 10);
}

export function isValidIrstRange(from: string, to: string): boolean {
  if (!IRST_DATE_RE.test(from) || !IRST_DATE_RE.test(to)) return false;
  return from <= to;
}

function defaultRange(): { from: string; to: string } {
  return { from: daysAgoIRST(7), to: daysAgoIRST(0) };
}

// rangeSignal mirrors filtersSignal's from/to but exists separately so
// panels that only care about time can subscribe without re-rendering
// on every chip toggle. Bidirectionally synced below.
export const rangeSignal = signal<{ from: string; to: string }>(
  (() => {
    const f = filtersSignal.value;
    if (f.from && f.to && isValidIrstRange(f.from, f.to)) {
      return { from: f.from, to: f.to };
    }
    return defaultRange();
  })(),
);

// Preset identifiers for the date picker UI.
export type DatePreset = '7d' | '30d' | '90d' | 'custom';

export function presetToRange(p: DatePreset): { from: string; to: string } {
  switch (p) {
    case '7d':
      return { from: daysAgoIRST(7), to: daysAgoIRST(0) };
    case '30d':
      return { from: daysAgoIRST(30), to: daysAgoIRST(0) };
    case '90d':
      return { from: daysAgoIRST(90), to: daysAgoIRST(0) };
    default:
      return defaultRange();
  }
}

export function setRange(from: string, to: string): void {
  if (!isValidIrstRange(from, to)) return;
  rangeSignal.value = { from, to };
  updateFilters({ from, to });
}

// Keep rangeSignal in sync when the URL hash's from/to change (e.g. the
// user pastes a deep link or clicks a filter chip that changes the date
// window). No-op when values match to prevent an effect loop.
effect(() => {
  const f = filtersSignal.value;
  if (!f.from || !f.to) return;
  if (!isValidIrstRange(f.from, f.to)) return;
  const cur = rangeSignal.value;
  if (cur.from !== f.from || cur.to !== f.to) {
    rangeSignal.value = { from: f.from, to: f.to };
  }
});

// Unused import guard: `navigate` and `hashSignal` are referenced indirectly
// via updateFilters, but TS tree-shake wouldn't drop them. Keep explicit so
// reviewers see the state-sync chain range.ts → filters.ts → hash.ts.
void hashSignal;
void navigate;
