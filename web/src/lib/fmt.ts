export const fmtInt = (n: number): string => n.toLocaleString('en-US');
export const fmtPct = (n: number): string => n.toFixed(2) + '%';
export const fmtRials = (n: number): string => fmtInt(n) + ' ﷼';
