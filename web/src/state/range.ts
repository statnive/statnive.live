import { signal } from '@preact/signals';

// IRST midnight YYYY-MM-DD strings — same shape as the Go API's Filter.
// Phase 5a: hardcoded to last 7 days. Phase 5b adds a Gregorian date
// picker (Jalali defers to v1.1).
function daysAgoIRST(days: number): string {
  // IRST = UTC+3:30. Compute today in IRST then subtract days.
  const nowMs = Date.now();
  const irstMs = nowMs + (3 * 60 + 30) * 60 * 1000;
  const irstDate = new Date(irstMs - days * 24 * 60 * 60 * 1000);
  return irstDate.toISOString().slice(0, 10);
}

export const rangeSignal = signal<{ from: string; to: string }>({
  from: daysAgoIRST(7),
  to: daysAgoIRST(0),
});
