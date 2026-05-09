export const fmtInt = (n: number): string => n.toLocaleString('en-US');
export const fmtPct = (n: number): string => n.toFixed(2) + '%';

// fmtMoney renders an integer revenue value as a currency-labelled
// string. Currency is a display-only label in this codebase — the
// stored integer is the major unit (no cents-division). The
// maximumFractionDigits / minimumFractionDigits = 0 pair forces
// 2-decimal currencies (EUR, USD) to render as "€1,500,000" rather
// than "€15,000.00", and zero-decimal currencies (JPY, IRR) keep
// their natural shape. If `currency` is empty or not a valid ISO 4217
// code (e.g. the API hasn't fanned out the active site yet), the
// function falls back to a plain integer + the raw code suffix so the
// panel still renders without throwing.
export const fmtMoney = (amount: number, currency: string): string => {
  if (!currency) {
    return fmtInt(amount);
  }

  try {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency,
      maximumFractionDigits: 0,
      minimumFractionDigits: 0,
    }).format(amount);
  } catch {
    return `${fmtInt(amount)} ${currency}`;
  }
};
