// rowMax returns the largest pick(r) across rows, or 0 when empty.
// Used by every dual-bar panel (Sources / Pages / Campaigns) to scale
// visitors + revenue bars against the row-set peak.
export function rowMax<T>(rows: T[], pick: (r: T) => number): number {
  let m = 0;
  for (const r of rows) {
    const v = pick(r);
    if (v > m) m = v;
  }
  return m;
}

// pctOfMax renders value / max as a CSS width percentage. Returns '0%'
// when max <= 0 so callers can drop straight into a `style.width`
// without per-call division guards. Shared by DualBar + the chart-strip
// bar lists on Campaigns so a width change lands in one place.
export function pctOfMax(value: number, max: number): string {
  if (max <= 0) return '0%';
  return Math.round((value / max) * 100) + '%';
}
