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
